package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
