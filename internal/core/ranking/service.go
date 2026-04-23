package ranking

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/core/contest"
	"github.com/your-org/my-oj/internal/models"
	"github.com/your-org/my-oj/internal/mq"
)

// RankingService subscribes to judge results and maintains the live scoreboard
// in Redis, publishing incremental deltas for WebSocket clients.
//
// It runs as a separate goroutine alongside the API server.  It uses its own
// consumer group ("ranker") on QueueJudgeResults so it receives every result
// independently of the API server's consumer group.
type RankingService struct {
	consumer mq.Consumer
	rdb      *redis.Client
	registry *contest.Registry
	log      *zap.Logger
	// contestCache caches contest metadata (settings, freeze time) to avoid
	// hitting the DB on every submission.  Entries are short-lived (30 s TTL).
	contestCache *contestMetaCache
}

// ContestMetaLoader is called by the service to fetch a contest's metadata.
// Typically wraps a DB query; inject a stub in tests.
type ContestMetaLoader func(ctx context.Context, contestID models.ID) (*ContestMeta, error)

// ContestMeta is the subset of Contest data the ranking service needs.
type ContestMeta struct {
	ContestType  models.ContestType
	Settings     models.ContestSettings
	StartTime    time.Time
	FreezeTime   *time.Time
	EndTime      time.Time
}

// NewRankingService creates a RankingService.
func NewRankingService(
	consumer mq.Consumer,
	rdb *redis.Client,
	registry *contest.Registry,
	loader ContestMetaLoader,
	log *zap.Logger,
) *RankingService {
	return &RankingService{
		consumer: consumer,
		rdb:      rdb,
		registry: registry,
		log:      log,
		contestCache: &contestMetaCache{
			loader: loader,
			ttl:    30 * time.Second,
			data:   make(map[models.ID]*cachedMeta),
		},
	}
}

// Run starts the service loop.  Blocks until ctx is cancelled.
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

