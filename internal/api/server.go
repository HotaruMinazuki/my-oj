// Package api wires together the HTTP/WebSocket server for the OJ platform.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/api/handler"
	"github.com/your-org/my-oj/internal/api/middleware"
	"github.com/your-org/my-oj/internal/core/ranking"
	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/mq"
	"github.com/your-org/my-oj/internal/storage"
)

// ServerConfig holds all tunable knobs for the HTTP server.
type ServerConfig struct {
	Addr            string
	JWTSigningKey   []byte
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration
}

// Server is the top-level API server.
type Server struct {
	cfg        ServerConfig
	httpServer *http.Server
	log        *zap.Logger
	hub        *ranking.Hub
}

// NewServer constructs and wires the Gin engine, middleware, and all route handlers.
func NewServer(
	cfg ServerConfig,
	rdb *redis.Client,
	publisher mq.Publisher,
	store storage.ObjectStore,
	rankingService *ranking.RankingService,
	hub *ranking.Hub,
	submissions handler.SubmissionRepo,
	problems handler.ProblemRepo,
	log *zap.Logger,
) *Server {
	if cfg.ReadTimeout == 0 {
		cfg.ReadTimeout = 30 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 60 * time.Second
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = 15 * time.Second
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// ── Global middleware ──────────────────────────────────────────────────────
	r.Use(requestLogger(log))
	r.Use(gin.Recovery())
	r.Use(corsHeaders())

	// ── Handlers ───────────────────────────────────────────────────────────────
	rankingH    := handler.NewRankingHandler(hub, rdb, log)
	submissionH := handler.NewSubmissionHandler(submissions, problems, publisher, store, log)
	adminH      := handler.NewAdminHandler(rankingService, store, log)

	auth      := middleware.Auth(cfg.JWTSigningKey)
	adminOnly := middleware.RequireRole(models.RoleAdmin)

	// ── WebSocket ─────────────────────────────────────────────────────────────
	// No JWT on the WebSocket path — browsers can't set custom headers on native
	// WebSocket upgrades.  Authenticate via a short-lived token in the query string
	// if needed (implementation left as a follow-up hardening step).
	r.GET("/ws/ranking/:contest_id", rankingH.ServeWS)

	// ── Public REST API ───────────────────────────────────────────────────────
	v1 := r.Group("/api/v1")
	{
		// Ranking snapshot — read-only, no auth required for public contests.
		v1.GET("/contests/:contest_id/ranking", rankingH.GetSnapshot)

		// Authenticated routes
		authed := v1.Group("/", auth)
		{
			authed.GET("/contests/:contest_id/ranking/me", rankingH.GetUserRank)

			// Contest submissions
			authed.POST("/contests/:contest_id/submissions", submissionH.Submit)

			// Practice (out-of-contest) submissions
			authed.POST("/submissions", submissionH.SubmitPractice)
			authed.GET("/submissions/:id", submissionH.GetSubmission)
		}

		// Admin routes — require auth + admin role
		admin := v1.Group("/admin", auth, adminOnly)
		{
			// 滚榜: call repeatedly during post-contest ceremony
			admin.POST("/contests/:contest_id/unfreeze-next", adminH.UnfreezeNext)

			// Test-case management: upload a zip of .in/.out files for a problem
			admin.POST("/problems/:id/testcases", adminH.UploadTestcases)
		}
	}

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	return &Server{cfg: cfg, httpServer: srv, log: log, hub: hub}
}

// Run starts the HTTP listener and blocks until ctx is cancelled, then
// gracefully drains in-flight requests.
func (s *Server) Run(ctx context.Context) error {
	go s.hub.Run(ctx)

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("api server listening", zap.String("addr", s.cfg.Addr))
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		s.log.Info("api server shutting down")
		return s.httpServer.Shutdown(shutCtx)
	}
}

// ─── middleware helpers ───────────────────────────────────────────────────────

func requestLogger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		log.Info("http",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("ip", c.ClientIP()),
		)
	}
}

func corsHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
