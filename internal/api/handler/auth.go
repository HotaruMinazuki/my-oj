package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/api/middleware"
	"github.com/your-org/my-oj/internal/models"
)

// AuthUserRepo is the storage interface needed by AuthHandler.
type AuthUserRepo interface {
	Create(ctx context.Context, u *models.User) error
	GetByUsername(ctx context.Context, username string) (*models.User, error)
	GetByID(ctx context.Context, id models.ID) (*models.User, error)
}

// AuthHandler handles /api/v1/auth/register and /api/v1/auth/login.
type AuthHandler struct {
	users      AuthUserRepo
	signingKey []byte
	log        *zap.Logger
}

func NewAuthHandler(users AuthUserRepo, signingKey []byte, log *zap.Logger) *AuthHandler {
	return &AuthHandler{users: users, signingKey: signingKey, log: log}
}

// ─── Register ─────────────────────────────────────────────────────────────────

type registerReq struct {
	Username     string `json:"username"     binding:"required,min=3,max=32"`
	Email        string `json:"email"        binding:"required,email"`
	Password     string `json:"password"     binding:"required,min=6"`
	Organization string `json:"organization"`
}

// Register handles POST /api/v1/auth/register
func (h *AuthHandler) Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check username uniqueness.
	existing, err := h.users.GetByUsername(c.Request.Context(), req.Username)
	if err != nil {
		h.log.Error("register: check username", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if existing != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "username already taken"})
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		h.log.Error("register: hash password", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	u := &models.User{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: hash,
		Role:         models.RoleContestant,
		Organization: req.Organization,
	}
	if err := h.users.Create(c.Request.Context(), u); err != nil {
		h.log.Error("register: create user", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	token, err := h.issueToken(u)
	if err != nil {
		h.log.Error("register: issue token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"token": token,
		"user":  u,
	})
}

// ─── Login ────────────────────────────────────────────────────────────────────

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login handles POST /api/v1/auth/login
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	u, err := h.users.GetByUsername(c.Request.Context(), req.Username)
	if err != nil {
		h.log.Error("login: get user", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if u == nil || !checkPassword(req.Password, u.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	token, err := h.issueToken(u)
	if err != nil {
		h.log.Error("login: issue token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"user":  u,
	})
}

// Me handles GET /api/v1/auth/me — returns the current user's profile.
func (h *AuthHandler) Me(c *gin.Context) {
	uid, ok := middleware.UserIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	u, err := h.users.GetByID(c.Request.Context(), uid)
	if err != nil || u == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, u)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func (h *AuthHandler) issueToken(u *models.User) (string, error) {
	claims := middleware.Claims{
		UserID: u.ID,
		Role:   u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", u.ID),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(72 * time.Hour)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(h.signingKey)
}

// hashPassword returns "hex(sha256(salt||password)):hex(salt)".
// Uses crypto/rand for the salt.
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(password))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum) + ":" + hex.EncodeToString(salt), nil
}

// checkPassword verifies a plaintext password against a stored hash.
func checkPassword(password, stored string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 {
		return false
	}
	salt, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(password))
	computed := hex.EncodeToString(h.Sum(nil))
	return computed == parts[0]
}
