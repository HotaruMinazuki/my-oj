package sandbox

import "io"

// ─── Session Configuration ────────────────────────────────────────────────────

// SessionConfig defines the isolation parameters used when preparing a sandbox session.
type SessionConfig struct {
	// WorkDir is the host-side writable root for this session (e.g., /tmp/judge/<uuid>).
	WorkDir string
	// ExtraBindMounts injects read-only host paths into the sandbox.
	// Use this to make checker/interactor binaries available without copying.
	ExtraBindMounts []BindMount
	// MaxProcesses caps OS-level threads+processes inside the sandbox.
	// 1 = standard/special, 2 = interactive, N = communication.
	MaxProcesses int
	// NetworkEnabled must be false for all contestant code.
	// Set true only for trusted internal tools (checkers launched outside the sandbox).
	NetworkEnabled bool
}

// BindMount describes a host path projected into the sandbox namespace.
type BindMount struct {
	HostPath    string
	SandboxPath string
	ReadOnly    bool
}

// ─── Resource Limits ──────────────────────────────────────────────────────────

// ResourceLimits defines per-process constraints enforced via cgroups + rlimit.
// The backend is responsible for translating these into the appropriate
// isolate box-options or nsjail flags.
type ResourceLimits struct {
	// TimeLimitMs is the CPU-time limit in milliseconds (enforced by cgroup cpu.stat).
	TimeLimitMs int64
	// WallTimeLimitMs is the wall-clock limit; prevents sleep/blocking tricks.
	// Recommended default: max(TimeLimitMs * 2, TimeLimitMs + 1000).
	WallTimeLimitMs int64
	// MemLimitKB is the address-space cap in kilobytes (cgroup memory.max).
	MemLimitKB int64
	// FileSizeKB limits the size of any file the process may create (rlimit FSIZE).
	FileSizeKB int64
	// MaxOpenFiles caps open file descriptors (rlimit NOFILE).
	MaxOpenFiles int
	// MaxChildProcesses limits fork/clone within this process (rlimit NPROC).
	// Redundant with SessionConfig.MaxProcesses but adds a per-process safety net.
	MaxChildProcesses int
}

// ─── Single-Process Execution ─────────────────────────────────────────────────

// ExecRequest describes one process to run inside an active sandbox session.
type ExecRequest struct {
	// Executable is the path inside the sandbox to the binary.
	Executable string
	Args       []string
	Env        []string // supplemental environment variables

	// I/O streams are caller-managed.
	// Standard judge: Stdin = test-input reader, Stdout = output-capture writer.
	// Interactive/Comm: nil — the sandbox backend wires the pipes internally.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Limits ResourceLimits
}

// ExecResult contains the outcome of a single sandboxed process execution.
type ExecResult struct {
	ExitCode int // 0 = clean exit
	Signal   int // terminating signal number; 0 if none

	TimeUsedMs int64
	MemUsedKB  int64

	Status ExecStatus
	// Message carries additional context, e.g., the seccomp-violating syscall name.
	Message string
}

// ExecStatus is the sandbox-level classification of a process exit.
type ExecStatus string

const (
	ExecOK      ExecStatus = "OK"
	ExecTLE     ExecStatus = "TLE"     // CPU time limit exceeded
	ExecWallTLE ExecStatus = "WallTLE" // wall-clock limit exceeded (idling / blocking I/O)
	ExecMLE     ExecStatus = "MLE"     // memory limit exceeded
	ExecRE      ExecStatus = "RE"      // non-zero exit or killed by signal
	// ExecSCViol means seccomp rejected a syscall — strong evidence of escape attempt.
	ExecSCViol ExecStatus = "SCViol"
	// ExecSE is an internal sandbox failure; the judger node should report SystemError.
	ExecSE ExecStatus = "SE"
)

// ─── Interactive (Pair) Execution ─────────────────────────────────────────────

// PairExecRequest runs a contestant program paired with a trusted interactor binary.
//
// I/O routing (managed entirely by the sandbox backend):
//
//	contestant.stdout ──→ interactor.stdin
//	interactor.stdout ──→ contestant.stdin
//
// The test-case input is fed exclusively to the interactor via InteractorInput;
// the contestant program never sees the raw input file.
// The interactor signals its verdict by writing to its own stderr, which is
// captured into PairExecResult.InteractorOutput.
type PairExecRequest struct {
	// Contestant is subject to full resource limits and seccomp policy.
	Contestant ExecRequest
	// Interactor runs with relaxed seccomp (needs file I/O for test-case input).
	// It is NOT subject to contestant resource limits; use InteractorLimits instead.
	Interactor       ExecRequest
	InteractorLimits ResourceLimits

	// InteractorInput is the test-case input stream delivered to the interactor.
	InteractorInput io.Reader

	// PipeBufferSize overrides the kernel pipe buffer size in bytes.
	// 0 = OS default (65536 on Linux). Increase for problems with large token exchanges.
	PipeBufferSize int
}

// PairExecResult captures the joint outcome of an interactive execution.
type PairExecResult struct {
	Contestant ExecResult
	Interactor ExecResult
	// InteractorOutput is the interactor's stderr — typically a score or "AC"/"WA" token.
	InteractorOutput string
	// WallTimeMs is the elapsed wall time from first-process-start to last-process-exit.
	WallTimeMs int64
}

// ─── Communication (Group) Execution ──────────────────────────────────────────

// GroupExecRequest runs N contestant processes connected by a directed channel graph.
// Used for communication problems where processes must collaborate to produce output.
type GroupExecRequest struct {
	// Processes lists each contestant process by index (0-based).
	// All are subject to the same resource limits defined in their ExecRequest.Limits.
	Processes []ExecRequest

	// Channels defines the unidirectional pipe wiring between processes.
	// Multiple channels may fan-out from or fan-in to a single process.
	Channels []ChannelSpec

	// GraderProcess is an optional trusted process that monitors the group's I/O.
	// If non-nil, it is started after all contestant processes and runs unrestricted.
	GraderProcess *ExecRequest

	// GlobalWallTimeLimitMs caps the total elapsed time for the entire group.
	// Individual process WallTimeLimitMs still apply per-process.
	GlobalWallTimeLimitMs int64
}

// ChannelSpec defines one unidirectional byte-stream between two processes.
type ChannelSpec struct {
	Name         string // human-readable label for logs/debugging
	From         int    // writer process index
	To           int    // reader process index
	BufferSizeKB int    // 0 = OS default
}

// GroupExecResult collects each process's verdict plus the grader's output.
type GroupExecResult struct {
	Processes    []ExecResult
	GraderOutput string // grader stderr (verdict / score line)
	WallTimeMs   int64
}
