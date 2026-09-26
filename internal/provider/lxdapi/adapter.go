package lxdapiprovider

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"vpsbill/internal/provider"
)

const Type = "lxdapi"

// Options are the per-node settings LXDAPI's system API cannot report itself.
type Options struct {
	PublicIPv4     string   `json:"public_ipv4"`
	NATInterface   string   `json:"nat_interface"`
	PortRangeStart int      `json:"port_range_start"`
	PortRangeEnd   int      `json:"port_range_end"`
	Images         []string `json:"images"`
	CapacityVCPU   int      `json:"capacity_vcpu"`
	CapacityRAMMB  int64    `json:"capacity_ram_mb"`
	CapacityDiskGB int64    `json:"capacity_disk_gb"`
	Username       string   `json:"username"`
	TLSVerify      bool     `json:"tls_verify"`
	TLSFingerprint string   `json:"tls_fingerprint"`
}

func init() {
	provider.Register(provider.Descriptor{
		Type: Type, Name: "LXDAPI", CredentialLabel: "API Hash",
		BaseURLHint: "https://节点IP:8443", VirtualizationTypes: []string{"lxc"},
		Options: []provider.OptionField{
			{Key: "public_ipv4", Label: "NAT 公网 IPv4", Kind: "text", Required: true, Placeholder: "203.0.113.10"},
			{Key: "nat_interface", Label: "NAT 出口网卡", Kind: "text", Required: true, Placeholder: "eth0"},
			{Key: "port_range_start", Label: "端口映射起始端口", Kind: "number", Required: true, Placeholder: "20000"},
			{Key: "port_range_end", Label: "端口映射结束端口", Kind: "number", Required: true, Placeholder: "60000"},
			{Key: "images", Label: "可售镜像别名", Kind: "list", Required: true, Placeholder: "debian12,ubuntu24", Help: "LXDAPI 系统接口不提供镜像列表，填写节点上已导入的镜像别名，逗号分隔"},
			{Key: "capacity_vcpu", Label: "可分配 vCPU", Kind: "number", Required: true},
			{Key: "capacity_ram_mb", Label: "可分配内存 (MB)", Kind: "number", Required: true},
			{Key: "capacity_disk_gb", Label: "可分配磁盘 (GB)", Kind: "number", Required: true},
			{Key: "username", Label: "LXDAPI 归属用户", Kind: "text", Placeholder: "vpsbill", Help: "容器在 LXDAPI 中的所属用户，留空为 vpsbill"},
			{Key: "tls_verify", Label: "校验 HTTPS 证书", Kind: "bool", Help: "节点使用受信任证书时开启"},
			{Key: "tls_fingerprint", Label: "证书 SHA-256 指纹", Kind: "text", Help: "未开启证书校验时必填，用于固定自签名证书"},
		},
	}, func(config provider.Config) (provider.Driver, error) {
		var options Options
		if len(config.Options) > 0 {
			if err := json.Unmarshal(config.Options, &options); err != nil {
				return nil, fmt.Errorf("decode LXDAPI options: %w", err)
			}
		}
		if options.Username == "" {
			options.Username = "vpsbill"
		}
		c, err := newClient(config.BaseURL, config.Credential, options, config.Timeout)
		if err != nil {
			return nil, err
		}
		return &Driver{client: c, options: options}, nil
	})
}

type Driver struct {
	client  *client
	options Options
}

var (
	_ provider.Driver           = (*Driver)(nil)
	_ provider.Reinstaller      = (*Driver)(nil)
	_ provider.PasswordResetter = (*Driver)(nil)
	_ provider.PortMapper       = (*Driver)(nil)
	_ provider.Metrics          = (*Driver)(nil)
	_ provider.Suspender        = (*Driver)(nil)
)

// HostInfo verifies the API hash with a cheap list call. Capacity comes from
// node options because the system API exposes no host statistics.
func (d *Driver) HostInfo(ctx context.Context) (provider.HostInfo, error) {
	var containers []container
	if err := d.client.do(ctx, http.MethodGet, "/api/system/containers", nil, &containers); err != nil {
		return provider.HostInfo{}, err
	}
	capacity := provider.Capacity{VCPU: d.options.CapacityVCPU, RAMMB: d.options.CapacityRAMMB, DiskGB: d.options.CapacityDiskGB}
	return provider.HostInfo{
		Raw:      map[string]any{"provider": Type, "containers": len(containers), "capacity": capacity, "public_ipv4": d.options.PublicIPv4},
		Capacity: capacity,
	}, nil
}

func (d *Driver) Images(context.Context) ([]provider.Image, error) {
	images := make([]provider.Image, 0, len(d.options.Images))
	for _, alias := range d.options.Images {
		images = append(images, provider.Image{ID: alias, Name: alias, Virtualization: "lxc", Enabled: true, Downloaded: true})
	}
	return images, nil
}

