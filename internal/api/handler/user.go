package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/infra/postgres"
	"github.com/your-org/my-oj/internal/models"
)

// ─── Repository contracts ─────────────────────────────────────────────────────

// UserDirectoryRepo provides user lookup and admin search.
type UserDirectoryRepo interface {
	GetByID(ctx context.Context, id models.ID) (*models.User, error)
	Search(ctx context.Context, q string, limit, offset int) ([]models.User, int, error)
}

// SubmissionHistoryRepo lists submissions (newest first) and per-user stats.
type SubmissionHistoryRepo interface {
	ListAll(ctx context.Context, f postgres.SubmissionFilter, limit, offset int) ([]postgres.SubmissionListItem, int, error)
	UserStats(ctx context.Context, userID models.ID) (*postgres.UserSubmissionStats, error)
}

// ContestHistoryRepo lists the contests a user registered for.
type ContestHistoryRepo interface {
	ListByParticipant(ctx context.Context, userID models.ID) ([]models.Contest, error)
}

// ─── Handler ──────────────────────────────────────────────────────────────────

// UserHandler serves public user profiles (所有记录公开) and the admin
// user-management / global-submission endpoints.
type UserHandler struct {
	users       UserDirectoryRepo
	submissions SubmissionHistoryRepo
	contests    ContestHistoryRepo
	log         *zap.Logger
}

func NewUserHandler(
	users UserDirectoryRepo,
	submissions SubmissionHistoryRepo,
	contests ContestHistoryRepo,
	log *zap.Logger,
) *UserHandler {
	return &UserHandler{users: users, submissions: submissions, contests: contests, log: log}
}

// ─── GET /api/v1/users/:id ────────────────────────────────────────────────────

// GetProfile returns a user's public profile with submission stats.
// Email is intentionally omitted — profiles are public, mailboxes are not.
func (h *UserHandler) GetProfile(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	u, err := h.users.GetByID(ctx, id)
	if err != nil {
		h.log.Error("get user profile", zap.Error(err), zap.Int64("user_id", id))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if u == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	stats, err := h.submissions.UserStats(ctx, id)
	if err != nil {
		h.log.Error("user stats", zap.Error(err), zap.Int64("user_id", id))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":           u.ID,
			"username":     u.Username,
			"role":         u.Role,
			"organization": u.Organization,
			"created_at":   u.CreatedAt,
		},
		"stats": stats,
	})
}

// ─── GET /api/v1/users/:id/submissions ────────────────────────────────────────

// ListSubmissions returns the user's submission history, newest first.
// Public by design: 用户所有记录公开 (source code itself is never exposed).
func (h *UserHandler) ListSubmissions(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}
	page, size := parsePageSize(c)

	items, total, err := h.submissions.ListAll(c.Request.Context(),
		postgres.SubmissionFilter{UserID: &id}, size, (page-1)*size)
	if err != nil {
		h.log.Error("list user submissions", zap.Error(err), zap.Int64("user_id", id))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"submissions": items,
		"total":       total,
		"page":        page,
		"size":        size,
	})
}

// ─── GET /api/v1/users/:id/contests ───────────────────────────────────────────

// ListContests returns every contest the user has registered for, newest first.
func (h *UserHandler) ListContests(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}
	contests, err := h.contests.ListByParticipant(c.Request.Context(), id)
	if err != nil {
		h.log.Error("list user contests", zap.Error(err), zap.Int64("user_id", id))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"contests": contests})
}

// ─── GET /api/v1/admin/users ──────────────────────────────────────────────────

// AdminSearchUsers lists/searches users for the admin panel. Unlike the public
// profile, this includes email (admin-only route).
func (h *UserHandler) AdminSearchUsers(c *gin.Context) {
	q := c.Query("q")
	page, size := parsePageSize(c)

	users, total, err := h.users.Search(c.Request.Context(), q, size, (page-1)*size)
	if err != nil {
		h.log.Error("admin search users", zap.Error(err), zap.String("q", q))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// User.Email marshals normally; User.PasswordHash is json:"-" so the
	// model is safe to return as-is on an admin-only route.
	c.JSON(http.StatusOK, gin.H{
		"users": users,
		"total": total,
		"page":  page,
		"size":  size,
	})
}

// ─── GET /api/v1/admin/submissions ────────────────────────────────────────────

// AdminListSubmissions returns ALL submissions newest-first, with optional
// user_id / problem_id / contest_id / status filters.
func (h *UserHandler) AdminListSubmissions(c *gin.Context) {
	page, size := parsePageSize(c)

	var f postgres.SubmissionFilter
	if v, err := strconv.ParseInt(c.Query("user_id"), 10, 64); err == nil && v > 0 {
		id := models.ID(v)
		f.UserID = &id
	}
	if v, err := strconv.ParseInt(c.Query("problem_id"), 10, 64); err == nil && v > 0 {
		id := models.ID(v)
		f.ProblemID = &id
	}
	if v, err := strconv.ParseInt(c.Query("contest_id"), 10, 64); err == nil && v > 0 {
		id := models.ID(v)
		f.ContestID = &id
	}
	f.Status = c.Query("status")

	items, total, err := h.submissions.ListAll(c.Request.Context(), f, size, (page-1)*size)
	if err != nil {
		h.log.Error("admin list submissions", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"submissions": items,
		"total":       total,
		"page":        page,
		"size":        size,
	})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func parseUserID(c *gin.Context) (models.ID, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return 0, false
	}
	return models.ID(id), true
}

func parsePageSize(c *gin.Context) (page, size int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ = strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return page, size
}
