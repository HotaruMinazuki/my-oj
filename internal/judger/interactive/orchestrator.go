// Package interactive implements judger.Orchestrator for interactive and
// communication problems, translating TestCaseRequests into sandbox pipe topologies.
package interactive

import (
	"context"
	"fmt"
	"strings"

	"github.com/your-org/my-oj/internal/judger"
	"github.com/your-org/my-oj/internal/judger/sandbox"
	"github.com/your-org/my-oj/internal/models"
)

// ─── Interactive Orchestrator ─────────────────────────────────────────────────

// InteractiveOrchestrator handles problems where the contestant binary and a
// trusted interactor binary communicate via a bidirectional kernel pipe.
//
// I/O routing (wired by the sandbox backend):
//
//	contestant.stdout ──▶ interactor.stdin
//	interactor.stdout ──▶ contestant.stdin
//
// The test-case input file is delivered exclusively to the interactor.
// The interactor signals its verdict on its stderr (captured into InteractorOutput).
type InteractiveOrchestrator struct {
	// ParseVerdict converts the interactor's stderr into a judger.Verdict.
	// Defaults to DefaultVerdictParser; override for custom interactor protocols.
	ParseVerdict judger.VerdictParser
}

// RunTestCase implements judger.Orchestrator.
func (o *InteractiveOrchestrator) RunTestCase(
	ctx context.Context,
	req *judger.TestCaseRequest,
	session sandbox.Session,
) (*judger.Verdict, error) {
	if req.JudgeConfig.InteractorPath == "" {
		return nil, fmt.Errorf("interactive: InteractorPath is empty for test case %d", req.TestCase.TestCaseID)
	}
	if len(req.RunCmd) == 0 {
		return nil, fmt.Errorf("interactive: RunCmd is empty")
	}

	// The test-case input is fed to the interactor, not to the contestant.
	inputReader, err := openSharedFile(req.TestCase.InputPath)
	if err != nil {
		return nil, fmt.Errorf("interactive: open input %s: %w", req.TestCase.InputPath, err)
	}
	defer inputReader.Close()

	result, err := session.ExecutePair(ctx, &sandbox.PairExecRequest{
		// Contestant: subject to full contestant resource limits and seccomp policy.
		// Stdin/Stdout are nil — the backend creates the bidirectional pipe internally.
		Contestant: sandbox.ExecRequest{
			Executable: req.RunCmd[0],
			Args:       req.RunCmd[1:],
			Limits:     req.Limits,
		},
		// Interactor: trusted binary; needs file I/O for the input file.
		// Runs with relaxed syscall rules and a separate memory budget.
		Interactor: sandbox.ExecRequest{
			Executable: req.JudgeConfig.InteractorPath,
		},
		InteractorLimits: sandbox.ResourceLimits{
			// Give the interactor 2 s extra wall time beyond the contestant's cap
			// so it can always finish writing its verdict after the contestant exits.
			WallTimeLimitMs: req.Limits.WallTimeLimitMs + 2000,
			MemLimitKB:      256 * 1024, // 256 MB; interactors can be verbose
			MaxOpenFiles:    64,
		},
		InteractorInput: inputReader,
	})
	if err != nil {
		return nil, fmt.Errorf("interactive: ExecutePair: %w", err)
	}

	return o.ParseVerdict(result.InteractorOutput, result.Contestant), nil
}

// ─── Communication Orchestrator ───────────────────────────────────────────────

// CommOrchestrator handles problems where N contestant processes must cooperate
// via named unidirectional pipes described by JudgeConfig.CommChannels.
//
// All processes run the same compiled binary; they distinguish their role via
// argv[1] (process index, 0-based).
type CommOrchestrator struct {
	ParseVerdict judger.VerdictParser
}

