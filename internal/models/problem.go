package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// ProblemJudgeMeta carries the non-JSONB judging fields of a Problem.
// These are loaded separately from JudgeConfig because they come from
// different columns (judge_type, time_limit_ms, mem_limit_kb vs judge_config JSONB).
type ProblemJudgeMeta struct {
	JudgeType   JudgeType
	TimeLimitMs int64
	MemLimitKB  int64
}

// CommChannel defines one unidirectional IPC pipe between two contestant processes.
// The full channel graph for a communication problem is a slice of these.
type CommChannel struct {
	Name         string `json:"name"`           // 调试用名称，�?"pipe_0�?"
	From         int    `json:"from"`           // 写端进程索引�?-based�?	To           int    `json:"to"`             // 读端进程索引�?-based�?	Type         string `json:"type"`           // "pipe" | "shm"（共享内存，待扩展）
	BufferSizeKB int    `json:"buffer_size_kb"` // 0 = OS default (64 KB on Linux)
}

// JudgeConfig carries judge-type-specific settings for a problem.
// Stored as JSONB �?the schema evolves without ALTER TABLE.
//
// Only the fields relevant to the problem's JudgeType are populated:
//
//	Standard   �?(nothing extra)
//	Special     �?CheckerPath, CheckerArgs
//	Interactive �?InteractorPath
//	Comm        �?CommProcessCount, CommChannels, GraderPath
type JudgeConfig struct {
	// Special judge checker binary path on shared storage.
	CheckerPath string   `json:"checker_path,omitempty"`
	CheckerArgs []string `json:"checker_args,omitempty"`

	// Interactive judge interactor binary path on shared storage.
	InteractorPath string `json:"interactor_path,omitempty"`

	// Communication problem: number of contestant processes (�?).
	CommProcessCount int           `json:"comm_process_count,omitempty"`
	CommChannels     []CommChannel `json:"comm_channels,omitempty"`
	// GraderPath is an optional trusted grader that observes the comm group.
	GraderPath string `json:"grader_path,omitempty"`
}

func (c JudgeConfig) Value() (driver.Value, error) { return json.Marshal(c) }
func (c *JudgeConfig) Scan(src any) error {
	b, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("JudgeConfig.Scan: expected []byte, got %T", src)
	}
	return json.Unmarshal(b, c)
}

// Problem is the canonical representation of a competitive programming problem.
type Problem struct {
	ID        ID        `db:"id"    json:"id"`
	Title     string    `db:"title" json:"title"`
	// Statement is Markdown; large blobs are served via CDN, not inlined in API responses.
	Statement string    `db:"statement" json:"statement,omitempty"`

	// Global limits �?may be overridden per-language when the JudgeTask is built.
	TimeLimitMs int64 `db:"time_limit_ms" json:"time_limit_ms"`
	MemLimitKB  int64 `db:"mem_limit_kb"  json:"mem_limit_kb"`

	JudgeType   JudgeType   `db:"judge_type"   json:"judge_type"`
	JudgeConfig JudgeConfig `db:"judge_config"  json:"judge_config"`

	// AllowedLangs is nil �?all configured languages allowed.
	AllowedLangs []Language `db:"allowed_langs" json:"allowed_langs,omitempty"`

	// IsPublic controls visibility outside of a contest context.
	IsPublic bool `db:"is_public" json:"is_public"`
	AuthorID ID   `db:"author_id" json:"author_id"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}
