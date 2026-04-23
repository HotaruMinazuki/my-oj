package ranking

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/models"
)

// WebSocket timing constants.
const (
	writeWait      = 10 * time.Second  // deadline for a single Write call
	pongWait       = 60 * time.Second  // deadline to receive a Pong from client
	pingPeriod     = 45 * time.Second  // how often to send a Ping (< pongWait)
	maxMessageSize = 4 * 1024          // max bytes read from client (control frames only)
	sendBufSize    = 64                // per-client send channel depth
)

// ─── Hub ──────────────────────────────────────────────────────────────────────

// Hub manages all WebSocket connections across all contests.
//
// Architecture:
//   - One Hub instance per API server process.
//   - Clients register with a contest ID; the Hub groups them into "rooms".
//   - A single PSUBSCRIBE oj:ranking:*:events goroutine receives ALL contest
//     deltas from Redis and fans them out to the correct room.
//   - All room mutations (register / unregister / broadcast) go through a single
//     goroutine (Hub.Run) to avoid lock contention.
//
// Performance characteristics:
//   - Broadcast is O(clients_in_room); no global lock during fan-out.
//   - Delta payload ≈ 200 bytes — 1000 connected clients → 200 KB per event.
//   - Full snapshot sent only once per client on connect.
type Hub struct {
	rdb      *redis.Client
	log      *zap.Logger
	upgrader websocket.Upgrader

	// rooms[contestID] is the set of clients subscribed to that contest.
	// Only the Hub.Run goroutine reads/writes this map.
	rooms map[models.ID]map[*Client]struct{}

	// Internal channels — all mutations go through these to avoid locking.
	register   chan *registerReq
	unregister chan *Client
	// broadcast carries pre-serialised delta payloads for one contest.
	broadcast chan *broadcastMsg
}

type registerReq struct {
	client    *Client
	contestID models.ID
	// snapshotCh receives the initial snapshot bytes; closed after delivery.
	snapshotCh chan []byte
}

type broadcastMsg struct {
	contestID models.ID
	payload   []byte // pre-serialised JSON
}

// NewHub creates a Hub and starts the Redis Pub/Sub subscriber.
func NewHub(rdb *redis.Client, log *zap.Logger) *Hub {
	return &Hub{
		rdb: rdb,
		log: log,
		upgrader: websocket.Upgrader{
			// Allow all origins in development.  Restrict in production.
			CheckOrigin:     func(r *http.Request) bool { return true },
			ReadBufferSize:  1024,
			WriteBufferSize: 4 * 1024,
		},
		rooms:      make(map[models.ID]map[*Client]struct{}),
		register:   make(chan *registerReq, 256),
		unregister: make(chan *Client, 256),
		broadcast:  make(chan *broadcastMsg, 2048),
	}
}

// Run is the Hub's main goroutine.  It MUST be called once before ServeWS.
// Blocks until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	// Start the Redis Pub/Sub subscriber in a separate goroutine.
	// It uses a pattern subscription to catch all contest channels.
	go h.redisPubSubLoop(ctx)

	for {
		select {
		case <-ctx.Done():
			h.shutdownAllClients()
			return

		case req := <-h.register:
			h.handleRegister(ctx, req)

		case client := <-h.unregister:
			h.handleUnregister(client)

		case msg := <-h.broadcast:
			h.handleBroadcast(msg)
		}
	}
}

// ServeWS upgrades an HTTP connection to WebSocket and registers the client.
// Intended to be called from an HTTP handler:
//
//	func rankingHandler(w http.ResponseWriter, r *http.Request) {
//	    contestID := parseContestID(r)
//	    hub.ServeWS(w, r, contestID)
//	}
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, contestID models.ID) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Warn("websocket upgrade failed", zap.Error(err))
		return
	}

	client := &Client{
		hub:       h,
		contestID: contestID,
		conn:      conn,
		// Buffered send channel: if the client can't keep up, we drop messages
		// rather than blocking the Hub's broadcast loop.
		send: make(chan []byte, sendBufSize),
	}

	// Request registration and wait for the initial snapshot.
	snapshotCh := make(chan []byte, 1)
	h.register <- &registerReq{client: client, contestID: contestID, snapshotCh: snapshotCh}

	// Block until Hub delivers the snapshot (fast: just a Redis GET).
	if snapshot, ok := <-snapshotCh; ok && len(snapshot) > 0 {
		client.send <- snapshot
	}

	// Launch I/O pumps.  Both goroutines call client.hub.unregister on exit.
	go client.writePump()
	go client.readPump()
}

// ─── Hub internal handlers (all called from the Run goroutine) ───────────────

func (h *Hub) handleRegister(ctx context.Context, req *registerReq) {
	room, ok := h.rooms[req.contestID]
	if !ok {
		room = make(map[*Client]struct{})
		h.rooms[req.contestID] = room
	}
	room[req.client] = struct{}{}

	h.log.Debug("client registered",
		zap.Int64("contest_id", req.contestID),
		zap.Int("room_size", len(room)),
	)

	// Fetch and send the current full snapshot asynchronously so the Hub
	// goroutine is not blocked on a Redis call.
	go func() {
		defer close(req.snapshotCh)
		raw, err := h.rdb.Get(ctx, redisKey(req.contestID, keySnapshot)).Bytes()
		if err != nil || len(raw) == 0 {
			return
		}
		// Wrap snapshot in a typed envelope so the client can distinguish it
		// from delta messages.
		envelope := map[string]any{
			"type": EventSnapshot,
			"data": json.RawMessage(raw),
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			return
		}
		req.snapshotCh <- payload
	}()
}

