package contest

import (
	"testing"
	"time"

	"github.com/your-org/my-oj/internal/models"
)

// minutesFromStart builds a helper that maps "minutes since contest start" to a
// concrete time, so the tests read in ICPC penalty units.
func minutesFromStart(start time.Time) func(int) time.Time {
	return func(min int) time.Time { return start.Add(time.Duration(min) * time.Minute) }
}

// TestICPC_RevealNext_UsesRealSubmitTime is the regression test for the 滚榜 bug
// where a revealed frozen AC was penalised with the contest END time instead of
// the submission's real time — inflating every revealed AC to the full contest
// duration. The contest below runs 0–120 min and freezes at minute 60.
func TestICPC_RevealNext_UsesRealSubmitTime(t *testing.T) {
	s := &ICPCStrategy{}
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	freeze := start.Add(60 * time.Minute)
	freezeTime := &freeze
	at := minutesFromStart(start)

	const fullContestPenalty = 120 // the value the old EndTime bug produced (0 WA)

	tests := []struct {
		name        string
		preFreezeWA int // wrong answers submitted before the freeze
		acAtMin     int // real submit time (minutes from start) of the frozen AC
		wantPenalty int
	}{
		{"frozen AC early in window, no WA", 0, 61, 61},
		{"frozen AC mid window with 1 pre-freeze WA", 1, 75, 75 + 20},
		{"frozen AC late, no WA", 0, 119, 119},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var entry *ScoreEntry
			var settings ContestSettings // nil → default 20-min penalty

			for i := 0; i < tc.preFreezeWA; i++ {
				entry = s.Apply(SubmissionEvent{
					UserID: 1, ProblemID: 2,
					Status:       models.StatusWrongAnswer,
					SubmitTime:   at(10 + i),
					ContestStart: start,
					FreezeTime:   freezeTime,
				}, entry, settings)
			}

			// Post-freeze AC — hidden until reveal.
			entry = s.Apply(SubmissionEvent{
				UserID: 1, ProblemID: 2,
				Status:       models.StatusAccepted,
				SubmitTime:   at(tc.acAtMin),
				ContestStart: start,
				FreezeTime:   freezeTime,
			}, entry, settings)

			if entry.Accepted {
				t.Fatal("frozen AC must not be Accepted before reveal")
			}
			if len(entry.FrozenResults) != 1 {
				t.Fatalf("want 1 frozen result, got %d", len(entry.FrozenResults))
			}
			if !entry.FrozenResults[0].SubmitTime.Equal(at(tc.acAtMin)) {
				t.Fatalf("frozen submit time not captured: got %v, want %v",
					entry.FrozenResults[0].SubmitTime, at(tc.acAtMin))
			}

			revealed, accepted := s.RevealNext(entry, start, settings)
			if !accepted {
				t.Fatal("reveal should accept the frozen AC")
			}
			if got := revealed.Penalty; got != tc.wantPenalty {
				t.Errorf("penalty = %d, want %d", got, tc.wantPenalty)
			}
			if !revealed.BestSubmitTime.Equal(at(tc.acAtMin)) {
				t.Errorf("BestSubmitTime = %v, want %v", revealed.BestSubmitTime, at(tc.acAtMin))
			}
			// Guard against the original bug regressing: no revealed AC should
			// collapse to the full-contest penalty unless that is genuinely correct.
			if got := revealed.Penalty; got == fullContestPenalty+tc.preFreezeWA*20 &&
				tc.wantPenalty != fullContestPenalty+tc.preFreezeWA*20 {
				t.Errorf("penalty collapsed to full-contest value (EndTime bug regressed)")
			}
		})
	}
}

// TestICPC_RevealNext_FrozenWAThenAC walks the full reveal chain for a problem
// whose frozen window holds a WA followed by an AC: the WA reveal must add a
// penalty attempt without solving, and the later AC reveal must fold that WA
// into its penalty using its own real submit time.
func TestICPC_RevealNext_FrozenWAThenAC(t *testing.T) {
	s := &ICPCStrategy{}
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	freeze := start.Add(60 * time.Minute)
	freezeTime := &freeze
	at := minutesFromStart(start)
	var settings ContestSettings

	var entry *ScoreEntry
	for _, ev := range []SubmissionEvent{
		{UserID: 1, ProblemID: 2, Status: models.StatusWrongAnswer, SubmitTime: at(65), ContestStart: start, FreezeTime: freezeTime},
		{UserID: 1, ProblemID: 2, Status: models.StatusAccepted, SubmitTime: at(80), ContestStart: start, FreezeTime: freezeTime},
	} {
		entry = s.Apply(ev, entry, settings)
	}

	if entry.FrozenAttempts != 2 || !entry.IsPending {
		t.Fatalf("want 2 pending frozen attempts, got attempts=%d pending=%v",
			entry.FrozenAttempts, entry.IsPending)
	}

	// Reveal #1: the frozen WA. Not solved, but it now counts as a wrong attempt.
	r1, ok1 := s.RevealNext(entry, start, settings)
	if ok1 || r1.Accepted {
		t.Fatal("revealing a WA must not mark the problem solved")
	}
	if r1.WrongAttemptCount != 1 {
		t.Errorf("WrongAttemptCount after WA reveal = %d, want 1", r1.WrongAttemptCount)
	}

	// Reveal #2: the frozen AC at minute 80, folding in the one revealed WA.
	r2, ok2 := s.RevealNext(r1, start, settings)
	if !ok2 || !r2.Accepted {
		t.Fatal("revealing the AC must mark the problem solved")
	}
	if want := 80 + 1*20; r2.Penalty != want {
		t.Errorf("penalty = %d, want %d (80 min + one WA)", r2.Penalty, want)
	}
	if r2.IsPending || r2.FrozenAttempts != 0 || len(r2.FrozenResults) != 0 {
		t.Errorf("entry should be fully revealed: pending=%v attempts=%d remaining=%d",
			r2.IsPending, r2.FrozenAttempts, len(r2.FrozenResults))
	}
}
