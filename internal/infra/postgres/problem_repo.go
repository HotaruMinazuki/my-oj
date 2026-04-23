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
