// Package sandbox defines the interface contract for process isolation backends.
//
// Design intent:
//   - Callers (orchestrators) depend only on this interface, never on concrete backends.
//   - Swapping isolate → nsjail → custom implementation = one line change at wire-up.
//   - ExecutePair and ExecuteGroup reserve the interface surface for interactive /
//     communication problems; their implementations live in the concrete backends.
package sandbox

import "context"

// Sandbox creates isolated execution sessions.
// Each concrete implementation wraps a specific backend (isolate, nsjail, …).
//
// Lifecycle:
//
//	sess, err := sb.Prepare(ctx, cfg)   // allocate sandbox slot
//	defer sess.Release()                // always release, even on error
//	result, err := sess.Execute(ctx, req)
type Sandbox interface {
	// Prepare allocates and initialises an isolated environment.
	// The returned Session is ready for Execute / ExecutePair / ExecuteGroup calls.
	// Callers MUST call Session.Release() to free OS-level resources.
	Prepare(ctx context.Context, cfg *SessionConfig) (Session, error)
}

// Session is a prepared, isolated execution environment tied to one judging task.
//
// A single Session may host:
//   - one process   (standard / special judge)
//   - two processes (interactive: contestant ↔ interactor)
//   - N processes   (communication: contestant group + optional grader)
//
// The session encapsulates all OS namespaces, cgroup hierarchies, and
// temporary directories; Release() tears all of that down atomically.
type Session interface {
	// ID returns the unique identifier for this session (used in logs and metrics).
	ID() string

	// Execute runs a single sandboxed process and blocks until it exits.
	// Used for standard and special-judge problems.
	Execute(ctx context.Context, req *ExecRequest) (*ExecResult, error)

	// ExecutePair runs a contestant process and a trusted interactor process,
	// wiring their stdin/stdout as a bidirectional pipe.
	// Used for interactive problems (交互题).
	//
	// Both processes are started concurrently. The method blocks until both exit.
	// If the contestant exits first, the interactor receives EOF on its stdin.
	// If the interactor exits first, the contestant receives EOF on its stdin (SIGPIPE).
	ExecutePair(ctx context.Context, req *PairExecRequest) (*PairExecResult, error)

	// ExecuteGroup runs N contestant processes connected by the channel graph
	// described in req.Channels, optionally supervised by a grader process.
	// Used for communication problems (通信题).
	//
	// Processes are started in index order; the grader (if present) is started last.
	// The method blocks until all processes exit or GlobalWallTimeLimitMs elapses.
	ExecuteGroup(ctx context.Context, req *GroupExecRequest) (*GroupExecResult, error)

	// Release tears down the sandbox environment and frees all resources:
	// cgroup hierarchies, namespaces, temp directories, open file descriptors.
	// Must be called exactly once; safe to call even if Execute* returned an error.
	Release() error
}

// ─── Future Extension Point ───────────────────────────────────────────────────

// SandboxFactory is the constructor signature for sandbox backends.
// Register concrete backends at application startup:
//
//	registry.Register("isolate", isolate.NewSandbox)
//	registry.Register("nsjail",  nsjail.NewSandbox)
type SandboxFactory func(cfg map[string]any) (Sandbox, error)
