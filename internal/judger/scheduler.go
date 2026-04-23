package judger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/judger/sandbox"
	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/mq"
)

// Scheduler is the judger node's central dispatch loop.
//
// Architecture:
//
//	cfg.Workers goroutines each independently pull from MQ → judge → publish result.
//	They share no mutable state (sandbox sessions are per-task), making the node
//	horizontally scalable without synchronisation overhead.
type Scheduler struct {
	consumer       mq.Consumer
	publisher      mq.Publisher
	sb             sandbox.Sandbox
	orchestrators  *OrchestratorRegistry
	compiler       *Compiler
	testcaseCache  *TestcaseCache
	cfg            JudgerConfig
	log            *zap.Logger
	nodeID         string
}

// NewScheduler constructs a Scheduler. All dependencies are injected; the Scheduler
// owns nothing that requires lifecycle management beyond what is passed in.
func NewScheduler(
	consumer mq.Consumer,
	publisher mq.Publisher,
	sb sandbox.Sandbox,
	reg *OrchestratorRegistry,
	compiler *Compiler,
	testcaseCache *TestcaseCache,
	cfg JudgerConfig,
	log *zap.Logger,
) *Scheduler {
	hostname, _ := os.Hostname()
	if cfg.GlobalTimeout == 0 {
		cfg.GlobalTimeout = time.Duration(cfg.GlobalTimeoutSec) * time.Second
	}
	if cfg.GlobalTimeout == 0 {
		cfg.GlobalTimeout = 5 * time.Minute
	}

	return &Scheduler{
		consumer:      consumer,
		publisher:     publisher,
		sb:            sb,
		orchestrators: reg,
		compiler:      compiler,
		testcaseCache: testcaseCache,
		cfg:           cfg,
		log:           log,
		nodeID:        fmt.Sprintf("%s-%d", hostname, os.Getpid()),
	}
}

// Run launches cfg.Workers goroutines and blocks until ctx is cancelled.
// Each worker independently subscribes; the MQ consumer group ensures
// no two workers process the same task.
func (s *Scheduler) Run(ctx context.Context) error {
	s.log.Info("judger scheduler starting",
		zap.String("node_id", s.nodeID),
		zap.Int("workers", s.cfg.Workers),
		zap.Duration("global_timeout", s.cfg.GlobalTimeout),
	)

	var wg sync.WaitGroup
	errCh := make(chan error, s.cfg.Workers)

	for i := range s.cfg.Workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Each worker runs its own Subscribe loop. If Subscribe returns,
			// it means ctx was cancelled (graceful shutdown) or a fatal MQ error.
			err := s.consumer.Subscribe(ctx, mq.QueueJudgeTasks,
				func(ctx context.Context, msg mq.Message) error {
					return s.handleMessage(ctx, msg, workerID)
				},
			)
			errCh <- err
		}(i)
	}

	// Wait for first worker exit; cancel remaining via parent ctx.
	err := <-errCh
	wg.Wait()
	return err
}

// handleMessage is the per-message entry point, called by the Consumer goroutine.
//
// Safety contracts:
//  1. deferred recover() converts any panic (including sandbox backend crashes)
//     into a SystemError result — the judger node never goes down from a single task.
//  2. context.WithTimeout enforces a global hard cap per task, preventing a hung
//     sandbox from starving other workers indefinitely.
//  3. Always returns nil so the MQ message is ACK'd even on failure.
//     (Non-nil return would leave the message in PEL, causing infinite retry.)
func (s *Scheduler) handleMessage(ctx context.Context, msg mq.Message, workerID int) (retErr error) {
	task, err := mq.UnmarshalTask(msg.Payload)
	if err != nil {
		// Malformed payload: ACK and discard — retrying won't help.
		s.log.Error("malformed task payload; discarding",
			zap.String("msg_id", msg.ID), zap.Error(err))
		return nil
	}

	log := s.log.With(
		zap.String("task_id", task.TaskID),
		zap.Int64("submission_id", task.SubmissionID),
		zap.String("language", string(task.Language)),
		zap.Int("worker", workerID),
	)

	// Hard deadline: prevents a rogue sandbox from occupying a worker forever.
	taskCtx, cancel := context.WithTimeout(ctx, s.cfg.GlobalTimeout)
	defer cancel()

	// ── Panic recovery ────────────────────────────────────────────────────────
	// Catches: nil-pointer dereferences in orchestrators, unexpected sandbox panics,
	// runtime errors from malformed test data, etc.
	defer func() {
		if r := recover(); r != nil {
			log.Error("PANIC in judge pipeline — recovered",
				zap.Any("panic_value", r),
				zap.ByteString("stack", debug.Stack()),
			)
			// Publish SE so the contestant sees feedback rather than a hung submission.
			// Use a fresh background context — taskCtx may already be cancelled.
			_ = s.publishSE(context.Background(), &task.JudgeTask,
				fmt.Sprintf("internal error (panic): %v", r))
			retErr = nil // ACK the message; do not retry a panicking task
		}
	}()

	log.Info("task dequeued")
	if err := s.runStateMachine(taskCtx, log, &task.JudgeTask); err != nil {
		log.Error("judge pipeline error; publishing SE", zap.Error(err))
		_ = s.publishSE(context.Background(), &task.JudgeTask, err.Error())
	}
	return nil
}

