package notifications

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"clicd-billing/internal/store/postgres"
)

type Worker struct {
	store                      *postgres.OutboxStore
	logger                     *slog.Logger
	workerID, endpoint, secret string
	poll                       time.Duration
	client                     *http.Client
	current                    func() (string, string, time.Duration)
}

func NewDynamicWorker(store *postgres.OutboxStore, logger *slog.Logger, workerID string, current func() (string, string, time.Duration)) *Worker {
	return &Worker{store: store, logger: logger, workerID: workerID, current: current, client: &http.Client{Timeout: 15 * time.Second}}
}

func NewWorker(store *postgres.OutboxStore, logger *slog.Logger, workerID, endpoint, secret string, poll time.Duration) *Worker {
	return &Worker{store: store, logger: logger, workerID: workerID, endpoint: endpoint, secret: secret, poll: poll, client: &http.Client{Timeout: 15 * time.Second}}
}

func (w *Worker) Run(ctx context.Context) {
	for {
		endpoint, _, poll := w.values()
		if endpoint != "" {
			w.drain(ctx)
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) drain(ctx context.Context) {
	for {
		event, err := w.store.Claim(ctx, w.workerID)
		if postgres.IsNoOutboxEvent(err) {
			return
		}
		if err != nil {
			w.logger.Error("claim outbox event", "error", err)
			return
		}
		if err = w.deliver(ctx, event); err != nil {
			w.logger.Warn("deliver notification event", "event_id", event.ID, "event_type", event.EventType, "error", err)
			if failErr := w.store.Fail(ctx, event.ID, w.workerID, err.Error()); failErr != nil {
				w.logger.Error("release failed outbox event", "event_id", event.ID, "error", failErr)
			}
			continue
		}
		if err = w.store.Complete(ctx, event.ID, w.workerID); err != nil {
			w.logger.Error("complete outbox event", "event_id", event.ID, "error", err)
		}
	}
}

func (w *Worker) deliver(ctx context.Context, event postgres.OutboxEvent) error {
	endpoint, secret, _ := w.values()
	if endpoint == "" {
		return errors.New("notification webhook is not configured")
	}
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CLICD-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set("X-CLICD-Event", event.EventType)
	request.Header.Set("Idempotency-Key", event.DeduplicationKey)
	response, err := w.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("notification endpoint returned %d: %s", response.StatusCode, string(data))
	}
	return nil
}

func (w *Worker) values() (string, string, time.Duration) {
	if w.current != nil {
		return w.current()
	}
	return w.endpoint, w.secret, w.poll
}
