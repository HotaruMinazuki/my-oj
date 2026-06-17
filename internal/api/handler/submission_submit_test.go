package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/storage"
)

// ─── stubs implementing the SubmissionHandler's collaborators ────────────────

type stubSubmissionRepo struct {
	created int
	byID    *models.Submission // returned by GetByID when set
}

func (s *stubSubmissionRepo) Create(ctx context.Context, sub *models.Submission) error {
	s.created++
	sub.ID = 1234
	return nil
}
func (s *stubSubmissionRepo) GetByID(ctx context.Context, id models.ID) (*models.Submission, error) {
	return s.byID, nil
}
func (s *stubSubmissionRepo) Update(ctx context.Context, sub *models.Submission) error { return nil }
func (s *stubSubmissionRepo) ListPendingByContest(ctx context.Context, contestID models.ID) ([]*models.Submission, error) {
	return nil, nil
}
func (s *stubSubmissionRepo) MarkSuperseded(ctx context.Context, ids []models.ID) error { return nil }

type stubProblemRepo struct{ testCases []models.JudgeTestCase }

func (p *stubProblemRepo) GetJudgeConfig(ctx context.Context, problemID models.ID) (*models.JudgeConfig, error) {
	return &models.JudgeConfig{}, nil
}
func (p *stubProblemRepo) GetJudgeMeta(ctx context.Context, problemID models.ID) (*models.ProblemJudgeMeta, error) {
	return &models.ProblemJudgeMeta{}, nil
}
func (p *stubProblemRepo) GetTestCases(ctx context.Context, problemID models.ID) ([]models.JudgeTestCase, error) {
	return p.testCases, nil
}

type stubContestRepo struct {
	allowed       bool
	canSubmitErr  error
	canSubmitArgs [3]models.ID // contestID, problemID, userID of the last call
	contest       *models.Contest
}

func (c *stubContestRepo) GetByID(ctx context.Context, id models.ID) (*models.Contest, error) {
	return c.contest, nil
}
func (c *stubContestRepo) ActiveContestForProblem(ctx context.Context, problemID, userID models.ID) (*models.ID, error) {
	return nil, nil
}
func (c *stubContestRepo) CanSubmitToContest(ctx context.Context, contestID, problemID, userID models.ID) (bool, error) {
	c.canSubmitArgs = [3]models.ID{contestID, problemID, userID}
	return c.allowed, c.canSubmitErr
}

type stubPublisher struct{ published int }

func (p *stubPublisher) Publish(ctx context.Context, queue string, payload []byte) (string, error) {
	p.published++
	return "msg-1", nil
}
func (p *stubPublisher) Close() error { return nil }

type stubStore struct{ puts int }

func (s *stubStore) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	s.puts++
	return nil
}
func (s *stubStore) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	return nil, nil
}
func (s *stubStore) GetToFile(ctx context.Context, bucket, key, destPath string) error { return nil }
func (s *stubStore) PutFile(ctx context.Context, bucket, key, srcPath, contentType string) error {
	return nil
}
func (s *stubStore) Stat(ctx context.Context, bucket, key string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, nil
}
func (s *stubStore) Delete(ctx context.Context, bucket, key string) error  { return nil }
func (s *stubStore) EnsureBucket(ctx context.Context, bucket string) error { return nil }

// newSubmitContext builds a gin context for POST /contests/:contest_id/submissions.
func newSubmitContext(contestID string, userID models.ID, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/contests/"+contestID+"/submissions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	c.Params = gin.Params{{Key: "contest_id", Value: contestID}}
	c.Set("user_id", userID)
	return c, w
}

const submitBody = `{"problem_id":7,"language":"C++17","source_code":"int main(){}"}`

// A submission the user is not eligible to make (problem not in the contest, the
// contest is not running, or the user is not registered) must be rejected with
// 403 BEFORE any submission row is created or any source code is stored.
func TestSubmit_Ineligible_Returns403_NoSideEffects(t *testing.T) {
	subs := &stubSubmissionRepo{}
	store := &stubStore{}
	pub := &stubPublisher{}
	contests := &stubContestRepo{allowed: false}
	h := NewSubmissionHandler(subs, &stubProblemRepo{testCases: []models.JudgeTestCase{{}}}, contests, pub, store, zap.NewNop())

	c, w := newSubmitContext("42", models.ID(9), submitBody)
	h.Submit(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
	if subs.created != 0 {
		t.Errorf("submission was created for a rejected submit (created=%d)", subs.created)
	}
	if store.puts != 0 {
		t.Errorf("source was uploaded for a rejected submit (puts=%d)", store.puts)
	}
	if pub.published != 0 {
		t.Errorf("judge task was enqueued for a rejected submit (published=%d)", pub.published)
	}
	// Gate is queried with the URL contest_id, body problem_id, and auth user_id.
	if got, want := contests.canSubmitArgs, [3]models.ID{42, 7, 9}; got != want {
		t.Errorf("CanSubmitToContest args = %v, want %v", got, want)
	}
}

// A failure resolving eligibility (DB error) must fail closed with 500 and create
// nothing — never silently allow the submission.
func TestSubmit_EligibilityError_Returns500_NoSideEffects(t *testing.T) {
	subs := &stubSubmissionRepo{}
	store := &stubStore{}
	contests := &stubContestRepo{canSubmitErr: errors.New("db down")}
	h := NewSubmissionHandler(subs, &stubProblemRepo{testCases: []models.JudgeTestCase{{}}}, contests, &stubPublisher{}, store, zap.NewNop())

	c, w := newSubmitContext("42", models.ID(9), submitBody)
	h.Submit(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	if subs.created != 0 || store.puts != 0 {
		t.Errorf("side effects on eligibility error (created=%d, puts=%d)", subs.created, store.puts)
	}
}

// An eligible submission (problem in a running contest the user may submit to)
// flows through normally: source stored, submission created, judge task enqueued,
// 201 Created. ICPC contest → not blind, so it judges live rather than deferring.
func TestSubmit_Eligible_Returns201(t *testing.T) {
	subs := &stubSubmissionRepo{}
	store := &stubStore{}
	pub := &stubPublisher{}
	contests := &stubContestRepo{
		allowed: true,
		contest: &models.Contest{ContestType: models.ContestICPC},
	}
	h := NewSubmissionHandler(subs, &stubProblemRepo{testCases: []models.JudgeTestCase{{}}}, contests, pub, store, zap.NewNop())

	c, w := newSubmitContext("42", models.ID(9), submitBody)
	h.Submit(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if subs.created != 1 {
		t.Errorf("submission not created (created=%d)", subs.created)
	}
	if store.puts != 1 {
		t.Errorf("source not uploaded (puts=%d)", store.puts)
	}
	if pub.published != 1 {
		t.Errorf("judge task not enqueued (published=%d)", pub.published)
	}
}
