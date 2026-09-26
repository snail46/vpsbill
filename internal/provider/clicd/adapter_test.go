package clicdprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vpsbill/internal/provider"
)

func openTestDriver(t *testing.T, handler http.HandlerFunc) provider.Driver {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	driver, err := provider.Open(Type, provider.Config{BaseURL: server.URL, Credential: "secret", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func TestEnsureInstanceMapsContainer(t *testing.T) {
	var created bool
	driver := openTestDriver(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/containers/svc-1":
			if !created {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "not found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"id": 42, "uuid": "u-42", "name": "svc-1", "status": "Running", "ip": "10.0.0.2", "ssh_password": "initial",
				"port_mappings": []map[string]any{{"container_port": 22, "host_port": 20022, "protocol": "tcp", "description": "SSH"}},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/containers":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["expires_at"] != "2030-01-02T03:04:05Z" || body["template_id"] != "debian-12" {
				t.Errorf("unexpected create body: %v", body)
			}
			created = true
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	expires := time.Date(2030, 1, 2, 11, 4, 5, 0, time.FixedZone("CST", 8*3600))
	result, err := driver.EnsureInstance(context.Background(), provider.CreateSpec{Name: "svc-1", TemplateID: "debian-12", ExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	instance := result.Instance
	if !result.Created || instance.ExternalID != "42" || instance.InitialPassword != "initial" || len(instance.PortMappings) != 1 {
		t.Fatalf("unexpected instance: %+v", result)
	}
	if provider.NormalizeStatus(instance.Status) != "running" {
		t.Fatalf("status %q not normalized", instance.Status)
	}
	encoded, _ := json.Marshal(instance)
	var decoded map[string]any
	_ = json.Unmarshal(encoded, &decoded)
	if _, leaked := decoded["ssh_password"]; leaked || decoded["id"] != "42" {
		t.Fatalf("instance JSON shape changed: %s", encoded)
	}
}

func TestErrorsAreTranslated(t *testing.T) {
	driver := openTestDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/containers/gone" || r.URL.Path == "/api/v1/containers/gone/delete" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "Container not found"})
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "port exhausted"})
	})
	_, err := driver.GetInstance(context.Background(), "gone")
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	_, err = driver.PowerAction(context.Background(), "svc", "start")
	var apiErr *provider.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Message != "port exhausted" {
		t.Fatalf("expected provider.Error, got %v", err)
	}
	if err := driver.DeleteInstance(context.Background(), "gone"); err != nil {
		t.Fatalf("deleting a missing instance must succeed, got %v", err)
	}
}

func TestCapabilitiesAndCapacity(t *testing.T) {
	driver := openTestDriver(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
			"cpu": map[string]any{"cores": 8}, "ram": map[string]any{"total_mb": 16384}, "disk": map[string]any{"total_gb": 500},
		}})
	})
	info, err := driver.HostInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Capacity != (provider.Capacity{VCPU: 8, RAMMB: 16384, DiskGB: 500}) {
		t.Fatalf("unexpected capacity %+v", info.Capacity)
	}
	capabilities := provider.CapabilitiesOf(driver)
	if !capabilities.Reinstall || !capabilities.PortMapping || !capabilities.HostProbe || len(capabilities.Console) != 2 || capabilities.Suspend {
		t.Fatalf("unexpected CLICD capabilities %+v", capabilities)
	}
}
