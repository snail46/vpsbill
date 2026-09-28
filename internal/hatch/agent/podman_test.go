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
	sizeQueries = 0
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
	mux.HandleFunc("GET /v4.0.0/libpod/containers/svc/json", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		labels, _ := created["labels"].(map[string]any)
		body := map[string]any{
			"State":  map[string]any{"Status": "running", "Pid": 4242},
			"Config": map[string]any{"Labels": labels},
		}
		if r.URL.Query().Get("size") == "true" {
			sizeQueries++
			body["SizeRootFs"] = int64(512 << 20)
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /v4.0.0/libpod/info", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"store":{"graphDriverName":"overlay","graphRoot":"/var/lib/hatch/podman-storage/storage"}}`))
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
	limits := (*created)["resource_limits"].(map[string]any)
	memory := limits["memory"].(map[string]any)
	if memory["limit"] != float64(512<<20) || memory["swap"] != float64(1024<<20) || limits["pids"].(map[string]any)["limit"] != float64(podmanPidsLimit) {
		t.Fatalf("resource limits = %v", limits)
	}
	if size := (*created)["storage_opts"].(map[string]any)["size"]; size != "5G" {
		t.Fatalf("disk size limit = %v", size)
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

// sizeQueries counts ?size=true inspections served by fakeLibpod.
var sizeQueries int

func TestPodmanReportsCachedDiskUsage(t *testing.T) {
	socket, _ := fakeLibpod(t)
	runtime := NewPodman(PodmanConfig{Socket: socket, Network: "podman"})
	ctx := context.Background()
	for range 3 {
		state, err := runtime.State(ctx, "svc")
		if err != nil {
			t.Fatal(err)
		}
		if state.DiskBytes != 512<<20 {
			t.Fatalf("disk bytes = %d", state.DiskBytes)
		}
	}
	if sizeQueries != 1 {
		t.Fatalf("root fs measured %d times, want once per TTL", sizeQueries)
	}
}

func TestPodmanRequiresProjectQuota(t *testing.T) {
	socket, _ := fakeLibpod(t)
	runtime := NewPodman(PodmanConfig{Socket: socket, Network: "podman"})
	var asked string
	mounts := map[string]mountEntry{
		"quota":   {Point: "/var/lib/hatch/podman-storage", FSType: "xfs", SuperOptions: "rw,attr2,inode64,prjquota"},
		"noquota": {Point: "/", FSType: "xfs", SuperOptions: "rw,attr2,inode64,noquota"},
		"ext4":    {Point: "/", FSType: "ext4", SuperOptions: "rw"},
	}
	for name, entry := range mounts {
		runtime.mount = func(path string) (mountEntry, bool) { asked = path; return entry, true }
		err := runtime.CheckDiskQuota(context.Background())
		if (err == nil) != (name == "quota") {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if asked != "/var/lib/hatch/podman-storage/storage" {
		t.Fatalf("checked %q instead of the graph root", asked)
	}
}
