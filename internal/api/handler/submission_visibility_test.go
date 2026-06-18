package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/api/middleware"
	"github.com/your-org/my-oj/internal/models"
)

// newGetSubmissionContext builds a gin context for GET /submissions/:id. role is
// the viewer's role; pass "" for an anonymous/contestant (non-admin) viewer.
func newGetSubmissionContext(id string, role models.UserRole) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/submissions/"+id, nil)
	c.Params = gin.Params{{Key: "id", Value: id}}
	if role != "" {
		c.Set(string(middleware.ContextKeyUserRole), role)
	}
	return c, w
}

// newGetSubmissionContextAs is like newGetSubmissionContext but also identifies
// the authenticated viewer by id (as OptionalAuth would). Pass viewerID 0 for an
// anonymous viewer.
func newGetSubmissionContextAs(id string, viewerID models.ID, role models.UserRole) (*gin.Context, *httptest.ResponseRecorder) {
	c, w := newGetSubmissionContext(id, role)
	if viewerID != 0 {
		c.Set(string(middleware.ContextKeyUserID), viewerID)
	}
	return c, w
}

// runningICPC / endedICPC are contests whose EffectiveStatus is, respectively,
// running and ended (an EndTime in the past forces "ended").
func runningICPC() *models.Contest { return &models.Contest{ContestType: models.ContestICPC} }
func endedICPC() *models.Contest {
	return &models.Contest{ContestType: models.ContestICPC, EndTime: time.Now().Add(-time.Hour)}
}

func icpcSubmission() *models.Submission {
	cid := models.ID(42)
	return &models.Submission{
		ID:           1234,
		UserID:       9,
		ProblemID:    7,
		ContestID:    &cid,
		Status:       models.StatusWrongAnswer,
		Score:        50, // partial score that would leak how many testcases passed
		JudgeMessage: "wrong answer on test 3",
		TestCaseResults: models.TestCaseResults{
			{TestCaseID: 1, Status: models.StatusAccepted, Score: 50},
			{TestCaseID: 2, Status: models.StatusWrongAnswer},
		},
	}
}

// Under ICPC, a non-admin viewing a submission must not receive the score (it
// would reveal how many testcases passed), nor the per-testcase breakdown or
// judge message; contest_type is set so the frontend hides the "得分" field.
func TestGetSubmission_ICPC_StripsScoreAndDetails(t *testing.T) {
	subs := &stubSubmissionRepo{byID: icpcSubmission()}
	contests := &stubContestRepo{contest: &models.Contest{ContestType: models.ContestICPC}}
	h := NewSubmissionHandler(subs, &stubProblemRepo{}, contests, &stubPublisher{}, &stubStore{}, zap.NewNop())

	c, w := newGetSubmissionContext("1234", "") // non-admin
	h.GetSubmission(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, w.Body.String())
	}
	if score, ok := got["score"].(float64); !ok || score != 0 {
		t.Errorf("score = %v, want 0 (ICPC must not leak partial score)", got["score"])
	}
	if got["contest_type"] != string(models.ContestICPC) {
		t.Errorf("contest_type = %v, want %q", got["contest_type"], models.ContestICPC)
	}
	if _, present := got["test_case_results"]; present {
		t.Errorf("test_case_results present in ICPC response: %v", got["test_case_results"])
	}
	if _, present := got["judge_message"]; present {
		t.Errorf("judge_message present in ICPC response: %v", got["judge_message"])
	}
}

// Admins always see the real score and per-testcase results, even for ICPC.
func TestGetSubmission_ICPC_AdminSeesFullResults(t *testing.T) {
	subs := &stubSubmissionRepo{byID: icpcSubmission()}
	contests := &stubContestRepo{contest: &models.Contest{ContestType: models.ContestICPC}}
	h := NewSubmissionHandler(subs, &stubProblemRepo{}, contests, &stubPublisher{}, &stubStore{}, zap.NewNop())

	c, w := newGetSubmissionContext("1234", models.RoleAdmin)
	h.GetSubmission(c)

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, w.Body.String())
	}
	if score, _ := got["score"].(float64); score != 50 {
		t.Errorf("admin score = %v, want 50", got["score"])
	}
	if _, present := got["test_case_results"]; !present {
		t.Errorf("admin should see test_case_results, but it was stripped")
	}
	if got["contest_type"] != nil {
		t.Errorf("contest_type should not be set for admins, got %v", got["contest_type"])
	}
}

