package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/your-org/my-oj/internal/models"
)

// ContestRepo provides full CRUD access to contests (separate from the
// ranking-only ContestMetaLoader).
type ContestRepo struct {
	db *sqlx.DB
}

func NewContestRepo(db *sqlx.DB) *ContestRepo {
	return &ContestRepo{db: db}
}

// ─── List ─────────────────────────────────────────────────────────────────────

func (r *ContestRepo) List(ctx context.Context, onlyPublic bool, limit, offset int) ([]models.Contest, int, error) {
	var countQ, listQ string
	if onlyPublic {
		countQ = `SELECT COUNT(*) FROM contests WHERE is_public = true`
		listQ = `
SELECT id, title, description, contest_type, status,
       start_time, end_time, freeze_time, is_public, allow_late_register, organizer_id,
       created_at, updated_at
FROM contests WHERE is_public = true
ORDER BY start_time DESC LIMIT $1 OFFSET $2`
	} else {
		countQ = `SELECT COUNT(*) FROM contests`
		listQ = `
SELECT id, title, description, contest_type, status,
       start_time, end_time, freeze_time, is_public, allow_late_register, organizer_id,
       created_at, updated_at
FROM contests
ORDER BY start_time DESC LIMIT $1 OFFSET $2`
	}

	var total int
	if err := r.db.QueryRowContext(ctx, countQ).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count contests: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, listQ, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list contests: %w", err)
	}
	defer rows.Close()

	var contests []models.Contest
	for rows.Next() {
		c, err := scanContest(rows)
		if err != nil {
			return nil, 0, err
		}
		contests = append(contests, *c)
	}
	return contests, total, rows.Err()
}

// ─── GetByID ──────────────────────────────────────────────────────────────────

func (r *ContestRepo) GetByID(ctx context.Context, id models.ID) (*models.Contest, error) {
	const q = `
SELECT id, title, description, contest_type, status,
       start_time, end_time, freeze_time, is_public, allow_late_register, organizer_id,
       created_at, updated_at
FROM contests WHERE id = $1`

	rows, err := r.db.QueryContext(ctx, q, id)
	if err != nil {
		return nil, fmt.Errorf("get contest %d: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	return scanContest(rows)
}

// ─── GetProblems ──────────────────────────────────────────────────────────────

type ContestProblemSummary struct {
	ProblemID   models.ID  `json:"problem_id"`
	Label       string     `json:"label"`
	Title       string     `json:"title"`
	MaxScore    int        `json:"max_score"`
	Ordinal     int        `json:"ordinal"`
	TimeLimitMs int64      `json:"time_limit_ms"`
	MemLimitKB  int64      `json:"mem_limit_kb"`
}

func (r *ContestRepo) GetProblems(ctx context.Context, contestID models.ID) ([]ContestProblemSummary, error) {
	const q = `
SELECT cp.problem_id, cp.label, p.title, cp.max_score, cp.ordinal,
       p.time_limit_ms, p.mem_limit_kb
FROM   contest_problems cp
JOIN   problems p ON p.id = cp.problem_id
WHERE  cp.contest_id = $1
ORDER  BY cp.ordinal`

	rows, err := r.db.QueryContext(ctx, q, contestID)
	if err != nil {
		return nil, fmt.Errorf("get contest problems: %w", err)
	}
	defer rows.Close()

	var out []ContestProblemSummary
	for rows.Next() {
		var s ContestProblemSummary
		if err := rows.Scan(
			&s.ProblemID, &s.Label, &s.Title, &s.MaxScore, &s.Ordinal,
			&s.TimeLimitMs, &s.MemLimitKB,
		); err != nil {
			return nil, fmt.Errorf("scan contest problem: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─── Register ─────────────────────────────────────────────────────────────────

func (r *ContestRepo) Register(ctx context.Context, contestID, userID models.ID) error {
	const q = `
INSERT INTO contest_participants (contest_id, user_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING`
	_, err := r.db.ExecContext(ctx, q, contestID, userID)
	return err
}

// IsRegistered returns true if the user has registered for the contest.
func (r *ContestRepo) IsRegistered(ctx context.Context, contestID, userID models.ID) (bool, error) {
	const q = `SELECT 1 FROM contest_participants WHERE contest_id=$1 AND user_id=$2`
	var dummy int
	err := r.db.QueryRowContext(ctx, q, contestID, userID).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ─── Create ───────────────────────────────────────────────────────────────────

func (r *ContestRepo) Create(ctx context.Context, c *models.Contest) error {
	const q = `
INSERT INTO contests (title, description, contest_type, status, start_time, end_time, freeze_time,
                      settings, is_public, allow_late_register, organizer_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
RETURNING id, created_at, updated_at`

	settingsJSON, err := json.Marshal(c.Settings)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	var freezeTime *time.Time
	if c.FreezeTime != nil {
		t := *c.FreezeTime
		freezeTime = &t
	}

	return r.db.QueryRowContext(ctx, q,
		c.Title, c.Description, string(c.ContestType), string(c.Status),
		c.StartTime, c.EndTime, freezeTime,
		settingsJSON, c.IsPublic, c.AllowLateRegister, c.OrganizerID,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func scanContest(row rowScanner) (*models.Contest, error) {
	var c models.Contest
	var freezeTime sql.NullTime

	if err := row.Scan(
		&c.ID, &c.Title, &c.Description, &c.ContestType, &c.Status,
		&c.StartTime, &c.EndTime, &freezeTime,
		&c.IsPublic, &c.AllowLateRegister, &c.OrganizerID,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scan contest: %w", err)
	}
	if freezeTime.Valid {
		t := freezeTime.Time
		c.FreezeTime = &t
	}
	return &c, nil
}
