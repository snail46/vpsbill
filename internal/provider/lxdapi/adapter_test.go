package lxdapiprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsbill/internal/provider"
)

// fakeNode emulates the LXDAPI system API: HTTP 200 envelopes, asynchronous
// create/delete tasks and v4 port mappings.
type fakeNode struct {
	mu         sync.Mutex
	containers map[string]map[string]any
	tasks      map[uint]map[string]any
	mappings   []portMapping
	nextID     uint
	creates    int
	lastCreate map[string]any
	ipv6       map[string][]string
	allocated  int
}

func newFakeNode() *fakeNode {
	return &fakeNode{containers: map[string]map[string]any{}, tasks: map[uint]map[string]any{}, ipv6: map[string][]string{}, nextID: 1}
}

func (f *fakeNode) reply(w http.ResponseWriter, code int, msg string, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": msg, "data": data})
}

func (f *fakeNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-API-Hash") != "hash" {
		f.reply(w, 401, "系统级认证失败", nil)
		return
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/api/system/containers":
		f.reply(w, 200, "success", []any{})
	case r.Method == http.MethodPost && path == "/api/system/containers":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.creates++
		f.lastCreate = body
		name := body["name"].(string)
		id := f.nextID
		f.nextID++
		f.tasks[id] = map[string]any{"ID": id, "action": "create", "status": "running", "container_name": name}
		// The task completes on its next poll.
		go func() {
			time.Sleep(5 * time.Millisecond)
			f.mu.Lock()
			f.containers[name] = map[string]any{"ID": 7, "Name": name, "Image": body["image"], "Password": body["password"], "Memory": body["memory"], "Disk": body["disk"], "IPv4MappingLimit": body["ipv4_mapping_limit"]}
			f.tasks[id]["status"] = "success"
			f.mu.Unlock()
		}()
		f.reply(w, 200, "success", map[string]any{"name": name, "task_id": id})
	case r.Method == http.MethodGet && path == "/api/system/ip":
		f.reply(w, 200, "success", map[string]any{"ipv4": []string{}, "ipv6": f.ipv6[r.URL.Query().Get("container")]})
	case r.Method == http.MethodPost && path == "/api/system/ip/allocate":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Query().Get("version") != "v6" {
			f.reply(w, 500, "IPv4功能未启用", nil)
			return
		}
		f.allocated++
		name := body["name"].(string)
		f.ipv6[name] = append(f.ipv6[name], "2001:db8::10")
		f.reply(w, 200, "success", nil)
	case r.Method == http.MethodGet && path == "/api/system/tasks":
		list := []any{}
		for _, current := range f.tasks {
			if current["container_name"] == r.URL.Query().Get("name") {
				list = append(list, current)
			}
		}
		f.reply(w, 200, "success", list)
	case r.Method == http.MethodGet && path == "/api/system/tasks/detail":
		for id, current := range f.tasks {
			if r.URL.Query().Get("id") == jsonNumber(id) {
				f.reply(w, 200, "success", current)
				return
			}
		}
		f.reply(w, 404, "任务不存在", nil)
	case r.Method == http.MethodGet && path == "/api/system/port-mapping":
		list := []portMapping{}
		for _, mapping := range f.mappings {
			if container := r.URL.Query().Get("container"); container == "" || container == mapping.ContainerName {
				list = append(list, mapping)
			}
		}
		f.reply(w, 200, "success", map[string]any{"ipv4": list})
	case r.Method == http.MethodPost && path == "/api/system/port-mapping/allocate":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		port := int(body["public_port"].(float64))
		for _, mapping := range f.mappings {
			if mapping.PublicPort == port {
				f.reply(w, 500, "端口已被占用", nil)
				return
			}
		}
		f.mappings = append(f.mappings, portMapping{ID: f.nextID, PublicIP: body["public_ip"].(string), PublicPort: port, ContainerName: body["container_name"].(string), ContainerPort: int(body["container_port"].(float64)), Protocol: body["protocol"].(string), Description: body["description"].(string)})
		f.nextID++
		f.reply(w, 200, "success", nil)
	case r.Method == http.MethodPost && path == "/api/system/port-mapping/release":
		var body struct{ ID uint }
		_ = json.NewDecoder(r.Body).Decode(&body)
		for i, mapping := range f.mappings {
			if mapping.ID == body.ID {
				f.mappings = append(f.mappings[:i], f.mappings[i+1:]...)
				break
			}
		}
		f.reply(w, 200, "success", nil)
	case strings.HasPrefix(path, "/api/system/containers/"):
		name := strings.TrimPrefix(path, "/api/system/containers/")
		name = strings.TrimSuffix(name, "/action")
		value, ok := f.containers[name]
		if !ok {
			f.reply(w, 404, "容器不存在", nil)
			return
		}
		switch r.Method {
		case http.MethodGet:
			f.reply(w, 200, "success", map[string]any{"container": value, "status": "Running"})
		case http.MethodDelete:
			delete(f.containers, name)
			id := f.nextID
			f.nextID++
			f.tasks[id] = map[string]any{"ID": id, "action": "delete", "status": "success", "container_name": name}
			f.reply(w, 200, "success", map[string]any{"task_id": id})
		default:
			f.reply(w, 200, "success", "ok")
		}
	default:
		f.reply(w, 404, "not found", nil)
	}
}

