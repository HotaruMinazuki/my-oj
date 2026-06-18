package handler

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/your-org/my-oj/internal/infra/postgres"
	"github.com/your-org/my-oj/internal/models"
)

// TestEventFeedResolverCompat guards the three fields the ICPC Resolver needs
// but the legacy CCS feed easily omits: judgement-type definitions (else every
// run is "unjudged"), a non-empty scoreboard-freeze-length (else checkContestState
// NPEs), and a <finalized> marker (else "Contest is not over").
func TestEventFeedResolverCompat(t *testing.T) {
	start := time.Unix(1781610126, 0)
	contest := &models.Contest{
		ID:        20,
		Title:     "demo",
		StartTime: start,
		EndTime:   start.Add(3 * time.Hour),
		// FreezeTime nil → no freeze; export must still emit 0:00:00.
	}

	feed := buildEventFeed(contest, nil, nil, nil)
	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"<acronym>AC</acronym>",
		"<solved>true</solved>",
		"<acronym>WA</acronym>",
		"<scoreboard-freeze-length>0:00:00</scoreboard-freeze-length>",
		"<finalized>",
		"<last-gold>0</last-gold>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

// TestEventFeed_CEPenaltyMatchesScoreboard guards that the CE penalty in the
// exported feed follows the contest's ce_no_penalty setting — the same single
// source of truth (models.ContestSettings.CENoPenalty) the live ICPC scoreboard
// reads. Both the <judgement> definition and the CE <run> must agree, so a
// CE-before-AC team never carries different penalty on the two boards.
func TestEventFeed_CEPenaltyMatchesScoreboard(t *testing.T) {
	start := time.Unix(1781610126, 0)

	tests := []struct {
		name     string
		settings models.ContestSettings
	}{
		{"default: CE not penalised", nil},
		{"ce_no_penalty=true", models.ContestSettings{"ce_no_penalty": true}},
		{"ce_no_penalty=false", models.ContestSettings{"ce_no_penalty": false}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			contest := &models.Contest{
				ID:        20,
				Title:     "demo",
				StartTime: start,
				EndTime:   start.Add(3 * time.Hour),
				Settings:  tc.settings,
			}
			// One CE submission, in the contest window.
			subs := []postgres.FeedSubmission{
				{ID: 1, UserID: 7, ProblemID: 3, Status: models.StatusCE, CreatedAt: start.Add(10 * time.Minute)},
			}

			feed := buildEventFeed(contest, nil, nil, subs)

			// Expected CE penalty = the live-scoreboard policy: penalise CE only
			// when ce_no_penalty is false.
			wantPenalty := !tc.settings.CENoPenalty()

			ceJudge := findJudgement(t, feed.Judgements, "CE")
			if ceJudge.Penalty != wantPenalty {
				t.Errorf("CE <judgement> penalty = %v, want %v", ceJudge.Penalty, wantPenalty)
			}
			if ceJudge.Solved {
				t.Errorf("CE <judgement> solved = true, want false")
			}

			ceRun := findRun(t, feed.Runs, "CE")
			if ceRun.Penalty != wantPenalty {
				t.Errorf("CE <run> penalty = %v, want %v", ceRun.Penalty, wantPenalty)
			}
			if ceRun.Solved {
				t.Errorf("CE <run> solved = true, want false")
			}

			// Faithful to "XML 里": survive a marshal round-trip.
			out, err := xml.MarshalIndent(feed, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			var got xmlContest
			if err := xml.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			if r := findRun(t, got.Runs, "CE"); r.Penalty != wantPenalty {
				t.Errorf("CE <run> penalty after round-trip = %v, want %v", r.Penalty, wantPenalty)
			}
		})
	}
}

func findJudgement(t *testing.T, js []xmlJudgementType, acronym string) xmlJudgementType {
	t.Helper()
	for _, j := range js {
		if j.Acronym == acronym {
			return j
		}
	}
	t.Fatalf("judgement %q not found", acronym)
	return xmlJudgementType{}
}

func findRun(t *testing.T, runs []xmlRun, acronym string) xmlRun {
	t.Helper()
	for _, r := range runs {
		if r.Result == acronym {
			return r
		}
	}
	t.Fatalf("run with result %q not found", acronym)
	return xmlRun{}
}
