package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// ContestStatus tracks the lifecycle phase of a contest.
type ContestStatus string

const (
	ContestStatusDraft   ContestStatus = "draft"
	ContestStatusReady   ContestStatus = "ready"
	ContestStatusRunning ContestStatus = "running"
	// ContestStatusFrozen means the public scoreboard stops updating (ICPC 封榜).
	// Submissions continue to be judged; results are revealed at unfreeze.
	ContestStatusFrozen ContestStatus = "frozen"
	ContestStatusEnded  ContestStatus = "ended"
)

// ContestSettings holds strategy-specific knobs as a flexible JSONB map.
// Each Strategy implementation defines and documents its own keys.
//
// Example for ICPC:  {"penalty_minutes": 20, "first_blood_bonus": 0}
// Example for OI:    {"partial_score": true, "output_only": false}
type ContestSettings map[string]any

func (s ContestSettings) Value() (driver.Value, error) { return json.Marshal(s) }
func (s *ContestSettings) Scan(src any) error {
	b, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("ContestSettings.Scan: expected []byte, got %T", src)
	}
	return json.Unmarshal(b, s)
}

// Contest is the top-level entity for a programming competition.
type Contest struct {
	ID          ID            `db:"id"           json:"id"`
	Title       string        `db:"title"        json:"title"`
	Description string        `db:"description"  json:"description,omitempty"`
	ContestType ContestType   `db:"contest_type" json:"contest_type"`
	Status      ContestStatus `db:"status"       json:"status"`

	StartTime time.Time  `db:"start_time"  json:"start_time"`
	EndTime   time.Time  `db:"end_time"    json:"end_time"`
	// FreezeTime is when the public scoreboard freezes. NULL = no freeze.
	FreezeTime *time.Time `db:"freeze_time" json:"freeze_time,omitempty"`

	// Settings carries strategy-specific config; parsed by the Strategy at load time.
	Settings ContestSettings `db:"settings" json:"settings"`

	IsPublic          bool `db:"is_public"           json:"is_public"`
	AllowLateRegister bool `db:"allow_late_register" json:"allow_late_register"`

	OrganizerID ID        `db:"organizer_id" json:"organizer_id"`
	CreatedAt   time.Time `db:"created_at"   json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"   json:"updated_at"`
}

// ContestProblem links a Problem to a Contest with contest-specific overrides.
type ContestProblem struct {
	ContestID ID `db:"contest_id" json:"contest_id"`
	ProblemID ID `db:"problem_id" json:"problem_id"`
	// Label is the display letter/number within the contest ("A", "B", "1").
	Label string `db:"label" json:"label"`
	// MaxScore overrides the problem default for this contest (OI/IOI).
	MaxScore int `db:"max_score" json:"max_score"`
	// Ordinal controls the column order on the scoreboard.
	Ordinal int `db:"ordinal" json:"ordinal"`
}

// ContestParticipant records a user's registration in a contest.
type ContestParticipant struct {
	ContestID ID `db:"contest_id" json:"contest_id"`
	UserID    ID `db:"user_id"    json:"user_id"`
	// TeamID is non-nil for team contests; multiple users share one TeamID.
	TeamID       *ID       `db:"team_id"      json:"team_id,omitempty"`
	RegisteredAt time.Time `db:"registered_at" json:"registered_at"`
}
