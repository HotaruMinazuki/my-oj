package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/api/middleware"
	"github.com/your-org/my-oj/internal/infra/postgres"
	"github.com/your-org/my-oj/internal/models"
)

// ContestCRUDRepo is the storage interface used by ContestHandler.
type ContestCRUDRepo interface {
	List(ctx context.Context, onlyPublic bool, limit, offset int) ([]models.Contest, int, error)
	GetByID(ctx context.Context, id models.ID) (*models.Contest, error)
	GetProblems(ctx context.Context, contestID models.ID) ([]postgres.ContestProblemSummary, error)
	Register(ctx context.Context, contestID, userID models.ID) error
	IsRegistered(ctx context.Context, contestID, userID models.ID) (bool, error)
	Create(ctx context.Context, c *models.Contest) error
	AddProblem(ctx context.Context, contestID, problemID models.ID, label string, maxScore, ordinal int) error
	// CreateContestProblem creates a brand-new problem (is_public=false) and links
	// it to the contest in one transaction. p.ID is back-filled on success.
	CreateContestProblem(ctx context.Context, contestID models.ID, p *models.Problem, label string, maxScore, ordinal int) error
	RemoveProblem(ctx context.Context, contestID, problemID models.ID) error
	Delete(ctx context.Context, id models.ID) error
	// ActiveContestForProblem resolves the running contest a problem submission
	// should count toward (used to auto-attribute problem-page submissions).
	ActiveContestForProblem(ctx context.Context, problemID, userID models.ID) (*models.ID, error)
}

// ContestHandler serves contest list, detail, registration, and admin endpoints.
type ContestHandler struct {
	contests ContestCRUDRepo
	log      *zap.Logger
}

func NewContestHandler(contests ContestCRUDRepo, log *zap.Logger) *ContestHandler {
	return &ContestHandler{contests: contests, log: log}
}

// ─── List  GET /api/v1/contests ───────────────────────────────────────────────

func (h *ContestHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	offset := (page - 1) * size

	roleVal, _ := c.Get(string(middleware.ContextKeyUserRole))
	role, _ := roleVal.(models.UserRole)
	onlyPublic := role != models.RoleAdmin

	contests, total, err := h.contests.List(c.Request.Context(), onlyPublic, size, offset)
	if err != nil {
		h.log.Error("list contests", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"contests": contests,
		"total":    total,
		"page":     page,
		"size":     size,
	})
}

// ─── Get  GET /api/v1/contests/:contest_id ────────────────────────────────────

func (h *ContestHandler) Get(c *gin.Context) {
	id, ok := parseContestID(c)
	if !ok {
		return
	}
	contest, err := h.contests.GetByID(c.Request.Context(), id)
	if err != nil {
		h.log.Error("get contest", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if contest == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "contest not found"})
		return
	}

	// Check if the current user is registered (optional enrichment).
	var registered bool
	if uid, ok := middleware.UserIDFromCtx(c); ok {
		registered, _ = h.contests.IsRegistered(c.Request.Context(), id, uid)
	}

	c.JSON(http.StatusOK, gin.H{
		"contest":    contest,
		"registered": registered,
	})
}

// ─── GetProblems  GET /api/v1/contests/:contest_id/problems ───────────────────

