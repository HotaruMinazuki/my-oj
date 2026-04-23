package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/core/ranking"
	"github.com/your-org/my-oj/internal/models"
)

// RankingHandler serves the live scoreboard over WebSocket and as a REST snapshot.
type RankingHandler struct {
	hub *ranking.Hub
	rdb *redis.Client
	log *zap.Logger
}

func NewRankingHandler(hub *ranking.Hub, rdb *redis.Client, log *zap.Logger) *RankingHandler {
	return &RankingHandler{hub: hub, rdb: rdb, log: log}
}

// ServeWS upgrades the connection to WebSocket and streams incremental rank deltas.
//
//	GET /ws/ranking/:contest_id
//
// The client receives an initial {"type":"snapshot","data":{…}} frame followed by
// {"type":"submission"|"firstblood"|"unfreeze","data":{…RankDelta…}} frames.
func (h *RankingHandler) ServeWS(c *gin.Context) {
	contestID, ok := parseContestID(c)
	if !ok {
		return
	}
	h.hub.ServeWS(c.Writer, c.Request, contestID)
}

// GetSnapshot returns the current full scoreboard as JSON.
//
//	GET /api/v1/contests/:contest_id/ranking
//
// This hits Redis directly (O(1) key read) so it is safe under high traffic.
func (h *RankingHandler) GetSnapshot(c *gin.Context) {
	contestID, ok := parseContestID(c)
	if !ok {
		return
	}

	key := rankingSnapshotKey(contestID)
	raw, err := h.rdb.Get(c.Request.Context(), key).Bytes()
	if err == redis.Nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ranking not yet available"})
		return
	}
	if err != nil {
		h.log.Error("failed to fetch ranking snapshot", zap.Error(err), zap.Int64("contest_id", contestID))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// raw is already valid JSON — forward without re-marshalling.
	c.Data(http.StatusOK, "application/json; charset=utf-8", raw)
}

// GetUserRank returns the authenticated user's current rank within a contest.
//
//	GET /api/v1/contests/:contest_id/ranking/me
func (h *RankingHandler) GetUserRank(c *gin.Context) {
	contestID, ok := parseContestID(c)
	if !ok {
		return
	}

	raw, err := h.rdb.Get(c.Request.Context(), rankingSnapshotKey(contestID)).Bytes()
	if err == redis.Nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "ranking not yet available"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	userIDVal, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	userID, _ := userIDVal.(models.ID)

	var snapshot ranking.RankSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "snapshot parse error"})
		return
	}

	for _, row := range snapshot.Rows {
		if row.UserID == userID {
			c.JSON(http.StatusOK, row)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "user not on board"})
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func parseContestID(c *gin.Context) (models.ID, bool) {
	id, err := strconv.ParseInt(c.Param("contest_id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contest_id"})
		return 0, false
	}
	return models.ID(id), true
}

// rankingSnapshotKey mirrors the key produced by ranking.redisKey — kept local
// to avoid importing the internal ranking package's unexported helper.
func rankingSnapshotKey(contestID models.ID) string {
	return "oj:ranking:" + strconv.FormatInt(int64(contestID), 10) + ":snapshot"
}
