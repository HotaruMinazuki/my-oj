package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/your-org/my-oj/internal/api/middleware"
	appmail "github.com/your-org/my-oj/internal/mail"
	"github.com/your-org/my-oj/internal/models"
)

// AuthUserRepo is the storage interface needed by AuthHandler.
type AuthUserRepo interface {
	Create(ctx context.Context, u *models.User) error
	GetByUsername(ctx context.Context, username string) (*models.User, error)
	GetByEmail(ctx context.Context, email string) (*models.User, error)
	// GetByLogin resolves an identifier that may be a username or an email.
	GetByLogin(ctx context.Context, identifier string) (*models.User, error)
	GetByID(ctx context.Context, id models.ID) (*models.User, error)
	UpdatePassword(ctx context.Context, id models.ID, passwordHash string) error
}

// AuthHandler handles registration, login, and email password-reset.
type AuthHandler struct {
	users      AuthUserRepo
	signingKey []byte
	rdb        *redis.Client
	mailer     appmail.Mailer
	appName    string
	log        *zap.Logger
}

func NewAuthHandler(
	users AuthUserRepo,
	signingKey []byte,
	rdb *redis.Client,
	mailer appmail.Mailer,
	appName string,
	log *zap.Logger,
) *AuthHandler {
	if appName == "" {
		appName = "OJ"
	}
	return &AuthHandler{users: users, signingKey: signingKey, rdb: rdb, mailer: mailer, appName: appName, log: log}
}

// Password-reset tunables.
const (
	resetCodeTTL     = 10 * time.Minute
	resetCooldown    = 60 * time.Second
	maxResetAttempts = 5
)

// ─── Register ─────────────────────────────────────────────────────────────────