// GetProblems lists the problems attached to a contest.
//
// Access policy:
//   - Admin: always allowed.
//   - Authenticated non-admin: allowed if the contest is public OR they are a
//     registered participant.
//   - Anonymous: allowed only if the contest is public AND status != draft.
//
// Draft contests (status == draft) are hidden from all non-admin viewers to
// avoid leaking problem labels/titles before publication.
func (h *ContestHandler) GetProblems(c *gin.Context) {
	id, ok := parseContestID(c)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	contest, err := h.contests.GetByID(ctx, id)
	if err != nil {
		h.log.Error("get contest for problems", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if contest == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "contest not found"})
		return
	}

	// Role + identity from optional auth.
	roleVal, _ := c.Get(string(middleware.ContextKeyUserRole))
	role, _ := roleVal.(models.UserRole)
	uid, authed := middleware.UserIDFromCtx(c)

	// Visibility: admins always; public contests to everyone; private contests
	// only to registered participants. (Status is time-derived now, so there is
	// no permanent "draft" state to block on.)
	if role != models.RoleAdmin && !contest.IsPublic {
		if !authed {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		registered, err := h.contests.IsRegistered(ctx, id, uid)
		if err != nil {
			h.log.Error("check registration", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if !registered {
			c.JSON(http.StatusForbidden, gin.H{"error": "not a participant of this contest"})
			return
		}
	}

	// Problems stay hidden from non-admins until the contest actually starts —
	// the client shows a countdown instead. Gating on the real clock (not the
	// status column) prevents leaking problem labels/titles before the start.
	if role != models.RoleAdmin && time.Now().Before(contest.StartTime) {
		c.JSON(http.StatusOK, gin.H{"problems": []any{}, "not_started": true})
		return
	}

	problems, err := h.contests.GetProblems(ctx, id)
	if err != nil {
		h.log.Error("get contest problems", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"problems": problems})
}

// ─── Register  POST /api/v1/contests/:contest_id/register ─────────────────────

func (h *ContestHandler) RegisterParticipant(c *gin.Context) {
	id, ok := parseContestID(c)
	if !ok {
		return
	}
	uid, ok := middleware.UserIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	contest, err := h.contests.GetByID(c.Request.Context(), id)
	if err != nil || contest == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "contest not found"})
		return
	}
	if contest.Status == models.ContestStatusEnded {
		c.JSON(http.StatusBadRequest, gin.H{"error": "contest has ended"})
		return
	}
	if !contest.AllowLateRegister && contest.Status == models.ContestStatusRunning {
		c.JSON(http.StatusBadRequest, gin.H{"error": "late registration not allowed"})
		return
	}

	if err := h.contests.Register(c.Request.Context(), id, uid); err != nil {
		h.log.Error("register for contest", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "registered successfully"})
}

// ─── Create (Admin)  POST /api/v1/admin/contests ──────────────────────────────

type createContestReq struct {
	Title             string                `json:"title"               binding:"required"`
	Description       string                `json:"description"`
	ContestType       models.ContestType    `json:"contest_type"`
	StartTime         time.Time             `json:"start_time"          binding:"required"`
	EndTime           time.Time             `json:"end_time"            binding:"required"`
	FreezeTime        *time.Time            `json:"freeze_time"`
	Settings          models.ContestSettings `json:"settings"`
	IsPublic          bool                  `json:"is_public"`
	AllowLateRegister bool                  `json:"allow_late_register"`
}

