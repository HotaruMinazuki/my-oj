package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

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
    $10, $11, $12, $13
) RETURNING id, created_at, updated_at`

// Create inserts a new Submission row and back-fills ID, CreatedAt, UpdatedAt.
// TestCaseResults is marshalled to JSONB; a nil slice stores SQL NULL.
func (r *SubmissionRepo) Create(ctx context.Context, s *models.Submission) error {
	tcJSON, err := marshalJSONB(s.TestCaseResults)
	if err != nil {
		return fmt.Errorf("marshal test_case_results: %w", err)
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
		tcJSON,
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
    test_case_results = $8,
    judge_node_id     = $9,
    updated_at        = NOW()
WHERE id = $1`

func (r *SubmissionRepo) Update(ctx context.Context, s *models.Submission) error {
	tcJSON, err := marshalJSONB(s.TestCaseResults)
	if err != nil {
		return fmt.Errorf("marshal test_case_results: %w", err)
	}

	res, err := r.db.ExecContext(ctx, updateSubmissionSQL,
		s.ID,
		string(s.Status),
		s.Score,
		s.TimeUsedMs,
		s.MemUsedKB,
		s.CompileLog,
		s.JudgeMessage,
		tcJSON,
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
