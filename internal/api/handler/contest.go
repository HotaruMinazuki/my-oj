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
	RemoveProblem(ctx context.Context, contestID, problemID models.ID) error
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

func (h *ContestHandler) GetProblems(c *gin.Context) {
	id, ok := parseContestID(c)
	if !ok {
		return
	}
	problems, err := h.contests.GetProblems(c.Request.Context(), id)
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

type addProblemReq struct {
	ProblemID models.ID `json:"problem_id" binding:"required"`
	Label     string    `json:"label"      binding:"required"`
	MaxScore  int       `json:"max_score"`
	Ordinal   int       `json:"ordinal"`
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

	if err := h.contests.AddProblem(
		c.Request.Context(), contestID, req.ProblemID,
		req.Label, req.MaxScore, req.Ordinal,
	); err != nil {
		// Duplicate (same contest + problem) returns Postgres error 23505;
		// surface as 409 for the UI to show a friendlier message.
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
