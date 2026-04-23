package nsjail

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/your-org/my-oj/internal/judger/sandbox"
)

// ExecutePair implements sandbox.Session for interactive problems.
//
// Pipe topology (all wiring done by Go before nsjail Start()):
//
//	os.Pipe() c2i: contestant→interactor
//	os.Pipe() i2c: interactor→contestant
//
//	┌─────────────────────────────────────────────────────────────────┐
//	│ Contestant nsjail                                               │
//	│   stdin  = i2cR  (reads tokens from interactor)                │
//	│   stdout = c2iW  (writes answers to interactor)                │
//	└─────────────────────────────────────────────────────────────────┘
//	                              ↕  (kernel pipes)
//	┌─────────────────────────────────────────────────────────────────┐
//	│ Interactor nsjail / exec                                        │
//	│   stdin  = c2iR  (reads answers from contestant)               │
//	│   stdout = i2cW  (writes questions to contestant)              │
//	│   stderr → &verdictBuf  (writes verdict line: "AC"/"WA"/"PC N")│
//	│   argv[last] = "/workspace/test_input" (test case file)        │
//	└─────────────────────────────────────────────────────────────────┘
//
// Deadlock prevention:
//
//	After both processes are started, the parent closes ALL four pipe ends.
//	From that point on:
//	  · contestant exit → c2iW closes → interactor's stdin reaches EOF → interactor exits
//	  · interactor exit → i2cW closes → contestant's stdin reaches EOF → contestant exits
//	The context deadline kills any remaining process if EOF propagation takes too long.
func (s *Session) ExecutePair(ctx context.Context, req *sandbox.PairExecRequest) (*sandbox.PairExecResult, error) {
	// ── Write test input to workspace ─────────────────────────────────────────
	// The interactor opens this file by path; it is NOT piped to interactor's stdin
	// (interactor's stdin is connected to the contestant→interactor pipe instead).
	inputPath := filepath.Join(s.sbCfg.WorkDir, "test_input")
	if err := copyReaderToFile(req.InteractorInput, inputPath); err != nil {
		return nil, fmt.Errorf("nsjail.ExecutePair: write test input: %w", err)
	}

	// ── Create bidirectional pipes ────────────────────────────────────────────
	c2iR, c2iW, err := os.Pipe() // contestant → interactor
	if err != nil {
		return nil, fmt.Errorf("nsjail.ExecutePair: pipe c2i: %w", err)
	}
	i2cR, i2cW, err := os.Pipe() // interactor → contestant
	if err != nil {
		c2iR.Close()
		c2iW.Close()
		return nil, fmt.Errorf("nsjail.ExecutePair: pipe i2c: %w", err)
	}

	// ── Log pipes (one per nsjail instance) ──────────────────────────────────
	cLogR, cLogW, err := os.Pipe()
	if err != nil {
		closeAll(c2iR, c2iW, i2cR, i2cW)
		return nil, fmt.Errorf("nsjail.ExecutePair: contestant log pipe: %w", err)
	}
	iLogR, iLogW, err := os.Pipe()
	if err != nil {
		closeAll(c2iR, c2iW, i2cR, i2cW, cLogR, cLogW)
		return nil, fmt.Errorf("nsjail.ExecutePair: interactor log pipe: %w", err)
	}

	// ── Build contestant command ──────────────────────────────────────────────
	cArgs := buildBaseArgs(&s.nsCfg, s.sbCfg, req.Contestant.Limits)
	cArgs = append(cArgs, "--")
	cArgs = append(cArgs, req.Contestant.Executable)
	cArgs = append(cArgs, req.Contestant.Args...)

	cCmd := exec.CommandContext(ctx, s.nsCfg.BinaryPath, cArgs...)
	cCmd.Stdin = i2cR   // contestant reads interactor output
	cCmd.Stdout = c2iW  // contestant writes to interactor
	cCmd.Stderr = &bytes.Buffer{} // contestant's stderr goes to /dev/null (sandboxed)
	cCmd.ExtraFiles = []*os.File{cLogW} // FD 3 = nsjail log
	cCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// ── Build interactor command ──────────────────────────────────────────────
	// Interactor runs with relaxed resource limits.
	// It receives the test input file path as its last argument.
	// Its stdin = contestant's stdout (c2iR); stdout = contestant's stdin (i2cW).
	var iCmd *exec.Cmd
	var verdictBuf bytes.Buffer

	if s.nsCfg.InteractorNoSandbox {
		// Trusted interactor: run directly without nsjail.
		iCmd = exec.CommandContext(ctx, req.Interactor.Executable,
			append(req.Interactor.Args, "/workspace/test_input")...)
	} else {
		interactorSCfg := &sandbox.SessionConfig{
			WorkDir:             s.sbCfg.WorkDir,
			ExtraBindMounts:     s.sbCfg.ExtraBindMounts,
			MaxProcesses:        1,
			NetworkEnabled:      false,
		}
		iArgs := buildBaseArgs(&s.nsCfg, interactorSCfg, req.InteractorLimits)
		// Interactor needs to read the test input file → extra file-read syscalls allowed.
		// The seccomp policy should whitelist open/openat for interactors.
		iArgs = append(iArgs, "--")
		iArgs = append(iArgs, req.Interactor.Executable)
		iArgs = append(iArgs, req.Interactor.Args...)
		// Append test_input path INSIDE the sandbox (/workspace/test_input).
		iArgs = append(iArgs, "/workspace/test_input")

		iCmd = exec.CommandContext(ctx, s.nsCfg.BinaryPath, iArgs...)
		iCmd.ExtraFiles = []*os.File{iLogW}
		iCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	iCmd.Stdin = c2iR   // interactor reads contestant output
	iCmd.Stdout = i2cW  // interactor writes to contestant
	iCmd.Stderr = &verdictBuf // capture interactor's verdict line

	// ── Start both processes ──────────────────────────────────────────────────
	start := time.Now()
	if err := cCmd.Start(); err != nil {
		closeAll(c2iR, c2iW, i2cR, i2cW, cLogR, cLogW, iLogR, iLogW)
		return nil, fmt.Errorf("nsjail.ExecutePair: start contestant: %w", err)
	}
	if err := iCmd.Start(); err != nil {
		// Contestant is already running; kill it before returning.
		_ = syscall.Kill(-cCmd.Process.Pid, syscall.SIGKILL)
		_ = cCmd.Wait()
		closeAll(c2iR, c2iW, i2cR, i2cW, cLogR, cLogW, iLogR, iLogW)
		return nil, fmt.Errorf("nsjail.ExecutePair: start interactor: %w", err)
	}

	// ── CRITICAL: Close ALL pipe ends in parent ───────────────────────────────
	//
	// Rule: every pipe end the parent does not directly read/write must be closed
	// after Start() so that OS reference counting enables EOF propagation.
	//
	// Without this:
	//   Contestant exits → c2iW still open in parent → interactor stdin never gets
	//   EOF → interactor hangs → deadlock despite contestant having exited.
	closeAll(
		i2cR,  // contestant now owns this (its stdin)
		c2iW,  // contestant now owns this (its stdout)
		c2iR,  // interactor now owns this (its stdin)
		i2cW,  // interactor now owns this (its stdout)
		cLogW, // nsjail (contestant) owns this
		iLogW, // nsjail (interactor) owns this
	)

	// ── Drain log pipes concurrently ─────────────────────────────────────────
	var cLogBuf, iLogBuf bytes.Buffer
	logsDone := drainConcurrently(&cLogBuf, cLogR, &iLogBuf, iLogR)

	// ── Wait for both processes ───────────────────────────────────────────────
	// Each Wait() runs in its own goroutine so neither blocks the other.
	// When the first process exits and its pipe write end closes, the other
	// process receives EOF and terminates naturally.
	type waitOut struct {
		state    *os.ProcessState
		err      error
		wallTime time.Duration
	}
	cCh := make(chan waitOut, 1)
	iCh := make(chan waitOut, 1)

	go func() {
		err := cCmd.Wait()
		cCh <- waitOut{cCmd.ProcessState, err, time.Since(start)}
	}()
	go func() {
		err := iCmd.Wait()
		iCh <- waitOut{iCmd.ProcessState, err, time.Since(start)}
	}()

	cOut := <-cCh
	iOut := <-iCh
	totalWall := time.Since(start)

	<-logsDone

	return &sandbox.PairExecResult{
		Contestant:       *parseResult(cOut.state, cOut.err, cOut.wallTime, cLogBuf.String()),
		Interactor:       *parseResult(iOut.state, iOut.err, iOut.wallTime, iLogBuf.String()),
		InteractorOutput: verdictBuf.String(),
		WallTimeMs:       totalWall.Milliseconds(),
	}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// copyReaderToFile materialises an io.Reader into a file at path.
func copyReaderToFile(r io.Reader, path string) error {
	if r == nil {
		return nil
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// closeAll closes every non-nil *os.File, ignoring errors.
// Used to clean up pipe ends after Start() or on error paths.
func closeAll(files ...*os.File) {
	for _, f := range files {
		if f != nil {
			f.Close()
		}
	}
}

// drainConcurrently starts goroutines that copy each reader into its buffer.
// Returns a channel that is closed when both copies are complete.
func drainConcurrently(buf1 *bytes.Buffer, r1 io.Reader, buf2 *bytes.Buffer, r2 io.Reader) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ch1 := make(chan struct{})
		ch2 := make(chan struct{})
		go func() { defer close(ch1); io.Copy(buf1, r1) }() //nolint:errcheck
		go func() { defer close(ch2); io.Copy(buf2, r2) }() //nolint:errcheck
		<-ch1
		<-ch2
	}()
	return done
}
