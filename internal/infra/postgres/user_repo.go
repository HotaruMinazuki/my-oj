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