type registerReq struct {
	Username     string `json:"username"     binding:"required,min=3,max=32"`
	// Email is optional. When omitted the account starts unbound and can bind
	// one later from the profile page.
	Email        string `json:"email"        binding:"omitempty,email"`
	Password     string `json:"password"     binding:"required,min=6,max=72"`
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

	// Check email uniqueness (一个邮箱只能注册一个账号) when one is provided.
	var emailPtr *string
	if email := normalizeEmail(req.Email); email != "" {
		taken, err := h.users.GetByEmail(c.Request.Context(), email)
		if err != nil {
			h.log.Error("register: check email", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if taken != nil {
			c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
			return
		}
		emailPtr = &email
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		h.log.Error("register: hash password", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	u := &models.User{
		Username:     req.Username,
		Email:        emailPtr,
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
	// Username carries the login identifier — either a username or an email.
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

	u, err := h.users.GetByLogin(c.Request.Context(), strings.TrimSpace(req.Username))
	if err != nil {
		h.log.Error("login: get user", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if u == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "不存在该用户"})
		return
	}
	ok, needsRehash := checkPassword(req.Password, u.PasswordHash)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "密码错误"})
		return
	}
	// 老格式登录成功后透明升级为 bcrypt；失败只记日志，不阻断登录。
	if needsRehash {
		if nh, err := hashPassword(req.Password); err == nil {
			if err := h.users.UpdatePassword(c.Request.Context(), u.ID, nh); err != nil {
				h.log.Warn("rehash on login: update password", zap.Error(err), zap.Int64("user_id", int64(u.ID)))
			}
		}
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

// ─── Password reset (邮箱验证码找回) ───────────────────────────────────────────

type resetRequestReq struct {
	Identifier string `json:"identifier" binding:"required"` // 用户名或邮箱
}

// RequestPasswordReset emails a 6-digit code to the account's bound email.
// POST /api/v1/auth/password-reset/request — public (used by the login page).
func (h *AuthHandler) RequestPasswordReset(c *gin.Context) {
	var req resetRequestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()

	u, err := h.users.GetByLogin(ctx, strings.TrimSpace(req.Identifier))
	if err != nil {
		h.log.Error("reset request: lookup", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if u == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
		return
	}
	if u.Email == nil || *u.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该账号未绑定邮箱，无法通过邮箱找回密码"})
		return
	}
	email := *u.Email

	// 60s 重发冷却
	cooldownKey := resetCooldownKey(u.ID)
	if n, _ := h.rdb.Exists(ctx, cooldownKey).Result(); n > 0 {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "验证码发送过于频繁，请稍后再试"})
		return
	}

	code, err := genVerifyCode()
	if err != nil {
		h.log.Error("reset request: gen code", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if err := h.rdb.Set(ctx, resetCodeKey(u.ID), code, resetCodeTTL).Err(); err != nil {
		h.log.Error("reset request: store code", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	h.rdb.Set(ctx, cooldownKey, "1", resetCooldown)
	h.rdb.Del(ctx, resetAttemptsKey(u.ID))

	subject := fmt.Sprintf("【%s】密码重置验证码", h.appName)
	body := fmt.Sprintf(
		"您正在重置 %s 账号「%s」的登录密码。\r\n\r\n验证码：%s\r\n\r\n验证码 %d 分钟内有效，请勿泄露给他人。若非本人操作，请忽略此邮件。",
		h.appName, u.Username, code, int(resetCodeTTL.Minutes()),
	)
	if err := h.mailer.Send(email, subject, body); err != nil {
		h.log.Error("reset request: send email", zap.Error(err), zap.String("email", email))
		h.rdb.Del(ctx, resetCodeKey(u.ID), cooldownKey)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "验证码发送失败，请稍后再试"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "验证码已发送",
		"email":        maskEmail(email),
		"smtp_enabled": h.mailer.Enabled(),
	})
}

type resetConfirmReq struct {
	Identifier  string `json:"identifier"   binding:"required"`
	Code        string `json:"code"         binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6,max=72"`
}

// ConfirmPasswordReset verifies the code and sets the new password.
// POST /api/v1/auth/password-reset/confirm — public.
func (h *AuthHandler) ConfirmPasswordReset(c *gin.Context) {
	var req resetConfirmReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()

	u, err := h.users.GetByLogin(ctx, strings.TrimSpace(req.Identifier))
	if err != nil {
		h.log.Error("reset confirm: lookup", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if u == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "账号不存在"})
		return
	}

	codeKey := resetCodeKey(u.ID)
	stored, err := h.rdb.Get(ctx, codeKey).Result()
	if err == redis.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "验证码已过期或不存在，请重新获取"})
		return
	}
	if err != nil {
		h.log.Error("reset confirm: get code", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if strings.TrimSpace(req.Code) != stored {
		attemptsKey := resetAttemptsKey(u.ID)
		n, _ := h.rdb.Incr(ctx, attemptsKey).Result()
		h.rdb.Expire(ctx, attemptsKey, resetCodeTTL)
		if n >= maxResetAttempts {
			h.rdb.Del(ctx, codeKey)
			c.JSON(http.StatusBadRequest, gin.H{"error": "验证码错误次数过多，请重新获取"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "验证码不正确"})
		return
	}

	hash, err := hashPassword(req.NewPassword)
	if err != nil {
		h.log.Error("reset confirm: hash", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if err := h.users.UpdatePassword(ctx, u.ID, hash); err != nil {
		h.log.Error("reset confirm: update password", zap.Error(err), zap.Int64("user_id", u.ID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	h.rdb.Del(ctx, codeKey, resetAttemptsKey(u.ID), resetCooldownKey(u.ID))

	c.JSON(http.StatusOK, gin.H{"message": "密码已重置，请使用新密码登录"})
}

func resetCodeKey(id models.ID) string     { return fmt.Sprintf("pwdreset:code:%d", id) }
func resetCooldownKey(id models.ID) string { return fmt.Sprintf("pwdreset:cd:%d", id) }
func resetAttemptsKey(id models.ID) string { return fmt.Sprintf("pwdreset:attempts:%d", id) }

// genVerifyCode returns a 6-digit numeric verification code.
func genVerifyCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// maskEmail partially hides an email for display, e.g. ab***@example.com.
func maskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return "***"
	}
	local, domain := email[:at], email[at:]
	switch {
	case len(local) <= 1:
		return local + "***" + domain
	case len(local) <= 3:
		return local[:1] + "***" + domain
	default:
		return local[:2] + "***" + domain
	}
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

// normalizeEmail trims and lowercases an email for consistent storage and
// case-insensitive uniqueness. Returns "" for an empty/blank input.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validEmail reports whether s parses as a single RFC 5322 address.
func validEmail(s string) bool {
	_, err := mail.ParseAddress(s)
	return err == nil
}

// bcryptCost is the bcrypt work factor for new password hashes.
const bcryptCost = 12

// hashPassword returns a bcrypt hash of the password (新格式，$2b$ 开头).
func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// checkPassword verifies a plaintext password against a stored hash.
// 兼容老格式 hex(sha256(salt||pw)):hex(salt) 与新格式 bcrypt。
// needsRehash=true 表示校验通过但仍是老格式，调用方应升级。
func checkPassword(password, stored string) (ok bool, needsRehash bool) {
	if strings.HasPrefix(stored, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)) == nil, false
	}
	return legacyCheck(password, stored), true
}

// legacyCheck verifies a password against the old salted SHA-256 format.
func legacyCheck(password, stored string) bool {
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
	return hex.EncodeToString(h.Sum(nil)) == parts[0]
}
