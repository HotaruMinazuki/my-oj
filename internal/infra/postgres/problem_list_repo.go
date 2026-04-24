package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/your-org/my-oj/internal/models"
)

// ListProblems returns a paginated list of problems.
// If onlyPublic is true, only is_public=true rows are returned (for non-admin users).
func (r *ProblemRepo) ListProblems(ctx context.Context, onlyPublic bool, limit, offset int) ([]models.Problem, int, error) {
	var countQ, listQ string
	var args []any

	if onlyPublic {
		countQ = `SELECT COUNT(*) FROM problems WHERE is_public = true`
		listQ = `
SELECT id, title, time_limit_ms, mem_limit_kb, judge_type, is_public, author_id, created_at, updated_at
FROM problems WHERE is_public = true
ORDER BY id DESC LIMIT $1 OFFSET $2`
		args = []any{limit, offset}
	} else {
		countQ = `SELECT COUNT(*) FROM problems`
		listQ = `
SELECT id, title, time_limit_ms, mem_limit_kb, judge_type, is_public, author_id, created_at, updated_at
FROM problems
ORDER BY id DESC LIMIT $1 OFFSET $2`
		args = []any{limit, offset}
	}

	var total int
	if err := r.db.QueryRowContext(ctx, countQ).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count problems: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list problems: %w", err)
	}
	defer rows.Close()

	var problems []models.Problem
	for rows.Next() {
		var p models.Problem
		if err := rows.Scan(
			&p.ID, &p.Title, &p.TimeLimitMs, &p.MemLimitKB,
			&p.JudgeType, &p.IsPublic, &p.AuthorID, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan problem row: %w", err)
		}
		problems = append(problems, p)
	}
	return problems, total, rows.Err()
}

// GetProblemByID fetches a single problem including its statement.
func (r *ProblemRepo) GetProblemByID(ctx context.Context, id models.ID) (*models.Problem, error) {
	const q = `
SELECT id, title, statement, time_limit_ms, mem_limit_kb,
       judge_type, judge_config, allowed_langs, is_public, author_id, created_at, updated_at
FROM problems WHERE id = $1`

	row := r.db.QueryRowContext(ctx, q, id)
	var p models.Problem
	var judgeConfigRaw []byte
	var allowedLangsRaw []byte

	err := row.Scan(
		&p.ID, &p.Title, &p.Statement, &p.TimeLimitMs, &p.MemLimitKB,
		&p.JudgeType, &judgeConfigRaw, &allowedLangsRaw,
		&p.IsPublic, &p.AuthorID, &p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get problem %d: %w", id, err)
	}

	if judgeConfigRaw != nil {
		if err := json.Unmarshal(judgeConfigRaw, &p.JudgeConfig); err != nil {
			return nil, fmt.Errorf("unmarshal judge_config: %w", err)
		}
	}
	if allowedLangsRaw != nil {
		if err := json.Unmarshal(allowedLangsRaw, &p.AllowedLangs); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_langs: %w", err)
		}
	}
	return &p, nil
}

// CreateProblem inserts a new problem and back-fills ID, CreatedAt, UpdatedAt.
func (r *ProblemRepo) CreateProblem(ctx context.Context, p *models.Problem) error {
	const q = `
INSERT INTO problems (title, statement, time_limit_ms, mem_limit_kb, judge_type, judge_config, allowed_langs, is_public, author_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id, created_at, updated_at`

	judgeConfigJSON, err := json.Marshal(p.JudgeConfig)
	if err != nil {
		return fmt.Errorf("marshal judge_config: %w", err)
	}
	var allowedLangsJSON []byte
	if len(p.AllowedLangs) > 0 {
		allowedLangsJSON, err = json.Marshal(p.AllowedLangs)
		if err != nil {
			return fmt.Errorf("marshal allowed_langs: %w", err)
		}
	}

	return r.db.QueryRowContext(ctx, q,
		p.Title, p.Statement, p.TimeLimitMs, p.MemLimitKB,
		string(p.JudgeType), judgeConfigJSON, allowedLangsJSON,
		p.IsPublic, p.AuthorID,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}
