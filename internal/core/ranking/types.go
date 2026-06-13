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

// Key constants (use via redisKey(cid, keyXxx)).
const (
	// keyEntries is a Redis Hash: field = "{uid}:{pid}" → ScoreEntry JSON
	keyEntries = "entries"
	// keySnapshot is a Redis String holding the latest BoardSnapshot JSON.
	// Read by the REST endpoint and on WebSocket connect.
	keySnapshot = "snapshot"
	// keyEvents is the Redis Pub/Sub channel for this contest's board updates.
	// Payloads are full pre-serialised WS frames: {"type":"snapshot","data":{…}}.
	keyEvents = "events"
	// keyFirstBlood is a Redis Hash: field = "{pid}" → "1" (set when first blood lands)
	keyFirstBlood = "firstblood"
)

// entryField formats the Hash field key for a (user, problem) pair.
func entryField(userID, problemID models.ID) string {
	return fmt.Sprintf("%d:%d", userID, problemID)
}

// EventType classifies a WS frame for client-side rendering.
type EventType string

const (
	// EventSnapshot carries the full board. Sent on connect and after every
	// scoreboard change — at this scale a full snapshot is small and pushing it
	// keeps the client trivially consistent (no delta-merge bugs).
	EventSnapshot EventType = "snapshot"
)

// ─── Board snapshot (client-facing; shape consumed by RankingBoard.vue) ───────

// BoardCell is one (user, problem) cell on the rendered scoreboard.
type BoardCell struct {
	Solved     bool `json:"solved"`
	Attempts   int  `json:"attempts"` // submissions counted on the public board
	Pending    int  `json:"pending"`  // frozen submissions (shown as "?")
	Penalty    int  `json:"penalty"`  // ICPC: minutes incl. WA penalty; OI: 0
	Score      int  `json:"score"`    // OI/IOI display score; ICPC: 0/1
	FirstBlood bool `json:"first_blood,omitempty"`
}

// BoardRow is one contestant's row.
type BoardRow struct {
	Rank         int                  `json:"rank"`
	UserID       models.ID            `json:"user_id"`
	Username     string               `json:"username"`
	Organization string               `json:"organization,omitempty"`
	Problems     map[string]BoardCell `json:"problems"` // keyed by display label ("A", "B", …)
	TotalSolved  int                  `json:"total_solved"`
	TotalPenalty int                  `json:"total_penalty"`
	TotalScore   int                  `json:"total_score"` // OI/IOI total points
}

// BoardSnapshot is the full board payload.
type BoardSnapshot struct {
	ContestID   models.ID  `json:"contest_id"`
	Frozen      bool       `json:"frozen"`
	Problems    []string   `json:"problems"` // ordered display labels
	Contestants []BoardRow `json:"contestants"`
	UpdatedAt   time.Time  `json:"updated_at"`
}