// EnsureInstance is safe to retry: an existing container or a still-running
// create task for the same name is adopted instead of submitting another.
func (d *Driver) EnsureInstance(ctx context.Context, spec provider.CreateSpec) (provider.EnsureResult, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return provider.EnsureResult{}, errors.New("container name is required")
	}
	if instance, err := d.GetInstance(ctx, spec.Name); err == nil {
		return provider.EnsureResult{Instance: instance}, nil
	} else if !errors.Is(err, provider.ErrNotFound) {
		return provider.EnsureResult{}, fmt.Errorf("check existing container: %w", err)
	}
	pending, err := d.pendingTask(ctx, spec.Name, "create")
	if err != nil {
		return provider.EnsureResult{}, err
	}
	if pending == 0 {
		password := spec.SSHPassword
		if spec.SSHAuthMode != "password" || password == "" {
			if password, err = randomPassword(); err != nil {
				return provider.EnsureResult{}, err
			}
		}
		var accepted struct {
			TaskID uint `json:"task_id"`
		}
		if err := d.client.do(ctx, http.MethodPost, "/api/system/containers", d.createRequest(spec, password), &accepted); err != nil {
			return provider.EnsureResult{}, fmt.Errorf("create container: %w", err)
		}
		pending = accepted.TaskID
	}
	if err := d.client.waitTask(ctx, pending); err != nil {
		return provider.EnsureResult{}, fmt.Errorf("create container: %w", err)
	}
	if err := d.topUpAddresses(ctx, spec); err != nil {
		return provider.EnsureResult{}, err
	}
	instance, err := d.GetInstance(ctx, spec.Name)
	if err != nil {
		return provider.EnsureResult{}, fmt.Errorf("container created but lookup failed: %w", err)
	}
	return provider.EnsureResult{Instance: instance, Created: true}, nil
}

func (d *Driver) createRequest(spec provider.CreateSpec, password string) map[string]any {
	mappingLimit, ipv4Pool, ipv6Pool := 0, 0, 0
	if spec.AssignNAT {
		mappingLimit = max(spec.PortMappingCount, 1)
	}
	if spec.AssignIPv4 {
		ipv4Pool = max(spec.IPv4Count, 1)
	}
	if spec.AssignIPv6 {
		ipv6Pool = max(spec.IPv6Count, 1)
	}
	return map[string]any{
		"name": spec.Name, "image": spec.TemplateID, "username": d.options.Username, "password": password,
		"cpu": spec.VCPU, "memory": spec.RAMMB, "disk": spec.DiskGB * 1024,
		"ingress": spec.NetworkDownMbps, "egress": spec.NetworkUpMbps, "traffic_limit": spec.MonthlyTrafficGB,
		"ipv4_mapping_limit": mappingLimit, "ipv4_pool_limit": ipv4Pool, "ipv6_pool_limit": ipv6Pool,
		"memory_swap": true, "allow_nesting": false, "privileged": false,
	}
}

// pendingTask returns the newest queued or running task of the given action
// for the container, or 0 when there is none.
func (d *Driver) pendingTask(ctx context.Context, name, action string) (uint, error) {
	tasks, err := d.client.tasks(ctx, name)
	if err != nil {
		return 0, fmt.Errorf("list tasks: %w", err)
	}
	for _, current := range tasks {
		if current.Action == action && (current.Status == "queued" || current.Status == "running") {
			return current.ID, nil
		}
	}
	return 0, nil
}

func (d *Driver) GetInstance(ctx context.Context, name string) (provider.Instance, error) {
	detail, err := d.client.container(ctx, name)
	if err != nil {
		return provider.Instance{}, err
	}
	mappings, err := d.mappings(ctx, name)
	if err != nil {
		return provider.Instance{}, err
	}
	value := detail.Container
	status := detail.Status
	if status == "" {
		status = value.Status
	}
	addresses := d.client.addresses(ctx, name)
	ip := firstOf(addresses.IPv4, firstAddress(value.IPv4))
	if ip == "" {
		ip = d.options.PublicIPv4
	}
	ipv6 := firstOf(addresses.IPv6, firstAddress(value.IPv6))
	instance := provider.Instance{
		ExternalID: strconv.FormatUint(uint64(value.ID), 10), Name: value.Name, Virtualization: "lxc", Status: status,
		Template: value.Image, IP: ip, IPv6: ipv6, VCPU: value.CPU, RAMMB: value.Memory,
		DiskGB: value.Disk / 1024, PortMappingLimit: value.IPv4MappingLimit, MonthlyTrafficGB: value.TrafficLimit,
		NetworkDownMbps: value.Ingress, NetworkUpMbps: value.Egress, InitialPassword: value.Password,
	}
	for _, mapping := range mappings {
		instance.PortMappings = append(instance.PortMappings, toPortMapping(mapping))
		if mapping.ContainerPort == 22 && instance.SSHPort == 0 {
			instance.SSHPort = mapping.PublicPort
		}
	}
	if instance.PortMappings == nil {
		instance.PortMappings = []provider.PortMapping{}
	}
	return instance, nil
}

