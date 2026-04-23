package models

// JudgeTask is the self-contained message published to the judge queue.
// A judger node MUST be able to execute the task with only this payload
// plus access to object storage (MinIO) — no DB calls during judging.
type JudgeTask struct {
	// TaskID is a UUID for idempotent deduplication at the MQ consumer.
	TaskID       string `json:"task_id"`
	SubmissionID ID     `json:"submission_id"`
	UserID       ID     `json:"user_id"`
	ProblemID    ID     `json:"problem_id"`
	ContestID    *ID    `json:"contest_id,omitempty"`

	Language Language `json:"language"`
	// SourceCodePath is the MinIO object key in the "submissions" bucket.
	// Format: "sources/{userID}/{problemID}/{uuid}.{ext}"
	// The judger downloads it via ObjectStore.GetToFile before compiling.
	SourceCodePath string `json:"source_code_path"`

	JudgeType   JudgeType   `json:"judge_type"`
	JudgeConfig JudgeConfig `json:"judge_config"`

	// Limits are fully resolved with language-specific overrides already applied.
	// The API server performs this resolution; the judger consumes as-is.
	TimeLimitMs int64 `json:"time_limit_ms"`
	MemLimitKB  int64 `json:"mem_limit_kb"`

	TestCases []JudgeTestCase `json:"test_cases"`

	// Priority determines queue ordering; higher = scheduled sooner.
	// Contest submissions should be higher priority than practice.
	Priority int `json:"priority"`
}

// JudgeTestCase is the per-test-case payload resolved by the API server.
// Paths are validated and access-checked before enqueuing.
type JudgeTestCase struct {
	TestCaseID ID     `json:"test_case_id"`
	GroupID    int    `json:"group_id"`
	Ordinal    int    `json:"ordinal"`
	InputPath  string `json:"input_path"`
	// OutputPath is empty for interactive/communication problems where
	// the interactor/grader determines correctness without a golden output file.
	OutputPath string `json:"output_path,omitempty"`
	Score      int    `json:"score"`
}
