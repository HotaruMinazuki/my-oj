package handler

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

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
