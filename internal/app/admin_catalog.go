package app

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"vpsbill/internal/provider"
	"vpsbill/internal/security"
	"vpsbill/internal/store/postgres"
)

type adminCatalog struct {
	store *postgres.CatalogStore
	box   *security.SecretBox
}

func newAdminCatalog(store *postgres.CatalogStore, box *security.SecretBox) *adminCatalog {
	return &adminCatalog{store: store, box: box}
}

func (a *adminCatalog) listNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.store.ListNodes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": nodes})
}

func (a *adminCatalog) createNode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProviderType        string         `json:"provider_type"`
		RegionCode          string         `json:"region_code"`
		RegionName          string         `json:"region_name"`
		Name                string         `json:"name"`
		BaseURL             string         `json:"base_url"`
		APIKey              string         `json:"api_key"`
		VirtualizationTypes []string       `json:"virtualization_types"`
		ProviderOptions     map[string]any `json:"provider_options"`
		ExpiresAt           string         `json:"expires_at"`
		TrafficQuotaGB      int            `json:"traffic_quota_gb"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validNodeBilling(w, &input.ExpiresAt, input.TrafficQuotaGB) {
		return
	}
	input.ProviderType = strings.TrimSpace(input.ProviderType)
	if input.ProviderType == "" {
		input.ProviderType = "clicd"
	}
	descriptor, ok := provider.Lookup(input.ProviderType)
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "provider_unsupported", "message": "不支持的对接方式"})
		return
	}
	if strings.TrimSpace(input.RegionCode) == "" || strings.TrimSpace(input.RegionName) == "" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.APIKey) == "" || len(input.VirtualizationTypes) == 0 ||
		(!descriptor.AgentManaged && strings.TrimSpace(input.BaseURL) == "") {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "节点名称、地区、API 地址、" + descriptor.CredentialLabel + " 和虚拟化类型不能为空"})
		return
	}
	if descriptor.AgentManaged {
		input.BaseURL = provider.AgentEndpoint(input.APIKey)
		if a.store.NodeExistsByEndpoint(r.Context(), input.BaseURL) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "node_create_failed", "message": "该 Agent 令牌已接入其他节点"})
			return
		}
	}
	options, err := provider.NormalizeOptions(descriptor, input.ProviderOptions)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	for _, virtualization := range input.VirtualizationTypes {
		if !containsString(descriptor.VirtualizationTypes, virtualization) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": descriptor.Name + " 支持的虚拟化类型为 " + strings.Join(descriptor.VirtualizationTypes, " / ")})
			return
		}
	}
	driver, err := provider.Open(input.ProviderType, provider.Config{BaseURL: input.BaseURL, Credential: input.APIKey, Options: options, Timeout: 12 * time.Second})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	info, err := driver.HostInfo(r.Context())
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "node_unreachable", "message": "无法验证 " + descriptor.Name + " 节点连接：" + err.Error()})
		return
	}
	hostInfo := info.Raw
	ciphertext, err := a.box.Seal(input.APIKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	totals := info.Capacity
	node, err := a.store.CreateNode(r.Context(), postgres.CreateNode{
		ProviderType:        input.ProviderType,
		RegionCode:          input.RegionCode,
		RegionName:          input.RegionName,
		Name:                input.Name,
		BaseURL:             input.BaseURL,
		APIKeyCiphertext:    ciphertext,
		ProviderOptions:     options,
		VirtualizationTypes: input.VirtualizationTypes,
		Capacity:            hostInfo,
		CapacityVCPU:        totals.VCPU,
		CapacityRAMMB:       totals.RAMMB,
		CapacityDiskGB:      totals.DiskGB,
	})
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "node_create_failed", "message": "节点名称可能已存在"})
		return
	}
	if err := a.store.UpdateNodeBilling(r.Context(), node.ID, input.ExpiresAt, input.TrafficQuotaGB); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	node.ExpiresAt, node.TrafficQuotaGB = input.ExpiresAt, input.TrafficQuotaGB
	writeJSON(w, http.StatusCreated, map[string]any{"data": node})
}

func (a *adminCatalog) testNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	node, err := a.store.NodeSecret(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	driver, ok := a.openNode(w, node)
	if !ok {
		return
	}
	info, err := driver.HostInfo(r.Context())
	if err != nil {
		_ = a.store.UpdateNodeHealth(r.Context(), id, "offline", map[string]any{})
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "node_unreachable", "message": "节点无响应"})
		return
	}
	if err := a.store.RecordNodeReport(r.Context(), id, info); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": info.Raw})
}

// openNode decrypts the node credential and opens its driver, writing the
// error response itself when that is not possible.
func (a *adminCatalog) openNode(w http.ResponseWriter, node postgres.Node) (provider.Driver, bool) {
	if _, registered := provider.Lookup(node.ProviderType); !registered {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "provider_unsupported", "message": "此对接方式在当前版本中不可用"})
		return nil, false
	}
	driver, err := provider.OpenSealed(a.box, node.Sealed(), 15*time.Second)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_node"})
		return nil, false
	}
	return driver, true
}

func (a *adminCatalog) listProviderTypes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": provider.Types()})
}

func (a *adminCatalog) hostProbe(w http.ResponseWriter, r *http.Request) {
	node, err := a.store.NodeSecret(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	driver, ok := a.openNode(w, node)
	if !ok {
		return
	}
	probe, ok := driver.(provider.HostProbe)
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "provider_unsupported", "message": "该节点对接方式没有探针详情"})
		return
	}
	type sourceResult struct {
		name  string
		value any
		err   error
	}
	requests := probe.ProbeSections()
	results := make(chan sourceResult, len(requests))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	for _, request := range requests {
		go func(request provider.ProbeSection) {
			value, loadErr := request.Load(ctx)
			results <- sourceResult{name: request.Name, value: value, err: loadErr}
		}(request)
	}
	sources := make(map[string]any, len(requests))
	sourceErrors := map[string]string{}
	for range requests {
		response := <-results
		if response.err != nil {
			sourceErrors[response.name] = response.err.Error()
			continue
		}
		sources[response.name] = response.value
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"node": node, "sources": sources, "errors": sourceErrors, "fetched_at": time.Now().UTC(),
	}})
}

func (a *adminCatalog) listPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := a.store.ListPlans(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": postgres.PlatformPlans(plans)})
}

type availableTemplate struct {
	ID             string   `json:"id"`
	ProviderType   string   `json:"provider_type"`
	Name           string   `json:"name"`
	Virtualization string   `json:"virtualization"`
	Distro         string   `json:"distro"`
	Release        string   `json:"release"`
	Arch           string   `json:"arch"`
	Description    string   `json:"description"`
	NodeIDs        []string `json:"node_ids"`
	NodeNames      []string `json:"node_names"`
}

func (a *adminCatalog) listTemplates(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.store.ListNodeSecrets(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	type result struct {
		node   postgres.Node
		images []provider.Image
		err    error
	}
	results := make(chan result, len(nodes))
	var wait sync.WaitGroup
	for _, node := range nodes {
		// Hosted nodes sell their owner's plans only.
		if node.OwnerAccountID != "" {
			continue
		}
		if _, registered := provider.Lookup(node.ProviderType); !registered || node.Status != "online" {
			continue
		}
		wait.Add(1)
		go func(node postgres.Node) {
			defer wait.Done()
			driver, openErr := provider.OpenSealed(a.box, node.Sealed(), 12*time.Second)
			if openErr != nil {
				results <- result{node: node, err: openErr}
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			images, imageErr := driver.Images(ctx)
			results <- result{node: node, images: images, err: imageErr}
		}(node)
	}
	go func() { wait.Wait(); close(results) }()
	byKey := map[string]*availableTemplate{}
	warnings := make([]string, 0)
	for response := range results {
		if response.err != nil {
			warnings = append(warnings, response.node.Name+": "+response.err.Error())
			continue
		}
		for _, image := range response.images {
			if !image.Enabled || !image.Downloaded || !validVirtualization(image.Virtualization) {
				continue
			}
			// Same-named images on different backends are separate templates:
			// a plan binds one provider type and only schedules to its nodes.
			key := response.node.ProviderType + "\x00" + image.Virtualization + "\x00" + image.ID
			item := byKey[key]
			if item == nil {
				item = &availableTemplate{ID: image.ID, ProviderType: response.node.ProviderType, Name: image.Name, Virtualization: image.Virtualization, Distro: image.Distro, Release: image.Release, Arch: image.Arch, Description: image.Description}
				byKey[key] = item
			}
			item.NodeIDs = append(item.NodeIDs, response.node.ID)
			item.NodeNames = append(item.NodeNames, response.node.Name)
		}
	}
	templates := make([]availableTemplate, 0, len(byKey))
	for _, item := range byKey {
		templates = append(templates, *item)
	}
	if len(templates) == 0 && len(warnings) > 0 {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "template_catalog_unavailable", "message": "无法从在线节点读取模板：" + strings.Join(warnings, "；")})
		return
	}
	sort.Slice(templates, func(i, j int) bool {
		if templates[i].Virtualization == templates[j].Virtualization {
			return templates[i].Name < templates[j].Name
		}
		return templates[i].Virtualization < templates[j].Virtualization
	})
	writeJSON(w, http.StatusOK, map[string]any{"data": templates, "warnings": warnings})
}

func (a *adminCatalog) createPlan(w http.ResponseWriter, r *http.Request) {
	var input postgres.Plan
	if !decodeJSON(w, r, &input) {
		return
	}
	// Hosted plans are created by their owners in the hosting center.
	input.OwnerAccountID, input.NodeID = "", ""
	if input.ProviderType = strings.TrimSpace(input.ProviderType); input.ProviderType == "" {
		input.ProviderType = "clicd"
	}
	if message := validatePlan(input); message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	input.DefaultTemplateID = strings.TrimSpace(input.DefaultTemplateID)
	for index := range input.AllowedTemplateIDs {
		input.AllowedTemplateIDs[index] = strings.TrimSpace(input.AllowedTemplateIDs[index])
	}
	if message := validatePriceLimits(input.Prices); message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	if message, err := checkPlanStock(r.Context(), a.store, input, ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	} else if message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "stock_exceeded", "message": message})
		return
	}
	plan, err := a.store.CreatePlan(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "plan_create_failed", "message": "套餐编码可能已存在或参数无效"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": plan})
}

func (a *adminCatalog) updatePlan(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "enabled 字段不能为空"})
		return
	}
	if err := a.store.SetPlanEnabled(r.Context(), r.PathValue("id"), *input.Enabled); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "plan_not_found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *adminCatalog) replacePlan(w http.ResponseWriter, r *http.Request) {
	var input postgres.Plan
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ProviderType = strings.TrimSpace(input.ProviderType); input.ProviderType == "" {
		input.ProviderType = "clicd"
	}
	if message := validatePlan(input); message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	input.DefaultTemplateID = strings.TrimSpace(input.DefaultTemplateID)
	for index := range input.AllowedTemplateIDs {
		input.AllowedTemplateIDs[index] = strings.TrimSpace(input.AllowedTemplateIDs[index])
	}
	if message := validatePriceLimits(input.Prices); message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	if message, err := checkPlanStock(r.Context(), a.store, input, r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	} else if message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "stock_exceeded", "message": message})
		return
	}
	plan, err := a.store.UpdatePlan(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "plan_update_failed", "message": "套餐不存在、编码重复或参数无效"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": plan})
}

// planStockCapacity previews the stock ceiling for a platform plan draft;
// "id" names the plan being edited.
func (a *adminCatalog) planStockCapacity(w http.ResponseWriter, r *http.Request) {
	var input postgres.Plan
	if !decodeJSON(w, r, &input) {
		return
	}
	input.OwnerAccountID, input.NodeID = "", ""
	writeStockCapacity(w, r, a.store, input, input.ID)
}

func validatePlan(plan postgres.Plan) string {
	if strings.TrimSpace(plan.Code) == "" || strings.TrimSpace(plan.Name) == "" {
		return "套餐编码和名称不能为空"
	}
	descriptor, ok := provider.Lookup(plan.ProviderType)
	if !ok {
		return "对接方式无效"
	}
	if !containsString(descriptor.VirtualizationTypes, plan.Virtualization) {
		return descriptor.Name + " 支持的虚拟化类型为 " + strings.Join(descriptor.VirtualizationTypes, " / ")
	}
	if plan.VCPU < 1 || plan.RAMMB < 64 || plan.DiskGB < 1 {
		return "CPU 至少 1 核、内存至少 64 MB、磁盘至少 1 GB"
	}
	if !plan.AssignNAT && !plan.AssignIPv4 && !plan.AssignIPv6 {
		return "套餐至少需要启用一种网络方式"
	}
	if plan.PortMappingCount < 0 || plan.PortMappingCount > 64 || plan.IPv4Count < 1 || plan.IPv4Count > 64 || plan.IPv6Count < 1 || plan.IPv6Count > 64 {
		return "网络数量参数无效"
	}
	defaultTemplate := strings.TrimSpace(plan.DefaultTemplateID)
	if defaultTemplate == "" || len(plan.AllowedTemplateIDs) == 0 {
		return "必须配置默认模板和至少一个允许模板"
	}
	found := false
	for _, templateID := range plan.AllowedTemplateIDs {
		templateID = strings.TrimSpace(templateID)
		if templateID == defaultTemplate {
			found = true
		}
		if templateID == "" {
			return "模板 ID 不能为空"
		}
	}
	if !found {
		return "默认模板必须包含在允许模板列表中"
	}
	if len(plan.Prices) == 0 {
		return "至少需要一个价格"
	}
	seen := map[string]bool{}
	for i, price := range plan.Prices {
		if len(strings.TrimSpace(price.Currency)) != 3 || price.AmountMinor <= 0 {
			return "价格币种或金额无效"
		}
		cycle := postgres.NormalizeBillingCycle(price.BillingCycle)
		if cycle == "" {
			return "计费周期无效（自定义周期为 1-365 天或 1-60 个月）"
		}
		key := strings.ToUpper(strings.TrimSpace(price.Currency)) + "/" + cycle
		if seen[key] {
			return "同一币种的计费周期不能重复"
		}
		seen[key] = true
		plan.Prices[i].BillingCycle = cycle
	}
	return ""
}

func validVirtualization(value string) bool {
	return value == "lxc" || value == "kvm" || value == "podman"
}

// updateNode edits a node's connection settings and re-verifies them against
// the node before saving. The provider type and region cannot change; an empty
// api_key keeps the stored credential.
func (a *adminCatalog) updateNode(w http.ResponseWriter, r *http.Request) {
	existing, err := a.store.NodeSecret(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	var input struct {
		Name                string         `json:"name"`
		BaseURL             string         `json:"base_url"`
		APIKey              string         `json:"api_key"`
		VirtualizationTypes []string       `json:"virtualization_types"`
		ProviderOptions     map[string]any `json:"provider_options"`
		ExpiresAt           string         `json:"expires_at"`
		TrafficQuotaGB      int            `json:"traffic_quota_gb"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !validNodeBilling(w, &input.ExpiresAt, input.TrafficQuotaGB) {
		return
	}
	descriptor, ok := provider.Lookup(existing.ProviderType)
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "provider_unsupported", "message": "此对接方式在当前版本中不可用"})
		return
	}
	if strings.TrimSpace(input.Name) == "" || len(input.VirtualizationTypes) == 0 || (!descriptor.AgentManaged && strings.TrimSpace(input.BaseURL) == "") {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "节点名称、API 地址和虚拟化类型不能为空"})
		return
	}
	for _, virtualization := range input.VirtualizationTypes {
		if !containsString(descriptor.VirtualizationTypes, virtualization) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": descriptor.Name + " 支持的虚拟化类型为 " + strings.Join(descriptor.VirtualizationTypes, " / ")})
			return
		}
	}
	credential, ciphertext := strings.TrimSpace(input.APIKey), []byte(nil)
	if credential == "" {
		if credential, err = a.box.Open(existing.APIKeyCiphertext); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
			return
		}
	} else if ciphertext, err = a.box.Seal(credential); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	if descriptor.AgentManaged {
		input.BaseURL = provider.AgentEndpoint(credential)
		if input.BaseURL != existing.BaseURL && a.store.NodeExistsByEndpoint(r.Context(), input.BaseURL) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "node_update_failed", "message": "该 Agent 令牌已接入其他节点"})
			return
		}
	}
	options, err := provider.NormalizeOptions(descriptor, input.ProviderOptions)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	driver, err := provider.Open(existing.ProviderType, provider.Config{BaseURL: input.BaseURL, Credential: credential, Options: options, Timeout: 12 * time.Second})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	info, err := driver.HostInfo(r.Context())
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "node_unreachable", "message": "无法验证 " + descriptor.Name + " 节点连接：" + err.Error()})
		return
	}
	err = a.store.UpdateNode(r.Context(), existing.ID, postgres.UpdateNode{
		Name: input.Name, BaseURL: input.BaseURL, APIKeyCiphertext: ciphertext, ProviderOptions: options,
		VirtualizationTypes: input.VirtualizationTypes, Capacity: info.Raw,
		CapacityVCPU: info.Capacity.VCPU, CapacityRAMMB: info.Capacity.RAMMB, CapacityDiskGB: info.Capacity.DiskGB,
	})
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "node_update_failed", "message": "节点名称可能已存在"})
		return
	}
	if err := a.store.UpdateNodeBilling(r.Context(), existing.ID, input.ExpiresAt, input.TrafficQuotaGB); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *adminCatalog) deleteNode(w http.ResponseWriter, r *http.Request) {
	err := a.store.DeleteNode(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, postgres.ErrNodeNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
	case errors.Is(err, postgres.ErrNodeInUse):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "node_in_use", "message": "节点上仍有未终止的服务，请先迁出或终止"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// validNodeBilling checks the optional host expiry date (YYYY-MM-DD) and
// monthly traffic allowance.
func validNodeBilling(w http.ResponseWriter, expiresAt *string, trafficQuotaGB int) bool {
	*expiresAt = strings.TrimSpace(*expiresAt)
	if *expiresAt != "" {
		if _, err := time.Parse("2006-01-02", *expiresAt); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "母鸡到期日格式应为 YYYY-MM-DD"})
			return false
		}
	}
	if trafficQuotaGB < 0 || trafficQuotaGB > 10_000_000 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "月流量限额无效"})
		return false
	}
	return true
}