// processContestResult is the core ranking update logic for one contest submission.
func (rs *RankingService) processContestResult(ctx context.Context, result *mq.ResultMessage) error {
	contestID := *result.ContestID
	log := rs.log.With(
		zap.Int64("contest_id", contestID),
		zap.Int64("submission_id", result.SubmissionID),
	)

	// ── Load contest metadata ─────────────────────────────────────────────────
	meta, err := rs.contestCache.get(ctx, contestID)
	if err != nil {
		return fmt.Errorf("ranking: load contest %d: %w", contestID, err)
	}

	strategy, err := rs.registry.Get(meta.ContestType)
	if err != nil {
		log.Warn("unknown contest type; skipping ranking update", zap.String("type", string(meta.ContestType)))
		return nil
	}

	// We need the problemID from the submission; it's not in ResultMessage directly.
	// Pull it from the TestCaseResults (first entry) or from the task.
	// Simpler: the API server should include ProblemID in ResultMessage.
	// For now, derive it from the task_id embedded in the result.
	// (In production, add ProblemID to ResultMessage — noted as a TODO.)
	problemID, userID, err := rs.resolveIDs(ctx, contestID, result)
	if err != nil {
		return fmt.Errorf("ranking: resolve IDs for submission %d: %w", result.SubmissionID, err)
	}

	// ── Load current ScoreEntry from Redis ────────────────────────────────────
	field := entryField(userID, problemID)
	rawEntry, err := rs.rdb.HGet(ctx, redisKey(contestID, keyEntries), field).Bytes()
	var prev *contest.ScoreEntry
	if err == nil {
		var e contest.ScoreEntry
		if err := json.Unmarshal(rawEntry, &e); err == nil {
			prev = &e
		}
	}

	oldStatus := entryStatus(toView(prev))

	// ── Apply strategy ────────────────────────────────────────────────────────
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

	// ── Check first blood ─────────────────────────────────────────────────────
	isFirstBlood := false
	if newEntry.Accepted && (prev == nil || !prev.Accepted) {
		// Attempt to set first blood atomically.
		fbKey := redisKey(contestID, keyFirstBlood)
		set, err := rs.rdb.HSetNX(ctx, fbKey, fmt.Sprintf("%d", problemID), "1").Result()
		if err == nil && set {
			newEntry.IsFirstBlood = true
			isFirstBlood = true
		}
	}

	// ── Persist new ScoreEntry ────────────────────────────────────────────────
	entryJSON, err := json.Marshal(newEntry)
	if err != nil {
		return fmt.Errorf("ranking: marshal entry: %w", err)
	}

	// ── Update UserAggregate ──────────────────────────────────────────────────
	aggKey := redisKey(contestID, keyAggregates)
	rawAgg, _ := rs.rdb.HGet(ctx, aggKey, fmt.Sprintf("%d", userID)).Bytes()
	var agg UserAggregate
	_ = json.Unmarshal(rawAgg, &agg)
	agg.UserID = userID

	// Recompute aggregate from scratch by reading all entries for this user.
	// This is O(problems) per update — fine for up to ~30 problems per contest.
	agg, err = rs.recomputeAggregate(ctx, contestID, userID, newEntry, prev, agg)
	if err != nil {
		return fmt.Errorf("ranking: recompute aggregate: %w", err)
	}

	aggJSON, _ := json.Marshal(agg)

	// ── Get old rank before update ────────────────────────────────────────────
	oldRank := rs.getUserRank(ctx, contestID, userID)

	// ── Atomic Redis pipeline ─────────────────────────────────────────────────
	pipe := rs.rdb.Pipeline()
	pipe.HSet(ctx, redisKey(contestID, keyEntries), field, entryJSON)
	pipe.HSet(ctx, aggKey, fmt.Sprintf("%d", userID), aggJSON)
	pipe.ZAdd(ctx, redisKey(contestID, keyBoard), redis.Z{
		Score:  boardScore(agg.Solved, agg.PenaltyMins),
		Member: fmt.Sprintf("%d", userID),
	})
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("ranking: redis pipeline: %w", err)
	}

	// ── Get new rank after update ─────────────────────────────────────────────
	newRank := rs.getUserRank(ctx, contestID, userID)

	// ── Build and publish delta ───────────────────────────────────────────────
	delta := &RankDelta{
		Type:       EventSubmission,
		ContestID:  contestID,
		Timestamp:  time.Now().UTC(),
		UserID:     userID,
		ProblemID:  problemID,
		OldStatus:  oldStatus,
		NewStatus:  entryStatus(toView(newEntry)),
		NewSolved:  agg.Solved,
		NewPenalty: agg.PenaltyMins,
		OldRank:    oldRank,
		NewRank:    newRank,
	}
	if isFirstBlood {
		delta.Type = EventFirstBlood
	}

	if err := rs.publishDelta(ctx, contestID, delta); err != nil {
		log.Warn("failed to publish ranking delta", zap.Error(err))
		// Non-fatal: the scoreboard will be eventually consistent on next refresh.
	}

	// ── Update full snapshot asynchronously ──────────────────────────────────
	go func() {
		if err := rs.rebuildSnapshot(context.Background(), contestID); err != nil {
			log.Warn("snapshot rebuild failed", zap.Error(err))
		}
	}()

	return nil
}

