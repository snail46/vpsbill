package app

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"clicd-billing/internal/clicd"
	"clicd-billing/internal/security"
	"clicd-billing/internal/store/postgres"
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
		ProviderType        string   `json:"provider_type"`
		RegionCode          string   `json:"region_code"`
		RegionName          string   `json:"region_name"`
		Name                string   `json:"name"`
		BaseURL             string   `json:"base_url"`
		APIKey              string   `json:"api_key"`
		VirtualizationTypes []string `json:"virtualization_types"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ProviderType = strings.TrimSpace(input.ProviderType)
	if input.ProviderType == "" {
		input.ProviderType = "clicd"
	}
	if input.ProviderType != "clicd" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "provider_unsupported", "message": "当前版本仅支持 CLICD 对接方式"})
		return
	}
	if strings.TrimSpace(input.RegionCode) == "" || strings.TrimSpace(input.RegionName) == "" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.APIKey) == "" || len(input.VirtualizationTypes) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "节点名称、地区、API 地址、API Key 和虚拟化类型不能为空"})
		return
	}
	for _, virtualization := range input.VirtualizationTypes {
		if virtualization != "lxc" && virtualization != "kvm" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "虚拟化类型只能为 lxc 或 kvm"})
			return
		}
	}
	client, err := clicd.NewClient(input.BaseURL, input.APIKey, 12*time.Second)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	hostInfo, err := client.HostInfo(r.Context())
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "node_unreachable", "message": "无法验证 CLICD 节点连接"})
		return
	}
	ciphertext, err := a.box.Seal(input.APIKey)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	totals := clicd.CapacityFromHostInfo(hostInfo)
	node, err := a.store.CreateNode(r.Context(), postgres.CreateNode{
		ProviderType:        input.ProviderType,
		RegionCode:          input.RegionCode,
		RegionName:          input.RegionName,
		Name:                input.Name,
		BaseURL:             input.BaseURL,
		APIKeyCiphertext:    ciphertext,
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
	writeJSON(w, http.StatusCreated, map[string]any{"data": node})
}

func (a *adminCatalog) testNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	node, err := a.store.NodeSecret(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	if node.ProviderType != "clicd" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "provider_unsupported", "message": "此对接方式暂不支持连接测试"})
		return
	}
	apiKey, err := a.box.Open(node.APIKeyCiphertext)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "secret_unavailable"})
		return
	}
	client, err := clicd.NewClient(node.BaseURL, apiKey, 12*time.Second)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_node"})
		return
	}
	hostInfo, err := client.HostInfo(r.Context())
	if err != nil {
		_ = a.store.UpdateNodeHealth(r.Context(), id, "offline", map[string]any{})
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "node_unreachable", "message": "CLICD 节点无响应"})
		return
	}
	totals := clicd.CapacityFromHostInfo(hostInfo)
	if err := a.store.UpdateNodeHealth(r.Context(), id, "online", hostInfo, int64(totals.VCPU), totals.RAMMB, totals.DiskGB); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": hostInfo})
}

func (a *adminCatalog) hostProbe(w http.ResponseWriter, r *http.Request) {
	node, err := a.store.NodeSecret(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	if node.ProviderType != "clicd" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "provider_unsupported", "message": "该节点对接方式没有 CLICD 探针详情"})
		return
	}
	apiKey, err := a.box.Open(node.APIKeyCiphertext)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "secret_unavailable"})
		return
	}
	client, err := clicd.NewClient(node.BaseURL, apiKey, 15*time.Second)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_node"})
		return
	}
	type sourceResult struct {
		name  string
		value any
		err   error
	}
	requests := []struct {
		name string
		load func(context.Context) (any, error)
	}{
		{name: "dashboard", load: client.Dashboard},
		{name: "host_info", load: func(ctx context.Context) (any, error) { return client.HostInfo(ctx) }},
		{name: "host_history", load: client.HostHistory},
		{name: "host_report", load: client.HostReport},
	}
	results := make(chan sourceResult, len(requests))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	for _, request := range requests {
		go func(request struct {
			name string
			load func(context.Context) (any, error)
		}) {
			value, loadErr := request.load(ctx)
			results <- sourceResult{name: request.name, value: value, err: loadErr}
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
	writeJSON(w, http.StatusOK, map[string]any{"data": plans})
}

type availableTemplate struct {
	ID             string   `json:"id"`
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
		images []clicd.Image
		err    error
	}
	results := make(chan result, len(nodes))
	var wait sync.WaitGroup
	for _, node := range nodes {
		if node.ProviderType != "clicd" || node.Status != "online" {
			continue
		}
		wait.Add(1)
		go func(node postgres.Node) {
			defer wait.Done()
			apiKey, openErr := a.box.Open(node.APIKeyCiphertext)
			if openErr != nil {
				results <- result{node: node, err: openErr}
				return
			}
			client, clientErr := clicd.NewClient(node.BaseURL, apiKey, 12*time.Second)
			if clientErr != nil {
				results <- result{node: node, err: clientErr}
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			images, imageErr := client.Images(ctx)
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
			if !image.Enabled || !image.Downloaded || (image.Type != "lxc" && image.Type != "kvm") {
				continue
			}
			key := image.Type + "\x00" + image.ID
			item := byKey[key]
			if item == nil {
				item = &availableTemplate{ID: image.ID, Name: image.Name, Virtualization: image.Type, Distro: image.Distro, Release: image.Release, Arch: image.Arch, Description: image.Description}
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
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "template_catalog_unavailable", "message": "无法从在线 CLICD 节点读取模板：" + strings.Join(warnings, "；")})
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
	if message := validatePlan(input); message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	input.DefaultTemplateID = strings.TrimSpace(input.DefaultTemplateID)
	for index := range input.AllowedTemplateIDs {
		input.AllowedTemplateIDs[index] = strings.TrimSpace(input.AllowedTemplateIDs[index])
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
	if message := validatePlan(input); message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	input.DefaultTemplateID = strings.TrimSpace(input.DefaultTemplateID)
	for index := range input.AllowedTemplateIDs {
		input.AllowedTemplateIDs[index] = strings.TrimSpace(input.AllowedTemplateIDs[index])
	}
	plan, err := a.store.UpdatePlan(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "plan_update_failed", "message": "套餐不存在、编码重复或参数无效"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": plan})
}

func validatePlan(plan postgres.Plan) string {
	if strings.TrimSpace(plan.Code) == "" || strings.TrimSpace(plan.Name) == "" {
		return "套餐编码和名称不能为空"
	}
	if plan.Virtualization != "lxc" && plan.Virtualization != "kvm" {
		return "虚拟化类型必须为 lxc 或 kvm"
	}
	if plan.VCPU < 1 || plan.RAMMB < 128 || plan.DiskGB < 1 {
		return "CPU、内存和磁盘参数无效"
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
	for _, price := range plan.Prices {
		if len(strings.TrimSpace(price.Currency)) != 3 || price.AmountMinor <= 0 {
			return "价格币种或金额无效"
		}
		switch price.BillingCycle {
		case "monthly", "quarterly", "semiannual", "annual":
		default:
			return "计费周期无效"
		}
	}
	return ""
}
