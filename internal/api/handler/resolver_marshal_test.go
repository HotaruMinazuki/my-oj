package handler

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestEventFeedEmitsJudgementTypes(t *testing.T) {
	feed := &xmlContest{Info: xmlInfo{ContestID: "20"}, Judgements: standardJudgementTypes}
	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"<judgement>",
		"<acronym>AC</acronym>",
		"<solved>true</solved>",
		"<penalty>false</penalty>",
		"<acronym>WA</acronym>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}
