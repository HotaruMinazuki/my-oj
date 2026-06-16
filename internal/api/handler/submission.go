package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/api/middleware"
	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/mq"
	"github.com/your-org/my-oj/internal/storage"
)

// ─── Repository contracts ─────────────────────────────────────────────────────

// SubmissionRepo abstracts the DB operations the handler needs.
type SubmissionRepo interface {
	Create(ctx context.Context, s *models.Submission) error
	GetByID(ctx context.Context, id models.ID) (*models.Submission, error)
	Update(ctx context.Context, s *models.Submission) error
	// ListPendingByContest returns the contest's still-Pending submissions with the
	// fields needed to rebuild a JudgeTask, for the deferred batch evaluation of
	// 盲考 (OI 挂机模式) contests. Ordered by id ascending, so the last entry per
	// (user, problem) is that contestant's latest submission to the problem.
	ListPendingByContest(ctx context.Context, contestID models.ID) ([]*models.Submission, error)
	// MarkSuperseded flags submissions as Superseded — voided because a later
	// submission to the same problem overrode them under OI last-submission rules.
	MarkSuperseded(ctx context.Context, ids []models.ID) error
}

// ProblemRepo is the subset of problem queries needed for submission validation.
type ProblemRepo interface {
	GetJudgeConfig(ctx context.Context, problemID models.ID) (*models.JudgeConfig, error)
	GetJudgeMeta(ctx context.Context, problemID models.ID) (*models.ProblemJudgeMeta, error)
	GetTestCases(ctx context.Context, problemID models.ID) ([]models.JudgeTestCase, error)
}

// SubmissionContestRepo resolves the contest a submission belongs to, so the
// handler can apply format-specific visibility rules (ICPC hides testcases) and
// auto-attribute problem-page submissions to a running contest.
type SubmissionContestRepo interface {
	GetByID(ctx context.Context, id models.ID) (*models.Contest, error)
	// ActiveContestForProblem returns the contest a submission for this problem
	// should count toward — a running contest containing the problem that the
	// user may submit to (public or registered) — or nil for plain practice.
	ActiveContestForProblem(ctx context.Context, problemID, userID models.ID) (*models.ID, error)
}

// ─── Handler ──────────────────────────────────────────────────────────────────

// SubmissionHandler handles code submission and result retrieval.
type SubmissionHandler struct {
	submissions SubmissionRepo
	problems    ProblemRepo
	contests    SubmissionContestRepo
	publisher   mq.Publisher
	store       storage.ObjectStore
	log         *zap.Logger
}

func NewSubmissionHandler(
	submissions SubmissionRepo,
	problems ProblemRepo,
	contests SubmissionContestRepo,
	publisher mq.Publisher,
	store storage.ObjectStore,
	log *zap.Logger,
) *SubmissionHandler {
	return &SubmissionHandler{
		submissions: submissions,
		problems:    problems,
		contests:    contests,
		publisher:   publisher,
		store:       store,
		log:         log,
	}
}

// ─── POST /api/v1/contests/:contest_id/submissions ───────────────────────────

type submitRequest struct {
	ProblemID  models.ID       `json:"problem_id"  binding:"required"`
	Language   models.Language `json:"language"    binding:"required"`
	SourceCode string          `json:"source_code" binding:"required,min=1,max=65536"`
}

