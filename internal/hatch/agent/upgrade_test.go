package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"

	"vpsbill/internal/hatch/protocol"
)

func TestUpgradeStagesTheBundledBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent build is a shell script")
	}
	build := []byte("#!/bin/sh\necho v2\n")
	sums := func(body []byte) string {
		sum := sha256.Sum256(body)
		return hex.EncodeToString(sum[:]) + "  hatch-agent-linux-" + runtime.GOARCH + "\n"
	}
	listed := sums(build)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/download/SHA256SUMS":
			_, _ = w.Write([]byte(listed))
		case "/api/v1/agent/download/hatch-agent-linux-" + runtime.GOARCH:
			_, _ = w.Write(build)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	client := &Client{config: Config{ServerURL: server.URL, StateDir: dir}, version: "v1", logger: slog.New(slog.NewTextHandler(io.Discard, nil)), http: server.Client()}
	restarted := make(chan struct{})
	client.SetExit(func() { close(restarted) })
	upgrade := func(version string) error {
		params, _ := json.Marshal(protocol.UpgradeParams{Version: version})
		return client.upgrade(context.Background(), params)
	}

	// The build must report the version the server named.
	if err := upgrade("v3"); err == nil {
		t.Fatal("a build reporting another version was staged")
	}
	listed = sums([]byte("something else"))
	if err := upgrade("v2"); err == nil {
		t.Fatal("a build not matching SHA256SUMS was staged")
	}
	if _, err := os.Stat(stagedBinary(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed upgrades left a staged build: %v", err)
	}

	listed = sums(build)
	if err := upgrade("v2"); err != nil {
		t.Fatal(err)
	}
	<-restarted
	if staged, err := os.ReadFile(stagedBinary(dir)); err != nil || string(staged) != string(build) {
		t.Fatalf("staged build = %q, %v", staged, err)
	}
	if err := upgrade("v2"); err == nil {
		t.Fatal("a second upgrade ran while the first waits to restart")
	}

	off := false
	client.config.AutoUpgrade = &off
	client.upgrading.Store(false)
	var protocolErr *protocol.Error
	if err := upgrade("v2"); !errors.As(err, &protocolErr) || protocolErr.Code != protocol.CodeUnsupported {
		t.Fatalf("auto_upgrade false: %v", err)
	}
}

func TestUpgradeRejectsOddVersions(t *testing.T) {
	client := &Client{config: Config{StateDir: t.TempDir()}, version: "v1"}
	for _, version := range []string{"", "../x", "a b", string(make([]byte, 65))} {
		params, _ := json.Marshal(protocol.UpgradeParams{Version: version})
		if err := client.upgrade(context.Background(), params); err == nil {
			t.Errorf("version %q accepted", version)
		}
	}
	params, _ := json.Marshal(protocol.UpgradeParams{Version: "v1"})
	if err := client.upgrade(context.Background(), params); err != nil {
		t.Errorf("same version: %v", err)
	}
}
