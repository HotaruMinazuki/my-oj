package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

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
