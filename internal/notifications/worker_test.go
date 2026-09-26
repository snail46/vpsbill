package notifications

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vpsbill/internal/store/postgres"
)

func TestDeliverSignsEventAndSetsIdempotencyKey(t *testing.T) {
	secret := "01234567890123456789012345678901"
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if got := r.Header.Get("X-VPSBill-Signature"); got != want {
			t.Fatalf("signature=%q want %q", got, want)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "ticket:1" {
			t.Fatalf("idempotency key=%q", got)
		}
		if got := r.Header.Get("X-VPSBill-Event"); got != "ticket.created" {
			t.Fatalf("event=%q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	worker := NewWorker(nil, slog.Default(), "test", server.URL, secret, time.Second)
	err := worker.deliver(context.Background(), postgres.OutboxEvent{ID: "00000000-0000-0000-0000-000000000001", AggregateType: "ticket", AggregateID: "00000000-0000-0000-0000-000000000002", EventType: "ticket.created", DeduplicationKey: "ticket:1", Payload: []byte(`{"subject":"help"}`), CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !received {
		t.Fatal("endpoint was not called")
	}
}

func TestDeliverRejectsNonSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("adapter unavailable"))
	}))
	defer server.Close()

	worker := NewWorker(nil, slog.Default(), "test", server.URL, "01234567890123456789012345678901", time.Second)
	err := worker.deliver(context.Background(), postgres.OutboxEvent{EventType: "service.suspended", DeduplicationKey: "service:1", Payload: []byte(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "adapter unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}