func jsonNumber(id uint) string {
	encoded, _ := json.Marshal(id)
	return string(encoded)
}

func openDriver(t *testing.T, node *fakeNode) provider.Driver {
	t.Helper()
	pollInterval = time.Millisecond
	server := httptest.NewTLSServer(node)
	t.Cleanup(server.Close)
	sum := sha256.Sum256(server.Certificate().Raw)
	options, _ := json.Marshal(Options{
		PublicIPv4: "203.0.113.10", NATInterface: "eth0", PortRangeStart: 30000, PortRangeEnd: 30002,
		Images: []string{"debian12"}, CapacityVCPU: 8, CapacityRAMMB: 16384, CapacityDiskGB: 400,
		TLSFingerprint: hex.EncodeToString(sum[:]),
	})
	driver, err := provider.Open(Type, provider.Config{BaseURL: server.URL, Credential: "hash", Options: options, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func TestEnsureInstanceWaitsForTaskAndIsIdempotent(t *testing.T) {
	node := newFakeNode()
	driver := openDriver(t, node)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	spec := provider.CreateSpec{Name: "svc-1", TemplateID: "debian12", VCPU: 1, RAMMB: 512, DiskGB: 10, AssignNAT: true, PortMappingCount: 5, SSHAuthMode: "auto_password"}
	result, err := driver.EnsureInstance(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Instance.ExternalID != "7" || result.Instance.IP != "203.0.113.10" || result.Instance.InitialPassword == "" {
		t.Fatalf("unexpected instance %+v", result)
	}
	if node.lastCreate["disk"] != float64(10240) || node.lastCreate["ipv4_mapping_limit"] != float64(5) || node.lastCreate["username"] != "vpsbill" {
		t.Fatalf("unexpected create request %v", node.lastCreate)
	}
	again, err := driver.EnsureInstance(ctx, spec)
	if err != nil || again.Created || node.creates != 1 {
		t.Fatalf("retry must adopt the existing container: %+v, creates=%d, err=%v", again, node.creates, err)
	}
}

func TestNotFoundAndAuthErrors(t *testing.T) {
	node := newFakeNode()
	driver := openDriver(t, node)
	if _, err := driver.GetInstance(context.Background(), "missing"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := driver.DeleteInstance(context.Background(), "missing"); err != nil {
		t.Fatalf("deleting a missing container must succeed, got %v", err)
	}
	options, _ := json.Marshal(Options{TLSVerify: true})
	if _, err := provider.Open(Type, provider.Config{BaseURL: "http://node:8443", Credential: "hash", Options: options}); err == nil {
		t.Fatal("plain HTTP must be rejected")
	}
	if _, err := provider.Open(Type, provider.Config{BaseURL: "https://node:8443", Credential: "hash"}); err == nil {
		t.Fatal("missing certificate pin must be rejected")
	}
}

func TestFingerprintMismatchRejected(t *testing.T) {
	server := httptest.NewTLSServer(newFakeNode())
	defer server.Close()
	options, _ := json.Marshal(Options{TLSFingerprint: strings.Repeat("ab", 32)})
	driver, err := provider.Open(Type, provider.Config{BaseURL: server.URL, Credential: "hash", Options: options, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.HostInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("expected fingerprint error, got %v", err)
	}
}

func TestPortMappingLifecycle(t *testing.T) {
	node := newFakeNode()
	node.containers["svc-1"] = map[string]any{"ID": 7, "Name": "svc-1"}
	node.mappings = []portMapping{{ID: 100, PublicIP: "203.0.113.10", PublicPort: 30000, ContainerName: "svc-1", ContainerPort: 22, Protocol: "tcp"}}
	node.nextID = 200
	driver := openDriver(t, node)
	mapper := driver.(provider.PortMapper)
	ctx := context.Background()

	instance, err := driver.GetInstance(ctx, "svc-1")
	if err != nil || instance.SSHPort != 30000 || instance.PortMappings[0].Description != "SSH" {
		t.Fatalf("SSH mapping not surfaced: %+v, %v", instance, err)
	}
	port, err := mapper.FreePort(ctx, "svc-1")
	if err != nil || port == 30000 || port < 30000 || port > 30002 {
		t.Fatalf("unexpected free port %d, %v", port, err)
	}
	mappings, err := mapper.AddPortMapping(ctx, "svc-1", provider.PortMapping{ContainerPort: 80, HostPort: port, Protocol: "tcp", Description: "web"})
	if err != nil || len(mappings) != 2 {
		t.Fatalf("add failed: %v, %v", mappings, err)
	}
	mappings, err = mapper.UpdatePortMapping(ctx, "svc-1", 1, provider.PortMapping{ContainerPort: 8080, Protocol: "udp", Description: "alt"})
	if err != nil || len(mappings) != 2 || mappings[1].ContainerPort != 8080 || mappings[1].HostPort != port {
		t.Fatalf("update must keep the public port: %v, %v", mappings, err)
	}
	mappings, err = mapper.DeletePortMapping(ctx, "svc-1", 1)
	if err != nil || len(mappings) != 1 {
		t.Fatalf("delete failed: %v, %v", mappings, err)
	}
	if _, err := mapper.DeletePortMapping(ctx, "svc-1", 5); err == nil {
		t.Fatal("out-of-range index must fail")
	}
}

func TestCapabilities(t *testing.T) {
	driver := openDriver(t, newFakeNode())
	capabilities := provider.CapabilitiesOf(driver)
	if !capabilities.Suspend || !capabilities.PortMapping || capabilities.HostProbe || len(capabilities.Console) != 0 {
		t.Fatalf("unexpected LXDAPI capabilities %+v", capabilities)
	}
	info, err := driver.HostInfo(context.Background())
	if err != nil || info.Capacity.VCPU != 8 || info.Capacity.DiskGB != 400 {
		t.Fatalf("unexpected host info %+v, %v", info, err)
	}
}

func TestEnsureTopsUpMissingIPv6(t *testing.T) {
	node := newFakeNode()
	driver := openDriver(t, node)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := driver.EnsureInstance(ctx, provider.CreateSpec{Name: "svc-6", TemplateID: "debian12", VCPU: 1, RAMMB: 512, DiskGB: 10, AssignNAT: true, AssignIPv6: true, IPv6Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if node.allocated != 1 || result.Instance.IPv6 != "2001:db8::10" {
		t.Fatalf("IPv6 not allocated: allocated=%d instance=%+v", node.allocated, result.Instance)
	}
	if node.lastCreate["ipv6_pool_limit"] != float64(1) {
		t.Fatalf("pool limit not requested: %v", node.lastCreate)
	}
}
