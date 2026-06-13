package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/your-org/my-oj/internal/models"
)

// UserRepo is the PostgreSQL-backed user store.
type UserRepo struct {
	db *sqlx.DB
}

func NewUserRepo(db *sqlx.DB) *UserRepo {
	return &UserRepo{db: db}
}

func (r *UserRepo) Create(ctx context.Context, u *models.User) error {
	const q = `
INSERT INTO users (username, email, password_hash, role, organization)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, created_at, updated_at`
	return r.db.QueryRowContext(ctx, q,
		u.Username, u.Email, u.PasswordHash, string(u.Role), u.Organization,
	).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
}

func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*models.User, error) {
	const q = `
SELECT id, username, email, password_hash, role, organization, created_at, updated_at
FROM users WHERE username = $1`
	var u models.User
	err := r.db.QueryRowContext(ctx, q, username).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash,
		&u.Role, &u.Organization, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	return &u, nil
}

// Search lists users whose username / email / organization contains q
// (case-insensitive), newest first; empty q lists everyone. Returns the total
// matching count for pagination. Used by the admin user-management page.
func (r *UserRepo) Search(ctx context.Context, q string, limit, offset int) ([]models.User, int, error) {
	pattern := "%" + q + "%"
	const countQ = `
SELECT COUNT(*) FROM users
WHERE username ILIKE $1 OR email ILIKE $1 OR organization ILIKE $1`
	var total int
	if err := r.db.QueryRowContext(ctx, countQ, pattern).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	const listQ = `
SELECT id, username, email, role, organization, created_at, updated_at
FROM users
WHERE username ILIKE $1 OR email ILIKE $1 OR organization ILIKE $1
ORDER BY id DESC
LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryContext(ctx, listQ, pattern, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("search users: %w", err)
	}
	defer rows.Close()

	var out []models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(
			&u.ID, &u.Username, &u.Email, &u.Role, &u.Organization, &u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, 0, fmt.Errorf("scan user row: %w", err)
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

// UpdateProfile updates the user-editable profile fields (currently the
// organization / 学校单位, used as the affiliation in the resolver XML export).
func (r *UserRepo) UpdateProfile(ctx context.Context, id models.ID, organization string) error {
	const q = `UPDATE users SET organization = $2, updated_at = NOW() WHERE id = $1`
	res, err := r.db.ExecContext(ctx, q, id, organization)
	if err != nil {
		return fmt.Errorf("update profile for user %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("user %d not found", id)
	}
	return nil
}

func (r *UserRepo) GetByID(ctx context.Context, id models.ID) (*models.User, error) {
	const q = `
SELECT id, username, email, password_hash, role, organization, created_at, updated_at
FROM users WHERE id = $1`
	var u models.User
	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash,
		&u.Role, &u.Organization, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &u, nil
}
