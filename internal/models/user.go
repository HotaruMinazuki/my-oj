package models

import "time"

// User is the platform account entity.
// PasswordHash is never serialised to JSON (json:"-").
type User struct {
	ID           ID       `db:"id"            json:"id"`
	Username     string   `db:"username"      json:"username"`
	Email        string   `db:"email"         json:"email"`
	PasswordHash string   `db:"password_hash" json:"-"`
	Role         UserRole `db:"role"          json:"role"`
	// Organization is used by team-contest grouping and scoreboard display.
	Organization string    `db:"organization"  json:"organization,omitempty"`
	CreatedAt    time.Time `db:"created_at"    json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"    json:"updated_at"`
}
