package nsjail

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/judger/sandbox"
)

// ExecuteGroup implements sandbox.Session for communication problems.
//
// FD assignment strategy per process:
//
//	FD 0 (stdin)  = read  end of the FIRST channel where channel.To   == processIdx
//	FD 1 (stdout) = write end of the FIRST channel where channel.From == processIdx
//	FD 3          = nsjail log pipe (internal; not visible to contestant)
//	FD 4+         = additional channel FDs, passed via --pass_fd
//	                  even index → read  end (incoming channel)
//	                  odd  index → write end (outgoing channel)
//
// The contestant program is expected to know its own index from argv[1].
// Additional channel FDs are communicated via a well-known convention:
// after argv[1], argv[2..] carries the FD numbers for extra channels in order.
//
// Example: 3-process ring A→B→C→A
//
//	channels: [{From:0,To:1}, {From:1,To:2}, {From:2,To:0}]
//
//	Process 0: stdin=c[2].r  stdout=c[0].w  (extra: none)
//	Process 1: stdin=c[0].r  stdout=c[1].w  (extra: none)
//	Process 2: stdin=c[1].r  stdout=c[2].w  (extra: none)
//
// ─────────────────────────────────────────────────────────────────────────────
// Deadlock prevention:
//
//	All pipe ends are accumulated per process.  After ALL processes are started,
//	the parent closes every pipe end it holds.  This is done in a single batch
//	AFTER the last Start() call to avoid premature EOF in the child.
func (s *Session) ExecuteGroup(ctx context.Context, req *sandbox.GroupExecRequest) (*sandbox.GroupExecResult, error) {
	n := len(req.Processes)
	if n == 0 {
		return nil, fmt.Errorf("nsjail.ExecuteGroup: no processes specified")
	}

	// ── Create one OS pipe per channel ───────────────────────────────────────
	type pipeEnds struct {
		r, w *os.File
		name string
	}
	pipes := make([]pipeEnds, len(req.Channels))
	for i, ch := range req.Channels {
		r, w, err := os.Pipe()
		if err != nil {
			// Clean up already-created pipes before returning.
			for j := 0; j < i; j++ {
				pipes[j].r.Close()
				pipes[j].w.Close()
			}
			return nil, fmt.Errorf("nsjail.ExecuteGroup: pipe %q: %w", ch.Name, err)
		}
		pipes[i] = pipeEnds{r, w, ch.Name}
	}

	// Collect every pipe end opened in parent; we close them all after Start().
	var allParentFDs []*os.File
	for _, p := range pipes {
		allParentFDs = append(allParentFDs, p.r, p.w)
	}

	// ── Assign FDs per process ────────────────────────────────────────────────
	type procPipes struct {
		stdin      *os.File   // → FD 0
		stdout     *os.File   // → FD 1
		extraFiles []*os.File // → FD 4, 5, ... (FD 3 is the nsjail log pipe)
		extraFDNos []int      // FD numbers inside the sandbox for the extra files
		extraArgs  []string   // appended to argv to communicate extra FD numbers
	}
	pp := make([]procPipes, n)

	for ci, ch := range req.Channels {
		// Recipient process: this channel's read end.
		toIdx := ch.To
		if toIdx < 0 || toIdx >= n {
			closeAll(allParentFDs...)
			return nil, fmt.Errorf("nsjail.ExecuteGroup: channel %q: To=%d out of range [0,%d)", ch.Name, toIdx, n)
		}
		if pp[toIdx].stdin == nil {
			pp[toIdx].stdin = pipes[ci].r
		} else {
			// Additional read end for this process.
			extraFD := 4 + len(pp[toIdx].extraFiles) // FD 3 is always the log pipe
			pp[toIdx].extraFiles = append(pp[toIdx].extraFiles, pipes[ci].r)
			pp[toIdx].extraFDNos = append(pp[toIdx].extraFDNos, extraFD)
			pp[toIdx].extraArgs = append(pp[toIdx].extraArgs, fmt.Sprintf("%d", extraFD))
		}

		// Sender process: this channel's write end.
		fromIdx := ch.From
		if fromIdx < 0 || fromIdx >= n {
			closeAll(allParentFDs...)
			return nil, fmt.Errorf("nsjail.ExecuteGroup: channel %q: From=%d out of range [0,%d)", ch.Name, fromIdx, n)
		}
		if pp[fromIdx].stdout == nil {
			pp[fromIdx].stdout = pipes[ci].w
		} else {
			extraFD := 4 + len(pp[fromIdx].extraFiles)
			pp[fromIdx].extraFiles = append(pp[fromIdx].extraFiles, pipes[ci].w)
			pp[fromIdx].extraFDNos = append(pp[fromIdx].extraFDNos, extraFD)
			pp[fromIdx].extraArgs = append(pp[fromIdx].extraArgs, fmt.Sprintf("%d", extraFD))
		}
	}

	// ── Build and start each contestant process ───────────────────────────────
	cmds := make([]*exec.Cmd, n)
	logPipes := make([]struct{ r, w *os.File }, n)
	var allLogWs []*os.File

	for i, procReq := range req.Processes {
		// Log pipe for this process's nsjail instance.
		lr, lw, err := os.Pipe()
		if err != nil {
			// Kill any already-started processes.
			for j := 0; j < i; j++ {
				_ = syscall.Kill(-cmds[j].Process.Pid, syscall.SIGKILL)
				_ = cmds[j].Wait()
			}
			closeAll(allParentFDs...)
			closeAll(allLogWs...)
			return nil, fmt.Errorf("nsjail.ExecuteGroup: log pipe for process %d: %w", i, err)
		}
		logPipes[i] = struct{ r, w *os.File }{lr, lw}
		allLogWs = append(allLogWs, lw)

		// nsjail args for this process.
		args := buildBaseArgs(&s.nsCfg, s.sbCfg, procReq.Limits)
		// Pass extra channel FDs through the sandbox.
		args = append(args, buildPassFDArgs(pp[i].extraFDNos)...)
		args = append(args, "--")
		args = append(args, procReq.Executable)
		args = append(args, procReq.Args...)
		// Append process index so the program knows its role.
		args = append(args, fmt.Sprintf("%d", i))
		// Append extra FD numbers so the program can open them.
		args = append(args, pp[i].extraArgs...)

		cmd := exec.CommandContext(ctx, s.nsCfg.BinaryPath, args...)
		cmd.Stdin = pp[i].stdin
		cmd.Stdout = pp[i].stdout
		cmd.Stderr = &bytes.Buffer{} // contestant stderr discarded
		// ExtraFiles layout:
		//   [0] = lw → FD 3 (nsjail log; not visible to contestant)
		//   [1..] = extra channel FDs → FD 4, 5, ...
		cmd.ExtraFiles = append([]*os.File{lw}, pp[i].extraFiles...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		if err := cmd.Start(); err != nil {
			// Kill already-started processes and clean up.
			for j := 0; j < i; j++ {
				_ = syscall.Kill(-cmds[j].Process.Pid, syscall.SIGKILL)
				_ = cmds[j].Wait()
			}
			closeAll(allParentFDs...)
			closeAll(allLogWs...)
			for _, lp := range logPipes {
				lp.r.Close()
			}
			return nil, fmt.Errorf("nsjail.ExecuteGroup: start process %d: %w", i, err)
		}
		cmds[i] = cmd
	}

	// ── Optionally start grader process ──────────────────────────────────────
	var graderCmd *exec.Cmd
	var graderBuf bytes.Buffer
	if req.GraderProcess != nil {
		// Grader runs outside the sandbox (trusted); it monitors the group's I/O.
		graderCmd = exec.CommandContext(ctx, req.GraderProcess.Executable, req.GraderProcess.Args...)
		graderCmd.Stderr = &graderBuf
		graderCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := graderCmd.Start(); err != nil {
			// Non-fatal: log and continue without grader.
			s.log.Error("failed to start grader", zap.Error(fmt.Errorf("grader: %w", err)))
			graderCmd = nil
		}
	}

	// ── CRITICAL: Close ALL parent-held pipe ends ─────────────────────────────
	// Must happen AFTER all Start() calls so children inherit the FDs first.
	// After closing, process exit propagates EOF to its pipe partners automatically.
	closeAll(allParentFDs...) // all channel pipe r/w ends
	closeAll(allLogWs...)     // all nsjail log write ends

	// ── Drain log pipes concurrently ─────────────────────────────────────────
	logBufs := make([]bytes.Buffer, n)
	logDoneCh := make(chan struct{})
	go func() {
		defer close(logDoneCh)
		done := make([]chan struct{}, n)
		for i := range n {
			done[i] = make(chan struct{})
			go func(idx int) {
				defer close(done[idx])
				//nolint:errcheck
				_, _ = logBufs[idx].ReadFrom(logPipes[idx].r)
				logPipes[idx].r.Close()
			}(i)
		}
		for i := range n {
			<-done[i]
		}
	}()

	// ── Wait for all contestant processes ─────────────────────────────────────
	start := time.Now()
	type waitOut struct {
		state    *os.ProcessState
		err      error
		wallTime time.Duration
	}
	waitChs := make([]chan waitOut, n)
	for i, cmd := range cmds {
		waitChs[i] = make(chan waitOut, 1)
		go func(i int, cmd *exec.Cmd) {
			err := cmd.Wait()
			waitChs[i] <- waitOut{cmd.ProcessState, err, time.Since(start)}
		}(i, cmd)
	}

	procResults := make([]sandbox.ExecResult, n)
	for i := range n {
		wo := <-waitChs[i]
		procResults[i] = *parseResult(wo.state, wo.err, wo.wallTime, logBufs[i].String())
	}

	// ── Wait for grader (if any) ──────────────────────────────────────────────
	if graderCmd != nil {
		_ = graderCmd.Wait()
	}

	totalWall := time.Since(start)
	<-logDoneCh

	return &sandbox.GroupExecResult{
		Processes:    procResults,
		GraderOutput: graderBuf.String(),
		WallTimeMs:   totalWall.Milliseconds(),
	}, nil
}
