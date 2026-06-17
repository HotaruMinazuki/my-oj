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

	"go.uber.org/zap"

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

	// ── Per-execution cgroup for peak-memory measurement ──────────────────────
	// We point nsjail at a cgroup dir we own so we can read its memory.peak after
	// nsjail tears down its own. Fail-safe: on any setup error this is a no-op and
	// nsjail runs unchanged (memory just stays 0) — measuring must never break a run.
	memCgroup, useMemCgroup := s.prepareMemCgroup()
	if useMemCgroup {
		defer cleanupCgroupTree(memCgroup)
	}

	// ── Build command ─────────────────────────────────────────────────────────
	args := buildBaseArgs(&s.nsCfg, s.sbCfg, req.Limits)
	if useMemCgroup {
		// nsjail creates NSJAIL.<pid> under this dir; cgroup v2 charges memory
		// hierarchically, so its peak shows up in <memCgroup>/memory.peak.
		args = append(args, "--cgroupv2_mount", memCgroup)
	}
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

	res := parseResult(cmd.ProcessState, waitErr, wallTime, logBuf.String())

	// All measurements below read from the judger-owned cgroup; they must run
	// before the deferred cleanupCgroupTree removes it. parseResult's log-scraped
	// values are only fallbacks for when the cgroup is unavailable.
	if useMemCgroup {
		if kb := readCgroupPeakKB(memCgroup); kb > 0 {
			res.MemUsedKB = kb
		}

		// ── MLE: authoritative kernel OOM counter (see cgroupOOMKilled). ──────────
		// parseResult's log-scraping misses OOM kills on this nsjail build (it
		// doesn't log them), so a process killed for exceeding memory.max was
		// landing as RE. Don't override a time-limit verdict already read from the
		// log — a CPU/wall-clock kill takes precedence.
		if res.Status != sandbox.ExecTLE && res.Status != sandbox.ExecWallTLE &&
			cgroupOOMKilled(memCgroup) {
			res.Status = sandbox.ExecMLE
			res.Message = "memory limit exceeded"
		}

		// ── Time: TimeLimitMs is a CPU-time limit, so report and judge on measured
		// CPU time, not wall time. nsjail's log strings for time limits are as
		// unreliable here as its OOM logging was (a real TLE could land as RE), and
		// --rlimit_cpu only enforces whole seconds — so a sub-second overrun under a
		// non-round limit can slip through. Measured cpu.stat closes both gaps. ────
		if cpuMs := readCgroupCPUMs(memCgroup); cpuMs > 0 {
			res.TimeUsedMs = cpuMs
			if res.Status != sandbox.ExecMLE && req.Limits.TimeLimitMs > 0 {
				// >= when the run was killed (rlimit_cpu kills at the ceil'd limit);
				// strict > when it exited cleanly, so a program finishing right at
				// the limit still passes.
				killed := res.Status != sandbox.ExecOK
				if (killed && cpuMs >= req.Limits.TimeLimitMs) ||
					(!killed && cpuMs > req.Limits.TimeLimitMs) {
					res.Status = sandbox.ExecTLE
					res.Message = "CPU time limit exceeded"
				}
			}
		}

		// ── Wall-clock TLE: catches sleeping / blocking I/O that burns no CPU.
		// Only upgrade a process that was actually killed; a clean finish under the
		// wall cap is fine even if it idled. ──────────────────────────────────────
		if res.Status != sandbox.ExecOK && res.Status != sandbox.ExecMLE &&
			res.Status != sandbox.ExecTLE && req.Limits.WallTimeLimitMs > 0 &&
			wallTime.Milliseconds() >= req.Limits.WallTimeLimitMs {
			res.Status = sandbox.ExecWallTLE
			res.Message = "wall-clock time limit exceeded"
		}
	}

	if res.Status != sandbox.ExecOK {
		// Surface nsjail's own log: when nsjail itself dies (bad mount, cgroup
		// setup, seccomp policy parse, exec failure) the reason exists ONLY
		// here — the sandboxed program never ran and produced no output.
		s.log.Warn("nsjail execution not OK",
			zap.String("status", string(res.Status)),
			zap.Int("exit_code", res.ExitCode),
			zap.String("executable", req.Executable),
			zap.String("nsjail_log", truncateLog(logBuf.String(), 2000)),
		)
	}
	return res, nil
}

// truncateLog caps a log string for structured logging.
func truncateLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
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
