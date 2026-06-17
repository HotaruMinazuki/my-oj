package nsjail

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// prepareMemCgroup creates a per-execution cgroup v2 directory that the judger
// owns, so peak memory can be read AFTER nsjail exits.
//
// Why we need our own: nsjail creates its NSJAIL.<pid> sub-cgroup and removes it
// during teardown, before our cmd.Wait() returns — so its memory.peak is gone by
// the time we could read it. Instead we point nsjail at a dir we control via
// --cgroupv2_mount; nsjail builds NSJAIL.<pid> underneath, and because cgroup v2
// charges memory hierarchically the peak bubbles up to our dir's memory.peak,
// which survives nsjail's cleanup.
//
//	Layout: <CgroupRoot>/<CgroupParent>/exec-<session-id>   (e.g. /sys/fs/cgroup/oj-judge/exec-<uuid>)
//
// Returns (mountDir, true) only when the cgroup is fully usable. On ANY failure it
// cleans up and returns ("", false) so the caller runs nsjail unchanged and memory
// simply stays 0 — measuring memory must never break execution.
func (s *Session) prepareMemCgroup() (mountDir string, ok bool) {
	if !s.nsCfg.CgroupV2 || s.nsCfg.DisableCgroup {
		return "", false
	}

	parent := filepath.Join(s.nsCfg.CgroupRoot, s.nsCfg.CgroupParent)
	if err := os.Mkdir(parent, 0o755); err != nil && !os.IsExist(err) {
		return "", false
	}
	// Delegate memory (+pids) to children so the per-exec dir gets a memory.peak
	// file and nsjail can set memory.max on its own sub-cgroup. Best-effort here —
	// the memory.peak Stat below is the real gate.
	enableControllers(parent)

	base := filepath.Join(parent, "exec-"+s.id)
	cleanupCgroupTree(base) // clear any stale leftover from a crashed run
	if err := os.Mkdir(base, 0o755); err != nil {
		return "", false
	}
	if err := enableControllers(base); err != nil {
		cleanupCgroupTree(base)
		return "", false
	}
	// memory.peak exists only when the memory controller is actually delegated to
	// `base` (parent's subtree_control includes memory). If it's missing, bail out
	// rather than redirect nsjail into a cgroup where its mem limit would fail.
	if _, err := os.Stat(filepath.Join(base, "memory.peak")); err != nil {
		cleanupCgroupTree(base)
		return "", false
	}
	return base, true
}

// enableControllers turns on the memory and pids controllers for a cgroup's
// children via cgroup.subtree_control. memory is required (an error is returned if
// it cannot be enabled); pids is best-effort.
func enableControllers(dir string) error {
	scf := filepath.Join(dir, "cgroup.subtree_control")
	if err := os.WriteFile(scf, []byte("+memory"), 0); err != nil {
		// Writing an already-enabled controller is a no-op on most kernels; only a
		// genuine "controller unavailable" failure should propagate.
		if !controllerEnabled(dir, "memory") {
			return err
		}
	}
	_ = os.WriteFile(scf, []byte("+pids"), 0) // best-effort
	return nil
}

// controllerEnabled reports whether name is listed in dir/cgroup.subtree_control.
func controllerEnabled(dir, name string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "cgroup.subtree_control"))
	if err != nil {
		return false
	}
	for _, f := range strings.Fields(string(b)) {
		if f == name {
			return true
		}
	}
	return false
}

// readCgroupPeakKB reads memory.peak (bytes) from the cgroup dir and returns it in
// KiB (rounded up). Returns 0 if the file is missing or unparseable.
func readCgroupPeakKB(dir string) int64 {
	b, err := os.ReadFile(filepath.Join(dir, "memory.peak"))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return (n + 1023) / 1024
}

// cleanupCgroupTree removes a cgroup directory and any descendant cgroups,
// children first, using rmdir — cgroup dirs cannot be removed with os.RemoveAll
// because their control files are not deletable. Errors are ignored (best-effort).
func cleanupCgroupTree(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			cleanupCgroupTree(filepath.Join(dir, e.Name()))
		}
	}
	_ = syscall.Rmdir(dir)
}
