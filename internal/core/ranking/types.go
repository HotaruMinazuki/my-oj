// Package ranking implements the real-time scoreboard update pipeline and
// WebSocket broadcast layer for contest rankings.
package ranking

import (
	"fmt"
	"time"

	"github.com/your-org/my-oj/internal/models"
)

// ─── Redis Key Schema ─────────────────────────────────────────────────────────

// redisKey returns a namespaced Redis key for a ranking resource.
func redisKey(contestID models.ID, suffix string) string {
	return fmt.Sprintf("oj:ranking:%d:%s", contestID, suffix)
}

// Key constants (use via redisKey(cid, KeyXxx)).
const (
	// keyEntries is a Redis Hash: field = "{uid}:{pid}" → ScoreEntry JSON
	keyEntries = "entries"
	// keyAggregates is a Redis Hash: field = "{uid}" → UserAggregate JSON
	keyAggregates = "aggregates"
	// keyBoard is a Redis Sorted Set: member = "{uid}", score = composite ranking key
	keyBoard = "board"
	// keySnapshot is a Redis String: full JSON of the current RankSnapshot
	keySnapshot = "snapshot"
	// keyEvents is the Redis Pub/Sub channel name for this contest's delta stream
	keyEvents = "events"
	// keyFirstBlood is a Redis Hash: field = "{pid}" → "1" (set when first blood lands)
	keyFirstBlood = "firstblood"
)

// entryField formats the Hash field key for a (user, problem) pair.
func entryField(userID, problemID models.ID) string {
	return fmt.Sprintf("%d:%d", userID, problemID)
}

// boardScore converts a user's rank stats into a single comparable Redis
// sorted-set score.  Higher score = better rank.
//
//	score = solved * 1_000_000 - penalty_minutes
//
// This is monotone in both keys and works for any realistic contest.
func boardScore(solved, penaltyMinutes int) float64 {
	return float64(solved*1_000_000 - penaltyMinutes)
}

// ─── Delta (incremental scoreboard event) ────────────────────────────────────

// EventType classifies a RankDelta for client-side rendering.
type EventType string

const (
	EventSubmission EventType = "submission" // a judged submission changed an entry
	EventFirstBlood EventType = "firstblood" // first team to solve a problem
	EventUnfreeze   EventType = "unfreeze"   // 滚榜: one frozen result revealed
	EventSnapshot   EventType = "snapshot"   // full board on initial connect
)

// RankDelta is the small JSON payload pushed over WebSocket on every scoreboard change.
// Designed to be < 512 bytes so thousands of concurrent sends are bandwidth-cheap.
type RankDelta struct {
	Type      EventType `json:"type"`
	ContestID models.ID `json:"contest_id"`
	Timestamp time.Time `json:"ts"`

	// Which (user, problem) changed.
	UserID    models.ID `json:"user_id"`
	ProblemID models.ID `json:"problem_id"`

	// Problem-level change.
	OldStatus string `json:"old_status"` // "none" | "wa" | "ac" | "pending"
	NewStatus string `json:"new_status"`

	// User aggregate after the change.
	NewSolved  int `json:"new_solved"`
	NewPenalty int `json:"new_penalty"`

	// Rank change.
	OldRank int `json:"old_rank"`
	NewRank int `json:"new_rank"`
}

// entryStatus returns the short status string sent in a RankDelta.
func entryStatus(e *ScoreEntryView) string {
	if e == nil {
		return "none"
	}
	if e.Accepted {
		return "ac"
	}
	if e.IsPending {
		return "pending"
	}
	if e.AttemptCount > 0 {
		return "wa"
	}
	return "none"
}

// ─── Snapshot (full board, sent on initial WebSocket connect) ─────────────────

// ScoreEntryView is the client-facing view of a ScoreEntry (no frozen internals).
type ScoreEntryView struct {
	Accepted          bool      `json:"accepted"`
	DisplayScore      int       `json:"display_score"`
	Penalty           int       `json:"penalty"`
	AttemptCount      int       `json:"attempt_count"`
	WrongAttemptCount int       `json:"wrong_attempt_count"`
	BestSubmitTime    time.Time `json:"best_submit_time,omitempty"`
	IsPending         bool      `json:"is_pending,omitempty"`
	FrozenAttempts    int       `json:"frozen_attempts,omitempty"`
	IsFirstBlood      bool      `json:"is_first_blood,omitempty"`
}

// RankRowView is one ranked row in the snapshot or a full-board response.
type RankRowView struct {
	Rank         int                        `json:"rank"`
	UserID       models.ID                  `json:"user_id"`
	TotalScore   int                        `json:"total_score"`
	TotalPenalty int                        `json:"total_penalty"`
	Entries      map[models.ID]*ScoreEntryView `json:"entries"` // keyed by problemID
}

// RankSnapshot is the full board payload sent to a client on connection.
type RankSnapshot struct {
	ContestID models.ID     `json:"contest_id"`
	Rows      []RankRowView `json:"rows"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// UserAggregate is the per-user summary cached in Redis for fast rank computation.
type UserAggregate struct {
	UserID       models.ID `json:"user_id"`
	Solved       int       `json:"solved"`
	PenaltyMins  int       `json:"penalty_mins"`
	LastACTime   time.Time `json:"last_ac_time,omitempty"`
}