func (d *Driver) PowerAction(ctx context.Context, name, action string) (string, error) {
	switch action {
	case "start", "stop", "restart":
	default:
		return "", errors.New("unsupported power action")
	}
	return "", d.client.action(ctx, name, action, nil, nil)
}

// DeleteInstance waits for LXDAPI's asynchronous delete so the billing core
// only releases inventory once the container is really gone.
func (d *Driver) DeleteInstance(ctx context.Context, name string) error {
	var accepted struct {
		TaskID uint `json:"task_id"`
	}
	err := d.client.do(ctx, http.MethodDelete, "/api/system/containers/"+url.PathEscape(name), nil, &accepted)
	if errors.Is(err, provider.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return d.client.waitTask(ctx, accepted.TaskID)
}

func (d *Driver) Reinstall(ctx context.Context, name string, spec provider.ReinstallSpec) (string, error) {
	var accepted struct {
		TaskID uint `json:"task_id"`
	}
	err := d.client.action(ctx, name, "reinstall", map[string]string{"image": spec.TemplateID, "password": spec.Password}, &accepted)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(uint64(accepted.TaskID), 10), nil
}

func (d *Driver) ResetPassword(ctx context.Context, name, password string) (string, error) {
	if password == "" {
		generated, err := randomPassword()
		if err != nil {
			return "", err
		}
		password = generated
	}
	if err := d.client.action(ctx, name, "reset-password", map[string]string{"password": password}, nil); err != nil {
		return "", err
	}
	return password, nil
}

func (d *Driver) Suspend(ctx context.Context, name string) error {
	return d.client.action(ctx, name, "pause", nil, nil)
}

func (d *Driver) Resume(ctx context.Context, name string) error {
	return d.client.action(ctx, name, "resume", nil, nil)
}

// mappings returns the container's IPv4 port mappings ordered by ID, which is
// the index order used by the PortMapper methods.
func (d *Driver) mappings(ctx context.Context, name string) ([]portMapping, error) {
	mappings, err := d.client.portMappings(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("list port mappings: %w", err)
	}
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].ID < mappings[j].ID })
	return mappings, nil
}

// FreePort picks a random public port in the configured range that no
// mapping on this node uses. LXDAPI rejects a port taken concurrently.
func (d *Driver) FreePort(ctx context.Context, _ string) (int, error) {
	start, end := d.options.PortRangeStart, d.options.PortRangeEnd
	if start < 1 || end > 65535 || start > end {
		return 0, errors.New("节点端口映射范围未正确配置")
	}
	all, err := d.client.portMappings(ctx, "")
	if err != nil {
		return 0, fmt.Errorf("list port mappings: %w", err)
	}
	used := make(map[int]bool, len(all))
	for _, mapping := range all {
		used[mapping.PublicPort] = true
	}
	size := end - start + 1
	if len(used) >= size {
		return 0, nil
	}
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
	return 0, nil
}

func (d *Driver) AddPortMapping(ctx context.Context, name string, mapping provider.PortMapping) ([]provider.PortMapping, error) {
	if err := d.allocate(ctx, name, mapping); err != nil {
		return nil, err
	}
	return d.listMappings(ctx, name)
}

// UpdatePortMapping releases the old rule and allocates the new one on the
// same public port. If the new rule is rejected the old one is restored.
func (d *Driver) UpdatePortMapping(ctx context.Context, name string, index int, mapping provider.PortMapping) ([]provider.PortMapping, error) {
	existing, err := d.mappingAt(ctx, name, index)
	if err != nil {
		return nil, err
	}
	if err := d.release(ctx, existing.ID); err != nil {
		return nil, err
	}
	mapping.HostPort = existing.PublicPort
	if err := d.allocate(ctx, name, mapping); err != nil {
		if restoreErr := d.allocate(ctx, name, toPortMapping(existing)); restoreErr != nil {
			return nil, fmt.Errorf("%w; restoring the previous mapping also failed: %v", err, restoreErr)
		}
		return nil, err
	}
	return d.listMappings(ctx, name)
}

func (d *Driver) DeletePortMapping(ctx context.Context, name string, index int) ([]provider.PortMapping, error) {
	existing, err := d.mappingAt(ctx, name, index)
	if err != nil {
		return nil, err
	}
	if err := d.release(ctx, existing.ID); err != nil {
		return nil, err
	}
	return d.listMappings(ctx, name)
}

