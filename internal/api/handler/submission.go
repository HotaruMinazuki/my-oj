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
}

// ProblemRepo is the subset of problem queries needed for submission validation.
type ProblemRepo interface {
	GetJudgeConfig(ctx context.Context, problemID models.ID) (*models.JudgeConfig, error)
	GetTestCases(ctx context.Context, problemID models.ID) ([]models.JudgeTestCase, error)
}

// ─── Handler ──────────────────────────────────────────────────────────────────

// SubmissionHandler handles code submission and result retrieval.
type SubmissionHandler struct {
	submissions SubmissionRepo
	problems    ProblemRepo
	publisher   mq.Publisher
	store       storage.ObjectStore
	log         *zap.Logger
}

func NewSubmissionHandler(
	submissions SubmissionRepo,
	problems ProblemRepo,
	publisher mq.Publisher,
	store storage.ObjectStore,
	log *zap.Logger,
) *SubmissionHandler {
	return &SubmissionHandler{
		submissions: submissions,
		problems:    problems,
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

	cfg, testCases, ok := h.loadProblemMeta(c, ctx, req.ProblemID)
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

	h.enqueueTask(c, ctx, sub, userID, &contestID, cfg, testCases, sourceKey)
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

	cfg, testCases, ok := h.loadProblemMeta(c, ctx, req.ProblemID)
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

	h.enqueueTask(c, ctx, sub, userID, nil, cfg, testCases, sourceKey)
}

// ─── GET /api/v1/submissions/:id ─────────────────────────────────────────────

func (h *SubmissionHandler) GetSubmission(c *gin.Context) {
	id64, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id64 <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid submission id"})
		return
	}

	ctx := c.Request.Context()
	sub, err := h.submissions.GetByID(ctx, models.ID(id64))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "submission not found"})
		return
	}

	userID, _ := mustUserID(c)
	roleVal, _ := c.Get("user_role")
	role, _ := roleVal.(models.UserRole)
	if role != models.RoleAdmin && sub.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	c.JSON(http.StatusOK, sub)
}

// ─── shared helpers ───────────────────────────────────────────────────────────

func (h *SubmissionHandler) loadProblemMeta(
	c *gin.Context,
	ctx context.Context,
	problemID models.ID,
) (*models.JudgeConfig, []models.JudgeTestCase, bool) {
	cfg, err := h.problems.GetJudgeConfig(ctx, problemID)
	if err != nil {
		h.log.Error("get judge config", zap.Error(err), zap.Int64("problem_id", problemID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load problem config"})
		return nil, nil, false
	}
	testCases, err := h.problems.GetTestCases(ctx, problemID)
	if err != nil {
		h.log.Error("get test cases", zap.Error(err), zap.Int64("problem_id", problemID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load test cases"})
		return nil, nil, false
	}
	return cfg, testCases, true
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

// enqueueTask builds and publishes a JudgeTask to the MQ.
// On MQ failure the submission is still created; a background requeue job can
// resend stale Pending submissions.
func (h *SubmissionHandler) enqueueTask(
	c *gin.Context,
	ctx context.Context,
	sub *models.Submission,
	userID models.ID,
	contestID *models.ID,
	cfg *models.JudgeConfig,
	testCases []models.JudgeTestCase,
	sourceKey string,
) {
	task := &mq.TaskMessage{
		JudgeTask: models.JudgeTask{
			TaskID:         uuid.New().String(),
			SubmissionID:   sub.ID,
			UserID:         userID,
			ProblemID:      sub.ProblemID,
			ContestID:      contestID,
			Language:       sub.Language,
			SourceCodePath: sourceKey, // MinIO key; judger downloads via ObjectStore
			JudgeConfig:    *cfg,
			TestCases:      testCases,
		},
		EnqueuedAt: time.Now().UTC(),
	}

	payload, _ := mq.MarshalTask(task)
	if _, err := h.publisher.Publish(ctx, mq.QueueJudgeTasks, payload); err != nil {
		h.log.Error("enqueue judge task", zap.Error(err), zap.Int64("submission_id", sub.ID))
		c.JSON(http.StatusAccepted, gin.H{
			"submission_id": sub.ID,
			"status":        sub.Status,
			"warning":       "enqueue failed; submission recorded and will be retried",
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"submission_id": sub.ID,
		"status":        sub.Status,
	})
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
