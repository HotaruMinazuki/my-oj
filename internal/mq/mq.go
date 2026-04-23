// Package mq defines the message-queue interface contract used by the API server
// (publisher) and judger nodes (consumer).
//
// Concrete implementations live in sub-packages (mq/redis).
// Swap the implementation at wire-up time; callers never import a concrete package.
package mq

import (
	"context"
	"encoding/json"
	"time"

	"github.com/your-org/my-oj/internal/models"
)

// ─── Transport Primitives ─────────────────────────────────────────────────────

// Message is a raw MQ envelope delivered to a MessageHandler.
type Message struct {
	// ID is the broker-assigned identifier (e.g., Redis Stream entry ID "1700000000000-0").
	// Passed back to Ack() for explicit acknowledgement.
	ID      string
	Payload []byte
}

// MessageHandler is called by the Consumer for each received message.
// Returning nil causes the message to be ACK'd.
// Returning a non-nil error leaves the message in the pending list for retry.
type MessageHandler func(ctx context.Context, msg Message) error

// Consumer pulls messages from a named queue and delivers them to a handler.
type Consumer interface {
	// Subscribe blocks, delivering messages to handler until ctx is cancelled.
	// Guarantees at-least-once delivery: each message is ACK'd only when
	// handler returns nil.
	Subscribe(ctx context.Context, queue string, handler MessageHandler) error
	Close() error
}

// Publisher sends messages to a named queue.
type Publisher interface {
	// Publish serialises payload and appends it to the queue.
	// Returns the broker-assigned message ID.
	Publish(ctx context.Context, queue string, payload []byte) (id string, err error)
	Close() error
}

// ─── Well-Known Queue Names ───────────────────────────────────────────────────

const (
	QueueJudgeTasks   = "oj:judge:tasks"   // API server → Judger node
	QueueJudgeResults = "oj:judge:results"  // Judger node → API server
)

// ─── Message Payloads ─────────────────────────────────────────────────────────

// TaskMessage is the envelope published to QueueJudgeTasks.
// It embeds models.JudgeTask unchanged so the judger node can use it directly.
type TaskMessage struct {
	models.JudgeTask
	EnqueuedAt time.Time `json:"enqueued_at"`
}

// ResultMessage is the envelope published to QueueJudgeResults.
// The API server consumes this to update the Submission row and notify ranking.
type ResultMessage struct {
	TaskID       string                  `json:"task_id"`
	SubmissionID models.ID               `json:"submission_id"`
	// UserID and ProblemID are denormalised here so the ranking service can
	// update the scoreboard without an extra DB round-trip.
	UserID    models.ID  `json:"user_id"`
	ProblemID models.ID  `json:"problem_id"`
	// ContestID is nil for out-of-contest (practice) submissions.
	ContestID *models.ID `json:"contest_id,omitempty"`

	Status     models.SubmissionStatus `json:"status"`
	Score      int                     `json:"score"`
	TimeUsedMs int64                   `json:"time_used_ms"`
	MemUsedKB  int64                   `json:"mem_used_kb"`
	// CompileLog is populated only on CE.
	CompileLog      string                  `json:"compile_log,omitempty"`
	JudgeMessage    string                  `json:"judge_message,omitempty"`
	TestCaseResults []models.TestCaseResult `json:"test_case_results,omitempty"`
	JudgeNodeID     string                  `json:"judge_node_id"`
	JudgedAt        time.Time               `json:"judged_at"`
}

// ─── Serialisation Helpers ────────────────────────────────────────────────────

func MarshalTask(t *TaskMessage) ([]byte, error)   { return json.Marshal(t) }
func MarshalResult(r *ResultMessage) ([]byte, error) { return json.Marshal(r) }

func UnmarshalTask(b []byte) (*TaskMessage, error) {
	var t TaskMessage
	return &t, json.Unmarshal(b, &t)
}

func UnmarshalResult(b []byte) (*ResultMessage, error) {
	var r ResultMessage
	return &r, json.Unmarshal(b, &r)
}
