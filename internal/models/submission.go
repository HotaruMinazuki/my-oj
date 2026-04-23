package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// TestCaseResult records the judger's verdict for a single test case execution.
type TestCaseResult struct {
	TestCaseID ID               `json:"test_case_id"`
	GroupID    int              `json:"group_id"`
	Status     SubmissionStatus `json:"status"`
	TimeUsedMs int64            `json:"time_used_ms"`
	MemUsedKB  int64            `json:"mem_used_kb"`
	// Score is the points actually awarded (0 for ICPC; partial for OI/IOI).
	Score int `json:"score"`
	// CheckerOutput is checker/interactor stderr — useful for "wrong answer" debugging.
	// Truncated to 4 KB before storage.
	CheckerOutput string `json:"checker_output,omitempty"`
}

type TestCaseResults []TestCaseResult

func (t TestCaseResults) Value() (driver.Value, error) { return json.Marshal(t) }
func (t *TestCaseResults) Scan(src any) error {
	b, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("TestCaseResults.Scan: expected []byte, got %T", src)
	}
	return json.Unmarshal(b, t)
}

// Submission is created the moment a user submits code, before any judging occurs.
type Submission struct {
	ID        ID  `db:"id"         json:"id"`
	UserID    ID  `db:"user_id"    json:"user_id"`
	ProblemID ID  `db:"problem_id" json:"problem_id"`
	// ContestID is NULL for out-of-contest practice submissions.
	ContestID *ID `db:"contest_id" json:"contest_id,omitempty"`

	Language Language `db:"language" json:"language"`
	// SourceCodePath points to the raw source on shared storage.
	// Never stored in the DB column directly to avoid table bloat.
	SourceCodePath string `db:"source_code_path" json:"-"`

	Status SubmissionStatus `db:"status" json:"status"`
	// Score is the total points awarded by the scoring Strategy.
	Score int `db:"score" json:"score"`

	// TimeUsedMs / MemUsedKB reflect the worst-case resource usage across all test cases.
	TimeUsedMs int64 `db:"time_used_ms" json:"time_used_ms"`
	MemUsedKB  int64 `db:"mem_used_kb"  json:"mem_used_kb"`

	// CompileLog holds the compiler's stderr on CE; truncated to 64 KB.
	CompileLog string `db:"compile_log" json:"compile_log,omitempty"`
	// JudgeMessage is a human-readable summary produced by the checker or interactor.
	JudgeMessage string `db:"judge_message" json:"judge_message,omitempty"`

	// TestCaseResults is the per-case breakdown, critical for OI/IOI partial scoring display.
	TestCaseResults TestCaseResults `db:"test_case_results" json:"test_case_results,omitempty"`

	// JudgeNodeID identifies which judger node processed this submission.
	// Used for debugging distributed judge failures.
	JudgeNodeID string `db:"judge_node_id" json:"judge_node_id,omitempty"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}
