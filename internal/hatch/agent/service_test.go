package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"vpsbill/internal/hatch/agent"
	"vpsbill/internal/hatch/agent/agenttest"
	"vpsbill/internal/hatch/protocol"
)

func newService(t *testing.T, dir string, runtime *agenttest.Runtime, nat *agenttest.NAT) *agent.Service {
	t.Helper()
	store, err := agent.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	config := agent.Config{PublicIPv4: "203.0.113.10", PortRangeStart: 30000, PortRangeEnd: 30009, StateDir: dir}
	service := agent.NewService(config, "test", store, []agent.Runtime{runtime}, nat, slog.New(slog.NewTextHandler(io.Discard, nil)))
	agent.SetPasswordRetry(service, 0)
	return service
}

func call[T any](t *testing.T, service *agent.Service, method string, params any) (T, error) {
	t.Helper()
	encoded, _ := json.Marshal(params)
	var result T
	value, err := service.Handle(context.Background(), method, encoded)
	if err != nil {
		return result, err
	}
	data, _ := json.Marshal(value)
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result, nil
}

var spec = protocol.CreateSpec{Name: "svc-1", Virtualization: "lxc", TemplateID: "debian12", VCPU: 1, RAMMB: 512, DiskGB: 10, AssignNAT: true, PortMappingCount: 3, MonthlyTrafficGB: 1}

func TestEnsureCreatesOnceWithPasswordAndSSHForward(t *testing.T) {
	runtime, nat := agenttest.NewRuntime("lxc"), &agenttest.NAT{}
	service := newService(t, t.TempDir(), runtime, nat)
	runtime.FailExec = 2 // the password is retried while the instance boots

	first, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec)
	if err != nil {
		t.Fatal(err)
	}
	created, _ := runtime.Get("svc-1")
	if !first.Created || first.Instance.Password == "" || created.Password != first.Instance.Password {
		t.Fatalf("password not applied: %+v / %+v", first, created)
	}
	if created.Spec.IPv4.String() != "10.20.30.254" || first.Instance.PublicIPv4 != "203.0.113.10" {
		t.Fatalf("unexpected addressing: %v %+v", created.Spec.IPv4, first.Instance)
	}
	if first.Instance.SSHPort == 0 || !strings.Contains(nat.Last(), "dnat to 10.20.30.254:22") {
		t.Fatalf("SSH forward missing: %+v\n%s", first.Instance, nat.Last())
	}

	second, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec)
	if err != nil || second.Created || second.Instance.Password != "" || runtime.Creates != 1 {
		t.Fatalf("retry must not recreate or reveal a new password: %+v creates=%d err=%v", second, runtime.Creates, err)
	}
}

func TestEnsureResumesAfterCrashBeforePassword(t *testing.T) {
	dir := t.TempDir()
	runtime, nat := agenttest.NewRuntime("lxc"), &agenttest.NAT{}
	service := newService(t, dir, runtime, nat)
	runtime.FailExec = 100
	if _, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec); err == nil {
		t.Fatal("expected password failure")
	}
	// A restarted agent (new service, same state dir) finishes the job.
	runtime.FailExec = 0
	restarted := newService(t, dir, runtime, nat)
	result, err := call[protocol.EnsureResult](t, restarted, protocol.MethodEnsure, spec)
	if err != nil || !result.Created || result.Instance.Password == "" || runtime.Creates != 1 {
		t.Fatalf("resume failed: %+v creates=%d err=%v", result, runtime.Creates, err)
	}
}

func TestEnsureStartsInstanceLeftStopped(t *testing.T) {
	dir := t.TempDir()
	runtime, nat := agenttest.NewRuntime("lxc"), &agenttest.NAT{}
	service := newService(t, dir, runtime, nat)
	runtime.FailExec = 100
	if _, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec); err == nil {
		t.Fatal("expected password failure")
	}
	// The create was interrupted and the instance stopped before its
	// password was set; the retry starts it instead of failing forever.
	runtime.FailExec = 0
	if err := runtime.Stop(context.Background(), spec.Name); err != nil {
		t.Fatal(err)
	}
	result, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec)
	if err != nil || result.Instance.Password == "" || result.Instance.Status != "running" || runtime.Creates != 1 {
		t.Fatalf("retry failed: %+v creates=%d err=%v", result, runtime.Creates, err)
	}
}

