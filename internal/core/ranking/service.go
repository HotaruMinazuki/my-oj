package ranking

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/core/contest"
	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/mq"
)

// RankingService subscribes to judge results and maintains the live scoreboard
// in Redis, publishing a full board snapshot to WebSocket clients on every change.
//
// It runs as a separate goroutine alongside the API server, using its own
// consumer group ("ranker") on QueueJudgeResults so it receives every result
// independently of the API server's DB-writer consumer group.
//
// All result processing happens on the single consumer goroutine, so no locking
// is needed for the Redis read-modify-write of an entry — Redis is the only
// shared state and each contest's entries are touched serially.
type RankingService struct {
	consumer mq.Consumer
	rdb      *redis.Client
	registry *contest.Registry
	log      *zap.Logger

	contestCache *contestMetaCache
	problems     ContestProblemsLoader
	users        UsernamesLoader
}

// ─── Loaders (injected; typically DB-backed) ──────────────────────────────────

// ContestMetaLoader fetches a contest's scoring metadata.
type ContestMetaLoader func(ctx context.Context, contestID models.ID) (*ContestMeta, error)

// ContestProblemsLoader returns a contest's problems in display order (label "A",
// "B", …) so the board can render columns and map problemID → label.
type ContestProblemsLoader func(ctx context.Context, contestID models.ID) ([]ProblemLabel, error)

// UsernamesLoader resolves a set of user IDs to display info in one round-trip.
type UsernamesLoader func(ctx context.Context, userIDs []models.ID) (map[models.ID]UserInfo, error)

// ProblemLabel pairs a problem with its scoreboard column label.
type ProblemLabel struct {
	ProblemID models.ID
	Label     string
}

// UserInfo is the contestant display data shown on the board.
type UserInfo struct {
	Username     string
	Organization string
}

// ContestMeta is the subset of Contest data the ranking service needs.
type ContestMeta struct {
	ContestType models.ContestType
	Settings    models.ContestSettings
	StartTime   time.Time
	FreezeTime  *time.Time
	EndTime     time.Time
}

// NewRankingService creates a RankingService.
func NewRankingService(
	consumer mq.Consumer,
	rdb *redis.Client,
	registry *contest.Registry,
	loader ContestMetaLoader,
	problems ContestProblemsLoader,
	users UsernamesLoader,
	log *zap.Logger,
) *RankingService {
	return &RankingService{
		consumer: consumer,
		rdb:      rdb,
		registry: registry,
		log:      log,
		problems: problems,
		users:    users,
		contestCache: &contestMetaCache{
			loader: loader,
			ttl:    30 * time.Second,
			data:   make(map[models.ID]*cachedMeta),
		},
	}
}

// Run starts the service loop. Blocks until ctx is cancelled.
func (rs *RankingService) Run(ctx context.Context) error {
	rs.log.Info("ranking service starting")
	return rs.consumer.Subscribe(ctx, mq.QueueJudgeResults,
		func(ctx context.Context, msg mq.Message) error {
			return rs.handleResult(ctx, msg)
		},
	)
}

// handleResult processes one judge result message.
func (rs *RankingService) handleResult(ctx context.Context, msg mq.Message) error {
	result, err := mq.UnmarshalResult(msg.Payload)
	if err != nil {
		rs.log.Error("malformed result payload; skipping", zap.Error(err))
		return nil // ACK malformed messages
	}

	// Only process contest submissions; skip out-of-contest practice runs.
	if result.ContestID == nil || result.UserID == 0 || result.ProblemID == 0 {
		return nil
	}

	return rs.processContestResult(ctx, result)
}

