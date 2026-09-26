package hatchprovider_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"vpsbill/internal/hatch/agent"
	"vpsbill/internal/hatch/agent/agenttest"
	"vpsbill/internal/hatch/gateway"
	"vpsbill/internal/provider"
	hatchprovider "vpsbill/internal/provider/hatch"
)

const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

var (
	hub      *gateway.Hub
	server   *httptest.Server
	runtime  = agenttest.NewRuntime("lxc")
	quietLog = slog.New(slog.NewTextHandler(io.Discard, nil))
)

func TestMain(m *testing.M) {
	registered := provider.AgentEndpoint(token)
	hub = gateway.NewHub(quietLog, func(_ context.Context, endpoint string) bool { return endpoint == registered })
	hatchprovider.Register(hub)
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/agent/connect", hub)
	server = httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stateDir := mustTempDir()
	defer os.RemoveAll(stateDir)
	store, err := agent.OpenStore(stateDir)
	if err != nil {
		panic(err)
	}
	config := agent.Config{ServerURL: server.URL, Token: token, PublicIPv4: "203.0.113.10", PortRangeStart: 40000, PortRangeEnd: 40100, LXD: &agent.LXDConfig{}}
	service := agent.NewService(config, "test", store, []agent.Runtime{runtime}, &agenttest.NAT{}, quietLog)
	client, err := agent.NewClient(config, "test", service, quietLog)
	if err != nil {
		panic(err)
	}
	go client.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := hub.Session(registered); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.Run()
}

func mustTempDir() string {
	path, err := os.MkdirTemp("", "hatch-agent-test-*")
	if err != nil {
		panic(err)
	}
	return path
}

func TestProvisioningThroughAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	driver, err := provider.Open(hatchprovider.Type, provider.Config{BaseURL: provider.AgentEndpoint(token), Credential: token})
	if err != nil {
		t.Fatal(err)
	}
	info, err := driver.HostInfo(ctx)
	if err != nil || info.Raw["public_ipv4"] != "203.0.113.10" || info.Capacity.VCPU == 0 {
		t.Fatalf("host info: %+v, %v", info, err)
	}
	images, err := driver.Images(ctx)
	if err != nil || len(images) != 1 || !images[0].Sellable("lxc") {
		t.Fatalf("images: %+v, %v", images, err)
	}
	spec := provider.CreateSpec{Name: "svc-e2e", Virtualization: "lxc", TemplateID: "debian12", VCPU: 1, RAMMB: 512, DiskGB: 10, AssignNAT: true, PortMappingCount: 2, SSHAuthMode: "auto_password"}
	result, err := driver.EnsureInstance(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	instance := result.Instance
	if !result.Created || instance.InitialPassword == "" || instance.IP != "203.0.113.10" || instance.SSHPort == 0 {
		t.Fatalf("unexpected instance %+v", result)
	}
	if provider.NormalizeStatus(instance.Status) != "running" {
		t.Fatalf("status %q", instance.Status)
	}

	mapper := driver.(provider.PortMapper)
	port, err := mapper.FreePort(ctx, "svc-e2e")
	if err != nil || port < 40000 {
		t.Fatalf("free port %d, %v", port, err)
	}
	mappings, err := mapper.AddPortMapping(ctx, "svc-e2e", provider.PortMapping{ContainerPort: 80, HostPort: port, Protocol: "tcp", Description: "web"})
	if err != nil || len(mappings) != 2 {
		t.Fatalf("add mapping: %v, %v", mappings, err)
	}
	_, err = mapper.AddPortMapping(ctx, "svc-e2e", provider.PortMapping{ContainerPort: 81, Protocol: "tcp"})
	var apiErr *provider.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("mapping limit must surface as a node error, got %v", err)
	}

	if _, err := driver.PowerAction(ctx, "svc-e2e", "stop"); err != nil {
		t.Fatal(err)
	}
	if got, _ := runtime.Get("svc-e2e"); got.Status != "stopped" {
		t.Fatalf("stop not applied: %+v", got)
	}
	if err := driver.DeleteInstance(ctx, "svc-e2e"); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.GetInstance(ctx, "svc-e2e"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestOfflineAgent(t *testing.T) {
	other := strings.Repeat("f", 64)
	driver, err := provider.Open(hatchprovider.Type, provider.Config{Credential: other})
	if err != nil {
		t.Fatal(err)
	}
	_, err = driver.HostInfo(context.Background())
	var apiErr *provider.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected offline error, got %v", err)
	}
}

func TestGatewayRejectsBadTokensAndLimitsUnknownAgents(t *testing.T) {
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/agent/connect"
	dial := func(value string) (*websocket.Conn, *http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + value}}})
	}
	if _, response, err := dial("short"); err == nil || response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("short token must be rejected, got %v", err)
	}
	var open []*websocket.Conn
	defer func() {
		for _, conn := range open {
			conn.CloseNow()
		}
	}()
	for i := range 8 {
		conn, _, err := dial(strings.Repeat(string(rune('a'+i)), 64))
		if err != nil {
			t.Fatalf("unknown agent %d rejected early: %v", i, err)
		}
		open = append(open, conn)
	}
	if _, response, err := dial(strings.Repeat("z", 64)); err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ninth unknown agent must be refused, got %v", err)
	}
}

func TestTerminalThroughAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	driver, err := provider.Open(hatchprovider.Type, provider.Config{Credential: token})
	if err != nil {
		t.Fatal(err)
	}
	spec := provider.CreateSpec{Name: "svc-term", Virtualization: "lxc", TemplateID: "debian12", VCPU: 1, RAMMB: 512, DiskGB: 10, AssignNAT: true}
	if _, err := driver.EnsureInstance(ctx, spec); err != nil {
		t.Fatal(err)
	}
	defer driver.DeleteInstance(ctx, "svc-term")
	if capabilities := provider.CapabilitiesOf(driver); len(capabilities.Console) != 1 || capabilities.Console[0] != "ssh" {
		t.Fatalf("hatch must advertise an ssh console: %+v", capabilities)
	}
	session, err := driver.(provider.Terminal).OpenTerminal(ctx, "svc-term", 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	read := func() string {
		buffer := make([]byte, 64)
		n, err := session.Read(buffer)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return string(buffer[:n])
	}
	if prompt := read(); prompt != "hatch$ " {
		t.Fatalf("unexpected prompt %q", prompt)
	}
	if _, err := session.Write([]byte("uptime\r")); err != nil {
		t.Fatal(err)
	}
	if echoed := read(); echoed != "echo:uptime\r" {
		t.Fatalf("unexpected echo %q", echoed)
	}
	if err := session.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.(provider.Terminal).OpenTerminal(ctx, "missing", 80, 24); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("expected not found for a missing instance, got %v", err)
	}
}