func TestEnsureRetryTakesCorrectedSpec(t *testing.T) {
	runtime := agenttest.NewRuntime("lxc")
	service := newService(t, t.TempDir(), runtime, &agenttest.NAT{})
	runtime.FailCreate = 1
	wrong := spec
	wrong.TemplateID = "missing-image"
	if _, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, wrong); err == nil {
		t.Fatal("expected create failure")
	}
	if _, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Instances[spec.Name].Spec.Image; got != spec.TemplateID {
		t.Fatalf("retry created %q, want %q", got, spec.TemplateID)
	}
}

func TestUnmanagedInstanceIsNotAdopted(t *testing.T) {
	runtime := agenttest.NewRuntime("lxc")
	runtime.Instances["svc-1"] = &agenttest.Instance{Status: "running"}
	service := newService(t, t.TempDir(), runtime, &agenttest.NAT{})
	_, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec)
	var protocolErr *protocol.Error
	if !errors.As(err, &protocolErr) || protocolErr.Code != protocol.CodeConflict {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestPortMappingLimitsAndRollback(t *testing.T) {
	runtime, nat := agenttest.NewRuntime("lxc"), &agenttest.NAT{}
	service := newService(t, t.TempDir(), runtime, nat)
	created, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec)
	if err != nil {
		t.Fatal(err)
	}
	// Pick public ports around the randomly assigned SSH port.
	var free []int
	for port := 30000; port <= 30009; port++ {
		if port != created.Instance.SSHPort {
			free = append(free, port)
		}
	}
	add := func(port int) ([]protocol.PortMapping, error) {
		return call[[]protocol.PortMapping](t, service, protocol.MethodAddPortMapping, protocol.PortMappingParams{Name: "svc-1", Mapping: protocol.PortMapping{PublicPort: port, ContainerPort: 80, Protocol: "both"}})
	}
	if _, err := add(29999); err == nil {
		t.Fatal("port outside the range must be rejected")
	}
	mappings, err := add(free[0])
	if err != nil || len(mappings) != 2 || !strings.Contains(nat.Last(), fmt.Sprintf("udp dport %d", free[0])) {
		t.Fatalf("add failed: %v %v\n%s", mappings, err, nat.Last())
	}
	if _, err := add(free[0]); err == nil {
		t.Fatal("duplicate public port must be rejected")
	}
	nat.Fail = true
	if _, err := add(free[1]); err == nil {
		t.Fatal("expected nft failure")
	}
	nat.Fail = false
	instance, _ := call[protocol.Instance](t, service, protocol.MethodGet, protocol.NameParams{Name: "svc-1"})
	if len(instance.PortMappings) != 2 {
		t.Fatalf("failed apply must roll back the stored mapping: %+v", instance.PortMappings)
	}
	if _, err := add(free[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := add(free[3]); err == nil {
		t.Fatal("limit of 3 mappings must be enforced")
	}
	mappings, err = call[[]protocol.PortMapping](t, service, protocol.MethodUpdatePortMapping, protocol.PortMappingParams{Name: "svc-1", Index: 1, Mapping: protocol.PortMapping{ContainerPort: 8080, Protocol: "tcp"}})
	if err != nil || mappings[1].PublicPort != free[0] || mappings[1].ContainerPort != 8080 {
		t.Fatalf("update must keep the public port: %v %v", mappings, err)
	}
}

func TestDeleteRemovesInstanceAndRules(t *testing.T) {
	runtime, nat := agenttest.NewRuntime("lxc"), &agenttest.NAT{}
	service := newService(t, t.TempDir(), runtime, nat)
	if _, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := call[any](t, service, protocol.MethodDelete, protocol.NameParams{Name: "svc-1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := runtime.Get("svc-1"); ok || strings.Contains(nat.Last(), "dnat to") {
		t.Fatalf("instance or rules left behind:\n%s", nat.Last())
	}
	if _, err := call[any](t, service, protocol.MethodDelete, protocol.NameParams{Name: "svc-1"}); err != nil {
		t.Fatalf("deleting twice must succeed: %v", err)
	}
	_, err := call[protocol.Instance](t, service, protocol.MethodGet, protocol.NameParams{Name: "svc-1"})
	var protocolErr *protocol.Error
	if !errors.As(err, &protocolErr) || protocolErr.Code != protocol.CodeNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}
}

func TestTrafficSurvivesCounterReset(t *testing.T) {
	runtime := agenttest.NewRuntime("lxc")
	service := newService(t, t.TempDir(), runtime, &agenttest.NAT{})
	if _, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec); err != nil {
		t.Fatal(err)
	}
	runtime.SetCounters("svc-1", 600<<20, 100<<20)
	if _, err := call[map[string]any](t, service, protocol.MethodTraffic, protocol.NameParams{Name: "svc-1"}); err != nil {
		t.Fatal(err)
	}
	runtime.SetCounters("svc-1", 400<<20, 0) // instance restarted
	traffic, err := call[map[string]any](t, service, protocol.MethodTraffic, protocol.NameParams{Name: "svc-1"})
	if err != nil {
		t.Fatal(err)
	}
	if traffic["total_used_bytes"] != float64(1100<<20) || traffic["exceeded"] != true {
		t.Fatalf("unexpected traffic %v", traffic)
	}
}

func TestUnsupportedVirtualization(t *testing.T) {
	service := newService(t, t.TempDir(), agenttest.NewRuntime("lxc"), &agenttest.NAT{})
	podmanSpec := spec
	podmanSpec.Virtualization = "podman"
	_, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, podmanSpec)
	var protocolErr *protocol.Error
	if !errors.As(err, &protocolErr) || protocolErr.Code != protocol.CodeUnsupported {
		t.Fatalf("expected unsupported, got %v", err)
	}
}