// processContestResult integrates one judged contest submission and rebuilds the board.
func (rs *RankingService) processContestResult(ctx context.Context, result *mq.ResultMessage) error {
	contestID := *result.ContestID
	userID, problemID := result.UserID, result.ProblemID
	log := rs.log.With(
		zap.Int64("contest_id", contestID),
		zap.Int64("submission_id", result.SubmissionID),
	)

	meta, err := rs.contestCache.get(ctx, contestID)
	if err != nil {
		return fmt.Errorf("ranking: load contest %d: %w", contestID, err)
	}
	strategy, err := rs.registry.Get(meta.ContestType)
	if err != nil {
		log.Warn("unknown contest type; skipping ranking update", zap.String("type", string(meta.ContestType)))
		return nil
	}

	// ── Read-modify-write the (user, problem) entry ──────────────────────────
	field := entryField(userID, problemID)
	var prev *contest.ScoreEntry
	if raw, err := rs.rdb.HGet(ctx, redisKey(contestID, keyEntries), field).Bytes(); err == nil {
		var e contest.ScoreEntry
		if json.Unmarshal(raw, &e) == nil {
			prev = &e
		}
	}

	event := contest.SubmissionEvent{
		UserID:       userID,
		ProblemID:    problemID,
		Status:       result.Status,
		Score:        result.Score,
		SubmitTime:   result.JudgedAt,
		ContestStart: meta.StartTime,
		FreezeTime:   meta.FreezeTime,
	}
	newEntry := strategy.Apply(event, prev, meta.Settings)

	// ── First blood (only when newly accepted) ───────────────────────────────
	if newEntry.Accepted && (prev == nil || !prev.Accepted) {
		set, err := rs.rdb.HSetNX(ctx, redisKey(contestID, keyFirstBlood),
			fmt.Sprintf("%d", problemID), "1").Result()
		if err == nil && set {
			newEntry.IsFirstBlood = true
		}
	}

	entryJSON, err := json.Marshal(newEntry)
	if err != nil {
		return fmt.Errorf("ranking: marshal entry: %w", err)
	}
	if err := rs.rdb.HSet(ctx, redisKey(contestID, keyEntries), field, entryJSON).Err(); err != nil {
		return fmt.Errorf("ranking: persist entry: %w", err)
	}

	// ── Rebuild the full board and broadcast it ──────────────────────────────
	snapshot, err := rs.rebuildSnapshot(ctx, contestID, meta, strategy)
	if err != nil {
		return fmt.Errorf("ranking: rebuild snapshot: %w", err)
	}
	if err := rs.publishSnapshot(ctx, contestID, snapshot); err != nil {
		log.Warn("publish board snapshot failed", zap.Error(err))
	}
	return nil
}

// RevealContest is the admin-triggered 解榜 action: it marks the contest as
// revealed, then recomputes and broadcasts the board so the (previously frozen)
// final results become visible to everyone. Idempotent — safe to call twice.
func (rs *RankingService) RevealContest(ctx context.Context, contestID models.ID) error {
	if err := rs.rdb.Set(ctx, redisKey(contestID, keyRevealed), "1", 0).Err(); err != nil {
		return fmt.Errorf("ranking: set revealed flag: %w", err)
	}
	meta, err := rs.contestCache.get(ctx, contestID)
	if err != nil {
		return fmt.Errorf("ranking: load contest %d: %w", contestID, err)
	}
	strategy, err := rs.registry.Get(meta.ContestType)
	if err != nil {
		return fmt.Errorf("ranking: strategy for contest %d: %w", contestID, err)
	}
	snapshot, err := rs.rebuildSnapshot(ctx, contestID, meta, strategy)
	if err != nil {
		return fmt.Errorf("ranking: rebuild on reveal: %w", err)
	}
	return rs.publishSnapshot(ctx, contestID, snapshot)
}

