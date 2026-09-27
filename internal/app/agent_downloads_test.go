package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentDownloadsServesOnlyBundledFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agent/download/{file}", agentDownloads(dir))

	for path, want := range map[string]int{
		"/api/v1/agent/download/install.sh":              http.StatusOK,
		"/api/v1/agent/download/secret.txt":              http.StatusNotFound,
		"/api/v1/agent/download/hatch-agent-linux-amd64": http.StatusNotFound,
		"/api/v1/agent/download/..%2Fsecret.txt":         http.StatusNotFound,
	} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != want {
			t.Errorf("%s: status %d, want %d", path, recorder.Code, want)
		}
	}
}