// Submit accepts a code submission, uploads it to MinIO, persists a Submission
// record, and enqueues a JudgeTask.
//
// The MinIO object key is stored in Submission.SourceCodePath and propagated
// verbatim into JudgeTask.SourceCodePath.  The judger node downloads the object
// from MinIO before compilation — no shared filesystem is required.
func (h *SubmissionHandler) Submit(c *gin.Context) {
	contestID, ok := parseContestID(c)
	if !ok {
		return
	}
	userID, ok := mustUserID(c)
	if !ok {
		return
	}

	var req submitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	cfg, meta, testCases, ok := h.loadProblemMeta(c, ctx, req.ProblemID)
	if !ok {
		return
	}

	sourceKey, ok := h.uploadSource(c, ctx, userID, req.ProblemID, req.Language, req.SourceCode)
	if !ok {
		return
	}

	sub := &models.Submission{
		UserID:         userID,
		ProblemID:      req.ProblemID,
		ContestID:      &contestID,
		Language:       req.Language,
		SourceCodePath: sourceKey, // MinIO object key, not a local path
		Status:         models.StatusPending,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := h.submissions.Create(ctx, sub); err != nil {
		h.log.Error("create submission", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	// 盲考/挂机模式: a running OI contest withholds the submission from the judge
	// until an admin runs the post-contest batch evaluation. It is persisted as
	// Pending and the contestant sees only "Pending".
	if h.deferJudging(ctx, &contestID) {
		c.JSON(http.StatusCreated, gin.H{"id": sub.ID, "status": sub.Status, "deferred": true})
		return
	}

	h.enqueueTask(c, ctx, sub, cfg, meta, testCases)
}

// ─── POST /api/v1/submissions (out-of-contest practice) ──────────────────────

func (h *SubmissionHandler) SubmitPractice(c *gin.Context) {
	userID, ok := mustUserID(c)
	if !ok {
		return
	}

	var req submitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()

	cfg, meta, testCases, ok := h.loadProblemMeta(c, ctx, req.ProblemID)
	if !ok {
		return
	}

	// If the problem is part of a running contest the user can submit to, this
	// "practice" submission actually counts toward that contest — so submitting
	// from the problem page is just as valid as from the contest page.
	contestID, err := h.contests.ActiveContestForProblem(ctx, req.ProblemID, userID)
	if err != nil {
		h.log.Error("resolve active contest", zap.Error(err), zap.Int64("problem_id", req.ProblemID))
		// Non-fatal: fall back to plain practice rather than blocking the submission.
		contestID = nil
	}

	sourceKey, ok := h.uploadSource(c, ctx, userID, req.ProblemID, req.Language, req.SourceCode)
	if !ok {
		return
	}

	sub := &models.Submission{
		UserID:         userID,
		ProblemID:      req.ProblemID,
		ContestID:      contestID, // nil → practice; non-nil → counts in the contest
		Language:       req.Language,
		SourceCodePath: sourceKey,
		Status:         models.StatusPending,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := h.submissions.Create(ctx, sub); err != nil {
		h.log.Error("create practice submission", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	// If this submission was auto-attributed to a running 盲考 (OI) contest, hold
	// it back from the judge just like a contest-page submission would be.
	if h.deferJudging(ctx, contestID) {
		c.JSON(http.StatusCreated, gin.H{"id": sub.ID, "status": sub.Status, "deferred": true})
		return
	}

	h.enqueueTask(c, ctx, sub, cfg, meta, testCases)
}

// ─── POST /api/v1/admin/contests/:contest_id/judge (admin 赛后评测) ────────────

// JudgeContest runs the deferred batch evaluation for a 盲考 (挂机模式) contest:
// every withheld (Pending) submission is enqueued for judging in one sweep. Only
// valid after the contest has ended — results then stream onto the scoreboard via
// the normal judge-result pipeline, which is the moment scores become public.
func (h *SubmissionHandler) JudgeContest(c *gin.Context) {
	contestID, ok := parseContestID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	contest, err := h.contests.GetByID(ctx, contestID)
	if err != nil || contest == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "contest not found"})
		return
	}
	if contest.EffectiveStatus(time.Now().UTC()) != models.ContestStatusEnded {
		c.JSON(http.StatusBadRequest, gin.H{"error": "比赛尚未结束，无法运行赛后评测"})
		return
	}

	pending, err := h.submissions.ListPendingByContest(ctx, contestID)
	if err != nil {
		h.log.Error("list pending submissions", zap.Error(err), zap.Int64("contest_id", contestID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	// OI 盲考: each problem counts ONLY the contestant's LAST submission. Judge just
	// those; void the earlier ones (Superseded) so they stay visible but unscored.
	toJudge := pending
	superseded := 0
	if contest.IsBlindJudged() {
		var voided []*models.Submission
		toJudge, voided = splitLatestPerProblem(pending)
		superseded = len(voided)
		if superseded > 0 {
			voidedIDs := make([]models.ID, len(voided))
			for i, s := range voided {
				voidedIDs[i] = s.ID
			}
			if err := h.submissions.MarkSuperseded(ctx, voidedIDs); err != nil {
				h.log.Error("mark superseded submissions", zap.Error(err), zap.Int64("contest_id", contestID))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
				return
			}
		}
	}

	// Cache each problem's judge meta so it is loaded once, not per submission.
	type problemMeta struct {
		cfg       *models.JudgeConfig
		meta      *models.ProblemJudgeMeta
		testCases []models.JudgeTestCase
		usable    bool
	}
	metaCache := make(map[models.ID]problemMeta)

	enqueued, skipped := 0, 0
	for _, sub := range toJudge {
		pm, cached := metaCache[sub.ProblemID]
		if !cached {
			cfg, meta, testCases, err := h.loadProblemMetaCtx(ctx, sub.ProblemID)
			pm = problemMeta{cfg: cfg, meta: meta, testCases: testCases, usable: err == nil && len(testCases) > 0}
			if err != nil {
				h.log.Warn("batch judge: load problem meta failed",
					zap.Int64("problem_id", sub.ProblemID), zap.Error(err))
			} else if len(testCases) == 0 {
				h.log.Warn("batch judge: problem has no test data; skipping its submissions",
					zap.Int64("problem_id", sub.ProblemID))
			}
			metaCache[sub.ProblemID] = pm
		}
		if !pm.usable {
			skipped++
			continue
		}
		if err := h.publishJudgeTask(ctx, sub, pm.cfg, pm.meta, pm.testCases); err != nil {
			h.log.Error("batch judge: enqueue failed",
				zap.Int64("submission_id", sub.ID), zap.Error(err))
			skipped++
			continue
		}
		enqueued++
	}

	h.log.Info("batch judge dispatched",
		zap.Int64("contest_id", contestID),
		zap.Int("enqueued", enqueued), zap.Int("skipped", skipped),
		zap.Int("superseded", superseded), zap.Int("total", len(pending)))
	c.JSON(http.StatusOK, gin.H{
		"message":    "已派发赛后评测",
		"enqueued":   enqueued,
		"skipped":    skipped,
		"superseded": superseded,
		"total":      len(pending),
	})
}

// splitLatestPerProblem partitions submissions into the latest one per
// (user, problem) — the ones to judge — and all the earlier ones, which are
// voided. "Latest" is the highest submission id for each (user, problem), so the
// result is independent of input order. Order within each output slice follows
// the input.
func splitLatestPerProblem(subs []*models.Submission) (latest, voided []*models.Submission) {
	type key struct{ user, problem models.ID }
	latestID := make(map[key]models.ID, len(subs))
	for _, s := range subs {
		k := key{s.UserID, s.ProblemID}
		if cur, ok := latestID[k]; !ok || s.ID > cur {
			latestID[k] = s.ID // highest id = latest submission, regardless of input order
		}
	}
	for _, s := range subs {
		if latestID[key{s.UserID, s.ProblemID}] == s.ID {
			latest = append(latest, s)
		} else {
			voided = append(voided, s)
		}
	}
	return latest, voided
}

// ─── GET /api/v1/submissions/:id ─────────────────────────────────────────────

// GetSubmission is public: 用户所有记录公开。The Submission model never
// serialises the source code path (json:"-"), so no code leaks — only the
// verdict, score, resource usage, and per-testcase results.
//
// ICPC 赛制例外: per-testcase results are hidden — contestants see only the
// overall verdict (AC/WA/TLE/MLE/RE/CE), as is standard for the format.
func (h *SubmissionHandler) GetSubmission(c *gin.Context) {
	id64, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id64 <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid submission id"})
		return
	}

	sub, err := h.submissions.GetByID(c.Request.Context(), models.ID(id64))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "submission not found"})
		return
	}

	h.applyContestVisibility(c, sub)
	c.JSON(http.StatusOK, sub)
}

// applyContestVisibility enforces format-specific result visibility for non-admin
// viewers of a contest submission:
//
//   - 盲考 (OI) while the contest is still running → the contestant sees ONLY
//     "Pending": every judged field is stripped (verdict, score, resource usage,
//     compile log, testcases). This holds even for a submission that was somehow
//     judged early — defense in depth alongside 挂机模式's deferred judging.
//   - ICPC → the per-testcase breakdown and judge message stay hidden (standard
//     for the format); the compile log stays for the contestant's own CE.
//
// Once a contest has ended, full results are shown to everyone.
func (h *SubmissionHandler) applyContestVisibility(c *gin.Context, sub *models.Submission) {
	if sub.ContestID == nil {
		return // practice submission — full details
	}
	roleVal, _ := c.Get(string(middleware.ContextKeyUserRole))
	if role, _ := roleVal.(models.UserRole); role == models.RoleAdmin {
		return // admins always see everything
	}

	contest, err := h.contests.GetByID(c.Request.Context(), *sub.ContestID)
	if err != nil || contest == nil {
		// Cannot determine the format — fail closed and hide details.
		h.log.Warn("resolve contest for submission visibility",
			zap.Int64("submission_id", sub.ID), zap.Error(err))
		sub.TestCaseResults = nil
		sub.JudgeMessage = ""
		return
	}

	if contest.IsBlindJudged() && contest.EffectiveStatus(time.Now().UTC()) != models.ContestStatusEnded {
		maskBlind(sub)
		return
	}
	if contest.ContestType == models.ContestICPC {
		sub.TestCaseResults = nil
		sub.JudgeMessage = ""
	}
}

// maskBlind reduces a submission to a bare "Pending", clearing every field that
// could leak a verdict or score during a 盲考 contest.
func maskBlind(sub *models.Submission) {
	sub.Status = models.StatusPending
	sub.Score = 0
	sub.TimeUsedMs = 0
	sub.MemUsedKB = 0
	sub.CompileLog = ""
	sub.JudgeMessage = ""
	sub.TestCaseResults = nil
}

// deferJudging reports whether a submission to this contest must be withheld from
// the judge for now — true for a 盲考 (OI) contest that has not yet ended. A nil
// contestID (plain practice) is never deferred.
func (h *SubmissionHandler) deferJudging(ctx context.Context, contestID *models.ID) bool {
	if contestID == nil {
		return false
	}
	contest, err := h.contests.GetByID(ctx, *contestID)
	if err != nil || contest == nil {
		// Can't classify the contest — judge normally. GetSubmission still masks
		// blind-contest results defensively, so a misclassification here can't leak.
		h.log.Warn("defer-judging: resolve contest failed",
			zap.Int64("contest_id", *contestID), zap.Error(err))
		return false
	}
	return contest.IsBlindJudged() &&
		contest.EffectiveStatus(time.Now().UTC()) != models.ContestStatusEnded
}

// ─── shared helpers ───────────────────────────────────────────────────────────

func (h *SubmissionHandler) loadProblemMeta(
	c *gin.Context,
	ctx context.Context,
	problemID models.ID,
) (*models.JudgeConfig, *models.ProblemJudgeMeta, []models.JudgeTestCase, bool) {
	cfg, meta, testCases, err := h.loadProblemMetaCtx(ctx, problemID)
	if err != nil {
		h.log.Error("load problem meta", zap.Error(err), zap.Int64("problem_id", problemID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load problem data"})
		return nil, nil, nil, false
	}
	// A task with zero test cases would be judged as SystemError; fail fast with
	// an actionable message instead of polluting the queue.
	if len(testCases) == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "problem has no test data; ask an admin to (re-)upload the testcase zip",
		})
		return nil, nil, nil, false
	}
	return cfg, meta, testCases, true
}

// loadProblemMetaCtx loads a problem's judge config, metadata and test cases
// without writing any HTTP response. Shared by the live submit path (wrapped by
// loadProblemMeta) and the deferred batch evaluation.
func (h *SubmissionHandler) loadProblemMetaCtx(
	ctx context.Context,
	problemID models.ID,
) (*models.JudgeConfig, *models.ProblemJudgeMeta, []models.JudgeTestCase, error) {
	cfg, err := h.problems.GetJudgeConfig(ctx, problemID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get judge config: %w", err)
	}
	meta, err := h.problems.GetJudgeMeta(ctx, problemID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get judge meta: %w", err)
	}
	testCases, err := h.problems.GetTestCases(ctx, problemID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get test cases: %w", err)
	}
	return cfg, meta, testCases, nil
}

// uploadSource uploads the source code to MinIO and returns the object key.
//
// Key format: sources/{userID}/{problemID}/{uuid}.{ext}
// Bucket    : storage.BucketSubmissions
//
// This key is stored verbatim in Submission.SourceCodePath and JudgeTask.SourceCodePath.
// The judger calls store.GetToFile(ctx, BucketSubmissions, key, destPath) to retrieve it.
func (h *SubmissionHandler) uploadSource(
	c *gin.Context,
	ctx context.Context,
	userID, problemID models.ID,
	lang models.Language,
	code string,
) (objectKey string, ok bool) {
	ext := langExt(lang)
	key := fmt.Sprintf("sources/%d/%d/%s.%s", userID, problemID, uuid.New().String(), ext)

	body := strings.NewReader(code)
	err := h.store.Put(ctx, storage.BucketSubmissions, key, body, int64(len(code)), "text/plain; charset=utf-8")
	if err != nil {
		h.log.Error("upload source to MinIO", zap.Error(err), zap.String("key", key))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage error"})
		return "", false
	}
	return key, true
}

// enqueueTask publishes a JudgeTask for a freshly created submission and writes
// the HTTP ack. On MQ failure the submission is still created; a background
// requeue job can resend stale Pending submissions.
func (h *SubmissionHandler) enqueueTask(
	c *gin.Context,
	ctx context.Context,
	sub *models.Submission,
	cfg *models.JudgeConfig,
	meta *models.ProblemJudgeMeta,
	testCases []models.JudgeTestCase,
) {
	if err := h.publishJudgeTask(ctx, sub, cfg, meta, testCases); err != nil {
		h.log.Error("enqueue judge task", zap.Error(err), zap.Int64("submission_id", sub.ID))
		c.JSON(http.StatusAccepted, gin.H{
			"id":      sub.ID,
			"status":  sub.Status,
			"warning": "enqueue failed; submission recorded and will be retried",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":     sub.ID,
		"status": sub.Status,
	})
}

// publishJudgeTask builds a JudgeTask from a submission and publishes it to the
// judge queue. Pure (no HTTP); shared by the live submit path and the deferred
// batch evaluation. The submission must carry UserID, ProblemID, ContestID,
// Language and SourceCodePath (the MinIO key the judger downloads).
func (h *SubmissionHandler) publishJudgeTask(
	ctx context.Context,
	sub *models.Submission,
	cfg *models.JudgeConfig,
	meta *models.ProblemJudgeMeta,
	testCases []models.JudgeTestCase,
) error {
	task := &mq.TaskMessage{
		JudgeTask: models.JudgeTask{
			TaskID:         uuid.New().String(),
			SubmissionID:   sub.ID,
			UserID:         sub.UserID,
			ProblemID:      sub.ProblemID,
			ContestID:      sub.ContestID,
			Language:       sub.Language,
			SourceCodePath: sub.SourceCodePath,
			JudgeType:      meta.JudgeType,
			JudgeConfig:    *cfg,
			TimeLimitMs:    meta.TimeLimitMs,
			MemLimitKB:     meta.MemLimitKB,
			TestCases:      testCases,
		},
		EnqueuedAt: time.Now().UTC(),
	}

	payload, _ := mq.MarshalTask(task)
	_, err := h.publisher.Publish(ctx, mq.QueueJudgeTasks, payload)
	return err
}

func langExt(lang models.Language) string {
	switch lang {
	case models.LangCPP17, models.LangCPP20:
		return "cpp"
	case models.LangC:
		return "c"
	case models.LangJava:
		return "java"
	case models.LangPython:
		return "py"
	case models.LangGo:
		return "go"
	case models.LangRust:
		return "rs"
	default:
		return "txt"
	}
}

func mustUserID(c *gin.Context) (models.ID, bool) {
	v, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return 0, false
	}
	id, ok := v.(models.ID)
	if !ok || id == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid user context"})
		return 0, false
	}
	return id, true
}
