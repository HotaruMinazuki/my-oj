// Package contest implements the Strategy pattern for contest scoring.
//
// Adding a new contest type:
//  1. Implement the Strategy interface.
//  2. Register it in NewRegistry().
//  3. No other code changes required.
package contest

import (
	"fmt"
	"sort"
	"time"

	"github.com/your-org/my-oj/internal/models"
)

// ─── Domain Types ─────────────────────────────────────────────────────────────

// SubmissionEvent is the input to a scoring Strategy — one judged submission.
type SubmissionEvent struct {
	UserID    models.ID
	ProblemID models.ID
	Status    models.SubmissionStatus
	// Score is the raw judger score (normalised 0–1000 for OI; ignored for ICPC).
	Score        int
	SubmitTime   time.Time
	ContestStart time.Time
	// FreezeTime is the moment the public scoreboard freezes.
	// nil = contest has no freeze.  Set by the caller from Contest.FreezeTime.
	FreezeTime *time.Time
}

// ScoreEntry is the per-user, per-problem state on the scoreboard.
// All fields are designed to be stored as JSON in Redis (or JSONB in Postgres).
type ScoreEntry struct {
	UserID    models.ID `json:"user_id"`
	ProblemID models.ID `json:"problem_id"`

	// ── Public display fields ─────────────────────────────────────────────────
	// Accepted is true when the problem is considered solved for scoring purposes.
	Accepted bool `json:"accepted"`
	// DisplayScore is the points shown on the public scoreboard.
	// ICPC: 0 or 1 (solved/unsolved).  OI: 0–MaxScore.
	DisplayScore int `json:"display_score"`
	// Penalty is the tiebreaker value.
	// ICPC: elapsed_minutes_at_AC + 20 × WA_before_AC.  OI: 0.
	Penalty int `json:"penalty"`
	// AttemptCount is the total number of submissions shown on the scoreboard.
	// ICPC: counts only pre-freeze submissions.
	AttemptCount int `json:"attempt_count"`
	// WrongAttemptCount is the number of pre-AC wrong submissions.
	// Used internally for ICPC penalty calculation; also drives the "+N" badge.
	WrongAttemptCount int `json:"wrong_attempt_count"`
	// BestSubmitTime is the timestamp of the submission that produced DisplayScore.
	BestSubmitTime time.Time `json:"best_submit_time,omitempty"`

	// ── Freeze mechanic (ICPC) ────────────────────────────────────────────────
	// FrozenAttempts is the count of submissions made after the scoreboard freeze.
	// These submissions are judged but their results are hidden on the public board.
	FrozenAttempts int `json:"frozen_attempts,omitempty"`
	// IsPending is true when FrozenAttempts > 0 and the problem is not yet solved
	// via a pre-freeze submission.  The UI shows "?" on pending cells.
	IsPending bool `json:"is_pending,omitempty"`
	// FrozenResults stores the actual judged outcomes of frozen submissions
	// in chronological order.  Consumed one-by-one during 滚榜 (rolling reveal).
	FrozenResults []models.SubmissionStatus `json:"frozen_results,omitempty"`

	// ── Metadata ─────────────────────────────────────────────────────────────
	// IsFirstBlood is true for the first team to solve this problem.
	// Set by the RankingService; shown as a badge in the UI.
	IsFirstBlood bool `json:"is_first_blood,omitempty"`
}

// RankRow is one entry in the rendered scoreboard sorted list.
type RankRow struct {
	// Rank is 1-based; tied teams share the same rank number.
	Rank       int                        `json:"rank"`
	UserID     models.ID                  `json:"user_id"`
	TotalScore int                        `json:"total_score"`
	// TotalPenalty is strategy-specific: minutes for ICPC, 0 for OI.
	TotalPenalty int                      `json:"total_penalty"`
	// LastACTime is used as a secondary tiebreaker (earlier = better).
	LastACTime   time.Time                `json:"last_ac_time,omitempty"`
	Entries      map[models.ID]*ScoreEntry `json:"entries"`
}

// ContestSettings is a flexible map of strategy-specific configuration keys.
// Each Strategy documents its own recognised keys.
type ContestSettings = models.ContestSettings

// ─── Strategy Interface ───────────────────────────────────────────────────────

// Strategy is the scoring algorithm for one contest type.
// All methods are stateless and pure; the caller owns all state.
type Strategy interface {
	// Name returns the ContestType this strategy handles.
	Name() models.ContestType

	// Apply integrates one submission event into the existing ScoreEntry for
	// (event.UserID, event.ProblemID).  prev is nil on the first submission.
	// Returns a new ScoreEntry (never mutates prev).
	Apply(event SubmissionEvent, prev *ScoreEntry, settings ContestSettings) *ScoreEntry

	// Rank sorts a flat slice of ScoreEntries into ranked RankRows.
	// Called on every scoreboard refresh.  Must be deterministic for ties.
	Rank(entries []*ScoreEntry, settings ContestSettings) []RankRow

	// IsFinalised returns true when the entry is settled and no future event
	// can change it.  Used by the RankingService to skip unnecessary recomputes.
	IsFinalised(entry *ScoreEntry, settings ContestSettings) bool
}

