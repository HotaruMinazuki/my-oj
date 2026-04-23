package nsjail

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/your-org/my-oj/internal/judger/sandbox"
)

// Execute implements sandbox.Session for single-process problems (standard / special judge).
//
// FD table inside nsjail:
//
//	FD 0 ← req.Stdin  (test-case input, or nil → /dev/null)
//	FD 1 → req.Stdout (contestant output buffer)
//	FD 2 → req.Stderr (discarded or captured by caller)
//	FD 3 ← logW pipe  (nsjail's own log, --log_fd 3; NOT visible to contestant)
func (s *Session) Execute(ctx context.Context, req *sandbox.ExecRequest) (*sandbox.ExecResult, error) {
	// ── Log pipe ──────────────────────────────────────────────────────────────
	// nsjail writes its structured log to logW; we drain logR for result parsing.
	logR, logW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("nsjail.Execute: create log pipe: %w", err)
	}
	defer logR.Close() // write end is closed right after Start()

	// ── Build command ─────────────────────────────────────────────────────────
	args := buildBaseArgs(&s.nsCfg, s.sbCfg, req.Limits)
	args = append(args, "--") // separator: nsjail args / sandboxed program
	args = append(args, req.Executable)
	args = append(args, req.Args...)

	cmd := exec.CommandContext(ctx, s.nsCfg.BinaryPath, args...)
	cmd.Stdin = req.Stdin
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	// ExtraFiles[0] → FD 3 (nsjail log); contestant never sees this.
	cmd.ExtraFiles = []*os.File{logW}
	// New process group so SIGKILL from context cancellation kills nsjail
	// and all its children atomically.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// ── Start ─────────────────────────────────────────────────────────────────
	start := time.Now()
	if err := cmd.Start(); err != nil {
		logW.Close()
		return nil, fmt.Errorf("nsjail.Execute: start nsjail: %w", err)
	}
	logW.Close() // parent doesn't write; close so nsjail gets EOF when it closes its end

	// Drain log concurrently so nsjail never blocks on a full pipe buffer.
	var logBuf bytes.Buffer
	logDone := make(chan struct{})
	go func() {
		defer close(logDone)
		io.Copy(&logBuf, logR)
	}()

	// ── Wait ──────────────────────────────────────────────────────────────────
	waitErr := cmd.Wait()
	wallTime := time.Since(start)
	<-logDone // guarantee log is fully read before we parse it

	return parseResult(cmd.ProcessState, waitErr, wallTime, logBuf.String()), nil
}

// ─── Result Parsing ───────────────────────────────────────────────────────────

// parseResult maps nsjail's process exit state and log output to an ExecResult.
//
// nsjail exit semantics:
//   - Exit 0                 → the sandboxed program exited 0 (ExecOK)
//   - Non-zero, log "Exceeded wall-clock" → ExecWallTLE
//   - Non-zero, log "rlimit cpu"           → ExecTLE
//   - Non-zero, log "memory" + "killed"    → ExecMLE (cgroup OOM)
//   - Non-zero, log "seccomp"              → ExecSCViol
//   - Non-zero, log "nsjail" + negative EC → ExecSE (nsjail internal failure)
//   - Non-zero, other                      → ExecRE
func parseResult(ps *os.ProcessState, waitErr error, wallTime time.Duration, nsjailLog string) *sandbox.ExecResult {
	r := &sandbox.ExecResult{
		TimeUsedMs: wallTime.Milliseconds(),
	}

	// Best-effort memory extraction from nsjail log.
	// nsjail >= 1.0 logs "Maximum VmRSS: N kB" on some builds.
	r.MemUsedKB = extractMemKB(nsjailLog)

	if waitErr == nil {
		r.Status = sandbox.ExecOK
		r.ExitCode = 0
		return r
	}

	// Decode exit code and termination signal.
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		r.ExitCode = exitErr.ExitCode()
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			r.Signal = int(ws.Signal())
		}
	}

	logL := strings.ToLower(nsjailLog)

	switch {
	case strings.Contains(logL, "exceeded wall-clock") ||
		strings.Contains(logL, "wall time limit"):
		r.Status = sandbox.ExecWallTLE
		r.Message = "wall-clock time limit exceeded"

	case strings.Contains(logL, "exceeded cpu") ||
		strings.Contains(logL, "rlimit cpu") ||
		strings.Contains(logL, "cpu time limit"):
		r.Status = sandbox.ExecTLE
		r.Message = "CPU time limit exceeded"

	case (strings.Contains(logL, "memory") || strings.Contains(logL, "oom")) &&
		(strings.Contains(logL, "killed") || strings.Contains(logL, "kill")):
		r.Status = sandbox.ExecMLE
		r.Message = "memory limit exceeded"

	case strings.Contains(logL, "seccomp"):
		r.Status = sandbox.ExecSCViol
		r.Message = extractSeccompSyscall(nsjailLog)

	case r.ExitCode < 0 && strings.Contains(logL, "nsjail"):
		// nsjail itself crashed or was killed (ExitCode == -1 when killed by signal).
		r.Status = sandbox.ExecSE
		r.Message = fmt.Sprintf("sandbox error (exit %d, signal %d)", r.ExitCode, r.Signal)

	default:
		r.Status = sandbox.ExecRE
		r.Message = fmt.Sprintf("exited with code %d, signal %d", r.ExitCode, r.Signal)
	}

	return r
}

// extractMemKB parses "Maximum VmRSS: N kB" from nsjail's log.
// Returns 0 if the pattern is not found.
func extractMemKB(log string) int64 {
	const marker = "Maximum VmRSS:"
	idx := strings.Index(log, marker)
	if idx == -1 {
		return 0
	}
	rest := strings.TrimSpace(log[idx+len(marker):])
	// Expected format: "12345 kB" or "12345kB"
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// extractSeccompSyscall tries to pull the violating syscall name from the nsjail
// seccomp log line, e.g. "seccomp violation: open (5)".
func extractSeccompSyscall(log string) string {
	const marker = "seccomp"
	idx := strings.Index(strings.ToLower(log), marker)
	if idx == -1 {
		return "seccomp: illegal syscall"
	}
	// Return up to 80 chars of the relevant log line.
	line := log[idx:]
	if nl := strings.IndexByte(line, '\n'); nl != -1 {
		line = line[:nl]
	}
	if len(line) > 80 {
		line = line[:80]
	}
	return "seccomp violation: " + strings.TrimSpace(line)
}
