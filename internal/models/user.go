package models

import "time"

// User is the platform account entity.
// PasswordHash is never serialised to JSON (json:"-").
//
// Email is optional (nullable): accounts may exist without a bound email, and a
// user can bind one later from their profile. It is omitted from JSON when unset
// and is only ever exposed to the account owner or an admin — never on public
// profiles.
type User struct {
	ID           ID       `db:"id"            json:"id"`
	Username     string   `db:"username"      json:"username"`
	Email        *string  `db:"email"         json:"email,omitempty"`
	PasswordHash string   `db:"password_hash" json:"-"`
	Role         UserRole `db:"role"          json:"role"`
	// Organization is used by team-contest grouping and scoreboard display.
	Organization string    `db:"organization"  json:"organization,omitempty"`
	CreatedAt    time.Time `db:"created_at"    json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"    json:"updated_at"`
}
