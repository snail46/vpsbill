package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"vpsbill/internal/chat"
	"vpsbill/internal/marketplace"
	"vpsbill/internal/provider"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// HostingRules are the terms a customer accepts before publishing a node.
var HostingRules = []string{
	"资源共享：托管旨在将用户的闲置服务器接入平台以分摊成本。不建议以高额盈利为唯一目的的大规模托管。",
	"清退保障：母鸡离线满 24 小时系统或管理员将进行清退（如有特殊原因需提前联系管理员说明）。如服务器因租约到期、硬件故障或不可抗力无法继续提供服务，需按照实例剩余价值（托管余额）的 2 倍清退所有受影响的实例。",
	"工单响应：托管后可能会有用户针对网络、系统等问题提交相关工单，请务必及时登录平台处理，保证服务质量。平台管理员可以查看并处理这些工单，但托管方是第一处理人。",
	"真实合规：需真实填写母鸡地理位置、线路描述、到期时间、流量限制等，严禁虚假宣传。买家的实例不会超过母机到期时间，到期日早于计费周期结束时按剩余时间折算收费。资源以 Agent 检测值为准，可在平台上限内设置超售倍数并公开展示；母机持续负载过高会自动暂停销售。",
	"交流群组：每台托管母鸡有独立聊天室，包含机主和各个购买方，管理员可以查看全部聊天记录。",
}

var agentTokenPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type marketplaceAPI struct {
	store    *postgres.MarketplaceStore
	catalog  *postgres.CatalogStore
	billing  *postgres.BillingStore
	settings *settings.Manager
	box      *security.SecretBox
	service  *marketplace.Service
	hub      *chat.Hub
}

func (a *marketplaceAPI) marketplaceOpen(w http.ResponseWriter) bool {
	if a.settings.Current().Marketplace.Enabled {
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "marketplace_disabled", "message": "托管中心暂未开放"})
	return false
}

// ---- Balance ----

func (a *marketplaceAPI) customerWallet(w http.ResponseWriter, r *http.Request) {
	wallet, err := a.billing.Wallet(r.Context(), customerPrincipalFromContext(r.Context()).AccountID, 200)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": wallet})
}

func (a *marketplaceAPI) customerTopup(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AmountMinor int64 `json:"amount_minor"`
		// AgreeTerms confirms the top-up notice: balance is for platform
		// services only and cannot be withdrawn or refunded.
		AgreeTerms bool `json:"agree_terms"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.AgreeTerms {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "terms_not_accepted", "message": "请先阅读并同意充值须知"})
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	invoice, err := a.billing.CreateTopupInvoice(r.Context(), identity.AccountID, identity.UserID, input.AmountMinor)
	if errors.Is(err, postgres.ErrInvalidAmount) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_amount", "message": "充值金额需在 ¥1 到 ¥100000 之间"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": invoice})
}

func (a *marketplaceAPI) customerPayWithBalance(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	result, err := a.billing.PayInvoiceWithBalance(r.Context(), identity.AccountID, r.PathValue("id"), identity.UserID)
	switch {
	case errors.Is(err, postgres.ErrInsufficientBalance):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "insufficient_balance", "message": "余额不足，请先充值"})
	case errors.Is(err, postgres.ErrInvoiceUnavailable):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "invoice_unavailable", "message": "账单不存在、已支付或不能用余额支付"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": result})
	}
}

func (a *marketplaceAPI) adminWallet(w http.ResponseWriter, r *http.Request) {
	wallet, err := a.billing.Wallet(r.Context(), r.PathValue("id"), 200)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "customer_not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": wallet})
}

func (a *marketplaceAPI) adminAdjustWallet(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AmountMinor int64  `json:"amount_minor"`
		Reason      string `json:"reason"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	wallet, err := a.billing.AdjustWallet(r.Context(), r.PathValue("id"), principalFromContext(r.Context()).UserID, input.AmountMinor, input.Reason)
	switch {
	case errors.Is(err, postgres.ErrInvalidAmount):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_amount", "message": "请填写非零金额（不超过 ¥100000）和调整原因"})
	case errors.Is(err, postgres.ErrInsufficientBalance):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "insufficient_balance", "message": "扣减后余额不能为负"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": wallet})
	}
}

// ---- Market ----