// rebuildSnapshot recomputes the full board from the entry hash, stores it at
// keySnapshot, and returns it. Frozen ICPC results stay hidden until an admin
// reveals the contest (解榜).
func (rs *RankingService) rebuildSnapshot(
	ctx context.Context,
	contestID models.ID,
	meta *ContestMeta,
	strategy contest.Strategy,
) (*BoardSnapshot, error) {
	rawEntries, err := rs.rdb.HGetAll(ctx, redisKey(contestID, keyEntries)).Result()
	if err != nil {
		return nil, fmt.Errorf("load entries: %w", err)
	}

	now := time.Now().UTC()
	// The board freezes at FreezeTime and STAYS frozen — through the end of the
	// contest — until an admin explicitly reveals (解榜). The reveal flag both
	// un-freezes the display and unlocks the hidden results below.
	revealed := rs.rdb.Exists(ctx, redisKey(contestID, keyRevealed)).Val() == 1
	inFreeze := meta.FreezeTime != nil && !now.Before(*meta.FreezeTime)
	frozen := inFreeze && !revealed

	var entries []*contest.ScoreEntry
	for _, raw := range rawEntries {
		var e contest.ScoreEntry
		if json.Unmarshal([]byte(raw), &e) != nil {
			continue
		}
		// Reveal frozen ICPC submissions only after the admin triggers 解榜.
		if revealed {
			if icpc, ok := strategy.(*contest.ICPCStrategy); ok {
				for len(e.FrozenResults) > 0 {
					rev, _ := icpc.RevealNext(&e, meta.StartTime, meta.Settings)
					e = *rev
				}
			}
		}
		ec := e
		entries = append(entries, &ec)
	}

	rows := strategy.Rank(entries, meta.Settings)

	// Resolve labels + usernames.
	labels, err := rs.problems(ctx, contestID)
	if err != nil {
		return nil, fmt.Errorf("load contest problems: %w", err)
	}
	labelByPID := make(map[models.ID]string, len(labels))
	orderedLabels := make([]string, 0, len(labels))
	for _, pl := range labels {
		labelByPID[pl.ProblemID] = pl.Label
		orderedLabels = append(orderedLabels, pl.Label)
	}

	userIDs := make([]models.ID, 0, len(rows))
	for _, r := range rows {
		userIDs = append(userIDs, r.UserID)
	}
	userInfo, err := rs.users(ctx, userIDs)
	if err != nil {
		return nil, fmt.Errorf("load usernames: %w", err)
	}

	contestants := make([]BoardRow, 0, len(rows))
	for _, r := range rows {
		cells := make(map[string]BoardCell, len(r.Entries))
		solved := 0
		for pid, e := range r.Entries {
			label, ok := labelByPID[pid]
			if !ok {
				continue // problem removed from contest
			}
			if e.Accepted {
				solved++
			}
			cells[label] = BoardCell{
				Solved: e.Accepted,
				// Attempts is the count of REJECTED submissions (penalty count),
				// not total tries — a first-try AC has 0, so the board shows no "+N".
				Attempts:   e.WrongAttemptCount,
				Pending:    e.FrozenAttempts,
				Penalty:    e.Penalty,
				Score:      e.DisplayScore,
				FirstBlood: e.IsFirstBlood,
			}
		}
		info := userInfo[r.UserID]
		contestants = append(contestants, BoardRow{
			Rank:         r.Rank,
			UserID:       r.UserID,
			Username:     info.Username,
			Organization: info.Organization,
			Problems:     cells,
			TotalSolved:  solved,
			TotalPenalty: r.TotalPenalty,
			TotalScore:   r.TotalScore,
		})
	}
	// rows are already ranked; keep that order stable.
	sort.SliceStable(contestants, func(i, j int) bool {
		return contestants[i].Rank < contestants[j].Rank
	})

	snapshot := &BoardSnapshot{
		ContestID:   contestID,
		ContestType: meta.ContestType,
		Frozen:      frozen,
		Problems:    orderedLabels,
		Contestants: contestants,
		UpdatedAt:   now,
	}

	payload, _ := json.Marshal(snapshot)
	if err := rs.rdb.Set(ctx, redisKey(contestID, keySnapshot), payload, 0).Err(); err != nil {
		return nil, fmt.Errorf("store snapshot: %w", err)
	}
	return snapshot, nil
}

// publishSnapshot pushes the full board as a typed WS frame to the contest's
// Pub/Sub channel. The Hub forwards it verbatim to connected clients.
func (rs *RankingService) publishSnapshot(ctx context.Context, contestID models.ID, snap *BoardSnapshot) error {
	frame, err := json.Marshal(map[string]any{
		"type": EventSnapshot,
		"data": snap,
	})
	if err != nil {
		return err
	}
	return rs.rdb.Publish(ctx, redisKey(contestID, keyEvents), frame).Err()
}

// ─── Contest metadata cache ───────────────────────────────────────────────────

type cachedMeta struct {
	meta      *ContestMeta
	expiresAt time.Time
}

type contestMetaCache struct {
	mu     sync.Mutex
	loader ContestMetaLoader
	ttl    time.Duration
	data   map[models.ID]*cachedMeta
}

func (c *contestMetaCache) get(ctx context.Context, id models.ID) (*ContestMeta, error) {
	c.mu.Lock()
	if cm, ok := c.data[id]; ok && time.Now().Before(cm.expiresAt) {
		c.mu.Unlock()
		return cm.meta, nil
	}
	c.mu.Unlock()

	meta, err := c.loader(ctx, id)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.data[id] = &cachedMeta{meta: meta, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
	return meta, nil
}
