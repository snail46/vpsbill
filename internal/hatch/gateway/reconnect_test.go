package gateway

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestReconnecting(t *testing.T) {
	hub := NewHub(slog.New(slog.DiscardHandler), func(context.Context, string) bool { return true })
	if !hub.Reconnecting("e") {
		t.Fatal("a freshly started server should expect its agents back")
	}
	hub.started = time.Now().Add(-time.Hour)
	if hub.Reconnecting("e") {
		t.Fatal("an agent that never connected is not reconnecting")
	}
	hub.gone["e"] = time.Now().Add(-time.Minute)
	if !hub.Reconnecting("e") {
		t.Fatal("an agent that dropped a minute ago should be expected back")
	}
	hub.gone["e"] = time.Now().Add(-3 * time.Minute)
	if hub.Reconnecting("e") {
		t.Fatal("an agent gone for minutes is offline")
	}
	hub.sessions["e"] = &Session{}
	if hub.Reconnecting("e") {
		t.Fatal("a connected agent is not reconnecting")
	}
}
