package app

import (
	"net/http"
	"strings"
	"time"

	"clicd-billing/internal/clicd"
	"clicd-billing/internal/config"
	"clicd-billing/internal/security"
	"clicd-billing/internal/store/postgres"
)

type adminCatalog struct {
	cfg   config.Config
	store *postgres.CatalogStore
	box   *security.SecretBox
}

func newAdminCatalog(cfg config.Config, store *postgres.CatalogStore, box *security.SecretBox) *adminCatalog {
	return &adminCatalog{cfg: cfg, store: store, box: box}
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

func (a *adminCatalog) listPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := a.store.ListPlans(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": plans})
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
