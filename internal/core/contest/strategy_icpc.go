package contest

import (
	"sort"
	"time"

	"github.com/your-org/my-oj/internal/models"
)

// ─── ICPC Strategy ────────────────────────────────────────────────────────────

// ICPCStrategy implements the International Collegiate Programming Contest scoring rules:
//
//   - Ranking primary key:   number of problems solved (descending)
//   - Ranking secondary key: total penalty time in minutes (ascending)
//   - Ranking tertiary key:  time of last accepted submission (ascending)
//
// Penalty per solved problem:
//
//	penalty_p = elapsed_minutes_at_AC + penalty_per_wrong_attempt × WA_before_AC
//
// Total penalty = sum of penalty_p over all solved problems.
// WA/TLE/MLE/RE/CE on an unsolved problem incurs NO penalty by itself —
// only WA submissions submitted BEFORE the eventual AC are penalized.
//
// Freeze mechanic:
//
//	After Contest.FreezeTime, submissions are judged but results are hidden on the
//	public scoreboard.  Affected cells show "?" (IsPending = true).
//	The frozen results are revealed during the post-contest 滚榜 ceremony via
//	RankingService.UnfreezeNext(), which pops FrozenResults one by one.
type ICPCStrategy struct{}

func (s *ICPCStrategy) Name() models.ContestType { return models.ContestICPC }

// Apply integrates one judged submission into the current ScoreEntry.
//
// State transitions:
//
//	Accepted (pre-freeze) → immutable; subsequent submissions are no-ops
//	Pre-freeze WA         → WrongAttemptCount++; no penalty yet (deferred to AC)
//	Pre-freeze AC         → compute full penalty; mark Accepted
//	Post-freeze any       → FrozenAttempts++; append to FrozenResults; IsPending = true
func (s *ICPCStrategy) Apply(event SubmissionEvent, prev *ScoreEntry, settings ContestSettings) *ScoreEntry {
	entry := cloneOrNew(prev, event)

	// ── Already solved pre-freeze: no further changes ─────────────────────────
	// A post-freeze submission after an AC is silently recorded but doesn't affect
	// the scoreboard (the problem is already solved).
	if entry.Accepted {
		return &entry
	}

	// ── Post-freeze submission: hide result, record for 滚榜 ─────────────────
	if isPostFreeze(event.SubmitTime, event.FreezeTime) {
		entry.FrozenAttempts++
		entry.IsPending = true
		entry.FrozenResults = append(entry.FrozenResults, event.Status)
		return &entry
	}

	// ── Pre-freeze submission ─────────────────────────────────────────────────
	entry.AttemptCount++

	if event.Status == models.StatusAccepted {
		penaltyMins := penaltyPerWA(settings)
		elapsedMins := int(event.SubmitTime.Sub(event.ContestStart).Minutes())

		entry.Accepted = true
		entry.DisplayScore = 1
		entry.IsPending = false   // any earlier frozen submissions become moot
		entry.FrozenAttempts = 0
		entry.FrozenResults = nil
		entry.BestSubmitTime = event.SubmitTime
		// Penalty = submission time + 20 min × each WA before this AC.
		entry.Penalty = elapsedMins + entry.WrongAttemptCount*penaltyMins
	} else {
		// WA / TLE / MLE / RE / CE — all count as a wrong attempt for penalty.
		// CE is debatable; most regions count it. Configurable via settings.
		if !ignoreCE(event.Status, settings) {
			entry.WrongAttemptCount++
		}
	}

	return &entry
}

