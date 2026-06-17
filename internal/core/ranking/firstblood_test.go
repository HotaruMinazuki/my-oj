package ranking

import (
	"testing"
	"time"

	"github.com/your-org/my-oj/internal/core/contest"
	"github.com/your-org/my-oj/internal/models"
)

// fbHolder returns the UserID flagged as first blood for the given problem,
// or 0 if none. Also asserts at most one holder per problem.
func fbHolder(t *testing.T, entries []*contest.ScoreEntry, problemID models.ID) models.ID {
	t.Helper()
	var holder models.ID
	for _, e := range entries {
		if e.ProblemID == problemID && e.IsFirstBlood {
			if holder != 0 {
				t.Fatalf("problem %d has multiple first-blood holders (%d and %d)", problemID, holder, e.UserID)
			}
			holder = e.UserID
		}
	}
	return holder
}

// TestAssignFirstBlood_EarliestACOwnsIt covers the common case: when several
// teams solve the same problem, only the earliest AC keeps first blood and every
// other holder is cleared (including any stale persisted flag).
func TestAssignFirstBlood_EarliestACOwnsIt(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	entries := []*contest.ScoreEntry{
		{UserID: 1, ProblemID: 100, Accepted: true, BestSubmitTime: base.Add(90 * time.Minute)},
		// Earliest AC for problem 100 — should own first blood.
		{UserID: 2, ProblemID: 100, Accepted: true, BestSubmitTime: base.Add(30 * time.Minute)},
		// Stale persisted flag on a non-earliest entry — must be cleared.
		{UserID: 3, ProblemID: 100, Accepted: true, BestSubmitTime: base.Add(60 * time.Minute), IsFirstBlood: true},
		// Not accepted — never eligible.
		{UserID: 4, ProblemID: 100, Accepted: false, BestSubmitTime: base.Add(10 * time.Minute)},
	}

	assignFirstBlood(entries)

	if got := fbHolder(t, entries, 100); got != 2 {
		t.Fatalf("first blood for problem 100 = user %d, want user 2", got)
	}
}

// TestAssignFirstBlood_TieBrokenByUserID checks the deterministic tiebreak:
// equal BestSubmitTime → the smaller UserID wins.
func TestAssignFirstBlood_TieBrokenByUserID(t *testing.T) {
	at := time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC)
	entries := []*contest.ScoreEntry{
		{UserID: 7, ProblemID: 200, Accepted: true, BestSubmitTime: at},
		{UserID: 5, ProblemID: 200, Accepted: true, BestSubmitTime: at},
		{UserID: 9, ProblemID: 200, Accepted: true, BestSubmitTime: at},
	}

	assignFirstBlood(entries)

	if got := fbHolder(t, entries, 200); got != 5 {
		t.Fatalf("tie first blood for problem 200 = user %d, want user 5 (smallest UserID)", got)
	}
}

// TestAssignFirstBlood_FrozenRevealedAC_GetsFirstBlood is the regression at the
// heart of this fix: a problem whose only AC happens during the freeze gets no
// first blood while frozen (Accepted is still false on the persisted entry), but
// once the AC is revealed during 滚榜 the recompute awards first blood to it.
func TestAssignFirstBlood_FrozenRevealedAC_GetsFirstBlood(t *testing.T) {
	icpc := &contest.ICPCStrategy{}
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	freeze := start.Add(4 * time.Hour)
	acTime := start.Add(5 * time.Hour) // post-freeze AC

	frozen := icpc.Apply(contest.SubmissionEvent{
		UserID:       42,
		ProblemID:    300,
		Status:       models.StatusAccepted,
		SubmitTime:   acTime,
		ContestStart: start,
		FreezeTime:   &freeze,
	}, nil, nil)

	if frozen.Accepted {
		t.Fatalf("precondition: post-freeze AC must not be Accepted on the persisted entry")
	}

	// While frozen, the AC is hidden → no first blood yet.
	assignFirstBlood([]*contest.ScoreEntry{frozen})
	if frozen.IsFirstBlood {
		t.Fatalf("frozen (unrevealed) AC must not own first blood")
	}

	// 滚榜: reveal the frozen submission, exactly as rebuildSnapshot does.
	revealed := frozen
	for len(revealed.FrozenResults) > 0 {
		revealed, _ = icpc.RevealNext(revealed, start, nil)
	}
	if !revealed.Accepted || !revealed.BestSubmitTime.Equal(acTime) {
		t.Fatalf("precondition: reveal must mark Accepted with the real submit time")
	}

	assignFirstBlood([]*contest.ScoreEntry{revealed})
	if !revealed.IsFirstBlood {
		t.Fatalf("revealed frozen AC must own first blood after 滚榜")
	}
}

// TestAssignFirstBlood_PreFreezeACKeepsItAgainstLaterReveal verifies a pre-freeze
// AC keeps first blood even though a different team's AC is revealed (later) from
// the freeze.
func TestAssignFirstBlood_PreFreezeACKeepsItAgainstLaterReveal(t *testing.T) {
	icpc := &contest.ICPCStrategy{}
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	freeze := start.Add(4 * time.Hour)

	// Team 1: solved before the freeze.
	preFreeze := icpc.Apply(contest.SubmissionEvent{
		UserID:       1,
		ProblemID:    400,
		Status:       models.StatusAccepted,
		SubmitTime:   start.Add(30 * time.Minute),
		ContestStart: start,
		FreezeTime:   &freeze,
	}, nil, nil)
	if !preFreeze.Accepted {
		t.Fatalf("precondition: pre-freeze AC must be Accepted")
	}

	// Team 2: solved during the freeze, revealed later.
	revealed := icpc.Apply(contest.SubmissionEvent{
		UserID:       2,
		ProblemID:    400,
		Status:       models.StatusAccepted,
		SubmitTime:   start.Add(5 * time.Hour),
		ContestStart: start,
		FreezeTime:   &freeze,
	}, nil, nil)
	for len(revealed.FrozenResults) > 0 {
		revealed, _ = icpc.RevealNext(revealed, start, nil)
	}

	entries := []*contest.ScoreEntry{preFreeze, revealed}
	assignFirstBlood(entries)

	if got := fbHolder(t, entries, 400); got != 1 {
		t.Fatalf("first blood for problem 400 = user %d, want user 1 (pre-freeze solver)", got)
	}
}
