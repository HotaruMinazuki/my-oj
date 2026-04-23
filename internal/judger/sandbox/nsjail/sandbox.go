// Package nsjail implements sandbox.Sandbox and sandbox.Session using nsjail
// as the process isolation backend.
//
// Deployment requirements:
//   - nsjail binary reachable at Config.BinaryPath
//   - Judger node process has CAP_SYS_ADMIN (for mount namespaces) and
//     CAP_NET_ADMIN (for network namespace) or runs as root.
//   - cgroup v2 hierarchy mounted at /sys/fs/cgroup (most modern distros).
//   - Config.CgroupParent directory must exist and be writable by the judger user.
//
// Package layout:
//
//	sandbox.go  — Config, Sandbox, Session, Prepare, Release
//	args.go     — nsjail argument builder
//	execute.go  — Execute() + result parsing
//	pair.go     — ExecutePair() for interactive problems
//	group.go    — ExecuteGroup() for communication problems
package nsjail

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/judger/sandbox"
)

// Config holds global nsjail settings shared across all sessions.
type Config struct {
	// BinaryPath is the absolute path to the nsjail executable.
	BinaryPath string

	// SeccompPolicyPath is the BPF seccomp filter applied to every sandboxed process.
	// The filter should whitelist only syscalls needed by standard competitive
	// programming binaries (read, write, exit, mmap, brk, …).
	// Leave empty to omit seccomp filtering (not recommended for production).
	SeccompPolicyPath string

	// ReadOnlyMounts overrides the default system-library bind-mount list.
	// Set this to the minimal set required by the language runtimes you support.
	ReadOnlyMounts []string

	// CgroupParent is the cgroup directory under which nsjail creates per-task
	// sub-cgroups, e.g. "oj-judge" → /sys/fs/cgroup/oj-judge/<session-id>/.
	// The parent cgroup must exist and the judger user must have write permission.
	CgroupParent string

	// CgroupV2 selects cgroup v2 flags.  Set true on kernels >= 4.15 with the
	// unified hierarchy.  cgroup v1 is still supported via the legacy flags.
	CgroupV2 bool

	// InteractorNoSandbox runs the interactor binary directly (exec.Command) instead
	// of wrapping it in nsjail.  Useful when the interactor needs capabilities that
	// would be blocked by the sandbox.  The interactor must be a trusted binary.
	InteractorNoSandbox bool
}

// defaultReadOnlyMounts is the minimal set of host paths bind-mounted read-only
// into every sandbox.  Language-specific entries (e.g. /usr/lib/jvm) can be added
// via SessionConfig.ExtraBindMounts.
var defaultReadOnlyMounts = []string{
	"/usr",
	"/lib",
	"/lib64",
	"/lib32",
	"/bin",
	"/sbin",
	// ld.so configuration so dynamic linking works inside the sandbox.
	"/etc/ld.so.cache",
	"/etc/ld.so.conf",
	"/etc/ld.so.conf.d",
	// Devices that many runtimes expect to be readable.
	"/dev/null",
	"/dev/zero",
	"/dev/urandom",
}

// ─── Sandbox ──────────────────────────────────────────────────────────────────

// Sandbox implements sandbox.Sandbox.
type Sandbox struct {
	cfg Config
	log *zap.Logger
}

// New creates a Sandbox and verifies that the nsjail binary exists.
func New(cfg Config, log *zap.Logger) (*Sandbox, error) {
	if _, err := os.Stat(cfg.BinaryPath); err != nil {
		return nil, fmt.Errorf("nsjail: binary not found at %q: %w", cfg.BinaryPath, err)
	}
	if cfg.CgroupParent == "" {
		cfg.CgroupParent = "oj-judge"
	}
	return &Sandbox{cfg: cfg, log: log}, nil
}

// Prepare implements sandbox.Sandbox.
// It allocates a session identifier; no OS resources are consumed until the
// first Execute/ExecutePair/ExecuteGroup call.
func (sb *Sandbox) Prepare(_ context.Context, cfg *sandbox.SessionConfig) (sandbox.Session, error) {
	if cfg.WorkDir == "" {
		return nil, fmt.Errorf("nsjail: SessionConfig.WorkDir must not be empty")
	}
	id := uuid.New().String()
	return &Session{
		id:    id,
		sbCfg: cfg,
		nsCfg: sb.cfg,
		log:   sb.log.With(zap.String("session", id)),
	}, nil
}

// ─── Session ──────────────────────────────────────────────────────────────────

// Session implements sandbox.Session.
// Each session wraps one or more nsjail child-process invocations.
// The session itself is stateless between calls; isolation is enforced by nsjail
// re-creating namespaces on every invocation.
type Session struct {
	id    string
	sbCfg *sandbox.SessionConfig
	nsCfg Config
	log   *zap.Logger
}

func (s *Session) ID() string { return s.id }

// Release is a no-op for nsjail: nsjail tears down all namespaces and cgroups
// when its process exits, so there is nothing to clean up here.
// WorkDir cleanup is the caller's (Scheduler's) responsibility.
func (s *Session) Release() error { return nil }
