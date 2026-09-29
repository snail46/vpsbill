package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"vpsbill/internal/clock"
	"vpsbill/internal/hatch/protocol"
)

var validName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,62}$`)

// Service executes protocol requests against the host.
type Service struct {
	config   Config
	version  string
	store    *Store
	runtimes map[string]Runtime
	nat      NATApplier
	logger   *slog.Logger
	now      func() time.Time
	// passwordRetry is the wait between root password attempts while a new
	// instance boots.
	passwordRetry time.Duration
	// run executes host commands (ip -6 neigh); replaced in tests.
	run CommandRunner

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
	natMu   sync.Mutex

	samplesMu sync.Mutex
	samples   map[string]sample
}

type sample struct {
	at    time.Time
	state RuntimeState
}

func NewService(config Config, version string, store *Store, runtimes []Runtime, nat NATApplier, logger *slog.Logger) *Service {
	byKind := make(map[string]Runtime, len(runtimes))
	for _, runtime := range runtimes {
		byKind[runtime.Virtualization()] = runtime
	}
	return &Service{
		config: config, version: version, store: store, runtimes: byKind, nat: nat, logger: logger,
		now: time.Now, passwordRetry: 3 * time.Second, run: runCommand, locks: map[string]*sync.Mutex{}, samples: map[string]sample{},
	}
}

// Init restores NAT rules and IPv6 neighbour proxies after an agent or
// host restart.
func (s *Service) Init(ctx context.Context) error {
	if iface := s.config.IPv6NDPInterface; iface != "" {
		if err := os.WriteFile("/proc/sys/net/ipv6/conf/"+iface+"/proxy_ndp", []byte("1"), 0o644); err != nil {
			return fmt.Errorf("enable proxy_ndp on %s: %w", iface, err)
		}
		for _, record := range s.store.List() {
			if err := s.proxyNDP(ctx, record.IPv6, true); err != nil {
				return err
			}
		}
	}
	return s.applyNAT(ctx)
}

// proxyNDP publishes (or withdraws) an instance IPv6 address on the uplink.
func (s *Service) proxyNDP(ctx context.Context, address string, publish bool) error {
	iface := s.config.IPv6NDPInterface
	if iface == "" || address == "" {
		return nil
	}
	if !publish {
		_, _ = s.run(ctx, "ip", "-6", "neigh", "del", "proxy", address, "dev", iface)
		return nil
	}
	if _, err := s.run(ctx, "ip", "-6", "neigh", "replace", "proxy", address, "dev", iface); err != nil {
		return fmt.Errorf("publish IPv6 %s: %w", address, err)
	}
	return nil
}

func errorf(code, format string, args ...any) *protocol.Error {
	return &protocol.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Handle dispatches one request. Errors are returned as *protocol.Error
// where the caller should see a specific code; anything else is internal.
func (s *Service) Handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	decode := func(target any) error {
		if err := json.Unmarshal(params, target); err != nil {
			return errorf(protocol.CodeInvalid, "invalid params: %v", err)
		}
		return nil
	}
	var name protocol.NameParams
	switch method {
	case protocol.MethodHostInfo:
		return s.hostInfo(ctx)
	case protocol.MethodImages:
		return s.images(ctx)
	case protocol.MethodEnsure:
		var spec protocol.CreateSpec
		if err := decode(&spec); err != nil {
			return nil, err
		}
		return s.ensure(ctx, spec)
	case protocol.MethodPower:
		var power protocol.PowerParams
		if err := decode(&power); err != nil {
			return nil, err
		}
		return nil, s.power(ctx, power.Name, power.Action)
	case protocol.MethodReinstall:
		var reinstall protocol.ReinstallParams
		if err := decode(&reinstall); err != nil {
			return nil, err
		}
		return nil, s.reinstall(ctx, reinstall)
	case protocol.MethodResetPassword:
		var password protocol.PasswordParams
		if err := decode(&password); err != nil {
			return nil, err
		}
		return s.resetPassword(ctx, password)
	case protocol.MethodAddPortMapping, protocol.MethodUpdatePortMapping, protocol.MethodDeletePortMapping:
		var mapping protocol.PortMappingParams
		if err := decode(&mapping); err != nil {
			return nil, err
		}
		return s.mutateMapping(ctx, method, mapping)
	}
	if err := decode(&name); err != nil {
		return nil, err
	}
	switch method {
	case protocol.MethodGet:
		return s.get(ctx, name.Name)
	case protocol.MethodDelete:
		return nil, s.delete(ctx, name.Name)
	case protocol.MethodSuspend:
		// Pause and resume are idempotent so a retried job after a lost
		// reply succeeds instead of failing on "already frozen".
		return nil, s.withInstance(ctx, name.Name, func(runtime Runtime, _ InstanceRecord) error {
			if state, err := runtime.State(ctx, name.Name); err == nil && state.Status == "paused" {
				return nil
			}
			return runtime.Pause(ctx, name.Name)
		})
	case protocol.MethodResume:
		return nil, s.withInstance(ctx, name.Name, func(runtime Runtime, _ InstanceRecord) error {
			if state, err := runtime.State(ctx, name.Name); err == nil && state.Status != "paused" {
				return nil
			}
			return runtime.Resume(ctx, name.Name)
		})
	case protocol.MethodUsage:
		return s.usage(ctx, name.Name)
	case protocol.MethodTraffic:
		return s.traffic(ctx, name.Name)
	case protocol.MethodFreePort:
		port, err := s.freePort()
		return protocol.PortParams{Port: port}, err
	}
	return nil, errorf(protocol.CodeUnsupported, "unknown method %q", method)
}

func (s *Service) lock(name string) func() {
	s.locksMu.Lock()
	mutex, ok := s.locks[name]
	if !ok {
		mutex = &sync.Mutex{}
		s.locks[name] = mutex
	}
	s.locksMu.Unlock()
	mutex.Lock()
	return mutex.Unlock
}

func (s *Service) runtime(virtualization string) (Runtime, error) {
	runtime, ok := s.runtimes[virtualization]
	if !ok {
		return nil, errorf(protocol.CodeUnsupported, "virtualization %q is not enabled on this host", virtualization)
	}
	return runtime, nil
}

func (s *Service) hostInfo(ctx context.Context) (protocol.HostInfo, error) {
	hostname, _ := os.Hostname()
	detected := detectCapacity(s.config.StateDir)
	health := readHealth()
	// Instances live in each runtime's own storage, so together those make
	// up the disk capacity.
	var storageTotal int64
	var quotaErrors map[string]string
	for _, kind := range sortedKeys(s.runtimes) {
		reporter, ok := s.runtimes[kind].(StorageReporter)
		if !ok {
			continue
		}
		if total, used, err := reporter.Storage(ctx); err == nil && total > 0 {
			storageTotal += total
			health.Disks = append(health.Disks, protocol.DiskUsage{Name: kind, TotalGB: total >> 30, UsedGB: used >> 30})
		}
		if err := reporter.CheckDiskQuota(ctx); err != nil {
			if quotaErrors == nil {
				quotaErrors = map[string]string{}
			}
			quotaErrors[kind] = err.Error()
		}
	}
	health.Disks = append(health.Disks, hostDisks()...)
	if storageTotal > 0 {
		detected.DiskGB = storageTotal >> 30
	}
	details := map[string]any{"instances": len(s.store.List()), "port_range": []int{s.config.PortRangeStart, s.config.PortRangeEnd}}
	for kind, runtime := range s.runtimes {
		if network, err := runtime.Network(ctx); err == nil {
			details[kind+"_network"] = network.IPv4.String()
			if network.IPv6.IsValid() {
				details[kind+"_network_ipv6"] = network.IPv6.String()
			}
		} else {
			details[kind+"_error"] = err.Error()
		}
	}
	return protocol.HostInfo{
		Hostname: hostname, AgentVersion: s.version, Runtimes: s.config.Runtimes(), PublicIPv4: s.config.PublicIPv4,
		Capacity: lowerCapacity(detected, s.config.Capacity), Detected: detected, MachineID: machineID(),
		Health: health, QuotaErrors: quotaErrors, Details: details,
	}, nil
}

func (s *Service) images(ctx context.Context) ([]protocol.Image, error) {
	result := []protocol.Image{}
	for _, runtime := range s.runtimes {
		images, err := runtime.Images(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, images...)
	}
	return result, nil
}

// ensure creates the instance once. Every step records progress first, so a
// retry after a crash or timeout resumes instead of creating a duplicate.
func (s *Service) ensure(ctx context.Context, spec protocol.CreateSpec) (protocol.EnsureResult, error) {
	if !validName.MatchString(spec.Name) {
		return protocol.EnsureResult{}, errorf(protocol.CodeInvalid, "invalid instance name %q", spec.Name)
	}
	if spec.TemplateID == "" || spec.VCPU < 1 || spec.RAMMB < 64 || spec.DiskGB < 1 {
		return protocol.EnsureResult{}, errorf(protocol.CodeInvalid, "template, vcpu, ram and disk are required")
	}
	runtime, err := s.runtime(spec.Virtualization)
	if err != nil {
		return protocol.EnsureResult{}, err
	}
	defer s.lock(spec.Name)()

	record, exists := s.store.Get(spec.Name)
	state, stateErr := runtime.State(ctx, spec.Name)
	runtimeHas := stateErr == nil
	if stateErr != nil && !errors.Is(stateErr, ErrInstanceNotFound) {
		return protocol.EnsureResult{}, fmt.Errorf("inspect instance: %w", stateErr)
	}
	if !exists && runtimeHas {
		return protocol.EnsureResult{}, errorf(protocol.CodeConflict, "instance %s exists but is not managed by hatch", spec.Name)
	}
	created := false
	if !exists {
		record, err = s.newRecord(ctx, runtime, spec)
		if err != nil {
			return protocol.EnsureResult{}, err
		}
	}
	if !runtimeHas {
		// Every instance must have its disk size enforced, or one customer
		// could fill the host.
		if reporter, ok := runtime.(StorageReporter); ok {
			if err := reporter.CheckDiskQuota(ctx); err != nil {
				return protocol.EnsureResult{}, errorf(protocol.CodeUnsupported, "%s", err.Error())
			}
		}
		if err := runtime.Create(ctx, runtimeSpec(record)); err != nil {
			return protocol.EnsureResult{}, fmt.Errorf("create instance: %w", err)
		}
		created = true
	}
	// An instance left stopped by an interrupted create cannot take its
	// password until it runs again.
	if !created && record.PasswordPending() && state.Status != "running" {
		if err := runtime.Start(ctx, spec.Name); err != nil {
			return protocol.EnsureResult{}, fmt.Errorf("start instance: %w", err)
		}
	}
	password := ""
	if created || record.PasswordPending() {
		password = spec.Password
		if password == "" {
			if password, err = randomPassword(); err != nil {
				return protocol.EnsureResult{}, err
			}
		}
		if err := s.setPassword(ctx, runtime, spec.Name, password, 5); err != nil {
			return protocol.EnsureResult{}, err
		}
		if err := s.store.Update(spec.Name, func(current *InstanceRecord) (*InstanceRecord, error) {
			current.PasswordSet = true
			return current, nil
		}); err != nil {
			return protocol.EnsureResult{}, err
		}
		created = true
	}
	if current, _ := s.store.Get(spec.Name); current.IPv6 != "" {
		if err := s.proxyNDP(ctx, current.IPv6, true); err != nil {
			return protocol.EnsureResult{}, err
		}
	}
	if spec.AssignNAT {
		if err := s.ensureSSHMapping(ctx, spec.Name); err != nil {
			return protocol.EnsureResult{}, err
		}
	}
	instance, err := s.get(ctx, spec.Name)
	if err != nil {
		return protocol.EnsureResult{}, err
	}
	instance.Password = password
	return protocol.EnsureResult{Instance: instance, Created: created}, nil
}

func (s *Service) newRecord(ctx context.Context, runtime Runtime, spec protocol.CreateSpec) (InstanceRecord, error) {
	network, err := runtime.Network(ctx)
	if err != nil {
		return InstanceRecord{}, fmt.Errorf("read %s network: %w", runtime.Virtualization(), err)
	}
	address, err := s.allocateAddress(network)
	if err != nil {
		return InstanceRecord{}, err
	}
	var ipv6 string
	if spec.AssignIPv6 {
		if !network.IPv6.IsValid() {
			return InstanceRecord{}, errorf(protocol.CodeInvalid, "%s network has no IPv6 subnet", runtime.Virtualization())
		}
		allocated, err := s.allocateIPv6(network)
		if err != nil {
			return InstanceRecord{}, err
		}
		ipv6 = allocated.String()
	}
	limit := 0
	if spec.AssignNAT {
		limit = max(spec.PortMappingCount, 1)
	}
	record := InstanceRecord{
		Name: spec.Name, Virtualization: spec.Virtualization, Template: spec.TemplateID, VCPU: spec.VCPU, RAMMB: spec.RAMMB,
		DiskGB: spec.DiskGB, NetworkDownMbps: spec.NetworkDownMbps, NetworkUpMbps: spec.NetworkUpMbps,
		MonthlyTrafficGB: spec.MonthlyTrafficGB, PortMappingLimit: limit, PrivateIPv4: address.String(), IPv6: ipv6,
		Mappings: []protocol.PortMapping{}, CreatedAt: s.now().UTC(),
	}
	err = s.store.Update(spec.Name, func(*InstanceRecord) (*InstanceRecord, error) { return &record, nil })
	return record, err
}

// allocateAddress picks the highest free address in the runtime's bridge
// subnet, leaving the low range to the bridge's own DHCP pool.
func (s *Service) allocateAddress(network NetworkInfo) (netip.Addr, error) {
	prefix := network.IPv4
	used := map[netip.Addr]bool{network.IPv4Gateway: true}
	for _, record := range s.store.List() {
		if address, err := netip.ParseAddr(record.PrivateIPv4); err == nil {
			used[address] = true
		}
	}
	last := lastAddress(prefix)
	for address := last.Prev(); address.IsValid() && prefix.Contains(address); address = address.Prev() {
		if address == prefix.Addr() {
			break
		}
		if !used[address] {
			return address, nil
		}
	}
	return netip.Addr{}, errorf(protocol.CodeConflict, "no free address in %s", prefix)
}

// allocateIPv6 picks a random unused address in the IPv6 subnet, so
// neighbouring customers cannot be found by counting up.
func (s *Service) allocateIPv6(network NetworkInfo) (netip.Addr, error) {
	used := map[netip.Addr]bool{network.IPv6Gateway: true}
	for _, record := range s.store.List() {
		if address, err := netip.ParseAddr(record.IPv6); err == nil {
			used[address] = true
		}
	}
	hostBits := min(128-network.IPv6.Bits(), 64)
	if hostBits < 8 {
		return netip.Addr{}, errorf(protocol.CodeConflict, "IPv6 subnet %s is too small", network.IPv6)
	}
	base := network.IPv6.Masked().Addr().As16()
	for range 64 {
		suffix, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), uint(hostBits)))
		if err != nil {
			return netip.Addr{}, err
		}
		value := suffix.Uint64()
		if value < 2 {
			continue
		}
		bytes := base
		for i := range 8 {
			bytes[15-i] |= byte(value >> (8 * i))
		}
		if candidate := netip.AddrFrom16(bytes); !used[candidate] {
			return candidate, nil
		}
	}
	return netip.Addr{}, errorf(protocol.CodeConflict, "no free address in %s", network.IPv6)
}

// lastAddress returns the broadcast address of an IPv4 prefix.
func lastAddress(prefix netip.Prefix) netip.Addr {
	bytes := prefix.Masked().Addr().As4()
	hostBits := 32 - prefix.Bits()
	value := uint32(bytes[0])<<24 | uint32(bytes[1])<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
	value |= (1 << hostBits) - 1
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)})
}

func runtimeSpec(record InstanceRecord) RuntimeSpec {
	address, _ := netip.ParseAddr(record.PrivateIPv4)
	ipv6, _ := netip.ParseAddr(record.IPv6)
	return RuntimeSpec{
		Name: record.Name, Image: record.Template, VCPU: record.VCPU, RAMMB: record.RAMMB, DiskGB: record.DiskGB,
		IPv4: address, IPv6: ipv6, NetworkDownMbps: record.NetworkDownMbps, NetworkUpMbps: record.NetworkUpMbps,
	}
}

// setPassword retries while a freshly started instance finishes booting.
func (s *Service) setPassword(ctx context.Context, runtime Runtime, name, password string, attempts int) error {
	var err error
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.passwordRetry):
			}
		}
		if err = runtime.Exec(ctx, name, passwordScript, map[string]string{"HATCH_PASSWORD": password}); err == nil {
			return nil
		}
	}
	return fmt.Errorf("set root password: %w", err)
}

func (s *Service) ensureSSHMapping(ctx context.Context, name string) error {
	record, _ := s.store.Get(name)
	for _, mapping := range record.Mappings {
		if mapping.ContainerPort == 22 {
			return nil
		}
	}
	_, err := s.mutateLocked(ctx, protocol.MethodAddPortMapping, protocol.PortMappingParams{Name: name, Mapping: protocol.PortMapping{ContainerPort: 22, Protocol: "tcp", Description: "SSH"}})
	return err
}

func (s *Service) get(ctx context.Context, name string) (protocol.Instance, error) {
	record, ok := s.store.Get(name)
	if !ok {
		return protocol.Instance{}, errorf(protocol.CodeNotFound, "instance %s not found", name)
	}
	runtime, err := s.runtime(record.Virtualization)
	if err != nil {
		return protocol.Instance{}, err
	}
	state, err := runtime.State(ctx, name)
	if errors.Is(err, ErrInstanceNotFound) {
		return protocol.Instance{}, errorf(protocol.CodeNotFound, "instance %s is missing from %s", name, record.Virtualization)
	}
	if err != nil {
		return protocol.Instance{}, err
	}
	s.recordSample(name, state)
	instance := protocol.Instance{
		Name: name, Virtualization: record.Virtualization, Status: state.Status, Template: record.Template,
		PrivateIPv4: record.PrivateIPv4, PublicIPv4: s.config.PublicIPv4, IPv6: record.IPv6, VCPU: record.VCPU, RAMMB: record.RAMMB,
		DiskGB: record.DiskGB, PortMappings: record.Mappings, PortMappingLimit: record.PortMappingLimit,
		MonthlyTrafficGB: record.MonthlyTrafficGB, NetworkDownMbps: record.NetworkDownMbps, NetworkUpMbps: record.NetworkUpMbps,
	}
	for _, mapping := range record.Mappings {
		if mapping.ContainerPort == 22 {
			instance.SSHPort = mapping.PublicPort
			break
		}
	}
	return instance, nil
}

func (s *Service) withInstance(ctx context.Context, name string, fn func(Runtime, InstanceRecord) error) error {
	defer s.lock(name)()
	record, ok := s.store.Get(name)
	if !ok {
		return errorf(protocol.CodeNotFound, "instance %s not found", name)
	}
	runtime, err := s.runtime(record.Virtualization)
	if err != nil {
		return err
	}
	err = fn(runtime, record)
	if errors.Is(err, ErrInstanceNotFound) {
		return errorf(protocol.CodeNotFound, "instance %s is missing from %s", name, record.Virtualization)
	}
	return err
}

func (s *Service) power(ctx context.Context, name, action string) error {
	return s.withInstance(ctx, name, func(runtime Runtime, _ InstanceRecord) error {
		switch action {
		case "start":
			return runtime.Start(ctx, name)
		case "stop":
			return runtime.Stop(ctx, name)
		case "restart":
			return runtime.Restart(ctx, name)
		default:
			return errorf(protocol.CodeInvalid, "unsupported power action %q", action)
		}
	})
}

// delete removes the instance and its record; a missing record is success so
// termination stays retryable.
func (s *Service) delete(ctx context.Context, name string) error {
	unlock := s.lock(name)
	record, ok := s.store.Get(name)
	if !ok {
		unlock()
		return nil
	}
	runtime, err := s.runtime(record.Virtualization)
	if err == nil {
		err = runtime.Delete(ctx, name)
	}
	if err == nil {
		err = s.store.Update(name, func(*InstanceRecord) (*InstanceRecord, error) { return nil, nil })
	}
	if err == nil {
		_ = s.proxyNDP(ctx, record.IPv6, false)
	}
	unlock()
	if err != nil {
		return err
	}
	s.forgetSample(name)
	return s.applyNAT(ctx)
}

// reinstall recreates the instance from a new image, keeping its address and
// port forwards.
func (s *Service) reinstall(ctx context.Context, params protocol.ReinstallParams) error {
	if params.TemplateID == "" || params.Password == "" {
		return errorf(protocol.CodeInvalid, "template and password are required")
	}
	return s.withInstance(ctx, params.Name, func(runtime Runtime, record InstanceRecord) error {
		if err := runtime.Delete(ctx, params.Name); err != nil {
			return err
		}
		record.Template = params.TemplateID
		record.PasswordSet = false
		if err := s.store.Update(params.Name, func(*InstanceRecord) (*InstanceRecord, error) { return &record, nil }); err != nil {
			return err
		}
		if err := runtime.Create(ctx, runtimeSpec(record)); err != nil {
			return fmt.Errorf("recreate instance: %w", err)
		}
		if err := s.setPassword(ctx, runtime, params.Name, params.Password, 5); err != nil {
			return err
		}
		return s.store.Update(params.Name, func(current *InstanceRecord) (*InstanceRecord, error) {
			current.PasswordSet = true
			return current, nil
		})
	})
}

func (s *Service) resetPassword(ctx context.Context, params protocol.PasswordParams) (protocol.PasswordParams, error) {
	password := params.Password
	if password == "" {
		generated, err := randomPassword()
		if err != nil {
			return protocol.PasswordParams{}, err
		}
		password = generated
	}
	err := s.withInstance(ctx, params.Name, func(runtime Runtime, _ InstanceRecord) error {
		return s.setPassword(ctx, runtime, params.Name, password, 1)
	})
	return protocol.PasswordParams{Name: params.Name, Password: password}, err
}

func (s *Service) mutateMapping(ctx context.Context, method string, params protocol.PortMappingParams) ([]protocol.PortMapping, error) {
	defer s.lock(params.Name)()
	return s.mutateLocked(ctx, method, params)
}

// mutateLocked changes one instance's port forwards and applies the full
// ruleset; if nftables rejects it the stored change is rolled back.
func (s *Service) mutateLocked(ctx context.Context, method string, params protocol.PortMappingParams) ([]protocol.PortMapping, error) {
	previous, ok := s.store.Get(params.Name)
	if !ok {
		return nil, errorf(protocol.CodeNotFound, "instance %s not found", params.Name)
	}
	mapping := params.Mapping
	if method != protocol.MethodDeletePortMapping {
		if mapping.ContainerPort < 1 || mapping.ContainerPort > 65535 {
			return nil, errorf(protocol.CodeInvalid, "container port must be 1-65535")
		}
		if mapping.Protocol != "tcp" && mapping.Protocol != "udp" && mapping.Protocol != "both" {
			return nil, errorf(protocol.CodeInvalid, "protocol must be tcp, udp or both")
		}
		if len(mapping.Description) > 80 {
			return nil, errorf(protocol.CodeInvalid, "description is limited to 80 characters")
		}
	}
	next := previous
	next.Mappings = append([]protocol.PortMapping(nil), previous.Mappings...)
	switch method {
	case protocol.MethodAddPortMapping:
		if previous.PortMappingLimit > 0 && len(previous.Mappings) >= previous.PortMappingLimit {
			return nil, errorf(protocol.CodeConflict, "port mapping limit %d reached", previous.PortMappingLimit)
		}
		if mapping.PublicPort == 0 {
			port, err := s.freePort()
			if err != nil {
				return nil, err
			}
			mapping.PublicPort = port
		} else if err := s.checkPublicPort(mapping.PublicPort); err != nil {
			return nil, err
		}
		next.Mappings = append(next.Mappings, mapping)
	case protocol.MethodUpdatePortMapping, protocol.MethodDeletePortMapping:
		if params.Index < 0 || params.Index >= len(next.Mappings) {
			return nil, errorf(protocol.CodeNotFound, "port mapping %d not found", params.Index)
		}
		if method == protocol.MethodDeletePortMapping {
			next.Mappings = append(next.Mappings[:params.Index], next.Mappings[params.Index+1:]...)
		} else {
			mapping.PublicPort = next.Mappings[params.Index].PublicPort
			next.Mappings[params.Index] = mapping
		}
	}
	if err := s.store.Update(params.Name, func(*InstanceRecord) (*InstanceRecord, error) { return &next, nil }); err != nil {
		return nil, err
	}
	if err := s.applyNAT(ctx); err != nil {
		_ = s.store.Update(params.Name, func(*InstanceRecord) (*InstanceRecord, error) { return &previous, nil })
		_ = s.applyNAT(ctx)
		return nil, err
	}
	return next.Mappings, nil
}

func (s *Service) usedPorts() map[int]bool {
	used := map[int]bool{}
	for _, record := range s.store.List() {
		for _, mapping := range record.Mappings {
			used[mapping.PublicPort] = true
		}
	}
	return used
}

func (s *Service) checkPublicPort(port int) error {
	if port < s.config.PortRangeStart || port > s.config.PortRangeEnd {
		return errorf(protocol.CodeInvalid, "public port must be within %d-%d", s.config.PortRangeStart, s.config.PortRangeEnd)
	}
	if s.usedPorts()[port] {
		return errorf(protocol.CodeConflict, "public port %d is already mapped", port)
	}
	return nil
}

func (s *Service) freePort() (int, error) {
	start, end := s.config.PortRangeStart, s.config.PortRangeEnd
	used := s.usedPorts()
	size := end - start + 1
	offset, err := rand.Int(rand.Reader, big.NewInt(int64(size)))
	if err != nil {
		return 0, err
	}
	for i := range size {
		port := start + (int(offset.Int64())+i)%size
		if !used[port] {
			return port, nil
		}
	}
	return 0, errorf(protocol.CodeConflict, "no free public port in %d-%d", start, end)
}

func (s *Service) applyNAT(ctx context.Context) error {
	s.natMu.Lock()
	defer s.natMu.Unlock()
	if err := s.nat.Apply(ctx, renderRuleset(s.config.NFTTable, s.store.List(), conntrackMax())); err != nil {
		return fmt.Errorf("apply port forwards: %w", err)
	}
	return nil
}

func (s *Service) recordSample(name string, state RuntimeState) {
	s.samplesMu.Lock()
	defer s.samplesMu.Unlock()
	s.samples[name] = sample{at: s.now(), state: state}
}

func (s *Service) forgetSample(name string) {
	s.samplesMu.Lock()
	defer s.samplesMu.Unlock()
	delete(s.samples, name)
}

// usage reports current resource use. Rates are computed against the
// previous observation of the instance, so the first call after start
// reports zero rates.
func (s *Service) usage(ctx context.Context, name string) (map[string]any, error) {
	record, ok := s.store.Get(name)
	if !ok {
		return nil, errorf(protocol.CodeNotFound, "instance %s not found", name)
	}
	runtime, err := s.runtime(record.Virtualization)
	if err != nil {
		return nil, err
	}
	state, err := runtime.State(ctx, name)
	if errors.Is(err, ErrInstanceNotFound) {
		return nil, errorf(protocol.CodeNotFound, "instance %s is missing", name)
	}
	if err != nil {
		return nil, err
	}
	s.samplesMu.Lock()
	previous, hasPrevious := s.samples[name]
	s.samplesMu.Unlock()
	s.recordSample(name, state)
	result := map[string]any{
		"memory_usage_bytes": state.MemoryBytes, "memory_total_bytes": int64(record.RAMMB) << 20,
		"disk_usage_bytes": state.DiskBytes, "cpu_usage_pct": 0.0,
		"network_rx_bps": 0.0, "network_tx_bps": 0.0, "disk_read_bps": 0.0, "disk_write_bps": 0.0,
	}
	elapsed := s.now().Sub(previous.at).Seconds()
	if hasPrevious && elapsed > 0 && elapsed < 600 {
		rate := func(current, before int64) float64 {
			if current < before {
				return 0
			}
			return float64(current-before) / elapsed
		}
		cpu := rate(state.CPUNanos, previous.state.CPUNanos) / 1e9 * 100 / float64(max(record.VCPU, 1))
		result["cpu_usage_pct"] = min(cpu, 100)
		result["network_rx_bps"] = rate(state.RXBytes, previous.state.RXBytes)
		result["network_tx_bps"] = rate(state.TXBytes, previous.state.TXBytes)
		result["disk_read_bps"] = rate(state.DiskReadBytes, previous.state.DiskReadBytes)
		result["disk_write_bps"] = rate(state.DiskWriteBytes, previous.state.DiskWriteBytes)
	}
	return result, nil
}

func (s *Service) traffic(ctx context.Context, name string) (map[string]any, error) {
	if err := s.meterInstance(ctx, name); err != nil {
		return nil, err
	}
	record, ok := s.store.Get(name)
	if !ok {
		return nil, errorf(protocol.CodeNotFound, "instance %s not found", name)
	}
	total := record.Traffic.RXBytes + record.Traffic.TXBytes
	return map[string]any{
		"month": record.Traffic.Month, "rx_bytes": record.Traffic.RXBytes, "tx_bytes": record.Traffic.TXBytes,
		"total_used_bytes": total, "limit_gb": record.MonthlyTrafficGB,
		"exceeded": record.MonthlyTrafficGB > 0 && total > int64(record.MonthlyTrafficGB)<<30,
	}, nil
}

// Meter folds interface counters into each instance's monthly ledger until
// ctx ends.
func (s *Service) Meter(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		for _, record := range s.store.List() {
			if err := s.meterInstance(ctx, record.Name); err != nil && !isNotFound(err) {
				s.logger.Warn("meter traffic", "instance", record.Name, "error", err)
			}
			if maintainer, ok := s.runtimes[record.Virtualization].(Maintainer); ok {
				if err := maintainer.Maintain(ctx, record.Name); err != nil && !errors.Is(err, ErrInstanceNotFound) {
					s.logger.Warn("maintain instance", "instance", record.Name, "error", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) meterInstance(ctx context.Context, name string) error {
	record, ok := s.store.Get(name)
	if !ok {
		return errorf(protocol.CodeNotFound, "instance %s not found", name)
	}
	runtime, err := s.runtime(record.Virtualization)
	if err != nil {
		return err
	}
	state, err := runtime.State(ctx, name)
	if errors.Is(err, ErrInstanceNotFound) {
		return errorf(protocol.CodeNotFound, "instance %s is missing", name)
	}
	if err != nil {
		return err
	}
	month := clock.Month(s.now())
	return s.store.Update(name, func(current *InstanceRecord) (*InstanceRecord, error) {
		if current == nil {
			return nil, errorf(protocol.CodeNotFound, "instance %s not found", name)
		}
		current.Traffic = advanceTraffic(current.Traffic, month, state.RXBytes, state.TXBytes)
		return current, nil
	})
}

// advanceTraffic adds the counter growth since the last sample. A counter
// lower than the last sample means the instance restarted, so the whole
// current value is new traffic.
func advanceTraffic(ledger TrafficRecord, month string, rx, tx int64) TrafficRecord {
	if ledger.Month != month {
		ledger.Month, ledger.RXBytes, ledger.TXBytes = month, 0, 0
	}
	delta := func(current, last int64) int64 {
		if current >= last {
			return current - last
		}
		return current
	}
	ledger.RXBytes += delta(rx, ledger.LastRX)
	ledger.TXBytes += delta(tx, ledger.LastTX)
	ledger.LastRX, ledger.LastTX = rx, tx
	return ledger
}

func isNotFound(err error) bool {
	var protocolErr *protocol.Error
	return errors.As(err, &protocolErr) && protocolErr.Code == protocol.CodeNotFound
}

const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func randomPassword() (string, error) {
	for {
		buffer := make([]byte, 16)
		for i := range buffer {
			index, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordAlphabet))))
			if err != nil {
				return "", err
			}
			buffer[i] = passwordAlphabet[index.Int64()]
		}
		value := string(buffer)
		if strings.ContainsAny(value, "23456789") && strings.ContainsAny(value, "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ") {
			return value, nil
		}
	}
}

// OpenTerminal starts an interactive shell in a managed instance.
func (s *Service) OpenTerminal(ctx context.Context, name string, cols, rows int) (TerminalSession, error) {
	record, ok := s.store.Get(name)
	if !ok {
		return nil, errorf(protocol.CodeNotFound, "instance %s not found", name)
	}
	runtime, err := s.runtime(record.Virtualization)
	if err != nil {
		return nil, err
	}
	terminal, err := runtime.Terminal(ctx, name, max(cols, 1), max(rows, 1))
	if errors.Is(err, ErrInstanceNotFound) {
		return nil, errorf(protocol.CodeNotFound, "instance %s is missing", name)
	}
	return terminal, err
}