// A non-ICPC, non-blind contest (IOI) keeps the score and testcases for everyone:
// the score is legitimate feedback there, and contest_type stays unset.
func TestGetSubmission_IOI_KeepsScore(t *testing.T) {
	sub := icpcSubmission() // reuse the fixture; only the contest type differs
	subs := &stubSubmissionRepo{byID: sub}
	contests := &stubContestRepo{contest: &models.Contest{ContestType: models.ContestIOI}}
	h := NewSubmissionHandler(subs, &stubProblemRepo{}, contests, &stubPublisher{}, &stubStore{}, zap.NewNop())

	c, w := newGetSubmissionContext("1234", "") // non-admin
	h.GetSubmission(c)

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, w.Body.String())
	}
	if score, _ := got["score"].(float64); score != 50 {
		t.Errorf("IOI score = %v, want 50 (non-ICPC must keep score)", got["score"])
	}
	if _, present := got["test_case_results"]; !present {
		t.Errorf("IOI should keep test_case_results")
	}
	if got["contest_type"] != nil {
		t.Errorf("contest_type should be unset for non-ICPC, got %v", got["contest_type"])
	}
}

// (a) While an ICPC contest is still running, a third party (non-author, non-admin)
// must NOT be able to read the real verdict by enumerating submission ids — the
// status is masked to a neutral "Judging" so the live/frozen scoreboard can't be
// reconstructed.
func TestGetSubmission_ICPC_Running_HidesVerdictFromOthers(t *testing.T) {
	subs := &stubSubmissionRepo{byID: icpcSubmission()} // author is user 9
	contests := &stubContestRepo{contest: runningICPC()}
	h := NewSubmissionHandler(subs, &stubProblemRepo{}, contests, &stubPublisher{}, &stubStore{}, zap.NewNop())

	c, w := newGetSubmissionContextAs("1234", models.ID(5), "") // a different, non-admin user
	h.GetSubmission(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, w.Body.String())
	}
	if got["status"] == string(models.StatusWrongAnswer) {
		t.Errorf("real verdict leaked to a third party during a running ICPC contest: status=%v", got["status"])
	}
	if got["status"] != string(models.StatusJudging) {
		t.Errorf("status = %v, want neutral %q", got["status"], models.StatusJudging)
	}
	if score, _ := got["score"].(float64); score != 0 {
		t.Errorf("score = %v, want 0", got["score"])
	}
	if _, present := got["test_case_results"]; present {
		t.Errorf("test_case_results leaked: %v", got["test_case_results"])
	}
	if _, present := got["judge_message"]; present {
		t.Errorf("judge_message leaked: %v", got["judge_message"])
	}
}

// (b) The author of a running-ICPC submission keeps seeing their own real verdict
// (so the frontend can poll for judging progress), but never the per-testcase
// breakdown or score.
func TestGetSubmission_ICPC_Running_AuthorSeesOwnVerdict(t *testing.T) {
	subs := &stubSubmissionRepo{byID: icpcSubmission()} // author is user 9
	contests := &stubContestRepo{contest: runningICPC()}
	h := NewSubmissionHandler(subs, &stubProblemRepo{}, contests, &stubPublisher{}, &stubStore{}, zap.NewNop())

	c, w := newGetSubmissionContextAs("1234", models.ID(9), "") // the author
	h.GetSubmission(c)

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, w.Body.String())
	}
	if got["status"] != string(models.StatusWrongAnswer) {
		t.Errorf("author status = %v, want real verdict %q", got["status"], models.StatusWrongAnswer)
	}
	if _, present := got["test_case_results"]; present {
		t.Errorf("author should not see test_case_results in ICPC: %v", got["test_case_results"])
	}
	if score, _ := got["score"].(float64); score != 0 {
		t.Errorf("author score = %v, want 0 (ICPC hides per-submission score)", got["score"])
	}
}

// (c) Once the ICPC contest has ended the overall verdict is public again — even a
// non-author sees the real status (testcases/score stay hidden per ICPC format).
func TestGetSubmission_ICPC_Ended_VerdictPublic(t *testing.T) {
	subs := &stubSubmissionRepo{byID: icpcSubmission()} // author is user 9
	contests := &stubContestRepo{contest: endedICPC()}
	h := NewSubmissionHandler(subs, &stubProblemRepo{}, contests, &stubPublisher{}, &stubStore{}, zap.NewNop())

	c, w := newGetSubmissionContextAs("1234", models.ID(5), "") // a non-author viewer
	h.GetSubmission(c)

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, w.Body.String())
	}
	if got["status"] != string(models.StatusWrongAnswer) {
		t.Errorf("ended-contest status = %v, want public verdict %q", got["status"], models.StatusWrongAnswer)
	}
	if _, present := got["test_case_results"]; present {
		t.Errorf("test_case_results still hidden after end for ICPC: %v", got["test_case_results"])
	}
	if score, _ := got["score"].(float64); score != 0 {
		t.Errorf("score = %v, want 0 (ICPC)", got["score"])
	}
}