// RunTestCase implements judger.Orchestrator.
func (o *CommOrchestrator) RunTestCase(
	ctx context.Context,
	req *judger.TestCaseRequest,
	session sandbox.Session,
) (*judger.Verdict, error) {
	n := req.JudgeConfig.CommProcessCount
	if n < 2 {
		return nil, fmt.Errorf("comm: CommProcessCount must be ≥ 2, got %d", n)
	}
	if len(req.RunCmd) == 0 {
		return nil, fmt.Errorf("comm: RunCmd is empty")
	}

	// All contestant processes run the same binary; argv[1] = process index.
	processes := make([]sandbox.ExecRequest, n)
	for i := range processes {
		processes[i] = sandbox.ExecRequest{
			Executable: req.RunCmd[0],
			Args:       append(req.RunCmd[1:], fmt.Sprintf("%d", i)),
			Limits:     req.Limits,
		}
	}

	// Translate model CommChannels → sandbox ChannelSpecs (direct field mapping).
	channels := make([]sandbox.ChannelSpec, len(req.JudgeConfig.CommChannels))
	for i, ch := range req.JudgeConfig.CommChannels {
		channels[i] = sandbox.ChannelSpec{
			Name:         ch.Name,
			From:         ch.From,
			To:           ch.To,
			BufferSizeKB: ch.BufferSizeKB,
		}
	}

	var grader *sandbox.ExecRequest
	if req.JudgeConfig.GraderPath != "" {
		grader = &sandbox.ExecRequest{Executable: req.JudgeConfig.GraderPath}
	}

	result, err := session.ExecuteGroup(ctx, &sandbox.GroupExecRequest{
		Processes:             processes,
		Channels:              channels,
		GraderProcess:         grader,
		GlobalWallTimeLimitMs: req.Limits.WallTimeLimitMs * 2,
	})
	if err != nil {
		return nil, fmt.Errorf("comm: ExecuteGroup: %w", err)
	}

	worst := worstProcess(result.Processes)
	return o.ParseVerdict(result.GraderOutput, worst), nil
}

// ─── Default Verdict Parser ───────────────────────────────────────────────────

// DefaultVerdictParser implements the Polygon/Codeforces interactor output protocol.
// It satisfies the judger.VerdictParser type and can be assigned directly:
//
//	&InteractiveOrchestrator{ParseVerdict: interactive.DefaultVerdictParser}
//
// Line format (first line of stderr, case-insensitive):
//
//	"AC"          → Accepted, full score (Score = TestCase.Score)
//	"WA [reason]" → Wrong Answer
//	"PC <n>"      → Partial Credit, n points awarded (OI/IOI)
func DefaultVerdictParser(stderr string, contestant sandbox.ExecResult) *judger.Verdict {
	// Contestant resource violations take precedence over interactor output.
	switch contestant.Status {
	case sandbox.ExecTLE, sandbox.ExecWallTLE:
		return &judger.Verdict{Status: models.StatusTLE, TimeUsedMs: contestant.TimeUsedMs, MemUsedKB: contestant.MemUsedKB}
	case sandbox.ExecMLE:
		return &judger.Verdict{Status: models.StatusMLE, TimeUsedMs: contestant.TimeUsedMs, MemUsedKB: contestant.MemUsedKB}
	case sandbox.ExecRE, sandbox.ExecSCViol:
		return &judger.Verdict{Status: models.StatusRE, TimeUsedMs: contestant.TimeUsedMs, MemUsedKB: contestant.MemUsedKB}
	case sandbox.ExecSE:
		return &judger.Verdict{Status: models.StatusSE}
	}

	// Parse first line of interactor stderr.
	line := strings.TrimSpace(strings.SplitN(stderr, "\n", 2)[0])
	upper := strings.ToUpper(line)

	v := &judger.Verdict{
		TimeUsedMs:   contestant.TimeUsedMs,
		MemUsedKB:    contestant.MemUsedKB,
		JudgeMessage: line,
	}

	switch {
	case upper == "AC":
		v.Status = models.StatusAccepted
		// Score = 1 (normalised); the scoring Strategy multiplies by MaxScore.
		v.Score = 1
	case strings.HasPrefix(upper, "PC "):
		v.Status = models.StatusAccepted
		fmt.Sscanf(line[3:], "%d", &v.Score)
	default:
		v.Status = models.StatusWrongAnswer
	}
	return v
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// worstProcess returns the ExecResult with the highest-severity status from a
// group of contestant processes. Used to produce a single representative verdict.
func worstProcess(results []sandbox.ExecResult) sandbox.ExecResult {
	sev := map[sandbox.ExecStatus]int{
		sandbox.ExecOK:      0,
		sandbox.ExecRE:      1,
		sandbox.ExecWallTLE: 2,
		sandbox.ExecTLE:     2,
		sandbox.ExecMLE:     3,
		sandbox.ExecSCViol:  4,
		sandbox.ExecSE:      5,
	}
	worst := results[0]
	for _, r := range results[1:] {
		if sev[r.Status] > sev[worst.Status] {
			worst = r
		}
	}
	return worst
}
