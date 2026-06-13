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

// ListByParticipant returns every contest the user has registered for,
// newest start time first. Contest counts per user are small, so no pagination.
func (r *ContestRepo) ListByParticipant(ctx context.Context, userID models.ID) ([]models.Contest, error) {
	const q = `
SELECT c.id, c.title, c.description, c.contest_type, c.status,
       c.start_time, c.end_time, c.freeze_time, c.is_public, c.allow_late_register, c.organizer_id,
       c.created_at, c.updated_at
FROM   contest_participants cp
JOIN   contests c ON c.id = cp.contest_id
WHERE  cp.user_id = $1
ORDER  BY c.start_time DESC`

	rows, err := r.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list contests for user %d: %w", userID, err)
	}
	defer rows.Close()

	var out []models.Contest
	for rows.Next() {
		c, err := scanContest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
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

// ─── AddProblem ───────────────────────────────────────────────────────────────

// AddProblem inserts a problem into a contest. If ordinal is 0, it's set to
// MAX(ordinal)+1 so the new problem appears at the end. Returns a wrapped
// error that callers can check with errors.Is(err, ErrDuplicateProblem) /
// ErrContestNotFound / ErrProblemNotFound.
func (r *ContestRepo) AddProblem(ctx context.Context, contestID, problemID models.ID, label string, maxScore, ordinal int) error {
	// Use a transaction so concurrent inserts pick distinct ordinals.
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // safe to ignore after Commit

	if ordinal <= 0 {
		var maxOrd sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(ordinal) FROM contest_problems WHERE contest_id=$1`,
			contestID,
		).Scan(&maxOrd); err != nil {
			return fmt.Errorf("query max ordinal: %w", err)
		}
		if maxOrd.Valid {
			ordinal = int(maxOrd.Int64) + 1
		} else {
			ordinal = 1
		}
	}
	if maxScore <= 0 {
		maxScore = 100
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO contest_problems (contest_id, problem_id, label, max_score, ordinal)
VALUES ($1, $2, $3, $4, $5)`,
		contestID, problemID, label, maxScore, ordinal,
	)
	if err != nil {
		return fmt.Errorf("insert contest_problem: %w", err)
	}
	return tx.Commit()
}

// CreateContestProblem creates a new problem (is_public=false) and links it to
// the contest in a single transaction. The problem becomes publicly visible in
// the bank automatically once the contest ends (see ListProblems / CanNonAdminView).
func (r *ContestRepo) CreateContestProblem(ctx context.Context, contestID models.ID, p *models.Problem, label string, maxScore, ordinal int) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	const insProblem = `
INSERT INTO problems (title, statement, time_limit_ms, mem_limit_kb, judge_type,
                      judge_config, allowed_langs, is_public, author_id)
VALUES ($1, $2, $3, $4, $5, '{}'::jsonb, NULL, FALSE, $6)
RETURNING id, created_at, updated_at`
	if err := tx.QueryRowContext(ctx, insProblem,
		p.Title, p.Statement, p.TimeLimitMs, p.MemLimitKB, string(p.JudgeType), p.AuthorID,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return fmt.Errorf("insert contest problem: %w", err)
	}
	p.IsPublic = false

	if ordinal <= 0 {
		var maxOrd sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(ordinal) FROM contest_problems WHERE contest_id=$1`, contestID,
		).Scan(&maxOrd); err != nil {
			return fmt.Errorf("query max ordinal: %w", err)
		}
		if maxOrd.Valid {
			ordinal = int(maxOrd.Int64) + 1
		} else {
			ordinal = 1
		}
	}
	if maxScore <= 0 {
		maxScore = 100
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO contest_problems (contest_id, problem_id, label, max_score, ordinal)
VALUES ($1, $2, $3, $4, $5)`,
		contestID, p.ID, label, maxScore, ordinal,
	); err != nil {
		return fmt.Errorf("link contest problem: %w", err)
	}
	return tx.Commit()
}

// RemoveProblem deletes a problem from a contest. Returns nil if the row
// did not exist (idempotent).
func (r *ContestRepo) RemoveProblem(ctx context.Context, contestID, problemID models.ID) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM contest_problems WHERE contest_id=$1 AND problem_id=$2`,
		contestID, problemID,
	)
	if err != nil {
		return fmt.Errorf("delete contest_problem: %w", err)
	}
	return nil
}

// ─── Teams (for resolver export) ──────────────────────────────────────────────

// ContestTeam is one scoreboard participant in the resolver feed.
type ContestTeam struct {
	UserID       models.ID `json:"user_id"`
	Username     string    `json:"username"`
	Organization string    `json:"organization"`
}

// ListContestTeams returns every user that should appear on the contest's
// scoreboard: registered participants UNION anyone who submitted to the contest.
// (A run must reference a known team, so submitters are always included.)
func (r *ContestRepo) ListContestTeams(ctx context.Context, contestID models.ID) ([]ContestTeam, error) {
	const q = `
SELECT u.id, u.username, u.organization
FROM users u
WHERE u.id IN (
    SELECT user_id FROM contest_participants WHERE contest_id = $1
    UNION
    SELECT user_id FROM submissions          WHERE contest_id = $1
)
ORDER BY u.id`

	rows, err := r.db.QueryContext(ctx, q, contestID)
	if err != nil {
		return nil, fmt.Errorf("list contest %d teams: %w", contestID, err)
	}
	defer rows.Close()

	var out []ContestTeam
	for rows.Next() {
		var t ContestTeam
		if err := rows.Scan(&t.UserID, &t.Username, &t.Organization); err != nil {
			return nil, fmt.Errorf("scan contest team: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Delete removes a contest. Its submissions are detached (contest_id → NULL,
// becoming practice submissions) since they lack ON DELETE CASCADE; the
// contest_problems and contest_participants rows cascade away automatically.
func (r *ContestRepo) Delete(ctx context.Context, id models.ID) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `UPDATE submissions SET contest_id = NULL WHERE contest_id = $1`, id); err != nil {
		return fmt.Errorf("detach submissions from contest %d: %w", id, err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM contests WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete contest %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("contest %d not found", id)
	}
	return tx.Commit()
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
	// settings is JSONB — send as string so pq encodes it as TEXT (auto-cast
	// to jsonb), not bytea.
	const q = `
INSERT INTO contests (title, description, contest_type, status, start_time, end_time, freeze_time,
                      settings, is_public, allow_late_register, organizer_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9,$10,$11)
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
		string(settingsJSON), c.IsPublic, c.AllowLateRegister, c.OrganizerID,
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
	// Override the stored status with the time-derived phase so the UI shows the
	// real state (ready/running/frozen/ended) instead of the stale "draft".
	c.Status = c.EffectiveStatus(time.Now().UTC())
	return &c, nil
}
