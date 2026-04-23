// Package judger contains the high-level judging orchestration layer.
// It sits between the MQ consumer (task dispatch) and the sandbox (raw execution).
package judger

import (
	"context"
	"fmt"

	"github.com/your-org/my-oj/internal/judger/sandbox"
	"github.com/your-org/my-oj/internal/models"
)

// Verdict is the per-test-case outcome produced by an Orchestrator.
type Verdict struct {
	Status       models.SubmissionStatus
	Score        int
	TimeUsedMs   int64
	MemUsedKB    int64
	// JudgeMessage is the human-readable output from the checker / interactor stderr.
	// Truncated to 4 KB by the Orchestrator before returning.
	JudgeMessage string
}

// TestCaseRequest is the input to Orchestrator.RunTestCase.
// It carries everything needed to judge one test case; no DB access required.
type TestCaseRequest struct {
	// RunCmd is the full command to execute the contestant's program inside the sandbox.
	// RunCmd[0] is the executable; RunCmd[1:] are fixed args.
	// Example: ["./main"] for C++, ["python3", "main.py"] for Python.
	// For communication problems, all contestant processes use the same RunCmd.
	RunCmd []string

	TestCase    models.JudgeTestCase
	JudgeConfig models.JudgeConfig

	// Limits are already resolved (language overrides applied) by the task builder.
	Limits sandbox.ResourceLimits
}

// Orchestrator runs one test case under a specific judge type and returns a verdict.
//
// There is exactly one Orchestrator implementation per JudgeType:
//   - StandardOrchestrator   → models.JudgeStandard
//   - SpecialOrchestrator    → models.JudgeSpecial
//   - InteractiveOrchestrator → models.JudgeInteractive
//   - CommOrchestrator       → models.JudgeCommunication
//
// Implementations must not call session.Release(); the caller manages session lifetime.
type Orchestrator interface {
	// RunTestCase executes the compiled binary against one test case.
	// `session` is an already-prepared sandbox session.
	RunTestCase(ctx context.Context, req *TestCaseRequest, session sandbox.Session) (*Verdict, error)
}

// OrchestratorRegistry maps JudgeType → Orchestrator.
// It is populated at startup and is read-only during request handling.
type OrchestratorRegistry struct {
	m map[models.JudgeType]Orchestrator
}

func NewOrchestratorRegistry() *OrchestratorRegistry {
	return &OrchestratorRegistry{m: make(map[models.JudgeType]Orchestrator)}
}

// Register associates a JudgeType with its Orchestrator.
// Not safe for concurrent use; call only during application initialisation.
func (r *OrchestratorRegistry) Register(jt models.JudgeType, o Orchestrator) {
	r.m[jt] = o
}

// Get returns the Orchestrator for jt or an error if none is registered.
func (r *OrchestratorRegistry) Get(jt models.JudgeType) (Orchestrator, error) {
	o, ok := r.m[jt]
	if !ok {
		return nil, fmt.Errorf("judger: no orchestrator registered for JudgeType %q", jt)
	}
	return o, nil
}
