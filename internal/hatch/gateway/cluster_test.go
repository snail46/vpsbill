package gateway_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"vpsbill/internal/hatch/agent"
	"vpsbill/internal/hatch/agent/agenttest"
	"vpsbill/internal/hatch/gateway"
	"vpsbill/internal/hatch/protocol"
	"vpsbill/internal/provider"
)

type memoryDirectory struct {
	mu      sync.Mutex
	holders map[string][2]string // endpoint -> instance, url
}

func (d *memoryDirectory) Register(_ context.Context, endpoint, instanceID, internalURL string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.holders[endpoint] = [2]string{instanceID, internalURL}
	return nil
}

func (d *memoryDirectory) Unregister(_ context.Context, endpoint, instanceID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.holders[endpoint][0] == instanceID {
		delete(d.holders, endpoint)
	}
	return nil
}

func (d *memoryDirectory) Lookup(_ context.Context, endpoint, selfID string) (string, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	holder, ok := d.holders[endpoint]
	if !ok || holder[0] == selfID {
		return "", false, nil
	}
	return holder[1], true, nil
}

// instance is one API process: public agent endpoint plus internal handler.
func instance(t *testing.T, id string, directory gateway.Directory, known gateway.KnownFunc) (*gateway.Hub, *httptest.Server) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := gateway.NewHub(logger, known)
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	hub.EnableCluster(&gateway.Cluster{InstanceID: id, InternalURL: server.URL, Key: gateway.ClusterKey("shared"), Directory: directory})
	mux.Handle("GET /api/v1/agent/connect", hub)
	mux.Handle("/internal/v1/agent/", hub.InternalHandler())
	return hub, server
}

func TestForwardingToTheInstanceHoldingTheAgent(t *testing.T) {
	const token = "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	endpoint := provider.AgentEndpoint(token)
	directory := &memoryDirectory{holders: map[string][2]string{}}
	known := func(context.Context, string) bool { return true }
	holder, holderServer := instance(t, "api-a", directory, known)
	other, _ := instance(t, "api-b", directory, known)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := agent.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime := agenttest.NewRuntime("lxc")
	config := agent.Config{ServerURL: holderServer.URL, Token: token, PortRangeStart: 40000, PortRangeEnd: 40010, LXD: &agent.LXDConfig{}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := agent.NewService(config, "test", store, []agent.Runtime{runtime}, &agenttest.NAT{}, logger)
	client, err := agent.NewClient(config, "test", service, logger)
	if err != nil {
		t.Fatal(err)
	}
	go client.Run(ctx)
	for {
		if _, ok := holder.Session(endpoint); ok {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("agent never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, local := other.Session(endpoint); local {
		t.Fatal("agent must only be connected to the first instance")
	}

	var images []protocol.Image
	if err := other.Call(ctx, endpoint, protocol.MethodImages, struct{}{}, &images); err != nil || len(images) != 1 {
		t.Fatalf("forwarded call failed: %v %v", images, err)
	}
	err = other.Call(ctx, endpoint, protocol.MethodGet, protocol.NameParams{Name: "missing"}, nil)
	var agentErr *protocol.Error
	if !errors.As(err, &agentErr) || agentErr.Code != protocol.CodeNotFound {
		t.Fatalf("agent errors must survive forwarding, got %v", err)
	}

	var ensured protocol.EnsureResult
	spec := protocol.CreateSpec{Name: "svc-fwd", Virtualization: "lxc", TemplateID: "debian12", VCPU: 1, RAMMB: 256, DiskGB: 5}
	if err := other.Call(ctx, endpoint, protocol.MethodEnsure, spec, &ensured); err != nil || !ensured.Created {
		t.Fatalf("forwarded ensure failed: %+v %v", ensured, err)
	}
	terminal, err := other.OpenTerminal(ctx, endpoint, "svc-fwd", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	buffer := make([]byte, 64)
	if n, err := terminal.Read(buffer); err != nil || string(buffer[:n]) != "hatch$ " {
		t.Fatalf("forwarded terminal prompt %q, %v", buffer[:n], err)
	}
	_, _ = terminal.Write([]byte("date\r"))
	if n, _ := terminal.Read(buffer); string(buffer[:n]) != "echo:date\r" {
		t.Fatalf("forwarded terminal echo %q", buffer[:n])
	}
	if _, err := other.OpenTerminal(ctx, endpoint, "missing", 80, 24); !errors.As(err, &agentErr) || agentErr.Code != protocol.CodeNotFound {
		t.Fatalf("forwarded terminal errors must survive, got %v", err)
	}

	if err := other.Call(ctx, provider.AgentEndpoint("f"+token[1:]), protocol.MethodImages, struct{}{}, nil); !errors.Is(err, gateway.ErrOffline) {
		t.Fatalf("unknown agent must be offline, got %v", err)
	}
}

func TestInternalEndpointRequiresSignature(t *testing.T) {
	directory := &memoryDirectory{holders: map[string][2]string{}}
	_, server := instance(t, "api-a", directory, func(context.Context, string) bool { return true })
	response, err := http.Post(server.URL+"/internal/v1/agent/call", "application/json", bytes.NewReader([]byte(`{"endpoint":"agent://x","method":"host.info"}`)))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unsigned internal call must be rejected, got %d", response.StatusCode)
	}
}
