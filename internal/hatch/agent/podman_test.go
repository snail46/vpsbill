package agent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type recorder struct {
	mu       sync.Mutex
	commands []string
}

func (r *recorder) run(_ context.Context, name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := name + " " + strings.Join(args, " ")
	r.commands = append(r.commands, line)
	if name == "nsenter" {
		return "2: eth0@if15: <BROADCAST,MULTICAST,UP> mtu 1500\n", nil
	}
	return "", nil
}

func (r *recorder) matching(prefix string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []string
	for _, command := range r.commands {
		if strings.HasPrefix(command, prefix) {
			result = append(result, command)
		}
	}
	return result
}

// fakeLibpod serves the libpod endpoints the Podman runtime uses on a unix
// socket.
func fakeLibpod(t *testing.T) (string, *map[string]any) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "podman.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	created := map[string]any{}
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v4.0.0/libpod/containers/create", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewDecoder(r.Body).Decode(&created)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"Id":"abc"}`))
	})
	mux.HandleFunc("POST /v4.0.0/libpod/containers/svc/start", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /v4.0.0/libpod/containers/svc/json", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		labels, _ := created["labels"].(map[string]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"State":  map[string]any{"Status": "running", "Pid": 4242},
			"Config": map[string]any{"Labels": labels},
		})
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return socket, &created
}

func TestPodmanAppliesBandwidthLimitsOncePerVeth(t *testing.T) {
	socket, created := fakeLibpod(t)
	runtime := NewPodman(PodmanConfig{Socket: socket, Network: "podman"})
	commands := &recorder{}
	runtime.run = commands.run
	runtime.interfaceName = func(index int) (string, error) {
		if index != 15 {
			t.Fatalf("unexpected ifindex %d", index)
		}
		return "veth15", nil
	}
	ctx := context.Background()
	err := runtime.Create(ctx, RuntimeSpec{Name: "svc", Image: "vps:latest", VCPU: 1, RAMMB: 512, DiskGB: 5, IPv4: netip.MustParseAddr("10.88.0.200"), NetworkDownMbps: 100, NetworkUpMbps: 20})
	if err != nil {
		t.Fatal(err)
	}
	if labels := (*created)["labels"].(map[string]any); labels[labelDown] != "100" || labels[labelUp] != "20" {
		t.Fatalf("limits not stored as labels: %v", labels)
	}
	if got := commands.matching("tc qdisc add dev veth15 root tbf rate 100mbit"); len(got) != 1 {
		t.Fatalf("download limit not applied: %v", commands.commands)
	}
	if got := commands.matching("tc filter add dev veth15 parent ffff: protocol all prio 1 matchall action police rate 20mbit"); len(got) != 1 {
		t.Fatalf("upload limit not applied: %v", commands.commands)
	}
	if err := runtime.Maintain(ctx, "svc"); err != nil {
		t.Fatal(err)
	}
	if got := commands.matching("tc qdisc add dev veth15 root"); len(got) != 1 {
		t.Fatalf("unchanged veth must not be reshaped: %v", got)
	}
}

func TestBurstBytes(t *testing.T) {
	if burstBytes(1) != "32768b" || burstBytes(1000) != "1250000b" {
		t.Fatalf("unexpected bursts %s %s", burstBytes(1), burstBytes(1000))
	}
}
