package agent_test

import (
	"testing"
	"time"

	"vpsbill/internal/hatch/agent"
	"vpsbill/internal/hatch/agent/agenttest"
	"vpsbill/internal/hatch/protocol"
)

// A page asks for an instance and its usage at the same moment; the rates
// must still span the time since an observation made well before.
func TestUsageRatesIgnoreObservationsMadeMomentsAgo(t *testing.T) {
	runtime, nat := agenttest.NewRuntime("lxc"), &agenttest.NAT{}
	service := newService(t, t.TempDir(), runtime, nat)
	if _, err := call[protocol.EnsureResult](t, service, protocol.MethodEnsure, spec); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	agent.SetClock(service, func() time.Time { return now })
	name := protocol.NameParams{Name: "svc-1"}

	runtime.SetCounters("svc-1", 0, 0)
	if _, err := call[protocol.Instance](t, service, protocol.MethodGet, name); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	runtime.SetCounters("svc-1", 10000, 5000)
	if _, err := call[protocol.Instance](t, service, protocol.MethodGet, name); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Millisecond)
	usage, err := call[map[string]float64](t, service, protocol.MethodUsage, name)
	if err != nil {
		t.Fatal(err)
	}
	if rx := usage["network_rx_bps"]; rx < 990 || rx > 1000 {
		t.Fatalf("rx rate = %v, want about 1000 B/s over the last 10 s", rx)
	}
}

// Without any earlier observation, usage looks twice instead of reporting
// zero rates.
func TestUsageWithoutHistoryLooksTwice(t *testing.T) {
	runtime, nat, dir := agenttest.NewRuntime("lxc"), &agenttest.NAT{}, t.TempDir()
	if _, err := call[protocol.EnsureResult](t, newService(t, dir, runtime, nat), protocol.MethodEnsure, spec); err != nil {
		t.Fatal(err)
	}
	service := newService(t, dir, runtime, nat) // a restarted agent: no observations yet
	agent.SetFirstSampleWait(service, 50*time.Millisecond)
	started := time.Now()
	if _, err := call[map[string]float64](t, service, protocol.MethodUsage, protocol.NameParams{Name: "svc-1"}); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) < 50*time.Millisecond {
		t.Fatal("usage did not take a second look")
	}
	started = time.Now()
	if _, err := call[map[string]float64](t, service, protocol.MethodUsage, protocol.NameParams{Name: "svc-1"}); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) >= 50*time.Millisecond {
		t.Fatal("usage waited although it had observed the instance moments ago")
	}
}
