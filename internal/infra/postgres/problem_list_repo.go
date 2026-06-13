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
		// A problem is visible in the public bank if it is explicitly public, OR
		// it belongs to a contest that has already ended — contest problems are
		// auto-published to the bank once their contest finishes.
		const publicWhere = `
WHERE is_public = true
   OR EXISTS (
        SELECT 1 FROM contest_problems cp
        JOIN   contests c ON c.id = cp.contest_id
        WHERE  cp.problem_id = problems.id AND c.end_time < NOW()
      )`
		countQ = `SELECT COUNT(*) FROM problems ` + publicWhere
		listQ = `
SELECT id, title, time_limit_ms, mem_limit_kb, judge_type, is_public, author_id, created_at, updated_at
FROM problems ` + publicWhere + `
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

// CanNonAdminView reports whether a non-admin user may view a private problem.
// A contest problem (is_public=false) is viewable when:
//   - any contest containing it has ended (auto-published to everyone), OR
//   - a contest containing it is currently running AND the contest is public
//     or the user is a registered participant.
// (Public problems never reach this query — the caller short-circuits them.)
func (r *ProblemRepo) CanNonAdminView(ctx context.Context, problemID, userID models.ID, authed bool) (bool, error) {
	const q = `
SELECT EXISTS (
  SELECT 1
  FROM   contest_problems cp
  JOIN   contests c ON c.id = cp.contest_id
  WHERE  cp.problem_id = $1
    AND (
      c.end_time < NOW()
      OR (
        c.start_time <= NOW() AND NOW() <= c.end_time
        AND (
          c.is_public
          OR ($2 AND EXISTS (
                SELECT 1 FROM contest_participants p
                WHERE p.contest_id = c.id AND p.user_id = $3))
        )
      )
    )
)`
	var ok bool
	if err := r.db.QueryRowContext(ctx, q, problemID, authed, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("check problem %d visibility: %w", problemID, err)
	}
	return ok, nil
}

// UpdateProblem updates the editable fields of a problem (title, statement,
// limits, judge type, visibility). The testcase data is managed separately via
// the testcase-upload endpoint.
func (r *ProblemRepo) UpdateProblem(ctx context.Context, p *models.Problem) error {
	const q = `
UPDATE problems SET
  title         = $2,
  statement     = $3,
  time_limit_ms = $4,
  mem_limit_kb  = $5,
  judge_type    = $6,
  is_public     = $7,
  updated_at    = NOW()
WHERE id = $1`
	res, err := r.db.ExecContext(ctx, q,
		p.ID, p.Title, p.Statement, p.TimeLimitMs, p.MemLimitKB, string(p.JudgeType), p.IsPublic,
	)
	if err != nil {
		return fmt.Errorf("update problem %d: %w", p.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("problem %d not found", p.ID)
	}
	return nil
}

// DeleteProblem removes a problem and everything that depends on it. Its
// submissions are deleted first (they have no ON DELETE CASCADE), then the row
// itself — test_cases and contest_problems cascade away automatically.
func (r *ProblemRepo) DeleteProblem(ctx context.Context, id models.ID) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `DELETE FROM submissions WHERE problem_id = $1`, id); err != nil {
		return fmt.Errorf("delete submissions for problem %d: %w", id, err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM problems WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete problem %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("problem %d not found", id)
	}
	return tx.Commit()
}

// CreateProblem inserts a new problem and back-fills ID, CreatedAt, UpdatedAt.
func (r *ProblemRepo) CreateProblem(ctx context.Context, p *models.Problem) error {
	// NOTE: pass JSONB payloads as string (not []byte). lib/pq encodes []byte as
	// bytea, which Postgres refuses to implicitly cast to jsonb.
	const q = `
INSERT INTO problems (title, statement, time_limit_ms, mem_limit_kb, judge_type, judge_config, allowed_langs, is_public, author_id)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9)
RETURNING id, created_at, updated_at`

	judgeConfigJSON, err := json.Marshal(p.JudgeConfig)
	if err != nil {
		return fmt.Errorf("marshal judge_config: %w", err)
	}

	// NULL when no restriction; otherwise a JSON array string.
	var allowedLangsParam interface{}
	if len(p.AllowedLangs) > 0 {
		b, err := json.Marshal(p.AllowedLangs)
		if err != nil {
			return fmt.Errorf("marshal allowed_langs: %w", err)
		}
		allowedLangsParam = string(b)
	} else {
		allowedLangsParam = nil
	}

	return r.db.QueryRowContext(ctx, q,
		p.Title, p.Statement, p.TimeLimitMs, p.MemLimitKB,
		string(p.JudgeType), string(judgeConfigJSON), allowedLangsParam,
		p.IsPublic, p.AuthorID,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}
