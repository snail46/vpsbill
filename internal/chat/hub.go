// Package chat fans hosted-node chat messages out to open browser sockets.
// Messages are stored first; Postgres NOTIFY then wakes every API process so
// subscribers on any replica see them.
package chat

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"vpsbill/internal/store/postgres"
)

type Hub struct {
	db     *pgxpool.Pool
	store  *postgres.MarketplaceStore
	logger *slog.Logger

	mu    sync.Mutex
	rooms map[string]map[chan postgres.ChatMessage]struct{}
}

func NewHub(db *pgxpool.Pool, store *postgres.MarketplaceStore, logger *slog.Logger) *Hub {
	return &Hub{db: db, store: store, logger: logger, rooms: map[string]map[chan postgres.ChatMessage]struct{}{}}
}

// Subscribe returns a channel of new messages in a room and a function that
// ends the subscription. Slow subscribers miss messages rather than block
// the room; the browser reloads history when it reconnects.
func (h *Hub) Subscribe(nodeID string) (<-chan postgres.ChatMessage, func()) {
	channel := make(chan postgres.ChatMessage, 32)
	h.mu.Lock()
	if h.rooms[nodeID] == nil {
		h.rooms[nodeID] = map[chan postgres.ChatMessage]struct{}{}
	}
	h.rooms[nodeID][channel] = struct{}{}
	h.mu.Unlock()
	return channel, func() {
		h.mu.Lock()
		if room := h.rooms[nodeID]; room != nil {
			if _, ok := room[channel]; ok {
				delete(room, channel)
				close(channel)
			}
			if len(room) == 0 {
				delete(h.rooms, nodeID)
			}
		}
		h.mu.Unlock()
	}
}

func (h *Hub) broadcast(message postgres.ChatMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for channel := range h.rooms[message.NodeID] {
		select {
		case channel <- message:
		default:
		}
	}
}

// Run listens for new messages until ctx ends, reconnecting after errors.
func (h *Hub) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if err := h.listen(ctx); err != nil && ctx.Err() == nil {
			h.logger.Warn("chat listener stopped; reconnecting", "error", err)
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
		}
	}
}

func (h *Hub) listen(ctx context.Context) error {
	pooled, err := h.db.Acquire(ctx)
	if err != nil {
		return err
	}
	// LISTEN state belongs to the session; never hand it back to the pool.
	conn := pooled.Hijack()
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "LISTEN "+postgres.ChatNotifyChannel); err != nil {
		return err
	}
	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		id, err := strconv.ParseInt(notification.Payload, 10, 64)
		if err != nil {
			continue
		}
		h.mu.Lock()
		idle := len(h.rooms) == 0
		h.mu.Unlock()
		if idle {
			continue
		}
		message, err := h.store.ChatMessage(ctx, id)
		if err != nil {
			h.logger.Warn("load chat message", "id", id, "error", err)
			continue
		}
		h.broadcast(message)
	}
}