// runStateMachine executes the judge state machine for one task.
//
// States:
//
//	PENDING → COMPILING ─── CE ──▶ publishResult(CE) ──▶ done
//	                    └── OK ──▶ JUDGING ──▶ (per test case) ──▶ publishResult ──▶ done
func (s *Scheduler) runStateMachine(ctx context.Context, log *zap.Logger, task *models.JudgeTask) error {
	// Create an ephemeral work directory for this task.
	// All sandbox file I/O is confined here; cleaned up unconditionally on return.
	workDir := filepath.Join(s.cfg.WorkBaseDir, uuid.New().String())
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("mkdir workdir: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(workDir); err != nil {
			log.Warn("failed to clean up workdir", zap.String("dir", workDir), zap.Error(err))
		}
	}()

	// Prepare sandbox session.
	session, err := s.sb.Prepare(ctx, &sandbox.SessionConfig{
		WorkDir:        workDir,
		MaxProcesses:   maxProcesses(task.JudgeType),
		NetworkEnabled: false,
	})
	if err != nil {
		return fmt.Errorf("sandbox.Prepare: %w", err)
	}
	defer func() {
		if err := session.Release(); err != nil {
			log.Warn("session.Release failed", zap.String("session", session.ID()), zap.Error(err))
		}
	}()

	// ── Stage 0: TESTCASE PREFETCH ───────────────────────────────────────────
	// Download (or validate cache for) the testcase zip before touching the
	// sandbox.  This step is intentionally outside the sandbox session:
	//   • It talks to MinIO, not the sandboxed process.
	//   • Multiple workers share the cache; only one download runs per problem.
	log.Info("stage: TESTCASE PREFETCH", zap.Int64("problem_id", task.ProblemID))

	tcDir, err := s.testcaseCache.EnsureTestcases(ctx, task.ProblemID)
	if err != nil {
		return fmt.Errorf("testcase prefetch: %w", err)
	}

	// Resolve relative InputPath / OutputPath stored in JudgeTask to absolute
	// local paths within the extracted testcase directory.
	// Convention: the zip is extracted flat; filenames are "{ordinal}.in" / "{ordinal}.out".
	for i := range task.TestCases {
		tc := &task.TestCases[i]
		if tc.InputPath != "" && !filepath.IsAbs(tc.InputPath) {
			tc.InputPath = filepath.Join(tcDir, tc.InputPath)
		}
		if tc.OutputPath != "" && !filepath.IsAbs(tc.OutputPath) {
			tc.OutputPath = filepath.Join(tcDir, tc.OutputPath)
		}
	}

	// ── Stage 1: COMPILING ────────────────────────────────────────────────────
	log.Info("stage: COMPILING", zap.String("lang", string(task.Language)))

	compileResult, err := s.compiler.Compile(ctx, task.Language, task.SourceCodePath, workDir, session)
	if err != nil {
		// Sandbox-level failure during compile → SE (not CE).
		return fmt.Errorf("compile (SE): %w", err)
	}

	if !compileResult.Success {
		// Compile Error: terminal state. Do not enter JUDGING.
		log.Info("compile error; publishing CE")
		return s.publishResult(ctx, &mq.ResultMessage{
			TaskID:       task.TaskID,
			SubmissionID: task.SubmissionID,
			UserID:       task.UserID,
			ProblemID:    task.ProblemID,
			ContestID:    task.ContestID,
			Status:       models.StatusCE,
			CompileLog:   compileResult.Log,
			JudgeNodeID:  s.nodeID,
			JudgedAt:     time.Now().UTC(),
		})
	}

	log.Info("compile OK", zap.Strings("run_cmd", compileResult.RunCmd))

	// ── Stage 2: JUDGING ──────────────────────────────────────────────────────
	log.Info("stage: JUDGING", zap.Int("test_cases", len(task.TestCases)))

	orch, err := s.orchestrators.Get(task.JudgeType)
	if err != nil {
		return err // unknown JudgeType → SE
	}

	tcResults := make([]models.TestCaseResult, 0, len(task.TestCases))
	var maxTimeMs, maxMemKB int64

	for i, tc := range task.TestCases {
		// Respect global deadline; remaining test cases are not run on timeout.
		if ctx.Err() != nil {
			log.Warn("global timeout hit mid-judging; aborting remaining test cases",
				zap.Int("completed", i), zap.Int("total", len(task.TestCases)))
			break
		}

		tcLog := log.With(zap.Int("tc_index", i), zap.Int64("tc_id", tc.TestCaseID))
		tcLog.Debug("judging test case")

		verdict, err := orch.RunTestCase(ctx, &TestCaseRequest{
			RunCmd:      compileResult.RunCmd,
			TestCase:    tc,
			JudgeConfig: task.JudgeConfig,
			Limits:      resolvedLimits(task),
		}, session)

		if err != nil {
			tcLog.Error("orchestrator error", zap.Error(err))
			tcResults = append(tcResults, models.TestCaseResult{
				TestCaseID: tc.TestCaseID,
				GroupID:    tc.GroupID,
				Status:     models.StatusSE,
			})
			continue
		}

		if verdict.TimeUsedMs > maxTimeMs {
			maxTimeMs = verdict.TimeUsedMs
		}
		if verdict.MemUsedKB > maxMemKB {
			maxMemKB = verdict.MemUsedKB
		}

		tcResults = append(tcResults, models.TestCaseResult{
			TestCaseID:    tc.TestCaseID,
			GroupID:       tc.GroupID,
			Status:        verdict.Status,
			TimeUsedMs:    verdict.TimeUsedMs,
			MemUsedKB:     verdict.MemUsedKB,
			Score:         verdict.Score,
			CheckerOutput: truncate(verdict.JudgeMessage, 4096),
		})

		tcLog.Info("test case judged",
			zap.String("status", string(verdict.Status)),
			zap.Int64("time_ms", verdict.TimeUsedMs),
			zap.Int64("mem_kb", verdict.MemUsedKB),
		)
	}

	// ── Stage 3: Aggregate & publish ─────────────────────────────────────────
	finalStatus, finalScore := aggregate(tcResults, task.JudgeType)

	log.Info("judging complete",
		zap.String("status", string(finalStatus)),
		zap.Int("score", finalScore),
		zap.Int64("max_time_ms", maxTimeMs),
	)

	return s.publishResult(ctx, &mq.ResultMessage{
		TaskID:          task.TaskID,
		SubmissionID:    task.SubmissionID,
		UserID:          task.UserID,
		ProblemID:       task.ProblemID,
		ContestID:       task.ContestID,
		Status:          finalStatus,
		Score:           finalScore,
		TimeUsedMs:      maxTimeMs,
		MemUsedKB:       maxMemKB,
		CompileLog:      compileResult.Log,
		TestCaseResults: tcResults,
		JudgeNodeID:     s.nodeID,
		JudgedAt:        time.Now().UTC(),
	})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (s *Scheduler) publishResult(ctx context.Context, r *mq.ResultMessage) error {
	payload, err := mq.MarshalResult(r)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if _, err := s.publisher.Publish(ctx, mq.QueueJudgeResults, payload); err != nil {
		return fmt.Errorf("publish result: %w", err)
	}
	return nil
}

