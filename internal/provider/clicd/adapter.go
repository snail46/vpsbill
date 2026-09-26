// Package clicdprovider adapts the CLICD node API client to the provider
// contract. The HTTP client itself stays in internal/clicd.
package clicdprovider

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"vpsbill/internal/clicd"
	"vpsbill/internal/provider"
)

const Type = "clicd"

func init() {
	provider.Register(provider.Descriptor{
		Type: Type, Name: "CLICD", CredentialLabel: "API Key",
		BaseURLHint: "http://节点IP:8999", VirtualizationTypes: []string{"lxc", "kvm"},
	}, func(config provider.Config) (provider.Driver, error) {
		client, err := clicd.NewClient(config.BaseURL, config.Credential, config.Timeout)
		if err != nil {
			return nil, err
		}
		return &Driver{client: client}, nil
	})
}

type Driver struct {
	client *clicd.Client
}

var (
	_ provider.Driver           = (*Driver)(nil)
	_ provider.Reinstaller      = (*Driver)(nil)
	_ provider.PasswordResetter = (*Driver)(nil)
	_ provider.PortMapper       = (*Driver)(nil)
	_ provider.Metrics          = (*Driver)(nil)
	_ provider.Console          = (*Driver)(nil)
	_ provider.HostProbe        = (*Driver)(nil)
)

// translate maps CLICD's error envelope onto the provider error vocabulary.
func translate(err error) error {
	var apiErr *clicd.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	converted := &provider.Error{Provider: Type, StatusCode: apiErr.StatusCode, Message: apiErr.Message}
	if clicd.IsNotFound(err) {
		return errors.Join(provider.ErrNotFound, converted)
	}
	return converted
}

func (d *Driver) HostInfo(ctx context.Context) (provider.HostInfo, error) {
	info, err := d.client.HostInfo(ctx)
	if err != nil {
		return provider.HostInfo{}, translate(err)
	}
	totals := clicd.CapacityFromHostInfo(info)
	return provider.HostInfo{Raw: info, Capacity: provider.Capacity{VCPU: totals.VCPU, RAMMB: totals.RAMMB, DiskGB: totals.DiskGB}}, nil
}

func (d *Driver) Images(ctx context.Context) ([]provider.Image, error) {
	images, err := d.client.Images(ctx)
	if err != nil {
		return nil, translate(err)
	}
	result := make([]provider.Image, 0, len(images))
	for _, image := range images {
		result = append(result, provider.Image{
			ID: image.ID, Name: image.Name, Virtualization: image.Type, Distro: image.Distro, Release: image.Release,
			Arch: image.Arch, Variant: image.Variant, Description: image.Description, Downloaded: image.Downloaded, Enabled: image.Enabled,
		})
	}
	return result, nil
}

func (d *Driver) EnsureInstance(ctx context.Context, spec provider.CreateSpec) (provider.EnsureResult, error) {
	expiresAt := ""
	if spec.ExpiresAt != nil {
		expiresAt = spec.ExpiresAt.UTC().Format(time.RFC3339)
	}
	result, err := d.client.EnsureContainer(ctx, clicd.CreateSpec{
		Name: spec.Name, Virtualization: spec.Virtualization, TemplateID: spec.TemplateID,
		VCPU: spec.VCPU, RAMMB: spec.RAMMB, DiskGB: spec.DiskGB, AssignNAT: spec.AssignNAT,
		PortMappingCount: spec.PortMappingCount, AssignIPv4: spec.AssignIPv4, IPv4Count: spec.IPv4Count,
		AssignIPv6: spec.AssignIPv6, IPv6Count: spec.IPv6Count, SSHAuthMode: spec.SSHAuthMode,
		SSHPassword: spec.SSHPassword, SSHPublicKey: spec.SSHPublicKey, ExpiresAt: expiresAt,
		NetworkDownMbps: spec.NetworkDownMbps, NetworkUpMbps: spec.NetworkUpMbps,
		MonthlyTrafficGB: spec.MonthlyTrafficGB, SnapshotLimit: spec.SnapshotLimit,
	})
	if err != nil {
		return provider.EnsureResult{}, translate(err)
	}
	return provider.EnsureResult{Instance: instance(result.Container), Created: result.Created}, nil
}

func (d *Driver) GetInstance(ctx context.Context, name string) (provider.Instance, error) {
	container, err := d.client.GetContainer(ctx, name)
	if err != nil {
		return provider.Instance{}, translate(err)
	}
	return instance(container), nil
}

func (d *Driver) PowerAction(ctx context.Context, name, action string) (string, error) {
	taskID, err := d.client.PowerAction(ctx, name, action)
	return taskID, translate(err)
}

