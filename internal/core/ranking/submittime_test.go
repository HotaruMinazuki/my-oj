package ranking

import (
	"testing"
	"time"

	"github.com/your-org/my-oj/internal/core/contest"
	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/mq"
)

// icpcEntryFor reproduces exactly what processContestResult persists to Redis for
// a fresh (no prior) ICPC submission: it builds the SubmissionEvent the same way
// the service does — crucially taking SubmitTime from submitTimeOf(result) — and
// runs it through the real ICPC strategy. The returned ScoreEntry is byte-for-byte
// what gets HSet into the entries hash.
func icpcEntryFor(result *mq.ResultMessage, start time.Time, freeze *time.Time) *contest.ScoreEntry {
	event := contest.SubmissionEvent{
		UserID:       result.UserID,
		ProblemID:    result.ProblemID,
		Status:       result.Status,
		Score:        result.Score,
		SubmitTime:   submitTimeOf(result),
		ContestStart: start,
		FreezeTime:   freeze,
	}
	return (&contest.ICPCStrategy{}).Apply(event, nil, nil)
}

// TestSubmitTimeOf_PrefersSubmittedAt is the core of the fix: ranking must key off
// the contestant's real submission instant, not the judge-completion time.
func TestSubmitTimeOf_PrefersSubmittedAt(t *testing.T) {
	submitted := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
	judged := submitted.Add(17 * time.Minute) // queueing + compile + run latency

	got := submitTimeOf(&mq.ResultMessage{SubmittedAt: submitted, JudgedAt: judged})
	if !got.Equal(submitted) {
		t.Fatalf("submitTimeOf = %v, want SubmittedAt %v", got, submitted)
	}
}

// TestSubmitTimeOf_FallsBackToJudgedAt covers legacy messages enqueued before the
// SubmittedAt field existed (zero value) — they must still rank, using JudgedAt.
func TestSubmitTimeOf_FallsBackToJudgedAt(t *testing.T) {
	judged := time.Date(2026, 1, 1, 10, 40, 0, 0, time.UTC)

	got := submitTimeOf(&mq.ResultMessage{JudgedAt: judged}) // SubmittedAt zero
	if !got.Equal(judged) {
		t.Fatalf("submitTimeOf (zero SubmittedAt) = %v, want JudgedAt %v", got, judged)
	}
}

// TestProcessContestResult_PenaltyUsesSubmitTime asserts the ICPC ScoreEntry that
// would be written to Redis computes penalty from SubmittedAt − StartTime, NOT
// from the (later) JudgedAt that judging latency produced.
func TestProcessContestResult_PenaltyUsesSubmitTime(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	result := &mq.ResultMessage{
		UserID:      1,
		ProblemID:   100,
		Status:      models.StatusAccepted,
		SubmittedAt: start.Add(30 * time.Minute), // hit submit at +30m
		JudgedAt:    start.Add(47 * time.Minute), // judging finished at +47m
	}

	entry := icpcEntryFor(result, start, nil)

	if !entry.Accepted {
		t.Fatalf("first-try AC must be Accepted")
	}
	// First-try AC, no WA → penalty == elapsed minutes at submission == 30.
	if entry.Penalty != 30 {
		t.Fatalf("penalty = %d, want 30 (SubmittedAt−start); 47 would mean JudgedAt was used", entry.Penalty)
	}
	if !entry.BestSubmitTime.Equal(result.SubmittedAt) {
		t.Fatalf("BestSubmitTime = %v, want SubmittedAt %v", entry.BestSubmitTime, result.SubmittedAt)
	}
}

// TestProcessContestResult_PreFreezeSubmitNotFrozen guards the freeze boundary: a
// submission made BEFORE the freeze but judged AFTER it must score normally on the
// public board, not be hidden as frozen. Keying off JudgedAt would wrongly freeze it.
func TestProcessContestResult_PreFreezeSubmitNotFrozen(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	freeze := start.Add(60 * time.Minute)
	result := &mq.ResultMessage{
		UserID:      2,
		ProblemID:   200,
		Status:      models.StatusAccepted,
		SubmittedAt: start.Add(55 * time.Minute), // 5 min before freeze
		JudgedAt:    start.Add(65 * time.Minute), // judged 5 min after freeze
	}

	entry := icpcEntryFor(result, start, &freeze)

	if !entry.Accepted {
		t.Fatalf("pre-freeze AC must be Accepted on the public board")
	}
	if entry.IsPending {
		t.Fatalf("pre-freeze submission must not be marked pending/frozen")
	}
	if len(entry.FrozenResults) != 0 {
		t.Fatalf("pre-freeze submission must not be recorded as a frozen result, got %d", len(entry.FrozenResults))
	}
	if entry.Penalty != 55 {
		t.Fatalf("penalty = %d, want 55 (SubmittedAt−start)", entry.Penalty)
	}
}
