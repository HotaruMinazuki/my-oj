package models

// ID is the primary key type for all models.
type ID = int64

// Language represents a supported programming language identifier.
// The actual compile/run commands are configured externally (configs/languages.yaml),
// not hardcoded here — adding a new language never requires touching this file.
type Language string

const (
	LangC      Language = "C"
	LangCPP17  Language = "C++17"
	LangCPP20  Language = "C++20"
	LangJava   Language = "Java21"
	LangPython Language = "Python3"
	LangGo     Language = "Go"
	LangRust   Language = "Rust"
)

// JudgeType determines which execution model and orchestrator a problem uses.
type JudgeType string

const (
	// JudgeStandard: diff contestant stdout against expected output (token or byte-exact).
	JudgeStandard JudgeType = "standard"
	// JudgeSpecial: a checker binary evaluates the contestant's stdout with full context.
	JudgeSpecial JudgeType = "special"
	// JudgeInteractive: contestant ↔ interactor communicate via bidirectional pipes.
	// Neither process sees the raw test input directly.
	JudgeInteractive JudgeType = "interactive"
	// JudgeCommunication: N contestant processes communicate via a configured channel graph.
	JudgeCommunication JudgeType = "communication"
)

// SubmissionStatus is the lifecycle state of a submission, flowing left→right.
type SubmissionStatus string

const (
	StatusPending     SubmissionStatus = "Pending"
	StatusCompiling   SubmissionStatus = "Compiling"
	StatusJudging     SubmissionStatus = "Judging"
	StatusAccepted    SubmissionStatus = "Accepted"
	StatusWrongAnswer SubmissionStatus = "WrongAnswer"
	StatusTLE         SubmissionStatus = "TimeLimitExceeded"
	StatusMLE         SubmissionStatus = "MemoryLimitExceeded"
	StatusRE          SubmissionStatus = "RuntimeError"
	StatusCE          SubmissionStatus = "CompileError"
	// StatusSE is returned when the judger itself encounters an internal failure;
	// it signals the task should be retried on another node.
	StatusSE SubmissionStatus = "SystemError"
)

// UserRole controls platform permissions.
type UserRole string

const (
	RoleAdmin      UserRole = "admin"
	RoleContestant UserRole = "contestant"
	RoleGuest      UserRole = "guest"
)

// ContestType identifies which scoring Strategy is loaded at runtime.
// Adding a new contest type = registering a new Strategy; no other change needed.
type ContestType string

const (
	ContestICPC   ContestType = "ICPC"
	ContestOI     ContestType = "OI"
	ContestIOI    ContestType = "IOI"
	ContestTeam   ContestType = "Team"   // 团队赛
	ContestCustom ContestType = "Custom" // 外部插件赛制
)