func (d *Driver) DeleteInstance(ctx context.Context, name string) error {
	return translate(d.client.DeleteContainer(ctx, name))
}

func (d *Driver) Reinstall(ctx context.Context, name string, spec provider.ReinstallSpec) (string, error) {
	taskID, err := d.client.Reinstall(ctx, name, clicd.ReinstallSpec{TemplateID: spec.TemplateID, SSHAuthMode: "password", SSHPassword: spec.Password})
	return taskID, translate(err)
}

func (d *Driver) ResetPassword(ctx context.Context, name, password string) (string, error) {
	result, err := d.client.ResetPassword(ctx, name, password)
	return result, translate(err)
}

func (d *Driver) FreePort(ctx context.Context, name string) (int, error) {
	port, err := d.client.RandomPort(ctx, name)
	return port, translate(err)
}

func (d *Driver) AddPortMapping(ctx context.Context, name string, mapping provider.PortMapping) ([]provider.PortMapping, error) {
	result, err := d.client.AddPortMapping(ctx, name, toClicdMapping(mapping))
	return mappings(result), translate(err)
}

func (d *Driver) UpdatePortMapping(ctx context.Context, name string, index int, mapping provider.PortMapping) ([]provider.PortMapping, error) {
	result, err := d.client.UpdatePortMapping(ctx, name, index, toClicdMapping(mapping))
	return mappings(result), translate(err)
}

func (d *Driver) DeletePortMapping(ctx context.Context, name string, index int) ([]provider.PortMapping, error) {
	result, err := d.client.DeletePortMapping(ctx, name, index)
	return mappings(result), translate(err)
}

func (d *Driver) InstanceUsage(ctx context.Context, name string) (any, error) {
	value, err := d.client.ContainerUsage(ctx, name)
	return value, translate(err)
}

func (d *Driver) InstanceTraffic(ctx context.Context, name string) (any, error) {
	value, err := d.client.ContainerTraffic(ctx, name)
	return value, translate(err)
}

func (d *Driver) InstanceHistory(ctx context.Context, name string) (any, error) {
	value, err := d.client.ContainerHistory(ctx, name)
	return value, translate(err)
}

func (d *Driver) ConsoleKinds() []string { return []string{"ssh", "vnc"} }

func (d *Driver) ConsoleTicket(ctx context.Context, name, kind, userAgent string) (string, error) {
	ticket, err := d.client.ConsoleTicket(ctx, name, kind, userAgent)
	return ticket, translate(err)
}

func (d *Driver) ConsoleTarget(name, kind string) (*url.URL, error) {
	return d.client.ConsoleTarget(name, kind)
}

func (d *Driver) ProbeSections() []provider.ProbeSection {
	return []provider.ProbeSection{
		{Name: "dashboard", Load: d.client.Dashboard},
		{Name: "host_info", Load: func(ctx context.Context) (any, error) { return d.client.HostInfo(ctx) }},
		{Name: "host_history", Load: d.client.HostHistory},
		{Name: "host_report", Load: d.client.HostReport},
	}
}

func instance(container clicd.Container) provider.Instance {
	externalID := ""
	if container.ID != 0 {
		externalID = strconv.Itoa(container.ID)
	}
	return provider.Instance{
		ExternalID: externalID, UUID: container.UUID, Name: container.Name, Virtualization: container.Virtualization,
		Status: container.Status, Template: container.Template, IP: container.IP, IPv6: container.IPv6,
		VCPU: container.VCPU, RAMMB: container.RAMMB, DiskGB: container.DiskGB, SSHPort: container.SSHPort,
		PortMappings: mappings(container.PortMappings), PortMappingLimit: container.PortMappingLimit,
		MonthlyTrafficGB: container.MonthlyTrafficGB, NetworkDownMbps: container.NetworkDownMbps,
		NetworkUpMbps: container.NetworkUpMbps, InitialPassword: container.SSHPassword,
	}
}

func mappings(values []clicd.PortMapping) []provider.PortMapping {
	result := make([]provider.PortMapping, 0, len(values))
	for _, value := range values {
		result = append(result, provider.PortMapping{ContainerPort: value.ContainerPort, HostPort: value.HostPort, HostIP: value.HostIP, Protocol: value.Protocol, Description: value.Description})
	}
	return result
}

func toClicdMapping(value provider.PortMapping) clicd.PortMapping {
	return clicd.PortMapping{ContainerPort: value.ContainerPort, HostPort: value.HostPort, HostIP: value.HostIP, Protocol: value.Protocol, Description: value.Description}
}