// UnfreezeNext reveals the earliest frozen submission for the lowest-ranked
// team with pending results.  Called by the organizer's 滚榜 API endpoint.
//
// Returns the delta that was published, or nil if nothing was revealed.
func (rs *RankingService) UnfreezeNext(ctx context.Context, contestID models.ID) (*RankDelta, error) {
	meta, err := rs.contestCache.get(ctx, contestID)
	if err != nil {
		return nil, err
	}
	strategy, err := rs.registry.Get(meta.ContestType)
	if err != nil {
		return nil, err
	}
	icpcStrategy, ok := strategy.(*contest.ICPCStrategy)
	if !ok {
		return nil, fmt.Errorf("UnfreezeNext: only supported for ICPC contests")
	}

	// ── Collect all pending entries ───────────────────────────────────────────
	allEntries, err := rs.rdb.HGetAll(ctx, redisKey(contestID, keyEntries)).Result()
	if err != nil {
		return nil, fmt.Errorf("UnfreezeNext: load entries: %w", err)
	}

	type pendingEntry struct {
		userID    models.ID
		problemID models.ID
		entry     *contest.ScoreEntry
		userRank  int
	}

	var pending []pendingEntry
	for field, raw := range allEntries {
		var e contest.ScoreEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue
		}
		if !e.IsPending || len(e.FrozenResults) == 0 {
			continue
		}
		uid, pid := parseEntryField(field)
		rank := rs.getUserRank(ctx, contestID, uid)
		pending = append(pending, pendingEntry{uid, pid, &e, rank})
	}

	if len(pending) == 0 {
		return nil, nil // nothing left to reveal
	}

	// ── 滚榜 order: reveal lowest-ranked team first ───────────────────────────
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].userRank > pending[j].userRank // higher rank number = worse rank
	})
	target := pending[0]

	// ── Reveal the earliest frozen result ────────────────────────────────────
	// Use the current time as the frozen submission time (approximation).
	// In production, store the actual submission timestamp with the frozen result.
	frozenTime := time.Now().UTC()
	updated, accepted := icpcStrategy.RevealNext(target.entry, meta.StartTime, frozenTime, meta.Settings)

	// ── Persist ───────────────────────────────────────────────────────────────
	oldStatus := entryStatus(toView(target.entry))
	entryJSON, _ := json.Marshal(updated)
	field := entryField(target.userID, target.problemID)
	if err := rs.rdb.HSet(ctx, redisKey(contestID, keyEntries), field, entryJSON).Err(); err != nil {
		return nil, fmt.Errorf("UnfreezeNext: persist entry: %w", err)
	}

	if accepted {
		agg, _ := rs.recomputeAggregate(ctx, contestID, target.userID, updated, target.entry, UserAggregate{})
		aggJSON, _ := json.Marshal(agg)
		rs.rdb.HSet(ctx, redisKey(contestID, keyAggregates), fmt.Sprintf("%d", target.userID), aggJSON)
		rs.rdb.ZAdd(ctx, redisKey(contestID, keyBoard), redis.Z{
			Score:  boardScore(agg.Solved, agg.PenaltyMins),
			Member: fmt.Sprintf("%d", target.userID),
		})
	}

	newRank := rs.getUserRank(ctx, contestID, target.userID)
	delta := &RankDelta{
		Type:      EventUnfreeze,
		ContestID: contestID,
		Timestamp: time.Now().UTC(),
		UserID:    target.userID,
		ProblemID: target.problemID,
		OldStatus: oldStatus,
		NewStatus: entryStatus(toView(updated)),
		OldRank:   target.userRank,
		NewRank:   newRank,
	}

	_ = rs.publishDelta(ctx, contestID, delta)
	go rs.rebuildSnapshot(context.Background(), contestID) //nolint:errcheck
	return delta, nil
}

// ─── Redis helpers ────────────────────────────────────────────────────────────

// getUserRank returns 1-based rank from the Redis sorted set.
// Returns 0 if the user is not on the board.
func (rs *RankingService) getUserRank(ctx context.Context, contestID, userID models.ID) int {
	// ZREVRANK: 0-indexed rank in descending order (highest score = rank 0).
	rank, err := rs.rdb.ZRevRank(ctx, redisKey(contestID, keyBoard),
		fmt.Sprintf("%d", userID)).Result()
	if err != nil {
		return 0
	}
	return int(rank) + 1
}

// recomputeAggregate rebuilds a user's aggregate stats by scanning all their
// problem entries.  Updates are small (≤30 problems) so this is fast.
func (rs *RankingService) recomputeAggregate(
	ctx context.Context,
	contestID, userID models.ID,
	latestEntry *contest.ScoreEntry,
	_ *contest.ScoreEntry,
	_ UserAggregate,
) (UserAggregate, error) {
	all, err := rs.rdb.HGetAll(ctx, redisKey(contestID, keyEntries)).Result()
	if err != nil {
		return UserAggregate{}, err
	}

	agg := UserAggregate{UserID: userID}
	prefix := fmt.Sprintf("%d:", userID)
	for field, raw := range all {
		if !strings.HasPrefix(field, prefix) {
			continue
		}
		var e contest.ScoreEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue
		}
		if e.UserID != userID {
			continue
		}
		// Overlay the latest entry (not yet persisted when this runs).
		if e.ProblemID == latestEntry.ProblemID {
			e = *latestEntry
		}
		if e.Accepted {
			agg.Solved++
			agg.PenaltyMins += e.Penalty
			if e.BestSubmitTime.After(agg.LastACTime) {
				agg.LastACTime = e.BestSubmitTime
			}
		}
	}
	return agg, nil
}

