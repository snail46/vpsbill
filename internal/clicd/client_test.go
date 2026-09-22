package clicd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnsureContainerReconcilesByName(t *testing.T) {
	var created atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "secret" {
			t.Fatalf("missing API key")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/containers/svc-123":
			if !created.Load() {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "Container not found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"id": 9, "uuid": "u-9", "name": "svc-123", "status": "running"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/containers":
			created.Store(true)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "message": "Container created successfully"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "secret", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.EnsureContainer(context.Background(), CreateSpec{Name: "svc-123"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Container.ID != 9 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestEnsureContainerReturnsExisting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"id": 4, "name": "svc-existing", "status": "running"}})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "secret", time.Second)
	result, err := client.EnsureContainer(context.Background(), CreateSpec{Name: "svc-existing"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created || result.Container.ID != 4 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestEnsureContainerReconcilesAfterAmbiguousCreateTimeout(t *testing.T) {
	var created atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/containers/svc-timeout":
			if !created.Load() {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"id": 12, "name": "svc-timeout", "status": "running"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/containers":
			// Model the dangerous case: CLICD committed the create, but its response
			// arrived after the caller timed out.
			created.Store(true)
			time.Sleep(40 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "secret", 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.EnsureContainer(context.Background(), CreateSpec{Name: "svc-timeout"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created || result.Container.ID != 12 {
		t.Fatalf("unexpected reconciled result: %+v", result)
	}
}

func TestPowerAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/containers/svc-123/restart" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"task_id": "task-9"}})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "secret", time.Second)
	taskID, err := client.PowerAction(context.Background(), "svc-123", "restart")
	if err != nil || taskID != "task-9" {
		t.Fatalf("task=%q err=%v", taskID, err)
	}
}

func TestPowerActionRejectsUnsupportedAction(t *testing.T) {
	client, _ := NewClient("http://localhost", "secret", time.Second)
	if _, err := client.PowerAction(context.Background(), "svc", "delete"); err == nil {
		t.Fatal("expected unsupported action error")
	}
}

func TestDeleteContainer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/containers/svc-123/delete" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "secret", time.Second)
	if err := client.DeleteContainer(context.Background(), "svc-123"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteContainerTreatsMissingAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "Container not found"})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "secret", time.Second)
	if err := client.DeleteContainer(context.Background(), "missing"); err != nil {
		t.Fatal(err)
	}
}
