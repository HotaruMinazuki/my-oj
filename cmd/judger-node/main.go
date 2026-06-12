// Command judger-node is the standalone judger node process.
// It pulls JudgeTasks from Redis Streams, downloads contestant source from MinIO,
// runs the judge pipeline inside nsjail, and publishes JudgeResults back to Redis.
//
// Start with:
//
//	./judger-node \
//	  -langs   configs/languages.yaml \
//	  -redis   127.0.0.1:6379 \
//	  -minio-addr   localhost:9000 \
//	  -minio-key    minioadmin \
//	  -minio-secret minioadmin \
//	  -workdir /tmp/oj-judge \
//	  -workers 2
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/your-org/my-oj/internal/judger"
	"github.com/your-org/my-oj/internal/judger/interactive"
	nsjailsandbox "github.com/your-org/my-oj/internal/judger/sandbox/nsjail"
	mqredis "github.com/your-org/my-oj/internal/mq/redis"
	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/storage"
)

func main() {
	langConfigPath := flag.String("langs",         "configs/languages.yaml",       "path to language config file")
	redisAddr      := flag.String("redis",         "127.0.0.1:6379",               "Redis host:port")
	minioAddr      := flag.String("minio-addr",    "localhost:9000",               "MinIO host:port")
	minioKey       := flag.String("minio-key",     "minioadmin",                   "MinIO access key")
	minioSecret    := flag.String("minio-secret",  "minioadmin",                   "MinIO secret key")
	minioSSL       := flag.Bool("minio-ssl",       false,                          "Use TLS for MinIO")
	workBaseDir    := flag.String("workdir",       "/tmp/oj-judge",                "sandbox work directory base")
	workers        := flag.Int("workers",          2,                              "concurrent judging workers")
	nsjailBin      := flag.String("nsjail",        "/usr/local/bin/nsjail",        "path to nsjail binary")
	seccompPolicy  := flag.String("seccomp",       "configs/seccomp/default.bpf", "seccomp BPF policy path")
	cgroupMode     := flag.String("cgroup",        "auto",                         "cgroup version: auto|v1|v2")
	flag.Parse()

	log := buildLogger()
	defer log.Sync() //nolint:errcheck

	// ── Object storage (MinIO) ────────────────────────────────────────────────
	// The judger uses MinIO exclusively to download contestant source code.
	// It never writes to the submissions bucket (read-only from its perspective).
	store, err := storage.NewMinio(storage.MinioConfig{
		Endpoint:  *minioAddr,
		AccessKey: *minioKey,
		SecretKey: *minioSecret,
		UseSSL:    *minioSSL,
	})
	if err != nil {
		log.Fatal("minio init", zap.Error(err))
	}
	log.Info("MinIO connected", zap.String("endpoint", *minioAddr))

	// ── Load language configs ─────────────────────────────────────────────────
	langCfgs, err := judger.LoadLangConfigs(*langConfigPath)
	if err != nil {
		log.Fatal("load language configs", zap.Error(err))
	}
	log.Info("languages loaded", zap.Int("count", len(langCfgs)))

	// ── Connect to Redis Streams ──────────────────────────────────────────────
	hostname, _ := os.Hostname()
	consumerName := fmt.Sprintf("%s-%d", hostname, os.Getpid())

	stream, err := mqredis.New(mqredis.Config{
		Addr:          *redisAddr,
		ConsumerGroup: "judger-nodes",
		ConsumerName:  consumerName,
	}, log)
	if err != nil {
		log.Fatal("redis connect", zap.Error(err))
	}
	defer stream.Close()
	log.Info("connected to Redis", zap.String("addr", *redisAddr))

	// ── Detect cgroup version ─────────────────────────────────────────────────
	// On hosts running cgroup v1 (older kernels, some cloud images), hardcoding
	// v2 makes nsjail fail with "cannot mount cgroup". Probe the unified hierarchy
	// marker — /sys/fs/cgroup/cgroup.controllers only exists under v2.
	cgroupV2 := true
	switch *cgroupMode {
	case "v1":
		cgroupV2 = false
	case "v2":
		cgroupV2 = true
	case "auto", "":
		if _, statErr := os.Stat("/sys/fs/cgroup/cgroup.controllers"); statErr != nil {
			cgroupV2 = false
			log.Warn("cgroup v2 not detected; falling back to v1",
				zap.String("probe", "/sys/fs/cgroup/cgroup.controllers"),
				zap.Error(statErr))
		}
	default:
		log.Fatal("invalid -cgroup value", zap.String("got", *cgroupMode))
	}
	log.Info("cgroup mode resolved", zap.Bool("v2", cgroupV2))

	// Probe whether nsjail will actually be able to create per-task cgroups.
	// nsjail treats cgroup setup failure as fatal for the execution, so an
	// unwritable hierarchy (e.g., container without sufficient privileges)
	// would otherwise fail EVERY submission. Degrade to rlimit-only enforcement
	// instead, loudly.
	disableCgroup := false
	if cgroupV2 {
		probeDir := "/sys/fs/cgroup/oj-judge-probe"
		if err := os.Mkdir(probeDir, 0o755); err != nil && !os.IsExist(err) {
			disableCgroup = true
			log.Warn("cgroup v2 hierarchy not writable; disabling cgroup limits "+
				"(memory/pids fall back to rlimits)", zap.Error(err))
		} else {
			_ = os.Remove(probeDir)
		}
	}

	// ── Build nsjail sandbox ──────────────────────────────────────────────────
	sb, err := nsjailsandbox.New(nsjailsandbox.Config{
		BinaryPath:        *nsjailBin,
		SeccompPolicyPath: *seccompPolicy,
		CgroupParent:      "oj-judge",
		CgroupV2:          cgroupV2,
		DisableCgroup:     disableCgroup,
	}, log)
	if err != nil {
		log.Fatal("nsjail init", zap.Error(err))
	}

	// ── Register orchestrators ────────────────────────────────────────────────
	reg := judger.NewOrchestratorRegistry()
	reg.Register(models.JudgeStandard, &judger.StandardOrchestrator{})
	reg.Register(models.JudgeSpecial, &judger.SpecialOrchestrator{})
	reg.Register(models.JudgeInteractive, &interactive.InteractiveOrchestrator{
		ParseVerdict: interactive.DefaultVerdictParser,
	})
	reg.Register(models.JudgeCommunication, &interactive.CommOrchestrator{
		ParseVerdict: interactive.DefaultVerdictParser,
	})

	// ── Compiler — uses MinIO to download source before compilation ──────────
	compiler := judger.NewCompiler(langCfgs, store)

	// ── Testcase cache ────────────────────────────────────────────────────────
	// Downloaded zips are extracted to {workBaseDir}/testcases/{problemID}/.
	// The LRU keeps at most maxCachedProblems problem dirs on disk; older entries
	// are evicted automatically.  Prune() removes leftovers from previous runs.
	tcBaseDir := *workBaseDir + "/testcases"
	const maxCachedProblems = 200
	tcCache := judger.NewTestcaseCache(tcBaseDir, maxCachedProblems, store, log)
	tcCache.Prune() // clean up stale dirs from any previous judger run

	// ── Assemble and start scheduler ──────────────────────────────────────────
	scheduler := judger.NewScheduler(
		stream, // Consumer
		stream, // Publisher (same client; Redis Streams support both roles)
		sb,
		reg,
		compiler,
		tcCache,
		judger.JudgerConfig{
			Workers:       *workers,
			WorkBaseDir:   *workBaseDir,
			GlobalTimeout: 5 * time.Minute,
		},
		log,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("judger node ready",
		zap.String("consumer", consumerName),
		zap.Int("workers", *workers),
	)

	if err := scheduler.Run(ctx); err != nil && err != context.Canceled {
		log.Error("scheduler exited with error", zap.Error(err))
		os.Exit(1)
	}

	log.Info("judger node shut down cleanly")
}

func buildLogger() *zap.Logger {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	l, err := cfg.Build(zap.WithCaller(true))
	if err != nil {
		panic(err)
	}
	return l
}