func (d *Driver) mappingAt(ctx context.Context, name string, index int) (portMapping, error) {
	mappings, err := d.mappings(ctx, name)
	if err != nil {
		return portMapping{}, err
	}
	if index < 0 || index >= len(mappings) {
		return portMapping{}, &provider.Error{Provider: Type, StatusCode: http.StatusNotFound, Message: "端口映射不存在"}
	}
	return mappings[index], nil
}

func (d *Driver) allocate(ctx context.Context, name string, mapping provider.PortMapping) error {
	return d.client.do(ctx, http.MethodPost, "/api/system/port-mapping/allocate?version=v4", map[string]any{
		"container_name": name, "interface": d.options.NATInterface, "public_ip": d.options.PublicIPv4,
		"public_port": mapping.HostPort, "container_port": mapping.ContainerPort,
		"protocol": mapping.Protocol, "description": mapping.Description,
	}, nil)
}

func (d *Driver) release(ctx context.Context, id uint) error {
	return d.client.do(ctx, http.MethodPost, "/api/system/port-mapping/release?version=v4", map[string]uint{"id": id}, nil)
}

func (d *Driver) listMappings(ctx context.Context, name string) ([]provider.PortMapping, error) {
	mappings, err := d.mappings(ctx, name)
	if err != nil {
		return nil, err
	}
	result := make([]provider.PortMapping, 0, len(mappings))
	for _, mapping := range mappings {
		result = append(result, toPortMapping(mapping))
	}
	return result, nil
}

func (d *Driver) InstanceUsage(ctx context.Context, name string) (any, error) {
	detail, err := d.client.container(ctx, name)
	if err != nil {
		return nil, err
	}
	value := detail.Container
	return map[string]any{
		"cpu_usage_pct": value.CPUUsage, "memory_usage_bytes": value.MemoryUsageRaw,
		"memory_total_bytes": int64(value.Memory) << 20, "disk_usage_bytes": value.DiskUsageRaw,
	}, nil
}

func (d *Driver) InstanceTraffic(ctx context.Context, name string) (any, error) {
	var value traffic
	if err := d.client.do(ctx, http.MethodGet, "/api/system/traffic?name="+url.QueryEscape(name), nil, &value); err != nil {
		return nil, err
	}
	return map[string]any{
		"total_used_bytes": value.RxBytes + value.TxBytes, "rx_bytes": value.RxBytes, "tx_bytes": value.TxBytes,
		"limit_gb": value.LimitGB, "locked": value.Locked,
	}, nil
}

// InstanceHistory is not offered by the LXDAPI system API.
func (d *Driver) InstanceHistory(context.Context, string) (any, error) { return nil, nil }

func toPortMapping(value portMapping) provider.PortMapping {
	description := value.Description
	if description == "" && value.ContainerPort == 22 {
		description = "SSH"
	}
	return provider.PortMapping{ContainerPort: value.ContainerPort, HostPort: value.PublicPort, HostIP: value.PublicIP, Protocol: value.Protocol, Description: description}
}

// firstAddress returns the first address of LXDAPI's free-form address list.
func firstAddress(value string) string {
	for _, field := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		if field = strings.Trim(field, `[]"`); field != "" {
			return field
		}
	}
	return ""
}

const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// randomPassword returns 16 characters with at least one letter and digit,
// matching the customer password policy.
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
		if strings.ContainsAny(value, "0123456789") && strings.ContainsAny(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			return value, nil
		}
	}
}

// topUpAddresses allocates dedicated IPv4/IPv6 addresses that LXDAPI did not
// assign during creation (it only logs a warning when its pool is short).
func (d *Driver) topUpAddresses(ctx context.Context, spec provider.CreateSpec) error {
	current := d.client.addresses(ctx, spec.Name)
	wanted := []struct {
		version string
		assign  bool
		count   int
		have    int
	}{
		{"v4", spec.AssignIPv4, max(spec.IPv4Count, 1), len(current.IPv4)},
		{"v6", spec.AssignIPv6, max(spec.IPv6Count, 1), len(current.IPv6)},
	}
	for _, item := range wanted {
		if !item.assign || item.have >= item.count {
			continue
		}
		body := map[string]any{"name": spec.Name, "user_id": d.options.Username, "count": item.count - item.have}
		if err := d.client.do(ctx, http.MethodPost, "/api/system/ip/allocate?version="+item.version, body, nil); err != nil {
			return fmt.Errorf("allocate IP%s addresses: %w", item.version, err)
		}
	}
	return nil
}

func firstOf(values []string, fallback string) string {
	if len(values) > 0 && values[0] != "" {
		return values[0]
	}
	return fallback
}
