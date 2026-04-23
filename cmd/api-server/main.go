// Command api-server is the HTTP/WebSocket frontend for the OJ platform.
//
// It accepts code submissions, uploads source code to MinIO, enqueues JudgeTasks,
// consumes JudgeResults to update the live scoreboard and Postgres, and streams
// rank deltas to connected browsers over WebSocket.
//
// Start with:
//
//	./api-server \
//	  -addr         :8080 \
//	  -dsn          "host=localhost port=5432 user=oj password=oj dbname=oj sslmode=disable" \
//	  -redis-addr   localhost:6379 \
//	  -minio-addr   localhost:9000 \
//	  -minio-key    minioadmin \
//	  -minio-secret minioadmin \
//	  -jwt-key      <secret>
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/your-org/my-oj/internal/api"
	"github.com/your-org/my-oj/internal/core/contest"
	"github.com/your-org/my-oj/internal/core/ranking"
	"github.com/your-org/my-oj/internal/infra/postgres"
	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/mq"
	mqredis "github.com/your-org/my-oj/internal/mq/redis"
	"github.com/your-org/my-oj/internal/storage"
)

func main() {
	addr        := flag.String("addr",         ":8080",          "HTTP listen address")
	dsn         := flag.String("dsn",          "",               "PostgreSQL DSN (required)")
	redisAddr   := flag.String("redis-addr",   "localhost:6379", "Redis host:port")
	minioAddr   := flag.String("minio-addr",   "localhost:9000", "MinIO host:port")
	minioKey    := flag.String("minio-key",    "minioadmin",     "MinIO access key")
	minioSecret := flag.String("minio-secret", "minioadmin",     "MinIO secret key")
	minioSSL    := flag.Bool("minio-ssl",      false,            "Use TLS for MinIO connection")
	jwtKey      := flag.String("jwt-key",      "",               "HMAC-SHA256 key for JWT validation (required)")
	logLevel    := flag.String("log-level",    "info",           "Zap log level (debug|info|warn|error)")
	flag.Parse()

	if *jwtKey == "" {
		fmt.Fprintln(os.Stderr, "api-server: -jwt-key is required")
		os.Exit(1)
	}
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "api-server: -dsn is required")
		os.Exit(1)
	}

	log := buildLogger(*logLevel)
	defer log.Sync() //nolint:errcheck

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── PostgreSQL connection pool ────────────────────────────────────────────
	db, err := postgres.Open(*dsn)
	if err != nil {
		log.Fatal("postgres open", zap.Error(err))
	}
	if err := db.PingContext(ctx); err != nil {
		log.Fatal("postgres ping", zap.Error(err))
	}
	defer db.Close()
	log.Info("PostgreSQL connected")

	// ── Object storage (MinIO) ────────────────────────────────────────────────
	store, err := storage.NewMinio(storage.MinioConfig{
		Endpoint:  *minioAddr,
		AccessKey: *minioKey,
		SecretKey: *minioSecret,
		UseSSL:    *minioSSL,
	})
	if err != nil {
		log.Fatal("minio init", zap.Error(err))
	}
	for _, bucket := range []string{storage.BucketSubmissions, storage.BucketTestcases} {
		if err := store.EnsureBucket(ctx, bucket); err != nil {
			log.Fatal("ensure bucket", zap.String("bucket", bucket), zap.Error(err))
		}
	}
	log.Info("MinIO ready", zap.String("endpoint", *minioAddr))

	// ── Redis ─────────────────────────────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:         *redisAddr,
		PoolSize:     32,
		MinIdleConns: 8,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal("redis ping failed", zap.Error(err))
	}

	baseConfig := mqredis.Config{
		Addr:         *redisAddr,
		ConsumerName: hostname(),
	}

	// Publisher: API server → judger nodes
	publisherCfg := baseConfig
	publisherCfg.ConsumerGroup = "api-server-pub"
	publisher, err := mqredis.New(publisherCfg, log)
	if err != nil {
		log.Fatal("publisher init", zap.Error(err))
	}

	// Ranking consumer: receives every judge result to update the scoreboard.
	rankCfg := baseConfig
	rankCfg.ConsumerGroup = "ranker"
	rankCfg.ConsumerName = hostname() + "-ranking"
	rankConsumer, err := mqredis.New(rankCfg, log)
	if err != nil {
		log.Fatal("rank consumer init", zap.Error(err))
	}

	// DB result consumer: writes judge results back to Postgres.
	dbCfg := baseConfig
	dbCfg.ConsumerGroup = "api-server-results"
	dbConsumer, err := mqredis.New(dbCfg, log)
	if err != nil {
		log.Fatal("db consumer init", zap.Error(err))
	}

	// ── Repositories ─────────────────────────────────────────────────────────
	submissionRepo := postgres.NewSubmissionRepo(db)
	problemRepo    := postgres.NewProblemRepo(db)
	contestLoader  := postgres.NewContestMetaLoader(db)

	// ── Strategy registry + ranking pipeline ──────────────────────────────────
	registry := contest.NewRegistry()
	rankingService := ranking.NewRankingService(
		rankConsumer, rdb, registry, contestLoader, log,
	)
	hub := ranking.NewHub(rdb, log)

	// ── API server ────────────────────────────────────────────────────────────
	srv := api.NewServer(
		api.ServerConfig{
			Addr:            *addr,
			JWTSigningKey:   []byte(*jwtKey),
			ReadTimeout:     30 * time.Second,
			WriteTimeout:    60 * time.Second,
			ShutdownTimeout: 15 * time.Second,
		},
		rdb,
		publisher,
		store,
		rankingService,
		hub,
		submissionRepo,
		problemRepo,
		log,
	)

	// ── Run services concurrently ─────────────────────────────────────────────
	errCh := make(chan error, 3)

	go func() {
		if err := rankingService.Run(ctx); err != nil {
			errCh <- fmt.Errorf("ranking service: %w", err)
		}
	}()

	// DB result consumer: persists judge results into Postgres.
	// Each ResultMessage maps 1-to-1 onto the mutable columns of a Submission row.
	go func() {
		if err := dbConsumer.Subscribe(ctx, mq.QueueJudgeResults,
			func(ctx context.Context, msg mq.Message) error {
				result, err := mq.UnmarshalResult(msg.Payload)
				if err != nil {
					log.Warn("malformed result payload; skipping", zap.Error(err))
					return nil // don't NACK — bad payloads are not retryable
				}

				sub := &models.Submission{
					ID:              result.SubmissionID,
					Status:          result.Status,
					Score:           result.Score,
					TimeUsedMs:      result.TimeUsedMs,
					MemUsedKB:       result.MemUsedKB,
					CompileLog:      result.CompileLog,
					JudgeMessage:    result.JudgeMessage,
					TestCaseResults: models.TestCaseResults(result.TestCaseResults),
					JudgeNodeID:     result.JudgeNodeID,
				}
				if err := submissionRepo.Update(ctx, sub); err != nil {
					log.Error("persist judge result",
						zap.Int64("submission_id", result.SubmissionID),
						zap.Error(err),
					)
					// Return error so the MQ layer can NACK and redeliver.
					return fmt.Errorf("update submission %d: %w", result.SubmissionID, err)
				}

				log.Info("submission updated",
					zap.Int64("submission_id", result.SubmissionID),
					zap.String("status", string(result.Status)),
					zap.Int64("time_ms", result.TimeUsedMs),
					zap.Int64("mem_kb", result.MemUsedKB),
				)
				return nil
			},
		); err != nil {
			errCh <- fmt.Errorf("db consumer: %w", err)
		}
	}()

	go func() {
		if err := srv.Run(ctx); err != nil {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		log.Error("service error; initiating shutdown", zap.Error(err))
		stop()
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	log.Info("api-server stopped")
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func buildLogger(level string) *zap.Logger {
	var lvl zapcore.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = zapcore.InfoLevel
	}
	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	l, _ := cfg.Build()
	return l
}

func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		return "api"
	}
	return h
}
