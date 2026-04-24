package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/api/middleware"
	"github.com/your-org/my-oj/internal/models"
)

// ProblemListRepo is the read/write interface used by ProblemHandler.
// The judger-facing ProblemRepo methods (GetJudgeConfig, GetTestCases) are
// declared separately in handler/submission.go.
type ProblemListRepo interface {
	ListProblems(ctx context.Context, onlyPublic bool, limit, offset int) ([]models.Problem, int, error)
	GetProblemByID(ctx context.Context, id models.ID) (*models.Problem, error)
	CreateProblem(ctx context.Context, p *models.Problem) error
}

// ProblemHandler serves problem list and detail endpoints.
type ProblemHandler struct {
	problems ProblemListRepo
	log      *zap.Logger
}

func NewProblemHandler(problems ProblemListRepo, log *zap.Logger) *ProblemHandler {
	return &ProblemHandler{problems: problems, log: log}
}

// ─── List  GET /api/v1/problems ───────────────────────────────────────────────

func (h *ProblemHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	offset := (page - 1) * size

	// Admins see all problems; others see only public ones.
	roleVal, _ := c.Get(string(middleware.ContextKeyUserRole))
	role, _ := roleVal.(models.UserRole)
	onlyPublic := role != models.RoleAdmin

	problems, total, err := h.problems.ListProblems(c.Request.Context(), onlyPublic, size, offset)
	if err != nil {
		h.log.Error("list problems", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"problems": problems,
		"total":    total,
		"page":     page,
		"size":     size,
	})
}

// ─── Get  GET /api/v1/problems/:id ────────────────────────────────────────────

func (h *ProblemHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid problem id"})
		return
	}

	p, err := h.problems.GetProblemByID(c.Request.Context(), models.ID(id))
	if err != nil {
		h.log.Error("get problem", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "problem not found"})
		return
	}

	// Non-admins cannot see private problems outside of a contest.
	roleVal, _ := c.Get(string(middleware.ContextKeyUserRole))
	role, _ := roleVal.(models.UserRole)
	if !p.IsPublic && role != models.RoleAdmin {
		c.JSON(http.StatusNotFound, gin.H{"error": "problem not found"})
		return
	}

	c.JSON(http.StatusOK, p)
}

// ─── Create (Admin)  POST /api/v1/admin/problems ──────────────────────────────

type createProblemReq struct {
	Title       string           `json:"title"        binding:"required"`
	Statement   string           `json:"statement"`
	TimeLimitMs int64            `json:"time_limit_ms"`
	MemLimitKB  int64            `json:"mem_limit_kb"`
	JudgeType   models.JudgeType `json:"judge_type"`
	IsPublic    bool             `json:"is_public"`
}

func (h *ProblemHandler) Create(c *gin.Context) {
	var req createProblemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	authorID, _ := middleware.UserIDFromCtx(c)

	p := &models.Problem{
		Title:       req.Title,
		Statement:   req.Statement,
		TimeLimitMs: req.TimeLimitMs,
		MemLimitKB:  req.MemLimitKB,
		JudgeType:   req.JudgeType,
		IsPublic:    req.IsPublic,
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

	if err := h.problems.CreateProblem(c.Request.Context(), p); err != nil {
		h.log.Error("create problem", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusCreated, p)
}
