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

func TestEnsureContainerReturnsValidationErrorWithoutMisleadingReconciliation(t *testing.T) {
	var getCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCount.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "Container not found"})
		case http.MethodPost:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "请填写自定义 SSH 密码"})
		}
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "secret", time.Second)
	_, err := client.EnsureContainer(context.Background(), CreateSpec{Name: "svc-invalid"})
	if err == nil || err.Error() != "create container: clicd api returned 400: 请填写自定义 SSH 密码" {
		t.Fatalf("unexpected error: %v", err)
	}
	if getCount.Load() != 1 {
		t.Fatalf("validation error should not trigger reconciliation lookup; got %d GETs", getCount.Load())
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

func TestProbeAndImageEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch r.URL.Path {
		case "/api/v1/dashboard":
			data = map[string]any{"total_containers": 3}
		case "/api/v1/host-info":
			data = map[string]any{"cpu": map[string]any{"cores": 8}}
		case "/api/v1/host-history":
			data = []map[string]any{{"ts": "2026-09-22T00:00:00Z", "cpu": 12.5}}
		case "/api/v1/host-report":
			data = map[string]any{"hostname": "node-1"}
		case "/api/v1/images":
			data = []map[string]any{{"id": "ubuntu-noble", "name": "Ubuntu 24.04", "type": "lxc", "downloaded": true, "enabled": true}}
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "secret", time.Second)
	if value, err := client.Dashboard(context.Background()); err != nil || value.(map[string]any)["total_containers"] != float64(3) {
		t.Fatalf("dashboard=%#v err=%v", value, err)
	}
	if value, err := client.HostInfo(context.Background()); err != nil || value["cpu"] == nil {
		t.Fatalf("host info=%#v err=%v", value, err)
	}
	if value, err := client.HostHistory(context.Background()); err != nil || len(value.([]any)) != 1 {
		t.Fatalf("host history=%#v err=%v", value, err)
	}
	if value, err := client.HostReport(context.Background()); err != nil || value.(map[string]any)["hostname"] != "node-1" {
		t.Fatalf("host report=%#v err=%v", value, err)
	}
	if value, err := client.Images(context.Background()); err != nil || len(value) != 1 || value[0].Name != "Ubuntu 24.04" {
		t.Fatalf("images=%#v err=%v", value, err)
	}
}

func TestCustomerInstanceEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "secret" {
			t.Fatalf("missing API key")
		}
		data := any(map[string]any{})
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/containers/svc-1/usage":
			data = map[string]any{"cpu_usage_pct": 17.5}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/containers/svc-1/history":
			data = []map[string]any{{"ts": 1, "cpu": 17.5}}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/containers/svc-1/traffic":
			data = map[string]any{"total_used_bytes": 1024}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/containers/svc-1/reset-password":
			data = map[string]any{"password": "NewPass123"}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/containers/svc-1/reinstall":
			data = map[string]any{"task_id": "task-reinstall"}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/containers/svc-1/random-port":
			data = map[string]any{"port": 24001}
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/containers/svc-1/port-mappings":
			data = []map[string]any{{"container_port": 80, "host_port": 24001, "protocol": "tcp"}}
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "secret", time.Second)
	if value, err := client.ContainerUsage(context.Background(), "svc-1"); err != nil || value.(map[string]any)["cpu_usage_pct"] != float64(17.5) {
		t.Fatalf("usage=%#v err=%v", value, err)
	}
	if _, err := client.ContainerHistory(context.Background(), "svc-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ContainerTraffic(context.Background(), "svc-1"); err != nil {
		t.Fatal(err)
	}
	if password, err := client.ResetPassword(context.Background(), "svc-1", "NewPass123"); err != nil || password != "NewPass123" {
		t.Fatalf("password=%q err=%v", password, err)
	}
	if task, err := client.Reinstall(context.Background(), "svc-1", ReinstallSpec{TemplateID: "debian", SSHAuthMode: "password", SSHPassword: "NewPass123"}); err != nil || task != "task-reinstall" {
		t.Fatalf("task=%q err=%v", task, err)
	}
	port, err := client.RandomPort(context.Background(), "svc-1")
	if err != nil || port != 24001 {
		t.Fatalf("port=%d err=%v", port, err)
	}
	mappings, err := client.AddPortMapping(context.Background(), "svc-1", PortMapping{ContainerPort: 80, HostPort: port, Protocol: "tcp"})
	if err != nil || len(mappings) != 1 || mappings[0].HostPort != 24001 {
		t.Fatalf("mappings=%#v err=%v", mappings, err)
	}
}

func TestConsoleTicketPreservesBrowserUserAgentAndTargetHidesCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/vnc-ticket" || r.UserAgent() != "CustomerBrowser/1.0" {
			t.Fatalf("path=%s user-agent=%q", r.URL.Path, r.UserAgent())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"ticket": "once-only"}})
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "secret", time.Second)
	ticket, err := client.ConsoleTicket(context.Background(), "svc-1", "vnc", "CustomerBrowser/1.0")
	if err != nil || ticket != "once-only" {
		t.Fatalf("ticket=%q err=%v", ticket, err)
	}
	target, err := client.ConsoleTarget("svc-1", "vnc")
	if err != nil || target.Path != "/api/vnc" || target.Query().Get("container") != "svc-1" || target.Query().Get("api_key") != "" {
		t.Fatalf("target=%v err=%v", target, err)
	}
}
