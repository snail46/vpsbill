// Package hatchprovider drives nodes through a connected Hatch agent.
package hatchprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"

	"vpsbill/internal/hatch/gateway"
	"vpsbill/internal/hatch/protocol"
	"vpsbill/internal/provider"
)

const Type = "hatch"

// Register makes the hatch provider available. It is called from main
// because drivers need the process's gateway hub.
func Register(hub *gateway.Hub) {
	provider.Register(provider.Descriptor{
		Type: Type, Name: "Hatch Agent", CredentialLabel: "Agent 令牌",
		VirtualizationTypes: []string{"lxc", "podman"}, AgentManaged: true,
		Options: []provider.OptionField{
			{Key: "public_ipv4", Label: "公网 IPv4（可选）", Kind: "text", Placeholder: "留空使用 Agent 自动检测的地址",
				Help: "客户看到的 SSH 和端口映射地址。Agent 检测不准时填写，例如母机只有内网地址、经 NAT 或隧道对外"},
		},
	}, func(config provider.Config) (provider.Driver, error) {
		if len(config.Credential) < 32 {
			return nil, errors.New("Agent 令牌无效")
		}
		var options Options
		if len(config.Options) > 0 {
			if err := json.Unmarshal(config.Options, &options); err != nil {
				return nil, errors.New("节点设置无效")
			}
		}
		if options.PublicIPv4 != "" {
			addr, err := netip.ParseAddr(options.PublicIPv4)
			if err != nil || !addr.Is4() {
				return nil, errors.New("公网 IPv4 格式不正确")
			}
		}
		return &Driver{hub: hub, endpoint: provider.AgentEndpoint(config.Credential), publicIPv4: options.PublicIPv4}, nil
	})
}

// Options are a node's settings beside what its agent reports.
type Options struct {
	// PublicIPv4 replaces the address the agent detected.
	PublicIPv4 string `json:"public_ipv4"`
}

type Driver struct {
	hub      *gateway.Hub
	endpoint string
	// publicIPv4 overrides the agent's own public address when set.
	publicIPv4 string
}

var (
	_ provider.Driver           = (*Driver)(nil)
	_ provider.Reinstaller      = (*Driver)(nil)
	_ provider.PasswordResetter = (*Driver)(nil)
	_ provider.PortMapper       = (*Driver)(nil)
	_ provider.Metrics          = (*Driver)(nil)
	_ provider.Suspender        = (*Driver)(nil)
	_ provider.Terminal         = (*Driver)(nil)
)

func (d *Driver) call(ctx context.Context, method string, params, result any) error {
	return d.translate(d.hub.Call(ctx, d.endpoint, method, params, result))
}

// translate maps gateway and agent errors onto the provider contract.
func (d *Driver) translate(err error) error {
	var agentErr *protocol.Error
	switch {
	case err == nil:
		return nil
	case errors.Is(err, gateway.ErrOffline):
		return &provider.Error{Provider: Type, StatusCode: http.StatusServiceUnavailable, Message: "Agent 未连接"}
	case !errors.As(err, &agentErr):
		return err
	case agentErr.Code == protocol.CodeNotFound:
		return errors.Join(provider.ErrNotFound, &provider.Error{Provider: Type, StatusCode: http.StatusNotFound, Message: agentErr.Message})
	case agentErr.Code == protocol.CodeUnsupported:
		return errors.Join(provider.ErrUnsupported, &provider.Error{Provider: Type, StatusCode: http.StatusNotImplemented, Message: agentErr.Message})
	case agentErr.Code == protocol.CodeInvalid || agentErr.Code == protocol.CodeConflict:
		return &provider.Error{Provider: Type, StatusCode: http.StatusUnprocessableEntity, Message: agentErr.Message}
	default:
		return &provider.Error{Provider: Type, StatusCode: http.StatusBadGateway, Message: agentErr.Message}
	}
}

