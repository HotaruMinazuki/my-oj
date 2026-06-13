package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/your-org/my-oj/internal/core/ranking"
	"github.com/your-org/my-oj/internal/models"
)

// NewContestMetaLoader returns a ranking.ContestMetaLoader backed by PostgreSQL.
//
// The loader is called by the RankingService on every judge result for a
// contest it hasn't cached yet (TTL 30 s).  It fetches only the columns the
// ranking pipeline actually needs — not the full Contest struct — to keep the
// query cheap and the coupling explicit.
func NewContestMetaLoader(db *sqlx.DB) ranking.ContestMetaLoader {
	return func(ctx context.Context, contestID models.ID) (*ranking.ContestMeta, error) {
		const q = `
SELECT contest_type, settings, start_time, freeze_time, end_time
FROM   contests
WHERE  id = $1`

		row := db.QueryRowContext(ctx, q, contestID)

		var (
			contestTypeStr string
			settingsRaw    []byte
			freezeTime     sql.NullTime
			meta           ranking.ContestMeta
		)

		err := row.Scan(
			&contestTypeStr,
			&settingsRaw,
			&meta.StartTime,
			&freezeTime,
			&meta.EndTime,
		)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("contest %d not found", contestID)
			}
			return nil, fmt.Errorf("query contest %d: %w", contestID, err)
		}

		meta.ContestType = models.ContestType(contestTypeStr)

		if freezeTime.Valid {
			t := freezeTime.Time
			meta.FreezeTime = &t
		}

		// settings is JSONB; unmarshal into ContestSettings (map[string]any).
		// A NULL column (no settings set) leaves meta.Settings as nil — the
		// Strategy implementations must treat nil as "use defaults".
		if settingsRaw != nil {
			if err := json.Unmarshal(settingsRaw, &meta.Settings); err != nil {
				return nil, fmt.Errorf("unmarshal settings for contest %d: %w", contestID, err)
			}
		}

		return &meta, nil
	}
}

// NewContestProblemsLoader returns a ranking.ContestProblemsLoader backed by
// PostgreSQL. It lists a contest's problems in display order so the scoreboard
// can render its columns (label "A", "B", …) and map problemID → label.
func NewContestProblemsLoader(db *sqlx.DB) ranking.ContestProblemsLoader {
	return func(ctx context.Context, contestID models.ID) ([]ranking.ProblemLabel, error) {
		const q = `
SELECT problem_id, label
FROM   contest_problems
WHERE  contest_id = $1
ORDER  BY ordinal`

		rows, err := db.QueryContext(ctx, q, contestID)
		if err != nil {
			return nil, fmt.Errorf("load contest %d problems: %w", contestID, err)
		}
		defer rows.Close()

		var out []ranking.ProblemLabel
		for rows.Next() {
			var pl ranking.ProblemLabel
			if err := rows.Scan(&pl.ProblemID, &pl.Label); err != nil {
				return nil, fmt.Errorf("scan contest problem: %w", err)
			}
			out = append(out, pl)
		}
		return out, rows.Err()
	}
}

// NewUsernamesLoader returns a ranking.UsernamesLoader that resolves a set of
// user IDs to display info in a single round-trip.
func NewUsernamesLoader(db *sqlx.DB) ranking.UsernamesLoader {
	return func(ctx context.Context, userIDs []models.ID) (map[models.ID]ranking.UserInfo, error) {
		out := make(map[models.ID]ranking.UserInfo, len(userIDs))
		if len(userIDs) == 0 {
			return out, nil
		}
		const q = `SELECT id, username, organization FROM users WHERE id = ANY($1)`
		rows, err := db.QueryContext(ctx, q, pq.Array([]int64(userIDs)))
		if err != nil {
			return nil, fmt.Errorf("load usernames: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var id models.ID
			var info ranking.UserInfo
			if err := rows.Scan(&id, &info.Username, &info.Organization); err != nil {
				return nil, fmt.Errorf("scan username: %w", err)
			}
			out[id] = info
		}
		return out, rows.Err()
	}
}