func TestIPv6AssignmentAndNeighbourProxy(t *testing.T) {
	dir := t.TempDir()
	store, err := agent.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	runtime := agenttest.NewRuntime("lxc")
	config := agent.Config{PublicIPv4: "203.0.113.10", PortRangeStart: 30000, PortRangeEnd: 30009, StateDir: dir, IPv6NDPInterface: "eth0"}
	service := agent.NewService(config, "test", store, []agent.Runtime{runtime}, &agenttest.NAT{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	agent.SetPasswordRetry(service, 0)
	var commands []string
	agent.SetCommandRunner(service, func(_ context.Context, name string, args ...string) (string, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return "", nil
	})
	withIPv6 := spec
	withIPv6.AssignIPv6 = true
	result, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, withIPv6)
	if err != nil {
		t.Fatal(err)
	}
	address := result.Instance.IPv6
	created, _ := runtime.Get("svc-1")
	if !strings.HasPrefix(address, "2001:db8:1:") || created.Spec.IPv6.String() != address {
		t.Fatalf("IPv6 not assigned: %q vs %v", address, created.Spec.IPv6)
	}
	if len(commands) != 1 || commands[0] != "ip -6 neigh replace proxy "+address+" dev eth0" {
		t.Fatalf("neighbour proxy not published: %v", commands)
	}
	if _, err := call[any](t, service, protocol.MethodDelete, protocol.NameParams{Name: "svc-1"}); err != nil {
		t.Fatal(err)
	}
	if commands[len(commands)-1] != "ip -6 neigh del proxy "+address+" dev eth0" {
		t.Fatalf("neighbour proxy not withdrawn: %v", commands)
	}
}

func TestSuspendAndResumeAreIdempotent(t *testing.T) {
	runtime, nat := agenttest.NewRuntime("lxc"), &agenttest.NAT{}
	service := newService(t, t.TempDir(), runtime, nat)
	if _, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec); err != nil {
		t.Fatal(err)
	}
	name := protocol.NameParams{Name: "svc-1"}
	for i := 0; i < 2; i++ {
		if _, err := call[any](t, service, protocol.MethodSuspend, name); err != nil {
			t.Fatalf("suspend #%d: %v", i+1, err)
		}
	}
	if instance, _ := runtime.Get("svc-1"); instance.Status != "paused" {
		t.Fatalf("status after suspend = %q", instance.Status)
	}
	for i := 0; i < 2; i++ {
		if _, err := call[any](t, service, protocol.MethodResume, name); err != nil {
			t.Fatalf("resume #%d: %v", i+1, err)
		}
	}
	if instance, _ := runtime.Get("svc-1"); instance.Status != "running" {
		t.Fatalf("status after resume = %q", instance.Status)
	}
}

func TestEnsureTunesLXCNetwork(t *testing.T) {
	runtime, nat := agenttest.NewRuntime("lxc"), &agenttest.NAT{}
	service := newService(t, t.TempDir(), runtime, nat)
	result, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec)
	if err != nil || result.Instance.Password == "" {
		t.Fatalf("ensure: %+v %v", result, err)
	}
	// One script sets the password, one writes the network settings; the
	// second must not clear the password.
	if execs := runtime.Instances[spec.Name].Execs; execs != 2 {
		t.Fatalf("execs = %d, want password and network tuning", execs)
	}
	if runtime.Instances[spec.Name].Password != result.Instance.Password {
		t.Fatal("network tuning overwrote the password")
	}
}