// ─── Registry ─────────────────────────────────────────────────────────────────

// Registry maps ContestType → Strategy.
type Registry struct {
	m map[models.ContestType]Strategy
}

func NewRegistry() *Registry {
	r := &Registry{m: make(map[models.ContestType]Strategy)}
	r.Register(&ICPCStrategy{})
	r.Register(&OIStrategy{})
	return r
}

func (r *Registry) Register(s Strategy) { r.m[s.Name()] = s }

func (r *Registry) Get(ct models.ContestType) (Strategy, error) {
	s, ok := r.m[ct]
	if !ok {
		return nil, fmt.Errorf("contest: no strategy for ContestType %q", ct)
	}
	return s, nil
}

// ─── OI Strategy ──────────────────────────────────────────────────────────────

// OIStrategy scores by the maximum score achieved.
// In strict mode ("strict_last_submission": true), the LAST submission wins.
type OIStrategy struct{}

func (s *OIStrategy) Name() models.ContestType { return models.ContestOI }

func (s *OIStrategy) Apply(event SubmissionEvent, prev *ScoreEntry, settings ContestSettings) *ScoreEntry {
	entry := cloneOrNew(prev, event)
	entry.AttemptCount++

	strict, _ := settings["strict_last_submission"].(bool)

	if strict || event.Score > entry.DisplayScore {
		entry.DisplayScore = event.Score
		entry.BestSubmitTime = event.SubmitTime
		if event.Score > 0 {
			entry.Accepted = true
		}
	}
	return &entry
}

func (s *OIStrategy) Rank(entries []*ScoreEntry, _ ContestSettings) []RankRow {
	return rankByScoreDesc(entries)
}

func (s *OIStrategy) IsFinalised(_ *ScoreEntry, _ ContestSettings) bool {
	return false // OI: a later submission can always improve the score
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

// cloneOrNew returns a copy of prev (or a zero-valued entry if prev == nil).
func cloneOrNew(prev *ScoreEntry, ev SubmissionEvent) ScoreEntry {
	if prev != nil {
		cp := *prev
		// Deep-copy the FrozenResults slice to avoid shared backing array.
		if len(prev.FrozenResults) > 0 {
			cp.FrozenResults = make([]models.SubmissionStatus, len(prev.FrozenResults))
			copy(cp.FrozenResults, prev.FrozenResults)
		}
		return cp
	}
	return ScoreEntry{UserID: ev.UserID, ProblemID: ev.ProblemID}
}

// rankByScoreDesc is a generic ranker for score-descending contests (OI).
func rankByScoreDesc(entries []*ScoreEntry) []RankRow {
	// Aggregate per user.
	users := aggregateUsers(entries, func(row *RankRow, e *ScoreEntry) {
		row.TotalScore += e.DisplayScore
		if e.Accepted && e.BestSubmitTime.After(row.LastACTime) {
			row.LastACTime = e.BestSubmitTime
		}
	})

	sort.Slice(users, func(i, j int) bool {
		return users[i].TotalScore > users[j].TotalScore
	})
	return assignRanks(users, func(a, b RankRow) bool {
		return a.TotalScore == b.TotalScore
	})
}

// aggregateUsers groups ScoreEntries by UserID and applies accumFn.
func aggregateUsers(entries []*ScoreEntry, accumFn func(*RankRow, *ScoreEntry)) []RankRow {
	m := make(map[models.ID]*RankRow)
	for _, e := range entries {
		row, ok := m[e.UserID]
		if !ok {
			row = &RankRow{UserID: e.UserID, Entries: make(map[models.ID]*ScoreEntry)}
			m[e.UserID] = row
		}
		row.Entries[e.ProblemID] = e
		accumFn(row, e)
	}
	rows := make([]RankRow, 0, len(m))
	for _, r := range m {
		rows = append(rows, *r)
	}
	return rows
}

// assignRanks sets the Rank field, giving the same rank to tied rows.
// Rows must already be sorted.
func assignRanks(rows []RankRow, tied func(a, b RankRow) bool) []RankRow {
	for i := range rows {
		if i == 0 || !tied(rows[i-1], rows[i]) {
			rows[i].Rank = i + 1
		} else {
			rows[i].Rank = rows[i-1].Rank
		}
	}
	return rows
}