func (a *marketplaceAPI) market(w http.ResponseWriter, r *http.Request) {
	if !a.marketplaceOpen(w) {
		return
	}
	nodes, err := a.store.HostedNodes(r.Context(), "", true, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	accountID := customerPrincipalFromContext(r.Context()).AccountID
	type listing struct {
		postgres.HostedNode
		Mine bool `json:"mine"`
	}
	result := make([]listing, 0, len(nodes))
	for _, node := range nodes {
		mine := node.OwnerAccountID == accountID
		node.OwnerAccountID = ""
		node.QuoteLease(time.Now())
		result = append(result, listing{HostedNode: node, Mine: mine})
	}
	runtime := a.settings.Current()
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"nodes": result, "fee_percent": runtime.Marketplace.FeePercent, "overcommit_limits": overcommitLimits(runtime)}})
}

// ---- Hosting center (the host's side) ----

func (a *marketplaceAPI) hosting(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	nodes, err := a.store.HostedNodes(r.Context(), identity.AccountID, false, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	wallet, err := a.billing.Wallet(r.Context(), identity.AccountID, 1)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	enrollKey, err := a.catalog.EnrollKey(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	pending, err := pendingAgents(r.Context(), a.catalog, a.box, identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	regions, err := a.billing.ListRegions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	runtime := a.settings.Current()
	server := strings.TrimRight(runtime.PublicURL, "/")
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"enabled":           runtime.Marketplace.Enabled,
		"fee_percent":       runtime.Marketplace.FeePercent,
		"offline_hours":     runtime.Marketplace.OfflineHours,
		"overcommit_limits": overcommitLimits(runtime),
		"rules":             HostingRules,
		"nodes":             nodes,
		"regions":           regions,
		"balance_minor":     wallet.BalanceMinor,
		"currency":          wallet.Currency,
		"install_command":   installCommand(server, enrollKey),
		"pending_agents":    pending,
	}})
}

