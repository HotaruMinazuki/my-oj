package judger

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/your-org/my-oj/internal/judger/sandbox"
	"github.com/your-org/my-oj/internal/models"
)

// StandardOrchestrator judges by comparing contestant stdout with the expected
// output file using whitespace-insensitive token comparison.
//
// Execution flow per test case:
//
//	open input file ──▶ Session.Execute() ──▶ capture stdout
//	                                              │
//	                                   ┌──────────▼──────────┐
//	                              sandbox exit status?
//	                             TLE/MLE/RE/etc. │  ExecOK
//	                                             │     │
//	                                    return   │  tokensEqual(stdout, expected)
//	                                    verdict  │     ├── true  → AC
//	                                             │     └── false → WA + hint
type StandardOrchestrator struct{}

// RunTestCase implements Orchestrator.
func (o *StandardOrchestrator) RunTestCase(
	ctx context.Context,
	req *TestCaseRequest,
	session sandbox.Session,
) (*Verdict, error) {
	if len(req.RunCmd) == 0 {
		return nil, fmt.Errorf("standard: RunCmd is empty")
	}

	// Open test-case input from shared storage.
	inputFile, err := os.Open(req.TestCase.InputPath)
	if err != nil {
		return nil, fmt.Errorf("standard: open input %s: %w", req.TestCase.InputPath, err)
	}
	defer inputFile.Close()

	var outBuf bytes.Buffer

	result, err := session.Execute(ctx, &sandbox.ExecRequest{
		Executable: req.RunCmd[0],
		Args:       req.RunCmd[1:],
		Stdin:      inputFile,
		Stdout:     &outBuf,
		// Stderr is intentionally discarded for standard problems;
		// writing to stderr is not an RE but should not affect the verdict.
		Stderr:  &bytes.Buffer{},
		Limits:  req.Limits,
	})
	if err != nil {
		return nil, fmt.Errorf("standard: execute: %w", err)
	}

	// Map sandbox exit status to verdict before checking output correctness.
	if result.Status != sandbox.ExecOK {
		return &Verdict{
			Status:     execStatusToSubmission(result.Status),
			TimeUsedMs: result.TimeUsedMs,
			MemUsedKB:  result.MemUsedKB,
		}, nil
	}

	// Read expected output from shared storage.
	expected, err := os.ReadFile(req.TestCase.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("standard: read expected output %s: %w", req.TestCase.OutputPath, err)
	}

	if tokensEqual(outBuf.Bytes(), expected) {
		return &Verdict{
			Status:     models.StatusAccepted,
			Score:      req.TestCase.Score,
			TimeUsedMs: result.TimeUsedMs,
			MemUsedKB:  result.MemUsedKB,
		}, nil
	}

	return &Verdict{
		Status:       models.StatusWrongAnswer,
		TimeUsedMs:   result.TimeUsedMs,
		MemUsedKB:    result.MemUsedKB,
		JudgeMessage: firstDiffHint(outBuf.Bytes(), expected),
	}, nil
}

// ─── Token Comparison ─────────────────────────────────────────────────────────

// tokensEqual performs whitespace-insensitive token comparison.
// This handles the common cases of trailing newlines, CR/LF differences,
// and extra blank lines that would otherwise cause spurious WA.
func tokensEqual(a, b []byte) bool {
	ta, tb := tokenize(a), tokenize(b)
	if len(ta) != len(tb) {
		return false
	}
	for i := range ta {
		if ta[i] != tb[i] {
			return false
		}
	}
	return true
}

func tokenize(data []byte) []string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Split(bufio.ScanWords)
	var tokens []string
	for sc.Scan() {
		tokens = append(tokens, sc.Text())
	}
	return tokens
}

// firstDiffHint returns a short, human-readable message indicating the first
// point of divergence between got and want. Shown to the contestant in the UI.
func firstDiffHint(got, want []byte) string {
	tg, tw := tokenize(got), tokenize(want)
	minLen := min(len(tg), len(tw))
	for i := range minLen {
		if tg[i] != tw[i] {
			return fmt.Sprintf("first difference at token %d: got %q, expected %q", i+1, tg[i], tw[i])
		}
	}
	return fmt.Sprintf("token count mismatch: got %d, expected %d", len(tg), len(tw))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
