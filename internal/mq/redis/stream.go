// Package mqredis implements mq.Publisher and mq.Consumer on top of Redis Streams.
//
// Reliability guarantees:
//   - XREADGROUP + consumer groups: each message is delivered to exactly one judger node.
//   - PEL (Pending Entry List) recovery: messages not ACK'd within reclaimAfter are
//     re-delivered on the next Subscribe call, handling hard judger crashes.
//   - XACK on handler success: at-least-once delivery.
package mqredis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/your-org/my-oj/internal/mq"
)

const (
	blockDuration = 5 * time.Second  // XREADGROUP BLOCK timeout; prevents busy-waiting
	reclaimAfter  = 60 * time.Second // reclaim pending messages older than this on startup
	pendingBatch  = 64               // messages to scan per PEL drain iteration
)

// Config holds connection and identity settings for a Stream instance.
type Config struct {
	Addr     string
	Password string
	DB       int
	// ConsumerGroup is shared by all judger nodes; Redis delivers each message to one member.
	ConsumerGroup string
	// ConsumerName must be unique per judger node instance (e.g., "hostname-pid").
	ConsumerName string
}

// Stream implements both mq.Publisher and mq.Consumer.
// A single Stream instance may be used for both roles simultaneously.
type Stream struct {
	client        *redis.Client
	consumerGroup string
	consumerName  string
	log           *zap.Logger
}

// New creates a Stream and verifies the Redis connection.
func New(cfg Config, log *zap.Logger) (*Stream, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
		// Keep connections alive to reduce reconnect latency under burst load.
		PoolSize:    16,
		MinIdleConns: 4,
	})

	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("mqredis: ping %s: %w", cfg.Addr, err)
	}

	return &Stream{
		client:        rdb,
		consumerGroup: cfg.ConsumerGroup,
		consumerName:  cfg.ConsumerName,
		log:           log,
	}, nil
}

// ─── Publisher ────────────────────────────────────────────────────────────────

// Publish appends payload to the named Redis Stream (XADD).
// Redis assigns the stream entry ID, which is returned as the message ID.
func (s *Stream) Publish(ctx context.Context, queue string, payload []byte) (string, error) {
	id, err := s.client.XAdd(ctx, &redis.XAddArgs{
		Stream: queue,
		// "*" lets Redis auto-generate a monotonic ID (timestamp-sequence).
		ID:     "*",
		Values: map[string]any{"data": string(payload)},
	}).Result()
	if err != nil {
		return "", fmt.Errorf("mqredis: XADD %s: %w", queue, err)
	}
	return id, nil
}

// ─── Consumer ─────────────────────────────────────────────────────────────────

// Subscribe creates the consumer group (if absent), drains our own PEL from any
// prior run, then enters the main XREADGROUP loop until ctx is cancelled.
func (s *Stream) Subscribe(ctx context.Context, queue string, handler mq.MessageHandler) error {
	if err := s.ensureGroup(ctx, queue); err != nil {
		return err
	}

	// On restart: drain our own pending list before reading new messages.
	// This re-processes tasks that were picked up but not ACK'd before a crash.
	if err := s.drainPending(ctx, queue, handler); err != nil {
		s.log.Warn("PEL drain encountered errors; some messages may be reprocessed",
			zap.String("queue", queue), zap.Error(err))
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		streams, err := s.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    s.consumerGroup,
			Consumer: s.consumerName,
			Streams:  []string{queue, ">"},
			Count:    1,              // one message per worker pull keeps flow control simple
			Block:    blockDuration,
		}).Result()

		if err != nil {
			// redis.Nil means BLOCK timeout with no messages; not an error.
			if err == redis.Nil {
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.Error("XREADGROUP failed", zap.String("queue", queue), zap.Error(err))
			// Brief back-off to avoid hammering a temporarily unavailable Redis.
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				s.dispatch(ctx, queue, msg, handler)
			}
		}
	}
}

// dispatch delivers one stream entry to the handler and ACKs on success.
func (s *Stream) dispatch(ctx context.Context, queue string, msg redis.XMessage, handler mq.MessageHandler) {
	data, _ := msg.Values["data"].(string)
	hmsg := mq.Message{ID: msg.ID, Payload: []byte(data)}

	if err := handler(ctx, hmsg); err != nil {
		// Leave in PEL; drainPending on next startup will re-deliver.
		s.log.Error("handler returned error; message stays in PEL",
			zap.String("id", msg.ID), zap.Error(err))
		return
	}

	// ACK after confirmed processing.
	if err := s.client.XAck(ctx, queue, s.consumerGroup, msg.ID).Err(); err != nil {
		// Not fatal — the message will be re-delivered but the handler must be idempotent.
		s.log.Error("XACK failed; potential duplicate delivery",
			zap.String("id", msg.ID), zap.Error(err))
	}
}

// ensureGroup creates the consumer group idempotently.
// Start ID "0" means re-deliver all existing stream entries on first creation,
// giving new judger nodes the full pending backlog.
func (s *Stream) ensureGroup(ctx context.Context, queue string) error {
	err := s.client.XGroupCreateMkStream(ctx, queue, s.consumerGroup, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("mqredis: XGROUP CREATE %s/%s: %w", queue, s.consumerGroup, err)
	}
	return nil
}

// drainPending re-processes entries in our own PEL that were not ACK'd
// in a previous run (e.g., judger crash). Uses XCLAIM to take ownership.
func (s *Stream) drainPending(ctx context.Context, queue string, handler mq.MessageHandler) error {
	for {
		pending, err := s.client.XPendingExt(ctx, &redis.XPendingExtArgs{
			Stream:   queue,
			Group:    s.consumerGroup,
			Start:    "-",
			End:      "+",
			Count:    pendingBatch,
			Consumer: s.consumerName,
		}).Result()
		if err != nil {
			return fmt.Errorf("XPENDING: %w", err)
		}
		if len(pending) == 0 {
			return nil
		}

		ids := make([]string, len(pending))
		for i, p := range pending {
			ids[i] = p.ID
		}

		// Re-claim all our own pending messages (MinIdle=0 claims regardless of age).
		msgs, err := s.client.XClaim(ctx, &redis.XClaimArgs{
			Stream:   queue,
			Group:    s.consumerGroup,
			Consumer: s.consumerName,
			MinIdle:  0,
			Messages: ids,
		}).Result()
		if err != nil {
			return fmt.Errorf("XCLAIM: %w", err)
		}

		for _, msg := range msgs {
			s.dispatch(ctx, queue, msg, handler)
		}

		if len(pending) < pendingBatch {
			return nil // drained
		}
	}
}

// Close releases the underlying Redis connection pool.
func (s *Stream) Close() error { return s.client.Close() }