func (s *Scheduler) publishSE(ctx context.Context, task *models.JudgeTask, msg string) error {
	return s.publishResult(ctx, &mq.ResultMessage{
		TaskID:       task.TaskID,
		SubmissionID: task.SubmissionID,
		UserID:       task.UserID,
		ProblemID:    task.ProblemID,
		ContestID:    task.ContestID,
		Status:       models.StatusSE,
		JudgeMessage: msg,
		JudgeNodeID:  s.nodeID,
		JudgedAt:     time.Now().UTC(),
	})
}

// maxProcesses returns the SessionConfig.MaxProcesses ceiling for a given JudgeType.
func maxProcesses(jt models.JudgeType) int {
	switch jt {
	case models.JudgeInteractive:
		return 2 // contestant + interactor
	case models.JudgeCommunication:
		return 16 // upper bound; actual count from JudgeConfig.CommProcessCount
	default:
		return 1
	}
}

// resolvedLimits builds the ResourceLimits for contestant execution.
// The time limit is already pre-scaled by the language multiplier by the API server
// when building the JudgeTask.
func resolvedLimits(task *models.JudgeTask) sandbox.ResourceLimits {
	return sandbox.ResourceLimits{
		TimeLimitMs:     task.TimeLimitMs,
		WallTimeLimitMs: task.TimeLimitMs*2 + 1000, // generous wall time cap
		MemLimitKB:      task.MemLimitKB,
		FileSizeKB:      64 * 1024, // 64 MB output file cap
		MaxOpenFiles:    32,
		MaxChildProcesses: 1,
	}
}

func truncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "...(truncated)"
}
