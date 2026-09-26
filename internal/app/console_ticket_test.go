package app

import (
	"testing"
	"time"
)

func TestConsoleTickets(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	tickets := newConsoleTickets("secret")
	tickets.now = func() time.Time { return now }
	ticket := tickets.issue("svc-1", "ssh")
	if err := tickets.verify(ticket, "svc-1", "ssh"); err != nil {
		t.Fatalf("valid ticket rejected: %v", err)
	}
	if tickets.verify(ticket, "svc-2", "ssh") == nil || tickets.verify(ticket, "svc-1", "vnc") == nil {
		t.Fatal("ticket must be bound to its service and console kind")
	}
	if tickets.verify(ticket+"x", "svc-1", "ssh") == nil || newConsoleTickets("other").verify(ticket, "svc-1", "ssh") == nil {
		t.Fatal("tampered or foreign ticket accepted")
	}
	now = now.Add(consoleTicketTTL + time.Second)
	if tickets.verify(ticket, "svc-1", "ssh") == nil {
		t.Fatal("expired ticket accepted")
	}
}
