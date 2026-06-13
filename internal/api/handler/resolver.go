package handler

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/infra/postgres"
	"github.com/your-org/my-oj/internal/models"
)

// ─── Repository contracts ─────────────────────────────────────────────────────

// ResolverContestRepo supplies the contest data the event feed needs.
type ResolverContestRepo interface {
	GetByID(ctx context.Context, id models.ID) (*models.Contest, error)
	GetProblems(ctx context.Context, contestID models.ID) ([]postgres.ContestProblemSummary, error)
	ListContestTeams(ctx context.Context, contestID models.ID) ([]postgres.ContestTeam, error)
}

// ResolverSubmissionRepo supplies the runs for the event feed.
type ResolverSubmissionRepo interface {
	ListForFeed(ctx context.Context, contestID models.ID) ([]postgres.FeedSubmission, error)
}

// ResolverHandler exports a contest as a legacy CCS event-feed XML, consumable
// by the ICPC Tools Resolver (org.icpc.tools.resolver.Resolver). The format is
// the classic <contest> feed parsed by contestModel's XMLFeedParser.
type ResolverHandler struct {
	contests    ResolverContestRepo
	submissions ResolverSubmissionRepo
	log         *zap.Logger
}

func NewResolverHandler(contests ResolverContestRepo, submissions ResolverSubmissionRepo, log *zap.Logger) *ResolverHandler {
	return &ResolverHandler{contests: contests, submissions: submissions, log: log}
}

// ─── GET /api/v1/admin/contests/:contest_id/resolver.xml ─────────────────────

// ExportEventFeed builds and returns the contest event feed as XML.
func (h *ResolverHandler) ExportEventFeed(c *gin.Context) {
	contestID, ok := parseContestID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	contest, err := h.contests.GetByID(ctx, contestID)
	if err != nil || contest == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "contest not found"})
		return
	}
	problems, err := h.contests.GetProblems(ctx, contestID)
	if err != nil {
		h.log.Error("resolver: load problems", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	teams, err := h.contests.ListContestTeams(ctx, contestID)
	if err != nil {
		h.log.Error("resolver: load teams", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	subs, err := h.submissions.ListForFeed(ctx, contestID)
	if err != nil {
		h.log.Error("resolver: load submissions", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	feed := buildEventFeed(contest, problems, teams, subs)

	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		h.log.Error("resolver: marshal xml", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.Header("Content-Disposition",
		fmt.Sprintf(`attachment; filename="contest-%d-event-feed.xml"`, contestID))
	c.Data(http.StatusOK, "application/xml; charset=utf-8",
		append([]byte(xml.Header), out...))
}

// ─── XML model (CCS event feed) ───────────────────────────────────────────────

type xmlContest struct {
	XMLName  xml.Name     `xml:"contest"`
	Info     xmlInfo      `xml:"info"`
	Problems []xmlProblem `xml:"problem"`
	Teams    []xmlTeam    `xml:"team"`
	Runs     []xmlRun     `xml:"run"`
}

type xmlInfo struct {
	ContestID     string `xml:"contest-id"`
	Title         string `xml:"title"`
	Length        string `xml:"length"`
	FreezeLength  string `xml:"scoreboard-freeze-length,omitempty"`
	Penalty       int    `xml:"penalty"`
	Started       bool   `xml:"started"`
	StartTime     string `xml:"starttime"`
}

type xmlProblem struct {
	ID    string `xml:"id"`
	Label string `xml:"label"`
	Name  string `xml:"name"`
}

type xmlTeam struct {
	ID         string `xml:"id"`
	Name       string `xml:"name"`
	University string `xml:"university"`
}

type xmlRun struct {
	ID        string  `xml:"id"`
	Problem   string  `xml:"problem"`
	Team      string  `xml:"team"`
	Judged    bool    `xml:"judged"`
	Result    string  `xml:"result"`
	Solved    bool    `xml:"solved"`
	Penalty   bool    `xml:"penalty"`
	Time      float64 `xml:"time"`
	Timestamp float64 `xml:"timestamp"`
}

// buildEventFeed assembles the CCS event-feed document from DB rows.
func buildEventFeed(
	contest *models.Contest,
	problems []postgres.ContestProblemSummary,
	teams []postgres.ContestTeam,
	subs []postgres.FeedSubmission,
) *xmlContest {
	penalty := 20
	if v, ok := contest.Settings["penalty_minutes"]; ok {
		if f, ok := v.(float64); ok && f >= 0 {
			penalty = int(f)
		}
	}

	info := xmlInfo{
		ContestID: fmt.Sprintf("%d", contest.ID),
		Title:     contest.Title,
		Length:    hms(contest.EndTime.Sub(contest.StartTime).Seconds()),
		Penalty:   penalty,
		Started:   true,
		StartTime: fmt.Sprintf("%.0f", float64(contest.StartTime.Unix())),
	}
	if contest.FreezeTime != nil && contest.FreezeTime.Before(contest.EndTime) {
		info.FreezeLength = hms(contest.EndTime.Sub(*contest.FreezeTime).Seconds())
	}

	feed := &xmlContest{Info: info}

	for _, p := range problems {
		feed.Problems = append(feed.Problems, xmlProblem{
			ID:    fmt.Sprintf("%d", p.ProblemID),
			Label: p.Label,
			Name:  p.Title,
		})
	}

	for _, t := range teams {
		feed.Teams = append(feed.Teams, xmlTeam{
			ID:         fmt.Sprintf("%d", t.UserID),
			Name:       t.Username,
			University: t.Organization,
		})
	}

	for _, s := range subs {
		acronym, counts, judged := mapResult(s.Status)
		if !judged {
			continue // skip Pending / Judging / SystemError — not real verdicts
		}
		elapsed := s.CreatedAt.Sub(contest.StartTime).Seconds()
		if elapsed < 0 {
			elapsed = 0
		}
		// Submissions after the contest end don't belong on the board.
		if !contest.EndTime.IsZero() && s.CreatedAt.After(contest.EndTime) {
			continue
		}
		solved := s.Status == models.StatusAccepted
		feed.Runs = append(feed.Runs, xmlRun{
			ID:        fmt.Sprintf("%d", s.ID),
			Problem:   fmt.Sprintf("%d", s.ProblemID),
			Team:      fmt.Sprintf("%d", s.UserID),
			Judged:    true,
			Result:    acronym,
			Solved:    solved,
			Penalty:   counts && !solved,
			Time:      elapsed,
			Timestamp: float64(s.CreatedAt.Unix()),
		})
	}

	return feed
}

// mapResult maps an internal status to (CCS acronym, counts-as-penalty, is-judged-verdict).
func mapResult(s models.SubmissionStatus) (acronym string, penalty bool, judged bool) {
	switch s {
	case models.StatusAccepted:
		return "AC", false, true
	case models.StatusWrongAnswer:
		return "WA", true, true
	case models.StatusTLE:
		return "TLE", true, true
	case models.StatusMLE:
		return "MLE", true, true
	case models.StatusRE:
		return "RTE", true, true
	case models.StatusCE:
		// Compile errors do not incur penalty by ICPC default.
		return "CE", false, true
	default:
		// Pending / Judging / Compiling / SystemError → not a contest verdict.
		return "", false, false
	}
}

// hms formats a duration in seconds as "H:MM:SS" (legacy event-feed length format).
func hms(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := int(seconds + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}