func (h *ContestHandler) Create(c *gin.Context) {
	var req createContestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	organizerID, _ := middleware.UserIDFromCtx(c)

	contest := &models.Contest{
		Title:             req.Title,
		Description:       req.Description,
		ContestType:       req.ContestType,
		Status:            models.ContestStatusDraft,
		StartTime:         req.StartTime,
		EndTime:           req.EndTime,
		FreezeTime:        req.FreezeTime,
		Settings:          req.Settings,
		IsPublic:          req.IsPublic,
		AllowLateRegister: req.AllowLateRegister,
		OrganizerID:       organizerID,
	}
	if contest.ContestType == "" {
		contest.ContestType = models.ContestICPC
	}

	if err := h.contests.Create(c.Request.Context(), contest); err != nil {
		h.log.Error("create contest", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusCreated, contest)
}

// ─── AddProblem (Admin)  POST /api/v1/admin/contests/:contest_id/problems ────

// addProblemReq creates a NEW problem inside the contest, or — when problem_id
// is provided — links an existing problem. The primary flow is in-contest
// creation: problems are authored here (not in the global bank) and become
// public in the bank automatically once the contest ends.
type addProblemReq struct {
	Label    string `json:"label" binding:"required"`
	MaxScore int    `json:"max_score"`
	Ordinal  int    `json:"ordinal"`

	// Link mode: reuse an existing problem.
	ProblemID models.ID `json:"problem_id"`

	// Create mode (problem_id omitted/0): author a new problem.
	Title       string           `json:"title"`
	Statement   string           `json:"statement"`
	TimeLimitMs int64            `json:"time_limit_ms"`
	MemLimitKB  int64            `json:"mem_limit_kb"`
	JudgeType   models.JudgeType `json:"judge_type"`
}

func (h *ContestHandler) AddProblem(c *gin.Context) {
	contestID, ok := parseContestID(c)
	if !ok {
		return
	}
	var req addProblemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// ── Link an existing problem ──────────────────────────────────────────────
	if req.ProblemID > 0 {
		if err := h.contests.AddProblem(
			c.Request.Context(), contestID, req.ProblemID,
			req.Label, req.MaxScore, req.Ordinal,
		); err != nil {
			if isUniqueViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"error": "problem already in contest"})
				return
			}
			if isForeignKeyViolation(err) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "contest or problem not found"})
				return
			}
			h.log.Error("add problem to contest", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"message": "problem added"})
		return
	}

	// ── Create a new in-contest problem (hidden until the contest ends) ───────
	if req.Title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title is required when creating a problem"})
		return
	}
	authorID, _ := middleware.UserIDFromCtx(c)
	p := &models.Problem{
		Title:       req.Title,
		Statement:   req.Statement,
		TimeLimitMs: req.TimeLimitMs,
		MemLimitKB:  req.MemLimitKB,
		JudgeType:   req.JudgeType,
		IsPublic:    false, // revealed to the bank automatically when the contest ends
		AuthorID:    authorID,
	}
	if p.TimeLimitMs == 0 {
		p.TimeLimitMs = 2000
	}
	if p.MemLimitKB == 0 {
		p.MemLimitKB = 262144
	}
	if p.JudgeType == "" {
		p.JudgeType = models.JudgeStandard
	}

	if err := h.contests.CreateContestProblem(
		c.Request.Context(), contestID, p, req.Label, req.MaxScore, req.Ordinal,
	); err != nil {
		if isUniqueViolation(err) {
			c.JSON(http.StatusConflict, gin.H{"error": "label already used in this contest"})
			return
		}
		if isForeignKeyViolation(err) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "contest not found"})
			return
		}
		h.log.Error("create contest problem", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "problem created", "problem_id": p.ID})
}

// ─── RemoveProblem (Admin) DELETE /api/v1/admin/contests/:contest_id/problems/:problem_id ──

func (h *ContestHandler) RemoveProblem(c *gin.Context) {
	contestID, ok := parseContestID(c)
	if !ok {
		return
	}
	pidStr := c.Param("problem_id")
	pid, err := strconv.ParseInt(pidStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid problem id"})
		return
	}

	if err := h.contests.RemoveProblem(c.Request.Context(), contestID, models.ID(pid)); err != nil {
		h.log.Error("remove problem from contest", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "problem removed"})
}

// ─── Delete (Admin)  DELETE /api/v1/admin/contests/:contest_id ────────────────

// Delete removes a contest. Its submissions are kept (detached to practice);
// contest_problems and contest_participants cascade away.
func (h *ContestHandler) Delete(c *gin.Context) {
	id, ok := parseContestID(c)
	if !ok {
		return
	}
	if err := h.contests.Delete(c.Request.Context(), id); err != nil {
		h.log.Error("delete contest", zap.Error(err), zap.Int64("contest_id", id))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "contest deleted", "id": id})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// parseContestID is defined in ranking.go — returns (models.ID, bool).

// isUniqueViolation reports whether err is a Postgres 23505 error (duplicate
// primary key / unique constraint).
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

// isForeignKeyViolation reports whether err is a Postgres 23503 error
// (referenced row does not exist).
func isForeignKeyViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23503"
}