func (d *Driver) HostInfo(ctx context.Context) (provider.HostInfo, error) {
	var info protocol.HostInfo
	if err := d.call(ctx, protocol.MethodHostInfo, struct{}{}, &info); err != nil {
		return provider.HostInfo{}, err
	}
	raw := map[string]any{
		"hostname": info.Hostname, "agent_version": info.AgentVersion, "runtimes": info.Runtimes,
		"public_ipv4": d.publicAddress(info.PublicIPv4), "details": info.Details, "detected": info.Detected,
	}
	result := provider.HostInfo{Raw: raw, MachineID: info.MachineID,
		Capacity: provider.Capacity{VCPU: info.Capacity.VCPU, RAMMB: info.Capacity.RAMMB, DiskGB: info.Capacity.DiskGB}}
	if health := info.Health; health != nil {
		result.Health = &provider.HostHealth{
			CPUs: health.CPUs, Load1: health.Load1, Load5: health.Load5, Load15: health.Load15,
			MemTotalMB: health.MemTotalMB, MemAvailableMB: health.MemAvailableMB,
			SwapTotalMB: health.SwapTotalMB, SwapFreeMB: health.SwapFreeMB, QuotaErrors: info.QuotaErrors,
		}
		for _, disk := range health.Disks {
			result.Health.Disks = append(result.Health.Disks, provider.DiskUsage{Name: disk.Name, TotalGB: disk.TotalGB, UsedGB: disk.UsedGB})
		}
	}
	if perf := info.DiskPerf; perf != nil {
		result.DiskPerf = &provider.DiskPerf{ReadMBps: perf.ReadMBps, WriteMBps: perf.WriteMBps, ReadIOPS: perf.ReadIOPS, WriteIOPS: perf.WriteIOPS,
			MeasuredAt: perf.MeasuredAt, IOLimitErrors: info.IOLimitErrors}
	}
	return result, nil
}

func (d *Driver) Images(ctx context.Context) ([]provider.Image, error) {
	var images []protocol.Image
	if err := d.call(ctx, protocol.MethodImages, struct{}{}, &images); err != nil {
		return nil, err
	}
	result := make([]provider.Image, 0, len(images))
	for _, image := range images {
		result = append(result, provider.Image{ID: image.ID, Name: image.Name, Virtualization: image.Virtualization, Description: image.Description, Enabled: true, Downloaded: true})
	}
	return result, nil
}

func (d *Driver) EnsureInstance(ctx context.Context, spec provider.CreateSpec) (provider.EnsureResult, error) {
	password := ""
	if spec.SSHAuthMode == "password" {
		password = spec.SSHPassword
	}
	var result protocol.EnsureResult
	err := d.call(ctx, protocol.MethodEnsure, protocol.CreateSpec{
		Name: spec.Name, Virtualization: spec.Virtualization, TemplateID: spec.TemplateID,
		VCPU: spec.VCPU, RAMMB: spec.RAMMB, DiskGB: spec.DiskGB, AssignNAT: spec.AssignNAT,
		PortMappingCount: spec.PortMappingCount, AssignIPv6: spec.AssignIPv6, Password: password,
		NetworkDownMbps: spec.NetworkDownMbps, NetworkUpMbps: spec.NetworkUpMbps, MonthlyTrafficGB: spec.MonthlyTrafficGB,
		DiskIO: protocol.DiskIO{ReadMBps: spec.DiskIO.ReadMBps, WriteMBps: spec.DiskIO.WriteMBps, ReadIOPS: spec.DiskIO.ReadIOPS, WriteIOPS: spec.DiskIO.WriteIOPS},
	}, &result)
	if err != nil {
		return provider.EnsureResult{}, err
	}
	return provider.EnsureResult{Instance: d.instance(result.Instance), Created: result.Created}, nil
}

func (d *Driver) GetInstance(ctx context.Context, name string) (provider.Instance, error) {
	var value protocol.Instance
	if err := d.call(ctx, protocol.MethodGet, protocol.NameParams{Name: name}, &value); err != nil {
		return provider.Instance{}, err
	}
	return d.instance(value), nil
}

func (d *Driver) PowerAction(ctx context.Context, name, action string) (string, error) {
	return "", d.call(ctx, protocol.MethodPower, protocol.PowerParams{Name: name, Action: action}, nil)
}

func (d *Driver) DeleteInstance(ctx context.Context, name string) error {
	err := d.call(ctx, protocol.MethodDelete, protocol.NameParams{Name: name}, nil)
	if errors.Is(err, provider.ErrNotFound) {
		return nil
	}
	return err
}

func (d *Driver) Reinstall(ctx context.Context, name string, spec provider.ReinstallSpec) (string, error) {
	return "", d.call(ctx, protocol.MethodReinstall, protocol.ReinstallParams{Name: name, TemplateID: spec.TemplateID, Password: spec.Password}, nil)
}

func (d *Driver) ResetPassword(ctx context.Context, name, password string) (string, error) {
	var result protocol.PasswordParams
	err := d.call(ctx, protocol.MethodResetPassword, protocol.PasswordParams{Name: name, Password: password}, &result)
	return result.Password, err
}

