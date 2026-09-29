package hatchprovider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"vpsbill/internal/hatch/agent"
	"vpsbill/internal/hatch/agent/agenttest"
	"vpsbill/internal/hatch/gateway"
	"vpsbill/internal/hatch/protocol"
	"vpsbill/internal/provider"
)

// An agent installed with an enroll key reaches its owner's pending list
// even while every slot for unregistered agents is taken.

func TestEnrolledAgentBypassesUnknownLimit(t *testing.T) {
	const enrollKey = "owner-key-0123456789abcdef"
	const pendingToken = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	var mu sync.Mutex
	var enrolled []protocol.Hello
	enrollHub := gateway.NewHub(quietLog, func(context.Context, string) bool { return false })
	enrollHub.SetEnroller(func(_ context.Context, endpoint, token string, hello protocol.Hello, _ *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		if hello.EnrollKey != enrollKey || endpoint != provider.AgentEndpoint(token) {
			return false
		}
		enrolled = append(enrolled, hello)
		return true
	})
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/agent/connect", enrollHub)
	enrollServer := httptest.NewServer(mux)
	defer enrollServer.Close()

	// Fill the unregistered slots; they stay until their hello times out.
	url := "ws" + strings.TrimPrefix(enrollServer.URL, "http") + "/api/v1/agent/connect"
	for i := range 8 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + strings.Repeat(string(rune('a'+i)), 64)}}})
		cancel()
		if err != nil {
			t.Fatalf("filler %d: %v", i, err)
		}
		defer conn.CloseNow()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, err := agent.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := agent.Config{ServerURL: enrollServer.URL, Token: pendingToken, EnrollKey: enrollKey, PublicIPv4: "203.0.113.20",
		PortRangeStart: 40000, PortRangeEnd: 40100, LXD: &agent.LXDConfig{}}
	service := agent.NewService(config, "test", store, []agent.Runtime{agenttest.NewRuntime("lxc")}, &agenttest.NAT{}, quietLog)
	client, err := agent.NewClient(config, "test", service, quietLog)
	if err != nil {
		t.Fatal(err)
	}
	go client.Run(ctx)
	endpoint := provider.AgentEndpoint(pendingToken)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := enrollHub.Session(endpoint); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := enrollHub.Session(endpoint); !ok {
		t.Fatal("enrolled agent was not admitted")
	}
	mu.Lock()
	if len(enrolled) != 1 || enrolled[0].Hostname == "" {
		t.Fatalf("enroller saw %+v", enrolled)
	}
	mu.Unlock()
}