func (a *marketplaceAPI) publishNode(w http.ResponseWriter, r *http.Request) {
	if !a.marketplaceOpen(w) {
		return
	}
	var input struct {
		postgres.HostedNodeInput
		// RegionName creates or picks a region by name when RegionID is empty.
		RegionName string `json:"region_name"`
		// EnrollmentID publishes a host from the pending list; Token is the
		// fallback of pasting the agent token.
		EnrollmentID string `json:"enrollment_id"`
		Token        string `json:"token"`
		AgreeRules   bool   `json:"agree_rules"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.AgreeRules {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "rules_not_accepted", "message": "请先阅读并同意托管规则与准则"})
		return
	}
	if message := input.Validate(); message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	if input.EnrollmentID != "" {
		token, err := enrollmentToken(r.Context(), a.catalog, a.box, input.EnrollmentID, identity.AccountID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "待接入的母机不存在，请刷新后重试"})
			return
		}
		input.Token = token
	}
	if !resolveRegion(w, r.Context(), a.catalog, &input.RegionID, input.RegionName) {
		return
	}
	token := strings.ToLower(strings.TrimSpace(input.Token))
	if !agentTokenPattern.MatchString(token) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "Agent 令牌应为安装脚本最后打印的 64 位十六进制字符串"})
		return
	}
	endpoint := provider.AgentEndpoint(token)
	if a.catalog.NodeExistsByEndpoint(r.Context(), endpoint) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "node_exists", "message": "该 Agent 令牌已接入其他母机"})
		return
	}
	driver, err := provider.Open("hatch", provider.Config{BaseURL: endpoint, Credential: token, Timeout: 12 * time.Second})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": err.Error()})
		return
	}
	info, err := driver.HostInfo(r.Context())
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "node_unreachable", "message": "没有检测到这台母机的 Agent 连接，请确认 Agent 正在运行、令牌正确：" + err.Error()})
		return
	}
	virtualization := hostedVirtualization(info.Raw)
	if len(virtualization) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "母机没有可用的 LXC 或 Podman 运行时"})
		return
	}
	ciphertext, err := a.box.Seal(token)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	id, err := a.store.CreateHostedNode(r.Context(), identity.AccountID, identity.UserID, input.HostedNodeInput, postgres.HostedNodeRegistration{
		BaseURL: endpoint, APIKeyCiphertext: ciphertext, VirtualizationTypes: virtualization, Capacity: info.Raw,
		CapacityVCPU: int64(info.Capacity.VCPU), CapacityRAMMB: info.Capacity.RAMMB, CapacityDiskGB: info.Capacity.DiskGB,
	})
	switch {
	case errors.Is(err, postgres.ErrNodeNameTaken):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "name_taken", "message": "母机名称已被使用，请换一个"})
	case errors.Is(err, postgres.ErrHostInDebt):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "host_in_debt", "message": "账户余额为负（有未结清的清退赔付），结清前不能发布新母机"})
	case err != nil:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "publish_failed", "message": "发布失败，请检查地域等信息"})
	default:
		_ = a.catalog.ClearEnrollment(r.Context(), endpoint)
		writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]string{"id": id}})
	}
}

// hostedVirtualization reads the runtimes a Hatch agent reports.
func hostedVirtualization(raw map[string]any) []string {
	result := []string{}
	// The value is []string straight from the driver or []any after a JSON
	// round trip; normalize through JSON.
	var values []string
	if encoded, err := json.Marshal(raw["runtimes"]); err == nil {
		_ = json.Unmarshal(encoded, &values)
	}
	for _, name := range values {
		if (name == "lxc" || name == "podman") && !containsString(result, name) {
			result = append(result, name)
		}
	}
	return result
}

// ownedNode checks that the node in the path belongs to the caller.
func (a *marketplaceAPI) ownedNode(w http.ResponseWriter, r *http.Request) (string, bool) {
	nodeID := r.PathValue("id")
	if !uuidPattern.MatchString(nodeID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return "", false
	}
	owner, retired, err := a.store.NodeOwner(r.Context(), nodeID)
	if err != nil || owner != customerPrincipalFromContext(r.Context()).AccountID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return "", false
	}
	if retired {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "node_retired", "message": "这台母机已清退"})
		return "", false
	}
	return nodeID, true
}

func (a *marketplaceAPI) updateHostedNode(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := a.ownedNode(w, r)
	if !ok {
		return
	}
	var input postgres.HostedNodeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if message := input.Validate(); message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	err := a.store.UpdateHostedNode(r.Context(), customerPrincipalFromContext(r.Context()).AccountID, nodeID, input)
	switch {
	case errors.Is(err, postgres.ErrNodeNameTaken):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "name_taken", "message": "母机名称已被使用，请换一个"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"id": nodeID}})
	}
}

func (a *marketplaceAPI) setHostedListing(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := a.ownedNode(w, r)
	if !ok {
		return
	}
	var input struct {
		Listed bool `json:"listed"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := a.store.SetListing(r.Context(), customerPrincipalFromContext(r.Context()).AccountID, nodeID, input.Listed); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"listed": input.Listed}})
}