func (h *Hub) handleUnregister(client *Client) {
	room, ok := h.rooms[client.contestID]
	if !ok {
		return
	}
	if _, exists := room[client]; !exists {
		return
	}
	delete(room, client)
	close(client.send) // signals writePump to exit

	if len(room) == 0 {
		delete(h.rooms, client.contestID)
	}

	h.log.Debug("client unregistered",
		zap.Int64("contest_id", client.contestID),
		zap.Int("room_size", len(room)),
	)
}

// handleBroadcast fans out a pre-serialised payload to every client in a room.
// Clients with a full send buffer receive a "room_full" log and are dropped;
// they'll reconnect and get a fresh snapshot.
func (h *Hub) handleBroadcast(msg *broadcastMsg) {
	room, ok := h.rooms[msg.contestID]
	if !ok || len(room) == 0 {
		return
	}
	for client := range room {
		select {
		case client.send <- msg.payload:
		default:
			// Client is too slow; evict it.  It can reconnect and receive a snapshot.
			h.log.Warn("client send buffer full; evicting",
				zap.Int64("contest_id", msg.contestID))
			delete(room, client)
			close(client.send)
		}
	}
}

func (h *Hub) shutdownAllClients() {
	for _, room := range h.rooms {
		for client := range room {
			close(client.send)
		}
	}
	h.rooms = make(map[models.ID]map[*Client]struct{})
}

// ─── Redis Pub/Sub subscriber ─────────────────────────────────────────────────

// redisPubSubLoop subscribes to oj:ranking:*:events with PSUBSCRIBE and forwards
// all messages to the Hub's broadcast channel.
//
// It uses a pattern subscription so a single Redis connection handles all contests.
// The channel name encodes the contest ID: "oj:ranking:{contestID}:events".
func (h *Hub) redisPubSubLoop(ctx context.Context) {
	const pattern = "oj:ranking:*:events"

	for {
		if ctx.Err() != nil {
			return
		}
		if err := h.runPubSubOnce(ctx, pattern); err != nil {
			h.log.Error("pubsub loop error; reconnecting in 2s", zap.Error(err))
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (h *Hub) runPubSubOnce(ctx context.Context, pattern string) error {
	sub := h.rdb.PSubscribe(ctx, pattern)
	defer sub.Close()

	// Verify subscription.
	if _, err := sub.Receive(ctx); err != nil {
		return fmt.Errorf("PSubscribe: %w", err)
	}

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return fmt.Errorf("pubsub channel closed")
			}
			h.dispatchPubSubMsg(msg)
		}
	}
}

// dispatchPubSubMsg parses the channel name to extract the contest ID, then
// wraps the delta payload in a typed envelope and queues it for broadcast.
//
// Channel name format: "oj:ranking:{contestID}:events"
func (h *Hub) dispatchPubSubMsg(msg *redis.Message) {
	// Parse contestID from channel name.
	// "oj:ranking:{contestID}:events" → split by ":" → index 2
	var contestID models.ID
	if _, err := fmt.Sscanf(msg.Channel, "oj:ranking:%d:events", &contestID); err != nil {
		h.log.Warn("malformed ranking channel name", zap.String("channel", msg.Channel))
		return
	}

	// Wrap the raw delta in a typed envelope:
	// {"type": "delta", "data": {…RankDelta…}}
	envelope, err := json.Marshal(map[string]any{
		"type": EventSubmission, // refined inside data
		"data": json.RawMessage(msg.Payload),
	})
	if err != nil {
		return
	}

	// Non-blocking send to broadcast channel.
	select {
	case h.broadcast <- &broadcastMsg{contestID: contestID, payload: envelope}:
	default:
		h.log.Warn("hub broadcast channel full; delta dropped",
			zap.Int64("contest_id", contestID))
	}
}

// ─── Client ───────────────────────────────────────────────────────────────────

// Client represents one WebSocket connection.
type Client struct {
	hub       *Hub
	contestID models.ID
	conn      *websocket.Conn
	// send is the outbound queue; writePump drains it to the WebSocket.
	send chan []byte
}

// writePump drains the send channel to the WebSocket connection.
//
// One goroutine per client.  Exits when:
//   - The send channel is closed (client unregistered by Hub).
//   - A write to the WebSocket fails (connection broken).
//   - A Ping cannot be written within writeWait.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel → send a clean close frame.
				c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(
					websocket.CloseNormalClosure, "server shutdown"))
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}

			// Flush any queued messages in the same write frame to batch small deltas.
			// This reduces syscall overhead when a burst of results arrives.
		drain:
			for {
				select {
				case extra, ok := <-c.send:
					if !ok {
						break drain
					}
					if _, err := w.Write(extra); err != nil {
						return
					}
				default:
					break drain
				}
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump handles the read side of the WebSocket connection.
//
// For ranking clients, the client is mostly passive (receives deltas, sends nothing).
// readPump exists to:
//   1. Detect disconnections (Read returns an error).
//   2. Handle Pong frames (extend the read deadline, keeping the connection alive).
//   3. Prevent goroutine leaks if the client sends unexpected messages.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		// Reset the read deadline on every Pong.  If no Pong arrives within
		// pongWait, the next ReadMessage times out and we detect the dead connection.
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		// We don't expect messages from the client, but we must call ReadMessage
		// to drive the Pong handler and detect disconnections.
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			// websocket.IsUnexpectedCloseError filters normal closes (browser tab closed).
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
				websocket.CloseAbnormalClosure,
			) {
				c.hub.log.Debug("websocket read error", zap.Error(err))
			}
			return
		}
	}
}

// ─── Concurrent safety note ───────────────────────────────────────────────────
//
// The Hub.rooms map is ONLY accessed from Hub.Run (the single dispatch goroutine).
// Client.send is written by Hub.Run and read by the client's writePump goroutine;
// the channel itself provides the required synchronisation.
// No explicit mutex is needed.