func (d *Driver) Suspend(ctx context.Context, name string) error {
	return d.call(ctx, protocol.MethodSuspend, protocol.NameParams{Name: name}, nil)
}

func (d *Driver) Resume(ctx context.Context, name string) error {
	return d.call(ctx, protocol.MethodResume, protocol.NameParams{Name: name}, nil)
}

func (d *Driver) FreePort(ctx context.Context, name string) (int, error) {
	var result protocol.PortParams
	err := d.call(ctx, protocol.MethodFreePort, protocol.NameParams{Name: name}, &result)
	return result.Port, err
}

func (d *Driver) AddPortMapping(ctx context.Context, name string, mapping provider.PortMapping) ([]provider.PortMapping, error) {
	return d.mutate(ctx, protocol.MethodAddPortMapping, name, -1, mapping)
}

func (d *Driver) UpdatePortMapping(ctx context.Context, name string, index int, mapping provider.PortMapping) ([]provider.PortMapping, error) {
	return d.mutate(ctx, protocol.MethodUpdatePortMapping, name, index, mapping)
}

func (d *Driver) DeletePortMapping(ctx context.Context, name string, index int) ([]provider.PortMapping, error) {
	return d.mutate(ctx, protocol.MethodDeletePortMapping, name, index, provider.PortMapping{})
}

func (d *Driver) mutate(ctx context.Context, method, name string, index int, mapping provider.PortMapping) ([]provider.PortMapping, error) {
	var result []protocol.PortMapping
	err := d.call(ctx, method, protocol.PortMappingParams{Name: name, Index: index, Mapping: protocol.PortMapping{
		PublicPort: mapping.HostPort, ContainerPort: mapping.ContainerPort, Protocol: mapping.Protocol, Description: mapping.Description,
	}}, &result)
	if err != nil {
		return nil, err
	}
	return portMappings(result, ""), nil
}

func (d *Driver) InstanceUsage(ctx context.Context, name string) (any, error) {
	var usage map[string]any
	err := d.call(ctx, protocol.MethodUsage, protocol.NameParams{Name: name}, &usage)
	return usage, err
}

func (d *Driver) InstanceTraffic(ctx context.Context, name string) (any, error) {
	var traffic map[string]any
	err := d.call(ctx, protocol.MethodTraffic, protocol.NameParams{Name: name}, &traffic)
	return traffic, err
}

// InstanceHistory is not collected by the agent yet.
func (d *Driver) InstanceHistory(context.Context, string) (any, error) { return nil, nil }

// publicAddress is the node's public IPv4: the configured one, else what
// the agent detected.
func (d *Driver) publicAddress(detected string) string {
	if d.publicIPv4 != "" {
		return d.publicIPv4
	}
	return detected
}

func (d *Driver) instance(value protocol.Instance) provider.Instance {
	value.PublicIPv4 = d.publicAddress(value.PublicIPv4)
	ip := value.PublicIPv4
	if ip == "" {
		ip = value.PrivateIPv4
	}
	return provider.Instance{
		ExternalID: value.Name, Name: value.Name, Virtualization: value.Virtualization, Status: value.Status,
		Template: value.Template, IP: ip, IPv6: value.IPv6, VCPU: value.VCPU, RAMMB: value.RAMMB, DiskGB: value.DiskGB,
		SSHPort: value.SSHPort, PortMappings: portMappings(value.PortMappings, value.PublicIPv4), PortMappingLimit: value.PortMappingLimit,
		MonthlyTrafficGB: value.MonthlyTrafficGB, NetworkDownMbps: value.NetworkDownMbps, NetworkUpMbps: value.NetworkUpMbps,
		InitialPassword: value.Password,
	}
}

func portMappings(values []protocol.PortMapping, hostIP string) []provider.PortMapping {
	result := make([]provider.PortMapping, 0, len(values))
	for _, value := range values {
		result = append(result, provider.PortMapping{ContainerPort: value.ContainerPort, HostPort: value.PublicPort, HostIP: hostIP, Protocol: value.Protocol, Description: value.Description})
	}
	return result
}

// OpenTerminal starts a root shell in the instance, carried as a stream over
// the agent's connection.
func (d *Driver) OpenTerminal(ctx context.Context, name string, cols, rows int) (provider.TerminalSession, error) {
	stream, err := d.hub.OpenTerminal(ctx, d.endpoint, name, cols, rows)
	if err != nil {
		return nil, d.translate(err)
	}
	return stream, nil
}
