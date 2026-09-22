package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OutboxStore struct{ db *pgxpool.Pool }

func NewOutboxStore(db *pgxpool.Pool) *OutboxStore { return &OutboxStore{db: db} }

type OutboxEvent struct {
	ID               string          `json:"id"`
	AggregateType    string          `json:"aggregate_type"`
	AggregateID      string          `json:"aggregate_id"`
	EventType        string          `json:"event_type"`
	DeduplicationKey string          `json:"deduplication_key"`
	Payload          json.RawMessage `json:"payload"`
	Attempts         int             `json:"attempts"`
	CreatedAt        time.Time       `json:"created_at"`
}

func (s *OutboxStore) Claim(ctx context.Context, workerID string) (OutboxEvent, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return OutboxEvent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var event OutboxEvent
	err = tx.QueryRow(ctx, `SELECT id,aggregate_type,aggregate_id,event_type,deduplication_key,payload,attempts,created_at
		FROM outbox_events WHERE published_at IS NULL AND available_at<=now() AND (locked_at IS NULL OR locked_at<now()-interval '5 minutes')
		ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&event.ID, &event.AggregateType, &event.AggregateID, &event.EventType, &event.DeduplicationKey, &event.Payload, &event.Attempts, &event.CreatedAt)
	if err != nil {
		return OutboxEvent{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE outbox_events SET locked_at=now(),locked_by=$2 WHERE id=$1", event.ID, workerID); err != nil {
		return OutboxEvent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return OutboxEvent{}, err
	}
	return event, nil
}

func (s *OutboxStore) Complete(ctx context.Context, id, workerID string) error {
	tag, err := s.db.Exec(ctx, `UPDATE outbox_events SET published_at=now(),locked_at=NULL,locked_by=NULL,last_error=NULL WHERE id=$1 AND locked_by=$2 AND published_at IS NULL`, id, workerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("outbox lease lost")
	}
	return nil
}

func (s *OutboxStore) Fail(ctx context.Context, id, workerID, message string) error {
	if len(message) > 2000 {
		message = message[:2000]
	}
	tag, err := s.db.Exec(ctx, `UPDATE outbox_events SET attempts=attempts+1,available_at=now()+make_interval(secs=>LEAST(3600,5*power(2,LEAST(attempts,9)))::int),locked_at=NULL,locked_by=NULL,last_error=$3 WHERE id=$1 AND locked_by=$2 AND published_at IS NULL`, id, workerID, message)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("outbox lease lost")
	}
	return nil
}

func IsNoOutboxEvent(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
