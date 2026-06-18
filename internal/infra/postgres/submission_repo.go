package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/your-org/my-oj/internal/models"
)

// SubmissionRepo is the PostgreSQL-backed implementation of handler.SubmissionRepo.
type SubmissionRepo struct {
	db *sqlx.DB
}

func NewSubmissionRepo(db *sqlx.DB) *SubmissionRepo {
	return &SubmissionRepo{db: db}
}

// ─── Create ───────────────────────────────────────────────────────────────────

const insertSubmissionSQL = `
INSERT INTO submissions (
    user_id, problem_id, contest_id, language, source_code_path,
    status, score, time_used_ms, mem_used_kb,
    compile_log, judge_message, test_case_results, judge_node_id
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12::jsonb, $13
) RETURNING id, created_at, updated_at`

// Create inserts a new Submission row and back-fills ID, CreatedAt, UpdatedAt.
// TestCaseResults is marshalled to JSONB; a nil slice stores SQL NULL.
func (r *SubmissionRepo) Create(ctx context.Context, s *models.Submission) error {
	tcJSON, err := marshalJSONB(s.TestCaseResults)
	if err != nil {
		return fmt.Errorf("marshal test_case_results: %w", err)
	}
	// pq encodes []byte as bytea, which Postgres will not implicitly cast to
	// jsonb. Send as string so it arrives as TEXT (with ::jsonb on the column).
	var tcParam interface{}
	if tcJSON != nil {
		tcParam = string(tcJSON)
	}

	row := r.db.QueryRowContext(ctx, insertSubmissionSQL,
		s.UserID,
		s.ProblemID,
		s.ContestID, // *int64; nil → SQL NULL automatically
		string(s.Language),
		s.SourceCodePath,
		string(s.Status),
		s.Score,
		s.TimeUsedMs,
		s.MemUsedKB,
		s.CompileLog,
		s.JudgeMessage,
		tcParam,
		s.JudgeNodeID,
	)
	return row.Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
}

// ─── GetByID ──────────────────────────────────────────────────────────────────

const selectSubmissionSQL = `
SELECT id, user_id, problem_id, contest_id,
       language, source_code_path,
       status, score, time_used_ms, mem_used_kb,
       compile_log, judge_message, test_case_results, judge_node_id,
       created_at, updated_at
FROM submissions
WHERE id = $1`

func (r *SubmissionRepo) GetByID(ctx context.Context, id models.ID) (*models.Submission, error) {
	row := r.db.QueryRowContext(ctx, selectSubmissionSQL, id)

	var s models.Submission
	var contestID sql.NullInt64  // contest_id is nullable
	var tcRaw []byte             // test_case_results JSONB, may be NULL

	err := row.Scan(
		&s.ID, &s.UserID, &s.ProblemID, &contestID,
		&s.Language, &s.SourceCodePath,
		&s.Status, &s.Score, &s.TimeUsedMs, &s.MemUsedKB,
		&s.CompileLog, &s.JudgeMessage, &tcRaw, &s.JudgeNodeID,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("submission %d not found", id)
		}
		return nil, fmt.Errorf("scan submission: %w", err)
	}

	if contestID.Valid {
		cid := models.ID(contestID.Int64)
		s.ContestID = &cid
	}
	if tcRaw != nil {
		if err := json.Unmarshal(tcRaw, &s.TestCaseResults); err != nil {
			return nil, fmt.Errorf("unmarshal test_case_results for submission %d: %w", id, err)
		}
	}

	return &s, nil
}

// ─── List (history pages) ─────────────────────────────────────────────────────

// SubmissionFilter narrows ListAll. Nil/zero fields are ignored.
type SubmissionFilter struct {
	UserID    *models.ID
	ProblemID *models.ID
	ContestID *models.ID
	Status    string
}

// SubmissionListItem is the lightweight row for history listings.
// Heavy columns (compile_log, test_case_results) are intentionally excluded —
// the detail endpoint serves those.
type SubmissionListItem struct {
	ID           models.ID               `json:"id"`
	UserID       models.ID               `json:"user_id"`
	Username     string                  `json:"username"`
	ProblemID    models.ID               `json:"problem_id"`
	ProblemTitle string                  `json:"problem_title"`
	ContestID    *models.ID              `json:"contest_id,omitempty"`
	Language     models.Language         `json:"language"`
	Status       models.SubmissionStatus `json:"status"`
	Score        int                     `json:"score"`
	TimeUsedMs   int64                   `json:"time_used_ms"`
	MemUsedKB    int64                   `json:"mem_used_kb"`
	CreatedAt    time.Time               `json:"created_at"`
}

