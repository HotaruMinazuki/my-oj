package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/api/middleware"
	"github.com/your-org/my-oj/internal/infra/postgres"
	"github.com/your-org/my-oj/internal/models"
)

// ─── Repository contracts ─────────────────────────────────────────────────────

// UserDirectoryRepo provides user lookup, admin search, and profile updates.
type UserDirectoryRepo interface {
	GetByID(ctx context.Context, id models.ID) (*models.User, error)
	GetByEmail(ctx context.Context, email string) (*models.User, error)
	Search(ctx context.Context, q string, limit, offset int) ([]models.User, int, error)
	UpdateProfile(ctx context.Context, id models.ID, organization string) error
	// BindEmail attaches an email to an account that has none (one-shot).
	BindEmail(ctx context.Context, id models.ID, email string) error
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
// Email is private (不对外公布): it is included only when the requester is the
// account owner or an admin (管理员可以看到).
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

	profile := gin.H{
		"id":           u.ID,
		"username":     u.Username,
		"role":         u.Role,
		"organization": u.Organization,
		"created_at":   u.CreatedAt,
	}
	// Owner or admin may see the (private) email; null when unbound.
	reqID, _ := middleware.UserIDFromCtx(c)
	role, _ := middleware.RoleFromCtx(c)
	if reqID == id || role == models.RoleAdmin {
		profile["email"] = u.Email
	}

	c.JSON(http.StatusOK, gin.H{
		"user":  profile,
		"stats": stats,
	})
}

// ─── PUT /api/v1/users/me ─────────────────────────────────────────────────────

type updateProfileReq struct {
	Organization string `json:"organization"`
	// Email, when present and non-empty, binds an email to an account that has
	// none. Binding is one-shot: an already-bound email cannot be changed here.
	Email *string `json:"email"`
}

// UpdateMe lets the authenticated user edit their own profile. The organization
// (学校/单位) is shown on the profile and used as the team affiliation in the
// contest resolver XML export. It can also bind an email if none is set yet.
func (h *UserHandler) UpdateMe(c *gin.Context) {
	uid, ok := mustUserID(c)
	if !ok {
		return
	}
	var req updateProfileReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len([]rune(req.Organization)) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "organization too long (max 100 chars)"})
		return
	}
	ctx := c.Request.Context()

	// Optional email binding.
	if req.Email != nil {
		if email := normalizeEmail(*req.Email); email != "" {
			if !h.bindEmail(c, uid, email) {
				return // bindEmail already wrote the error response
			}
		}
	}

	if err := h.users.UpdateProfile(ctx, uid, strings.TrimSpace(req.Organization)); err != nil {
		h.log.Error("update profile", zap.Error(err), zap.Int64("user_id", uid))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Return the current email so the client can refresh its cached user.
	u, err := h.users.GetByID(ctx, uid)
	if err != nil || u == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"organization": strings.TrimSpace(req.Organization),
		"email":        u.Email,
	})
}

// bindEmail validates and binds an email to the account, writing the
// appropriate error response and returning false on failure.
func (h *UserHandler) bindEmail(c *gin.Context, uid models.ID, email string) bool {
	ctx := c.Request.Context()
	if !validEmail(email) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "邮箱格式不正确"})
		return false
	}

	// Already bound? Binding is one-shot for self-service.
	me, err := h.users.GetByID(ctx, uid)
	if err != nil || me == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}
	if me.Email != nil {
		if normalizeEmail(*me.Email) == email {
			return true // no-op: same email already bound
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "邮箱已绑定，如需修改请联系管理员"})
		return false
	}

	// Uniqueness (一个邮箱只能注册一个账号).
	taken, err := h.users.GetByEmail(ctx, email)
	if err != nil {
		h.log.Error("bind email: check unique", zap.Error(err), zap.Int64("user_id", uid))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return false
	}
	if taken != nil && taken.ID != uid {
		c.JSON(http.StatusConflict, gin.H{"error": "该邮箱已被占用"})
		return false
	}

	if err := h.users.BindEmail(ctx, uid, email); err != nil {
		switch {
		case errors.Is(err, postgres.ErrEmailTaken):
			c.JSON(http.StatusConflict, gin.H{"error": "该邮箱已被占用"})
		case errors.Is(err, postgres.ErrEmailAlreadyBound):
			c.JSON(http.StatusBadRequest, gin.H{"error": "邮箱已绑定，如需修改请联系管理员"})
		default:
			h.log.Error("bind email", zap.Error(err), zap.Int64("user_id", uid))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		}
		return false
	}
	return true
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