// Rank sorts a flat slice of ScoreEntries into ICPC-ranked RankRows.
//
// Sort order (lexicographic, first non-equal key wins):
//  1. TotalScore (solved count)    — descending
//  2. TotalPenalty (penalty mins)  — ascending
//  3. LastACTime (last AC moment)  — ascending (earlier = better)
func (s *ICPCStrategy) Rank(entries []*ScoreEntry, _ ContestSettings) []RankRow {
	// ── Aggregate per user ────────────────────────────────────────────────────
	rows := aggregateUsers(entries, func(row *RankRow, e *ScoreEntry) {
		if e.Accepted {
			row.TotalScore++
			row.TotalPenalty += e.Penalty
			// LastACTime = the latest AC across all solved problems.
			// Used as tiebreaker: earlier last-AC is better (all three conditions
			// are identical to the rule "who solved their last problem first").
			if e.BestSubmitTime.After(row.LastACTime) {
				row.LastACTime = e.BestSubmitTime
			}
		}
	})

	// ── Sort ──────────────────────────────────────────────────────────────────
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.TotalScore != b.TotalScore {
			return a.TotalScore > b.TotalScore // more solved = better
		}
		if a.TotalPenalty != b.TotalPenalty {
			return a.TotalPenalty < b.TotalPenalty // less penalty = better
		}
		if !a.LastACTime.Equal(b.LastACTime) {
			return a.LastACTime.Before(b.LastACTime) // earlier last AC = better
		}
		// Final tiebreaker: smaller UserID (deterministic, arbitrary).
		return a.UserID < b.UserID
	})

	// ── Assign ranks (shared rank for identical scores) ───────────────────────
	return assignRanks(rows, func(a, b RankRow) bool {
		return a.TotalScore == b.TotalScore &&
			a.TotalPenalty == b.TotalPenalty &&
			a.LastACTime.Equal(b.LastACTime)
	})
}

// IsFinalised returns true when the entry is settled.
// For ICPC:
//   - Pre-freeze AC:   always final (no re-judging)
//   - IsPending:       NOT final — 滚榜 may reveal an AC that changes the rank
//   - Pre-freeze WA only: final until a new submission arrives (handled by Apply)
func (s *ICPCStrategy) IsFinalised(entry *ScoreEntry, _ ContestSettings) bool {
	return entry.Accepted && !entry.IsPending
}

// ─── 滚榜 (Rolling Reveal) ────────────────────────────────────────────────────

// RevealNext pops the earliest frozen submission for this entry and applies its
// result to the public scoreboard.
//
// Called by RankingService.UnfreezeNext() during the post-contest ceremony.
// Returns (updated entry, changed) where changed = true if the public score changed.
func (s *ICPCStrategy) RevealNext(
	entry *ScoreEntry,
	contestStart time.Time,
	frozenSubmitTime time.Time,
	settings ContestSettings,
) (updated *ScoreEntry, accepted bool) {
	if len(entry.FrozenResults) == 0 {
		return entry, false
	}

	e := cloneOrNew(entry, SubmissionEvent{UserID: entry.UserID, ProblemID: entry.ProblemID})

	// Pop the chronologically earliest frozen submission.
	status := e.FrozenResults[0]
	e.FrozenResults = e.FrozenResults[1:]
	e.FrozenAttempts--
	if e.FrozenAttempts <= 0 {
		e.FrozenAttempts = 0
		e.IsPending = false
	}

	if status == models.StatusAccepted {
		penaltyMins := penaltyPerWA(settings)
		elapsedMins := int(frozenSubmitTime.Sub(contestStart).Minutes())

		e.Accepted = true
		e.DisplayScore = 1
		e.IsPending = false
		e.FrozenAttempts = 0
		e.FrozenResults = nil
		e.BestSubmitTime = frozenSubmitTime
		e.Penalty = elapsedMins + e.WrongAttemptCount*penaltyMins
		return &e, true
	}

	// Non-AC reveal: this attempt was wrong, penalty still not counted
	// (only WA before eventual AC matter for ICPC penalty).
	e.WrongAttemptCount++
	e.AttemptCount++
	return &e, false
}

// ─── Settings helpers ─────────────────────────────────────────────────────────

// penaltyPerWA returns the penalty in minutes per wrong attempt before AC.
// Default is 20 minutes per ICPC rules.
// Override via settings["penalty_minutes"] (float64 from JSON).
func penaltyPerWA(settings ContestSettings) int {
	if v, ok := settings["penalty_minutes"]; ok {
		if f, ok := v.(float64); ok && f >= 0 {
			return int(f)
		}
	}
	return 20
}

// isPostFreeze returns true if t is at or after the freeze time.
func isPostFreeze(t time.Time, freezeTime *time.Time) bool {
	return freezeTime != nil && !t.Before(*freezeTime)
}

// ignoreCE returns true if compile errors should not count as wrong attempts.
// Default: CE counts (matches most regional judge rules).
// Override via settings["ce_no_penalty"] = true.
func ignoreCE(status models.SubmissionStatus, settings ContestSettings) bool {
	if status != models.StatusCE {
		return false
	}
	v, _ := settings["ce_no_penalty"].(bool)
	return v
}
