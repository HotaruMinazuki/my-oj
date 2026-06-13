package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/models"
)

// rankingHub is the subset of *ranking.Hub the handler needs.
type rankingHub interface {
	ServeWS(w http.ResponseWriter, r *http.Request, contestID models.ID)
}

// RankingHandler serves the live scoreboard over WebSocket and as a REST snapshot.
type RankingHandler struct {
	hub rankingHub
	rdb *redis.Client
	log *zap.Logger
}

func NewRankingHandler(hub rankingHub, rdb *redis.Client, log *zap.Logger) *RankingHandler {
	return &RankingHandler{hub: hub, rdb: rdb, log: log}
}

// ServeWS upgrades the connection to WebSocket and streams board snapshots.
//
//	GET /ws/ranking/:contest_id
//
// The client receives a {"type":"snapshot","data":{…BoardSnapshot…}} frame on
// connect and again after every scoreboard change.
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