// publishDelta serialises a RankDelta and publishes it to the contest's
// Redis Pub/Sub channel.  The Hub picks this up and forwards to WebSocket clients.
func (rs *RankingService) publishDelta(ctx context.Context, contestID models.ID, d *RankDelta) error {
	payload, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return rs.rdb.Publish(ctx, redisKey(contestID, keyEvents), payload).Err()
}

// rebuildSnapshot recomputes the full RankSnapshot and stores it in Redis.
// Called asynchronously after each scoreboard update.
func (rs *RankingService) rebuildSnapshot(ctx context.Context, contestID models.ID) error {
	allEntries, err := rs.rdb.HGetAll(ctx, redisKey(contestID, keyEntries)).Result()
	if err != nil {
		return err
	}

	// Parse all entries.
	var entries []*contest.ScoreEntry
	for _, raw := range allEntries {
		var e contest.ScoreEntry
		if json.Unmarshal([]byte(raw), &e) == nil {
			entries = append(entries, &e)
		}
	}

	// Load strategy and rank.
	meta, err := rs.contestCache.get(ctx, contestID)
	if err != nil {
		return err
	}
	strategy, err := rs.registry.Get(meta.ContestType)
	if err != nil {
		return err
	}

	rows := strategy.Rank(entries, meta.Settings)

	// Convert to view (strip frozen internals).
	viewRows := make([]RankRowView, len(rows))
	for i, r := range rows {
		vr := RankRowView{
			Rank:         r.Rank,
			UserID:       r.UserID,
			TotalScore:   r.TotalScore,
			TotalPenalty: r.TotalPenalty,
			Entries:      make(map[models.ID]*ScoreEntryView, len(r.Entries)),
		}
		for pid, e := range r.Entries {
			vr.Entries[pid] = toView(e)
		}
		viewRows[i] = vr
	}

	snapshot := RankSnapshot{
		ContestID: contestID,
		Rows:      viewRows,
		UpdatedAt: time.Now().UTC(),
	}
	payload, _ := json.Marshal(snapshot)
	return rs.rdb.Set(ctx, redisKey(contestID, keySnapshot), payload, 0).Err()
}

// resolveIDs extracts UserID and ProblemID from the result message.
// Both fields are denormalised into ResultMessage by the judger/API server.
func (rs *RankingService) resolveIDs(_ context.Context, _ models.ID, result *mq.ResultMessage) (problemID, userID models.ID, err error) {
	if result.UserID == 0 || result.ProblemID == 0 {
		return 0, 0, fmt.Errorf("ranking: missing user_id/problem_id in result for submission %d", result.SubmissionID)
	}
	return result.ProblemID, result.UserID, nil
}

// toView strips internal freeze fields from a ScoreEntry for public consumption.
func toView(e *contest.ScoreEntry) *ScoreEntryView {
	if e == nil {
		return nil
	}
	return &ScoreEntryView{
		Accepted:          e.Accepted,
		DisplayScore:      e.DisplayScore,
		Penalty:           e.Penalty,
		AttemptCount:      e.AttemptCount,
		WrongAttemptCount: e.WrongAttemptCount,
		BestSubmitTime:    e.BestSubmitTime,
		IsPending:         e.IsPending,
		FrozenAttempts:    e.FrozenAttempts,
		IsFirstBlood:      e.IsFirstBlood,
	}
}

func parseEntryField(field string) (userID, problemID models.ID) {
	parts := strings.SplitN(field, ":", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	u, _ := strconv.ParseInt(parts[0], 10, 64)
	p, _ := strconv.ParseInt(parts[1], 10, 64)
	return models.ID(u), models.ID(p)
}

// ─── Contest metadata cache ───────────────────────────────────────────────────

type cachedMeta struct {
	meta      *ContestMeta
	expiresAt time.Time
}

type contestMetaCache struct {
	loader ContestMetaLoader
	ttl    time.Duration
	data   map[models.ID]*cachedMeta
}

func (c *contestMetaCache) get(ctx context.Context, id models.ID) (*ContestMeta, error) {
	if cm, ok := c.data[id]; ok && time.Now().Before(cm.expiresAt) {
		return cm.meta, nil
	}
	meta, err := c.loader(ctx, id)
	if err != nil {
		return nil, err
	}
	c.data[id] = &cachedMeta{meta: meta, expiresAt: time.Now().Add(c.ttl)}
	return meta, nil
}
