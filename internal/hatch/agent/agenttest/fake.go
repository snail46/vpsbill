// Package agenttest provides in-memory runtime and NAT fakes for tests.
package agenttest

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"sync"

	"vpsbill/internal/hatch/agent"
	"vpsbill/internal/hatch/protocol"
)

type Instance struct {
	Spec     agent.RuntimeSpec
	Status   string
	Password string
	RXBytes  int64
	TXBytes  int64
}

// Runtime is an in-memory agent.Runtime.
type Runtime struct {
	Kind string

	mu        sync.Mutex
	Instances map[string]*Instance
	Creates   int
	// FailExec makes the next n Exec calls fail, as while an instance boots.
	FailExec int
}

func NewRuntime(kind string) *Runtime {
	return &Runtime{Kind: kind, Instances: map[string]*Instance{}}
}

func (r *Runtime) Virtualization() string { return r.Kind }

func (r *Runtime) Images(context.Context) ([]protocol.Image, error) {
	return []protocol.Image{{ID: "debian12", Name: "debian12", Virtualization: r.Kind}}, nil
}

func (r *Runtime) Network(context.Context) (agent.NetworkInfo, error) {
	return agent.NetworkInfo{
		IPv4: netip.MustParsePrefix("10.20.30.0/24"), IPv4Gateway: netip.MustParseAddr("10.20.30.1"),
		IPv6: netip.MustParsePrefix("2001:db8:1::/64"), IPv6Gateway: netip.MustParseAddr("2001:db8:1::1"),
	}, nil
}

func (r *Runtime) Create(_ context.Context, spec agent.RuntimeSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.Instances[spec.Name]; exists {
		return errors.New("already exists")
	}
	r.Creates++
	r.Instances[spec.Name] = &Instance{Spec: spec, Status: "running"}
	return nil
}

func (r *Runtime) State(_ context.Context, name string) (agent.RuntimeState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, ok := r.Instances[name]
	if !ok {
		return agent.RuntimeState{}, agent.ErrInstanceNotFound
	}
	return agent.RuntimeState{Status: instance.Status, IPv4: instance.Spec.IPv4.String(), RXBytes: instance.RXBytes, TXBytes: instance.TXBytes}, nil
}

func (r *Runtime) setStatus(name, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, ok := r.Instances[name]
	if !ok {
		return agent.ErrInstanceNotFound
	}
	instance.Status = status
	return nil
}

func (r *Runtime) Start(_ context.Context, name string) error   { return r.setStatus(name, "running") }
func (r *Runtime) Stop(_ context.Context, name string) error    { return r.setStatus(name, "stopped") }
func (r *Runtime) Restart(_ context.Context, name string) error { return r.setStatus(name, "running") }

// Pause and Resume reject repeated calls, like Incus and Podman do.
func (r *Runtime) Pause(ctx context.Context, name string) error {
	if state, err := r.State(ctx, name); err == nil && state.Status == "paused" {
		return errors.New("the container is already frozen")
	}
	return r.setStatus(name, "paused")
}

func (r *Runtime) Resume(ctx context.Context, name string) error {
	if state, err := r.State(ctx, name); err == nil && state.Status != "paused" {
		return errors.New("the container is not frozen")
	}
	return r.setStatus(name, "running")
}

func (r *Runtime) Delete(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.Instances, name)
	return nil
}

func (r *Runtime) Exec(_ context.Context, name, _ string, env map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, ok := r.Instances[name]
	if !ok {
		return agent.ErrInstanceNotFound
	}
	if instance.Status != "running" {
		return errors.New("can only exec in running instances")
	}
	if r.FailExec > 0 {
		r.FailExec--
		return errors.New("instance still booting")
	}
	instance.Password = env["HATCH_PASSWORD"]
	return nil
}

// SetCounters sets the interface counters reported by State.
func (r *Runtime) SetCounters(name string, rx, tx int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Instances[name].RXBytes, r.Instances[name].TXBytes = rx, tx
}

func (r *Runtime) Get(name string) (Instance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	instance, ok := r.Instances[name]
	if !ok {
		return Instance{}, false
	}
	return *instance, true
}

// NAT records the last applied ruleset and can be told to fail.
type NAT struct {
	mu      sync.Mutex
	Ruleset string
	Fail    bool
}

func (n *NAT) Apply(_ context.Context, ruleset string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.Fail {
		return errors.New("nft rejected the ruleset")
	}
	n.Ruleset = ruleset
	return nil
}

func (n *NAT) Last() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.Ruleset
}

// Terminal returns an echo shell: it prints a prompt and echoes input back.
func (r *Runtime) Terminal(_ context.Context, name string, cols, rows int) (agent.TerminalSession, error) {
	r.mu.Lock()
	_, ok := r.Instances[name]
	r.mu.Unlock()
	if !ok {
		return nil, agent.ErrInstanceNotFound
	}
	terminal := &EchoTerminal{output: make(chan []byte, 64), done: make(chan struct{}), Cols: cols, Rows: rows}
	terminal.output <- []byte("hatch$ ")
	return terminal, nil
}

type EchoTerminal struct {
	mu      sync.Mutex
	output  chan []byte
	done    chan struct{}
	once    sync.Once
	pending []byte
	Cols    int
	Rows    int
}

func (t *EchoTerminal) Read(p []byte) (int, error) {
	for len(t.pending) == 0 {
		select {
		case data := <-t.output:
			t.pending = data
		case <-t.done:
			return 0, io.EOF
		}
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	return n, nil
}

func (t *EchoTerminal) Write(p []byte) (int, error) {
	select {
	case t.output <- append([]byte("echo:"), p...):
		return len(p), nil
	case <-t.done:
		return 0, io.ErrClosedPipe
	}
}

func (t *EchoTerminal) Resize(cols, rows int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Cols, t.Rows = cols, rows
	return nil
}

func (t *EchoTerminal) Close() error {
	t.once.Do(func() { close(t.done) })
	return nil
}