// retireHostedNode lets a host withdraw its node. Every instance on it is
// cleared at twice its remaining value, as the hosting rules require.
func (a *marketplaceAPI) retireHostedNode(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := a.ownedNode(w, r)
	if !ok {
		return
	}
	var input struct {
		Confirm bool   `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.Confirm {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "confirmation_required", "message": "请确认按剩余价值 2 倍清退全部实例"})
		return
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "机主主动下架母机"
	}
	identity := customerPrincipalFromContext(r.Context())
	result, err := a.service.Clear(r.Context(), nodeID, 2, reason, "customer", identity.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (a *marketplaceAPI) hostedTemplates(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := a.ownedNode(w, r)
	if !ok {
		return
	}
	node, err := a.catalog.NodeSecret(r.Context(), nodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	driver, err := provider.OpenSealed(a.box, node.Sealed(), 12*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "node_unreachable", "message": "无法连接母机"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	images, err := driver.Images(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "node_unreachable", "message": "无法读取母机镜像：" + err.Error()})
		return
	}
	ready := make([]provider.Image, 0, len(images))
	for _, image := range images {
		if image.Enabled && image.Downloaded {
			ready = append(ready, image)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": ready})
}

type hostedPlanInput struct {
	Name               string   `json:"name"`
	Virtualization     string   `json:"virtualization"`
	VCPU               int      `json:"vcpu"`
	RAMMB              int      `json:"ram_mb"`
	DiskGB             int      `json:"disk_gb"`
	TrafficGB          int      `json:"traffic_gb"`
	NetworkDownMbps    int      `json:"network_down_mbps"`
	NetworkUpMbps      int      `json:"network_up_mbps"`
	PortMappingCount   int      `json:"port_mapping_count"`
	AllowedTemplateIDs []string `json:"allowed_template_ids"`
	DefaultTemplateID  string   `json:"default_template_id"`
	// Prices maps billing cycles to amounts in minor units; monthly is
	// required, other cycles are optional.
	Prices  map[string]int64 `json:"prices"`
	Enabled bool             `json:"enabled"`
	// Description is shown to buyers; PurchaseLimit caps instances per buyer
	// (0 = unlimited); EarlyRefund allows a full refund within an hour.
	Description   string `json:"description"`
	PurchaseLimit int    `json:"purchase_limit"`
	EarlyRefund   bool   `json:"early_refund"`
	// StockLimit is the plan's total stock (nil = capacity only);
	// PriceLimits caps how many times each cycle's price is sold.
	StockLimit  *int           `json:"stock_limit"`
	PriceLimits map[string]int `json:"price_limits"`
	// DiskIO caps each instance's disk (0 = unlimited).
	provider.DiskIO
}

func (in *hostedPlanInput) plan(node postgres.Node) (postgres.Plan, string) {
	in.Name = strings.TrimSpace(in.Name)
	in.DefaultTemplateID = strings.TrimSpace(in.DefaultTemplateID)
	in.Description = strings.TrimSpace(in.Description)
	switch {
	case len([]rune(in.Name)) < 2 || len([]rune(in.Name)) > 40:
		return postgres.Plan{}, "套餐名称需为 2-40 个字符"
	case !containsString(node.VirtualizationTypes, in.Virtualization):
		return postgres.Plan{}, "这台母机不支持所选虚拟化类型"
	case in.VCPU < 1 || in.RAMMB < 64 || in.DiskGB < 1:
		return postgres.Plan{}, "CPU 至少 1 核、内存至少 64 MB、磁盘至少 1 GB"
	// Overselling multiplies the total, but one instance cannot be larger
	// than the machine itself.
	case in.VCPU > max(node.ReportedVCPU, 1) || int64(in.RAMMB) > max(node.ReportedRAMMB, 1) || int64(in.DiskGB) > max(node.ReportedDiskGB, 1):
		return postgres.Plan{}, "单个实例的配置超过了母机的实际资源"
	case in.TrafficGB < 0 || in.NetworkDownMbps < 0 || in.NetworkUpMbps < 0 || in.NetworkDownMbps > 100000 || in.NetworkUpMbps > 100000:
		return postgres.Plan{}, "流量或带宽参数无效"
	case !in.DiskIO.Valid():
		return postgres.Plan{}, diskIOInvalid
	case in.PortMappingCount < 1 || in.PortMappingCount > 100:
		return postgres.Plan{}, "NAT 端口数量需在 1-100 之间"
	case len(in.AllowedTemplateIDs) == 0 || len(in.AllowedTemplateIDs) > 50 || !containsString(in.AllowedTemplateIDs, in.DefaultTemplateID):
		return postgres.Plan{}, "请至少选择一个系统镜像并设置默认镜像"
	case len([]rune(in.Description)) > 1000:
		return postgres.Plan{}, "套餐描述不能超过 1000 个字符"
	case in.PurchaseLimit < 0 || in.PurchaseLimit > 100:
		return postgres.Plan{}, "每人限购数量需在 0-100 之间（0 表示不限购）"
	}
	plan := postgres.Plan{
		Name: in.Name, ProviderType: "hatch", Virtualization: in.Virtualization, VCPU: in.VCPU, RAMMB: in.RAMMB, DiskGB: in.DiskGB,
		TrafficGB: in.TrafficGB, NetworkDownMbps: in.NetworkDownMbps, NetworkUpMbps: in.NetworkUpMbps,
		AssignNAT: true, PortMappingCount: in.PortMappingCount, IPv4Count: 1, IPv6Count: 1,
		DefaultTemplateID: in.DefaultTemplateID, AllowedTemplateIDs: in.AllowedTemplateIDs, Enabled: in.Enabled,
		Description: in.Description, PurchaseLimit: in.PurchaseLimit, EarlyRefund: in.EarlyRefund, DiskIO: in.DiskIO,
	}
	// A cycle left empty is not sold. Custom cycles are "d<N>" or "m<N>".
	cycles := make([]string, 0, len(in.Prices))
	for cycle := range in.Prices {
		cycles = append(cycles, cycle)
	}
	sort.Strings(cycles)
	seen := map[string]bool{}
	for _, raw := range cycles {
		amount := in.Prices[raw]
		if amount == 0 {
			continue
		}
		cycle := postgres.NormalizeBillingCycle(raw)
		switch {
		case cycle == "":
			return postgres.Plan{}, "计费周期无效（自定义周期为 1-365 天或 1-60 个月）"
		case seen[cycle]:
			return postgres.Plan{}, "计费周期不能重复"
		case amount < 100 || amount > 10_000_000:
			return postgres.Plan{}, "价格需在 ¥1 到 ¥100000 之间"
		}
		seen[cycle] = true
		price := postgres.Price{Currency: "CNY", BillingCycle: cycle, AmountMinor: amount}
		if limit, ok := in.PriceLimits[raw]; ok && limit > 0 {
			price.PurchaseLimit = &limit
		}
		plan.Prices = append(plan.Prices, price)
	}
	if len(plan.Prices) == 0 {
		return postgres.Plan{}, "请至少设置一个计费周期的价格"
	}
	if message := validatePriceLimits(plan.Prices); message != "" {
		return postgres.Plan{}, message
	}
	plan.NodeID, plan.StockLimit = node.ID, in.StockLimit
	return plan, ""
}

func (a *marketplaceAPI) createHostedPlan(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := a.ownedNode(w, r)
	if !ok {
		return
	}
	var input hostedPlanInput
	if !decodeJSON(w, r, &input) {
		return
	}
	node, err := a.catalog.NodeSecret(r.Context(), nodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	plan, message := input.plan(node)
	if message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	suffix := make([]byte, 5)
	if _, err := rand.Read(suffix); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	if !a.hostedStockAllowed(w, r, plan, "") {
		return
	}
	plan.Code = "H-" + strings.ToUpper(hex.EncodeToString(suffix))
	plan.OwnerAccountID, plan.NodeID = customerPrincipalFromContext(r.Context()).AccountID, nodeID
	created, err := a.catalog.CreatePlan(r.Context(), plan)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": created})
}

// ownedPlan checks that the plan belongs to the caller and returns its node.
func (a *marketplaceAPI) ownedPlan(w http.ResponseWriter, r *http.Request) (string, postgres.Node, bool) {
	planID := r.PathValue("id")
	owner, nodeID, err := a.store.HostedPlanOwner(r.Context(), planID)
	if err != nil || !uuidPattern.MatchString(planID) || owner != customerPrincipalFromContext(r.Context()).AccountID {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "plan_not_found"})
		return "", postgres.Node{}, false
	}
	node, err := a.catalog.NodeSecret(r.Context(), nodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "plan_not_found"})
		return "", postgres.Node{}, false
	}
	if node.RetiredAt != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "node_retired", "message": "这台母机已清退"})
		return "", postgres.Node{}, false
	}
	return planID, node, true
}

func (a *marketplaceAPI) updateHostedPlan(w http.ResponseWriter, r *http.Request) {
	planID, node, ok := a.ownedPlan(w, r)
	if !ok {
		return
	}
	var input hostedPlanInput
	if !decodeJSON(w, r, &input) {
		return
	}
	plan, message := input.plan(node)
	if message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": message})
		return
	}
	plans, err := a.catalog.ListPlans(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	for _, existing := range plans {
		if existing.ID == planID {
			plan.Code = existing.Code
		}
	}
	if !a.hostedStockAllowed(w, r, plan, planID) {
		return
	}
	updated, err := a.catalog.UpdatePlan(r.Context(), planID, plan)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": updated})
}

// hostedStockAllowed answers with an error when the plan's stock is above
// what the host's node can hold.
func (a *marketplaceAPI) hostedStockAllowed(w http.ResponseWriter, r *http.Request, plan postgres.Plan, existingID string) bool {
	message, err := checkPlanStock(r.Context(), a.catalog, plan, existingID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return false
	}
	if message != "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "stock_exceeded", "message": message})
		return false
	}
	return true
}

// hostedStockCapacity previews the stock ceiling for a plan draft on the
// host's node; plan_id names the plan being edited.
func (a *marketplaceAPI) hostedStockCapacity(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := a.ownedNode(w, r)
	if !ok {
		return
	}
	var input struct {
		PlanID         string `json:"plan_id"`
		Virtualization string `json:"virtualization"`
		VCPU           int    `json:"vcpu"`
		RAMMB          int    `json:"ram_mb"`
		DiskGB         int    `json:"disk_gb"`
		TrafficGB      int    `json:"traffic_gb"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.PlanID != "" {
		if owner, planNode, err := a.store.HostedPlanOwner(r.Context(), input.PlanID); err != nil || owner != customerPrincipalFromContext(r.Context()).AccountID || planNode != nodeID {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "plan_not_found"})
			return
		}
	}
	writeStockCapacity(w, r, a.catalog, postgres.Plan{NodeID: nodeID, Virtualization: input.Virtualization, VCPU: input.VCPU, RAMMB: input.RAMMB, DiskGB: input.DiskGB, TrafficGB: input.TrafficGB}, input.PlanID)
}

