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

// ProblemRepo is the PostgreSQL-backed implementation of handler.ProblemRepo.
type ProblemRepo struct {
	db *sqlx.DB
}

func NewProblemRepo(db *sqlx.DB) *ProblemRepo {
	return &ProblemRepo{db: db}
}

// ─── GetJudgeConfig ───────────────────────────────────────────────────────────

// GetJudgeConfig fetches only the JSONB judge_config column for a problem.
// The JSONB value is unmarshalled into a JudgeConfig struct; an empty column
// (NULL or {}) returns a zero-value JudgeConfig (safe for standard problems).
func (r *ProblemRepo) GetJudgeConfig(ctx context.Context, problemID models.ID) (*models.JudgeConfig, error) {
	const q = `SELECT judge_config FROM problems WHERE id = $1`

	row := r.db.QueryRowContext(ctx, q, problemID)

	var raw []byte
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("problem %d not found", problemID)
		}
		return nil, fmt.Errorf("query judge_config for problem %d: %w", problemID, err)
	}

	var cfg models.JudgeConfig
	if raw != nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal judge_config for problem %d: %w", problemID, err)
		}
	}
	return &cfg, nil
}

// ─── GetTestCases ─────────────────────────────────────────────────────────────

// GetTestCases returns all non-sample test cases for a problem, ordered by
// (group_id, ordinal) to match the judger's execution order.
// Paths stored in the DB are relative filenames (e.g. "1.in", "1.out") that
// the judger resolves against the local testcase cache directory at Stage 0.
func (r *ProblemRepo) GetTestCases(ctx context.Context, problemID models.ID) ([]models.JudgeTestCase, error) {
	const q = `
SELECT id, group_id, ordinal, input_path, output_path, score
FROM   test_cases
WHERE  problem_id = $1
ORDER  BY group_id, ordinal`

	rows, err := r.db.QueryContext(ctx, q, problemID)
	if err != nil {
		return nil, fmt.Errorf("query test_cases for problem %d: %w", problemID, err)
	}
	defer rows.Close()

	var out []models.JudgeTestCase
	for rows.Next() {
		var (
			id         models.ID
			groupID    int
			ordinal    int
			inputPath  string
			outputPath string
			score      int
		)
		if err := rows.Scan(&id, &groupID, &ordinal, &inputPath, &outputPath, &score); err != nil {
			return nil, fmt.Errorf("scan test case row: %w", err)
		}
		out = append(out, models.JudgeTestCase{
			TestCaseID: id,
			GroupID:    groupID,
			Ordinal:    ordinal,
			InputPath:  inputPath,
			OutputPath: outputPath,
			Score:      score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate test_cases for problem %d: %w", problemID, err)
	}
	return out, nil
}

// ReplaceTestCases atomically replaces all test-case rows of a problem.
// Called by the admin testcase-upload endpoint after the zip is stored in MinIO,
// so the DB metadata always mirrors the latest uploaded archive.
//
// It also syncs contest_problems.max_score for this problem to the sum of the new
// per-case scores: that sum IS the problem's OI/IOI full mark, so the contest's
// displayed 分值 stays consistent with what actually gets scored. Problems not in
// any contest simply update zero rows.
func (r *ProblemRepo) ReplaceTestCases(ctx context.Context, problemID models.ID, cases []models.JudgeTestCase) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `DELETE FROM test_cases WHERE problem_id = $1`, problemID); err != nil {
		return fmt.Errorf("delete old test cases for problem %d: %w", problemID, err)
	}

	const ins = `
INSERT INTO test_cases (problem_id, group_id, ordinal, input_path, output_path, score, is_sample)
VALUES ($1, $2, $3, $4, $5, $6, FALSE)`
	totalScore := 0
	for _, tc := range cases {
		if _, err := tx.ExecContext(ctx, ins,
			problemID, tc.GroupID, tc.Ordinal, tc.InputPath, tc.OutputPath, tc.Score,
		); err != nil {
			return fmt.Errorf("insert test case %d for problem %d: %w", tc.Ordinal, problemID, err)
		}
		totalScore += tc.Score
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE contest_problems SET max_score = $2 WHERE problem_id = $1`,
		problemID, totalScore,
	); err != nil {
		return fmt.Errorf("sync contest max_score for problem %d: %w", problemID, err)
	}
	return tx.Commit()
}

// GetJudgeMeta fetches the judging metadata (judge_type, time_limit_ms, mem_limit_kb)
// for a problem. These come from separate columns vs. the JSONB judge_config.
func (r *ProblemRepo) GetJudgeMeta(ctx context.Context, problemID models.ID) (*models.ProblemJudgeMeta, error) {
	const q = `SELECT judge_type, time_limit_ms, mem_limit_kb FROM problems WHERE id = $1`

	var meta models.ProblemJudgeMeta
	var judgeTypeStr string
	err := r.db.QueryRowContext(ctx, q, problemID).Scan(&judgeTypeStr, &meta.TimeLimitMs, &meta.MemLimitKB)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("problem %d not found", problemID)
		}
		return nil, fmt.Errorf("query judge meta for problem %d: %w", problemID, err)
	}
	meta.JudgeType = models.JudgeType(judgeTypeStr)
	return &meta, nil
}