// ListAll returns submissions newest-first with optional filters, plus the
// total row count for pagination. Serves both the public per-user history
// (filter.UserID set) and the admin global listing (no filter).
func (r *SubmissionRepo) ListAll(ctx context.Context, f SubmissionFilter, limit, offset int) ([]SubmissionListItem, int, error) {
	where := ""
	args := []interface{}{}
	add := func(cond string, v interface{}) {
		args = append(args, v)
		if where == "" {
			where = "WHERE "
		} else {
			where += " AND "
		}
		where += fmt.Sprintf(cond, len(args))
	}
	if f.UserID != nil {
		add("s.user_id = $%d", *f.UserID)
	}
	if f.ProblemID != nil {
		add("s.problem_id = $%d", *f.ProblemID)
	}
	if f.ContestID != nil {
		add("s.contest_id = $%d", *f.ContestID)
	}
	if f.Status != "" {
		add("s.status = $%d", f.Status)
	}

	countQ := `SELECT COUNT(*) FROM submissions s ` + where
	var total int
	if err := r.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count submissions: %w", err)
	}

	listQ := `
SELECT s.id, s.user_id, u.username, s.problem_id, p.title, s.contest_id,
       s.language, s.status, s.score, s.time_used_ms, s.mem_used_kb, s.created_at
FROM   submissions s
JOIN   users u    ON u.id = s.user_id
JOIN   problems p ON p.id = s.problem_id
` + where + fmt.Sprintf(`
ORDER BY s.id DESC
LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list submissions: %w", err)
	}
	defer rows.Close()

	var out []SubmissionListItem
	for rows.Next() {
		var it SubmissionListItem
		var contestID sql.NullInt64
		if err := rows.Scan(
			&it.ID, &it.UserID, &it.Username, &it.ProblemID, &it.ProblemTitle, &contestID,
			&it.Language, &it.Status, &it.Score, &it.TimeUsedMs, &it.MemUsedKB, &it.CreatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan submission list item: %w", err)
		}
		if contestID.Valid {
			cid := models.ID(contestID.Int64)
			it.ContestID = &cid
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

// FeedSubmission is the minimal submission shape for the resolver XML export.
type FeedSubmission struct {
	ID        models.ID
	UserID    models.ID
	ProblemID models.ID
	Status    models.SubmissionStatus
	CreatedAt time.Time
}

// ListForFeed returns every submission of a contest in chronological order,
// for building the resolver event feed. Not paginated — a contest's submission
// count is bounded and the export is an admin, on-demand operation.
func (r *SubmissionRepo) ListForFeed(ctx context.Context, contestID models.ID) ([]FeedSubmission, error) {
	const q = `
SELECT id, user_id, problem_id, status, created_at
FROM   submissions
WHERE  contest_id = $1
ORDER  BY created_at, id`

	rows, err := r.db.QueryContext(ctx, q, contestID)
	if err != nil {
		return nil, fmt.Errorf("list feed submissions for contest %d: %w", contestID, err)
	}
	defer rows.Close()

	var out []FeedSubmission
	for rows.Next() {
		var s FeedSubmission
		if err := rows.Scan(&s.ID, &s.UserID, &s.ProblemID, &s.Status, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan feed submission: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListPendingByContest returns the contest's still-unjudged (Pending) submissions
// with just the columns needed to (re)build a JudgeTask: id, user, problem,
// contest, language, the MinIO source key and the original submission time. It
// drives the deferred batch evaluation (赛后评测) of 盲考 contests, whose
// submissions are withheld from the judge until the contest ends.
//
// created_at is selected so the rebuilt JudgeTask carries the real submission
// instant (SubmittedAt); without it the batch path would enqueue a zero time and
// the ranking service would fall back to judge-completion time.
func (r *SubmissionRepo) ListPendingByContest(ctx context.Context, contestID models.ID) ([]*models.Submission, error) {
	const q = `
SELECT id, user_id, problem_id, contest_id, language, source_code_path, status, created_at
FROM   submissions
WHERE  contest_id = $1 AND status = $2
ORDER  BY id`

	rows, err := r.db.QueryContext(ctx, q, contestID, string(models.StatusPending))
	if err != nil {
		return nil, fmt.Errorf("list pending submissions for contest %d: %w", contestID, err)
	}
	defer rows.Close()

	var out []*models.Submission
	for rows.Next() {
		var s models.Submission
		var contestID sql.NullInt64
		if err := rows.Scan(
			&s.ID, &s.UserID, &s.ProblemID, &contestID,
			&s.Language, &s.SourceCodePath, &s.Status, &s.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending submission: %w", err)
		}
		if contestID.Valid {
			cid := models.ID(contestID.Int64)
			s.ContestID = &cid
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// MarkSuperseded sets the given submissions' status to Superseded — voided
// because a later submission to the same problem overrode them under OI's
// last-submission rule. The `status = Pending` guard keeps it idempotent and
// avoids ever clobbering an already-judged submission. Bulk UPDATE in one trip.
func (r *SubmissionRepo) MarkSuperseded(ctx context.Context, ids []models.ID) error {
	if len(ids) == 0 {
		return nil
	}
	raw := make([]int64, len(ids))
	for i, id := range ids {
		raw[i] = int64(id)
	}
	const q = `
UPDATE submissions
SET    status = $1, updated_at = NOW()
WHERE  id = ANY($2) AND status = $3`
	if _, err := r.db.ExecContext(ctx, q,
		string(models.StatusSuperseded), pq.Array(raw), string(models.StatusPending),
	); err != nil {
		return fmt.Errorf("mark submissions superseded: %w", err)
	}
	return nil
}

// UserSubmissionStats summarises one user's judging history for profile pages.
type UserSubmissionStats struct {
	Total    int `json:"total"`
	Accepted int `json:"accepted"`
	Solved   int `json:"solved"` // distinct problems with at least one AC
}

func (r *SubmissionRepo) UserStats(ctx context.Context, userID models.ID) (*UserSubmissionStats, error) {
	const q = `
SELECT COUNT(*),
       COUNT(*)                    FILTER (WHERE status = 'Accepted'),
       COUNT(DISTINCT problem_id)  FILTER (WHERE status = 'Accepted')
FROM submissions WHERE user_id = $1`
	var s UserSubmissionStats
	if err := r.db.QueryRowContext(ctx, q, userID).Scan(&s.Total, &s.Accepted, &s.Solved); err != nil {
		return nil, fmt.Errorf("user submission stats for %d: %w", userID, err)
	}
	return &s, nil
}

// ─── Update ───────────────────────────────────────────────────────────────────

// Update writes the mutable judging fields back to the DB.
// Only the result columns are updated; identity columns (user_id, problem_id,
// contest_id, language, source_code_path) are immutable after creation.
const updateSubmissionSQL = `
UPDATE submissions SET
    status            = $2,
    score             = $3,
    time_used_ms      = $4,
    mem_used_kb       = $5,
    compile_log       = $6,
    judge_message     = $7,
    test_case_results = $8::jsonb,
    judge_node_id     = $9,
    updated_at        = NOW()
WHERE id = $1`

func (r *SubmissionRepo) Update(ctx context.Context, s *models.Submission) error {
	tcJSON, err := marshalJSONB(s.TestCaseResults)
	if err != nil {
		return fmt.Errorf("marshal test_case_results: %w", err)
	}
	// Same bytea → jsonb pitfall as Create: pass as string, nil stays SQL NULL.
	var tcParam interface{}
	if tcJSON != nil {
		tcParam = string(tcJSON)
	}

	res, err := r.db.ExecContext(ctx, updateSubmissionSQL,
		s.ID,
		string(s.Status),
		s.Score,
		s.TimeUsedMs,
		s.MemUsedKB,
		s.CompileLog,
		s.JudgeMessage,
		tcParam,
		s.JudgeNodeID,
	)
	if err != nil {
		return fmt.Errorf("update submission %d: %w", s.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("submission %d not found on update", s.ID)
	}
	return nil
}
