package judger

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/your-org/my-oj/internal/judger/sandbox"
	"github.com/your-org/my-oj/internal/models"
)

// SpecialOrchestrator runs the contestant's program and then invokes a trusted
// checker binary to evaluate the output.
//
// Execution flow:
//
//	Session.Execute(contestant) ──▶ capture stdout to tmp file
//	                                        │
//	                            exec.Command(checker, input, output, answer)
//	                                        │
//	                                 parse checker stderr ──▶ Verdict
//
// The checker runs OUTSIDE the sandbox (it is trusted code supplied by the
// problem setter). It follows the Polygon/testlib convention:
//
//	checker <input_file> <output_file> <answer_file>
//
// Exit code 0 → AC; exit code 1 → WA; exit code 2 → PE (treated as WA here).
// The human-readable verdict is written to stderr.
type SpecialOrchestrator struct{}

// RunTestCase implements Orchestrator.
func (o *SpecialOrchestrator) RunTestCase(
	ctx context.Context,
	req *TestCaseRequest,
	session sandbox.Session,
) (*Verdict, error) {
	if req.JudgeConfig.CheckerPath == "" {
		return nil, fmt.Errorf("special: CheckerPath is empty for test case %d", req.TestCase.TestCaseID)
	}
	if len(req.RunCmd) == 0 {
		return nil, fmt.Errorf("special: RunCmd is empty")
	}

	// ── Step 1: run contestant's program ─────────────────────────────────────
	inputFile, err := os.Open(req.TestCase.InputPath)
	if err != nil {
		return nil, fmt.Errorf("special: open input %s: %w", req.TestCase.InputPath, err)
	}
	defer inputFile.Close()

	var outBuf bytes.Buffer

	result, err := session.Execute(ctx, &sandbox.ExecRequest{
		Executable: req.RunCmd[0],
		Args:       req.RunCmd[1:],
		Stdin:      inputFile,
		Stdout:     &outBuf,
		Stderr:     &bytes.Buffer{}, // discard contestant stderr
		Limits:     req.Limits,
	})
	if err != nil {
		return nil, fmt.Errorf("special: execute: %w", err)
	}

	// Non-OK sandbox status terminates before invoking the checker.
	if result.Status != sandbox.ExecOK {
		return &Verdict{
			Status:     execStatusToSubmission(result.Status),
			TimeUsedMs: result.TimeUsedMs,
			MemUsedKB:  result.MemUsedKB,
		}, nil
	}

	// ── Step 2: write contestant output to a temp file ────────────────────────
	tmpOut, err := os.CreateTemp("", "oj-spj-out-*")
	if err != nil {
		return nil, fmt.Errorf("special: create temp file: %w", err)
	}
	defer os.Remove(tmpOut.Name())

	if _, err := tmpOut.Write(outBuf.Bytes()); err != nil {
		return nil, fmt.Errorf("special: write temp output: %w", err)
	}
	tmpOut.Close()

	// ── Step 3: invoke checker outside the sandbox ────────────────────────────
	// Checker argv: checker <input> <contestant_output> <expected_output> [extra_args…]
	checkerArgs := make([]string, 0, 3+len(req.JudgeConfig.CheckerArgs))
	checkerArgs = append(checkerArgs,
		req.TestCase.InputPath,
		tmpOut.Name(),
		req.TestCase.OutputPath,
	)
	checkerArgs = append(checkerArgs, req.JudgeConfig.CheckerArgs...)

	var checkerStderr bytes.Buffer
	cmd := exec.CommandContext(ctx, req.JudgeConfig.CheckerPath, checkerArgs...)
	cmd.Stderr = &checkerStderr

	// Checker exit code alone is not the verdict; stderr carries the human-readable line.
	// We do not error on non-zero exit — that is the checker's way of signalling WA/PE.
	_ = cmd.Run()

	verdict := parseCheckerOutput(checkerStderr.String(), result)
	return verdict, nil
}

// parseCheckerOutput interprets the checker's stderr using the testlib convention:
//
//	first line: "ok …"        → AC
//	first line: "wrong answer"  → WA
//	first line: "FAIL …"       → SE (checker bug / testdata error)
//	first line: "points <n>"   → Partial Credit
//
// This is compatible with Codeforces/Polygon testlib checkers.
func parseCheckerOutput(stderr string, execResult *sandbox.ExecResult) *Verdict {
	v := &Verdict{
		TimeUsedMs: execResult.TimeUsedMs,
		MemUsedKB:  execResult.MemUsedKB,
	}

	firstLine := strings.ToLower(strings.TrimSpace(strings.SplitN(stderr, "\n", 2)[0]))
	v.JudgeMessage = strings.TrimSpace(strings.SplitN(stderr, "\n", 2)[0])

	switch {
	case strings.HasPrefix(firstLine, "ok"):
		v.Status = models.StatusAccepted
		v.Score = 1 // normalised; Strategy scales to actual points
	case strings.HasPrefix(firstLine, "points "):
		v.Status = models.StatusAccepted
		fmt.Sscanf(firstLine[7:], "%d", &v.Score)
	case strings.HasPrefix(firstLine, "fail"):
		// "FAIL" indicates a checker/testdata bug — surface as SE.
		v.Status = models.StatusSE
	default:
		// "wrong answer", "wrong output format", etc. → WA
		v.Status = models.StatusWrongAnswer
	}

	return v
}