func (a *marketplaceAPI) setHostedPlanEnabled(w http.ResponseWriter, r *http.Request) {
	planID, _, ok := a.ownedPlan(w, r)
	if !ok {
		return
	}
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := a.catalog.SetPlanEnabled(r.Context(), planID, input.Enabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"enabled": input.Enabled}})
}

// ---- Staff ----

func (a *marketplaceAPI) adminHostedNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.store.HostedNodes(r.Context(), "", false, true)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": nodes})
}

func (a *marketplaceAPI) adminClearNode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Multiplier int    `json:"multiplier"`
		Reason     string `json:"reason"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Multiplier != 1 && input.Multiplier != 2 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "清退倍数只能是 1 或 2"})
		return
	}
	if strings.TrimSpace(input.Reason) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "请填写清退原因"})
		return
	}
	result, err := a.service.Clear(r.Context(), r.PathValue("id"), input.Multiplier, input.Reason, "staff", principalFromContext(r.Context()).UserID)
	switch {
	case errors.Is(err, postgres.ErrHostedNodeNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
	case errors.Is(err, postgres.ErrNodeRetired):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "node_retired", "message": "这台母机已清退"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"data": result})
	}
}

func (a *marketplaceAPI) adminHoldClearance(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Until *time.Time `json:"until"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Until != nil && (input.Until.Before(time.Now()) || input.Until.After(time.Now().Add(90*24*time.Hour))) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "暂缓时间需在未来 90 天内"})
		return
	}
	if err := a.store.HoldClearance(r.Context(), r.PathValue("id"), principalFromContext(r.Context()).UserID, input.Until); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"until": input.Until}})
}

