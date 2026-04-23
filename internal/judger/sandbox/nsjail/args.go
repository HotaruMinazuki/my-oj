package nsjail

import (
	"fmt"

	"github.com/your-org/my-oj/internal/judger/sandbox"
)

// buildBaseArgs constructs the nsjail CLI arguments that are common to all
// execution modes (single, pair, group).
//
// FD contract:
//
//	The caller is responsible for setting cmd.ExtraFiles = []*os.File{logW, ...}
//	ExtraFiles[0] (→ FD 3 in nsjail) is the nsjail log pipe (--log_fd 3).
//	Additional ExtraFiles entries are for extra communication channels.
//
// Args that vary per call (executable, extra --pass_fd) are appended by the caller.
func buildBaseArgs(cfg *Config, scfg *sandbox.SessionConfig, limits sandbox.ResourceLimits) []string {
	args := []string{
		"--mode", "o",     // once: run exactly one process then exit
		"--log_fd", "3",   // nsjail writes its own log to FD 3 (ExtraFiles[0])
		"--quiet",         // suppress nsjail banner on stderr
		"--disable_proc",  // hide /proc (re-mounted as needed below)
	}

	// ── Time limits ──────────────────────────────────────────────────────────
	// --time_limit is wall-clock time (seconds).  Ceil to avoid rounding-down.
	if limits.WallTimeLimitMs > 0 {
		wallSecs := ceilDiv(limits.WallTimeLimitMs, 1000)
		args = append(args, "--time_limit", fmt.Sprintf("%d", wallSecs))
	}

	// RLIMIT_CPU enforces CPU time independently of wall time.
	// A process sleeping or doing blocking I/O won't burn CPU but will still
	// be killed by --time_limit; CPU time catches busy-looping edge cases.
	if limits.TimeLimitMs > 0 {
		cpuSecs := ceilDiv(limits.TimeLimitMs, 1000)
		args = append(args, "--rlimit_cpu", fmt.Sprintf("%d", cpuSecs))
	}

	// ── Memory limit ─────────────────────────────────────────────────────────
	if limits.MemLimitKB > 0 {
		memBytes := limits.MemLimitKB * 1024
		if cfg.CgroupV2 {
			args = append(args, "--cgroup_mem_max", fmt.Sprintf("%d", memBytes))
		} else {
			// cgroup v1: separate memory and memsw controllers.
			args = append(args,
				"--cgroup_mem_max", fmt.Sprintf("%d", memBytes),
				"--cgroup_mem_memsw_max", fmt.Sprintf("%d", memBytes),
			)
		}
		// Virtual memory cap via RLIMIT_AS catches programs that mmap excessively
		// without touching all pages (cgroup catches RSS but not VM abuse).
		// Use 2× the RAM limit so legitimate programs aren't false-killed.
		args = append(args, "--rlimit_as", fmt.Sprintf("%d", (limits.MemLimitKB*2)/1024)) // MB
	}

	// ── Process / PID limits ─────────────────────────────────────────────────
	if scfg.MaxProcesses > 0 {
		// cgroup pids.max prevents fork bombs from filling the PID table.
		args = append(args, "--cgroup_pids_max", fmt.Sprintf("%d", scfg.MaxProcesses))
		// RLIMIT_NPROC backs this up at the per-process level.
		args = append(args, "--rlimit_nproc", fmt.Sprintf("%d", scfg.MaxProcesses))
	}

	// ── File system limits ────────────────────────────────────────────────────
	if limits.FileSizeKB > 0 {
		// RLIMIT_FSIZE in MB (nsjail uses MB for this flag).
		fileMB := ceilDiv(limits.FileSizeKB, 1024)
		args = append(args, "--rlimit_fsize", fmt.Sprintf("%d", fileMB))
	}
	if limits.MaxOpenFiles > 0 {
		args = append(args, "--rlimit_nofile", fmt.Sprintf("%d", limits.MaxOpenFiles))
	}

	// ── cgroup parent ─────────────────────────────────────────────────────────
	if cfg.CgroupParent != "" {
		args = append(args, "--cgroup_mem_parent", cfg.CgroupParent)
		args = append(args, "--cgroup_pids_parent", cfg.CgroupParent)
		args = append(args, "--cgroup_cpu_parent", cfg.CgroupParent)
	}

	// ── Network isolation ─────────────────────────────────────────────────────
	// nsjail creates a new network namespace by default.  Contestants should
	// never have outbound network access.
	if !scfg.NetworkEnabled {
		args = append(args, "--iface_no_lo") // disable loopback inside sandbox
	}

	// ── Read-only bind mounts (system libraries) ──────────────────────────────
	mounts := cfg.ReadOnlyMounts
	if len(mounts) == 0 {
		mounts = defaultReadOnlyMounts
	}
	for _, m := range mounts {
		// nsjail -R <path> → read-only bind mount, same path inside sandbox.
		args = append(args, "-R", m)
	}

	// Extra mounts from the session config (e.g. checker/interactor binaries).
	for _, bm := range scfg.ExtraBindMounts {
		flag := "-R"
		if !bm.ReadOnly {
			flag = "-B"
		}
		target := bm.SandboxPath
		if target == "" {
			target = bm.HostPath
		}
		args = append(args, flag, fmt.Sprintf("%s:%s", bm.HostPath, target))
	}

	// ── Contestant workspace ──────────────────────────────────────────────────
	// The session's WorkDir is bind-mounted read-write as /workspace.
	// All compilation output, executables, and I/O temp files live here.
	args = append(args, "-B", fmt.Sprintf("%s:%s", scfg.WorkDir, "/workspace"))

	// Set the sandboxed process's working directory to /workspace.
	args = append(args, "-D", "/workspace")

	// ── Seccomp ───────────────────────────────────────────────────────────────
	// The seccomp policy file defines which syscalls are allowed.
	// A missing policy leaves the sandbox without syscall filtering — log a warning
	// but do not fail, so development environments without the policy can run.
	if cfg.SeccompPolicyPath != "" {
		args = append(args, "--seccomp_policy", cfg.SeccompPolicyPath)
	}

	return args
}

// buildPassFDArgs returns "--pass_fd N" pairs for a set of extra FD numbers.
// These tell nsjail to preserve the specified FDs in the sandboxed process.
// FD 3 (the log pipe) is intentionally NOT included here — nsjail consumes it
// internally and we don't want the contestant to see it.
func buildPassFDArgs(fds []int) []string {
	args := make([]string, 0, len(fds)*2)
	for _, fd := range fds {
		args = append(args, "--pass_fd", fmt.Sprintf("%d", fd))
	}
	return args
}

// ceilDiv returns ⌈a/b⌉ using integer arithmetic.
func ceilDiv(a, b int64) int64 {
	return (a + b - 1) / b
}