func (a *marketplaceAPI) adminSetListing(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Listed bool `json:"listed"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := a.store.SetListing(r.Context(), "", r.PathValue("id"), input.Listed); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "node_not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]bool{"listed": input.Listed}})
}

// ---- Chat ----

// chatViewer is who is reading a room: a customer account or staff.
type chatViewer struct {
	accountID, userID, authorType, name string
	canPost                             bool
	// stillMember is re-checked while a stream is open, so a buyer who
	// sold or lost their instance drops out of the room; nil for staff.
	stillMember func(context.Context) bool
}

func (a *marketplaceAPI) customerChatViewer(w http.ResponseWriter, r *http.Request) (chatViewer, bool) {
	identity := customerPrincipalFromContext(r.Context())
	nodeID := r.PathValue("node")
	if !uuidPattern.MatchString(nodeID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "room_not_found"})
		return chatViewer{}, false
	}
	role, retired, err := a.store.ChatRole(r.Context(), nodeID, identity.AccountID)
	if err != nil || role == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "room_not_found"})
		return chatViewer{}, false
	}
	name, err := a.store.ChatAuthorName(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return chatViewer{}, false
	}
	stillMember := func(ctx context.Context) bool {
		role, _, err := a.store.ChatRole(ctx, nodeID, identity.AccountID)
		return err == nil && role != ""
	}
	return chatViewer{accountID: identity.AccountID, userID: identity.UserID, authorType: role, name: name, canPost: !retired, stillMember: stillMember}, true
}

func (a *marketplaceAPI) staffChatViewer(w http.ResponseWriter, r *http.Request) (chatViewer, bool) {
	identity := principalFromContext(r.Context())
	nodeID := r.PathValue("node")
	if !uuidPattern.MatchString(nodeID) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "room_not_found"})
		return chatViewer{}, false
	}
	if _, retired, err := a.store.ChatRole(r.Context(), nodeID, ""); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "room_not_found"})
		return chatViewer{}, false
	} else {
		name := identity.DisplayName
		if name == "" {
			name = "平台管理员"
		}
		return chatViewer{userID: identity.UserID, authorType: "staff", name: name + "（平台）", canPost: !retired}, true
	}
}

func (a *marketplaceAPI) customerChatRooms(w http.ResponseWriter, r *http.Request) {
	a.chatRooms(w, r, customerPrincipalFromContext(r.Context()).AccountID)
}

func (a *marketplaceAPI) adminChatRooms(w http.ResponseWriter, r *http.Request) {
	a.chatRooms(w, r, "")
}

func (a *marketplaceAPI) chatRooms(w http.ResponseWriter, r *http.Request, accountID string) {
	rooms, err := a.store.ChatRooms(r.Context(), accountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rooms})
}

func (a *marketplaceAPI) customerChatMessages(w http.ResponseWriter, r *http.Request) {
	if viewer, ok := a.customerChatViewer(w, r); ok {
		a.chatMessages(w, r, viewer)
	}
}

func (a *marketplaceAPI) adminChatMessages(w http.ResponseWriter, r *http.Request) {
	if viewer, ok := a.staffChatViewer(w, r); ok {
		a.chatMessages(w, r, viewer)
	}
}

func (a *marketplaceAPI) chatMessages(w http.ResponseWriter, r *http.Request, viewer chatViewer) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	messages, err := a.store.ChatMessages(r.Context(), r.PathValue("node"), after, before, 100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	for i := range messages {
		messages[i] = messages[i].ForViewer(viewer.accountID, viewer.userID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"messages": messages, "can_post": viewer.canPost, "role": viewer.authorType}})
}

func (a *marketplaceAPI) customerPostChat(w http.ResponseWriter, r *http.Request) {
	if viewer, ok := a.customerChatViewer(w, r); ok {
		a.postChat(w, r, viewer)
	}
}

func (a *marketplaceAPI) adminPostChat(w http.ResponseWriter, r *http.Request) {
	if viewer, ok := a.staffChatViewer(w, r); ok {
		a.postChat(w, r, viewer)
	}
}

func (a *marketplaceAPI) postChat(w http.ResponseWriter, r *http.Request, viewer chatViewer) {
	var input struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !viewer.canPost {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "room_closed", "message": "母机已清退，聊天室只读"})
		return
	}
	message, err := a.store.PostChatMessage(r.Context(), r.PathValue("node"), viewer.authorType, viewer.userID, viewer.accountID, viewer.name, input.Body)
	var limited *postgres.ChatLimitError
	if errors.As(err, &limited) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "chat_limited", "message": limited.Message})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_message", "message": "消息不能为空，最多 2000 字"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": message.ForViewer(viewer.accountID, viewer.userID)})
}

func (a *marketplaceAPI) customerChatStream(w http.ResponseWriter, r *http.Request) {
	if viewer, ok := a.customerChatViewer(w, r); ok {
		a.chatStream(w, r, viewer)
	}
}

func (a *marketplaceAPI) adminChatStream(w http.ResponseWriter, r *http.Request) {
	if viewer, ok := a.staffChatViewer(w, r); ok {
		a.chatStream(w, r, viewer)
	}
}

// chatStream pushes new room messages over a WebSocket. Clients send
// messages with the POST endpoint; the socket is receive-only. The default
// origin check keeps other sites from opening it with the session cookie.
func (a *marketplaceAPI) chatStream(w http.ResponseWriter, r *http.Request, viewer chatViewer) {
	if a.hub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "chat_unavailable"})
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	messages, unsubscribe := a.hub.Subscribe(r.PathValue("node"))
	defer unsubscribe()
	ctx := conn.CloseRead(r.Context())
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if viewer.stillMember != nil && !viewer.stillMember(ctx) {
				_ = conn.Close(websocket.StatusPolicyViolation, "no longer a member of this room")
				return
			}
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		case message, open := <-messages:
			if !open {
				return
			}
			body, _ := json.Marshal(message.ForViewer(viewer.accountID, viewer.userID))
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(writeCtx, websocket.MessageText, body)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
