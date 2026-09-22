package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"clicd-billing/internal/clicd"
	"clicd-billing/internal/payment"
	"clicd-billing/internal/security"
	"clicd-billing/internal/settings"
	"clicd-billing/internal/store/postgres"
)

type customerPortal struct {
	store    *postgres.PortalStore
	billing  *postgres.BillingStore
	catalog  *postgres.CatalogStore
	settings *settings.Manager
	box      *security.SecretBox
}

func newCustomerPortal(runtime *settings.Manager, store *postgres.PortalStore, billing *postgres.BillingStore, catalog *postgres.CatalogStore, box *security.SecretBox) *customerPortal {
	return &customerPortal{store: store, billing: billing, catalog: catalog, settings: runtime, box: box}
}

func (p *customerPortal) listServices(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	rows, err := p.store.ListServices(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}
func (p *customerPortal) listInvoices(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	rows, err := p.store.ListInvoices(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}
func (p *customerPortal) listTransactions(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	rows, err := p.store.ListTransactions(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}
func (p *customerPortal) serviceAction(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	jobID, err := p.store.QueueServiceAction(r.Context(), identity.AccountID, identity.UserID, r.PathValue("id"), r.PathValue("action"), remoteIP(r), r.UserAgent())
	switch {
	case errors.Is(err, postgres.ErrServiceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_not_found"})
	case errors.Is(err, postgres.ErrServiceActionUnavailable):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "action_unavailable", "message": "当前服务或运行状态不允许该操作"})
	case errors.Is(err, postgres.ErrActionInProgress):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "action_in_progress", "message": "相同操作正在处理中"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]string{"job_id": jobID, "status": "pending"}})
	}
}

func (p *customerPortal) serviceClient(r *http.Request) (postgres.CustomerServiceAccess, *clicd.Client, error) {
	identity := customerPrincipalFromContext(r.Context())
	access, err := p.store.ServiceAccess(r.Context(), identity.AccountID, r.PathValue("id"))
	if err != nil {
		return access, nil, err
	}
	if access.Status != "active" || access.ProviderType != "clicd" {
		return access, nil, postgres.ErrServiceActionUnavailable
	}
	apiKey, err := p.box.Open(access.APIKeyCiphertext)
	if err != nil {
		return access, nil, fmt.Errorf("decrypt node credential: %w", err)
	}
	client, err := clicd.NewClient(access.BaseURL, apiKey, 20*time.Second)
	return access, client, err
}

func (p *customerPortal) serviceRuntime(w http.ResponseWriter, r *http.Request) {
	access, client, err := p.serviceClient(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	type result struct {
		name  string
		value any
		err   error
	}
	brief := r.URL.Query().Get("brief") == "1"
	count := 3
	results := make(chan result, 5)
	go func() {
		value, callErr := client.GetContainer(r.Context(), access.InstanceName)
		results <- result{"container", value, callErr}
	}()
	go func() {
		value, callErr := client.ContainerUsage(r.Context(), access.InstanceName)
		results <- result{"usage", value, callErr}
	}()
	go func() {
		value, callErr := client.ContainerTraffic(r.Context(), access.InstanceName)
		results <- result{"traffic", value, callErr}
	}()
	if !brief {
		count += 2
		go func() {
			value, callErr := client.ContainerHistory(r.Context(), access.InstanceName)
			results <- result{"history", value, callErr}
		}()
		go func() { value, callErr := client.Images(r.Context()); results <- result{"images", value, callErr} }()
	}
	sources := map[string]any{}
	errorsBySource := map[string]string{}
	var container clicd.Container
	var images []clicd.Image
	for range count {
		row := <-results
		if row.err != nil {
			errorsBySource[row.name] = row.err.Error()
			continue
		}
		sources[row.name] = row.value
		switch value := row.value.(type) {
		case clicd.Container:
			container = value
		case []clicd.Image:
			images = value
		}
	}
	if container.Name == "" {
		p.writeServiceError(w, errors.New(errorsBySource["container"]))
		return
	}
	allowed := make(map[string]bool, len(access.AllowedTemplateIDs))
	for _, id := range access.AllowedTemplateIDs {
		allowed[id] = true
	}
	templates := make([]clicd.Image, 0)
	for _, image := range images {
		if allowed[image.ID] && image.Enabled && image.Downloaded && (image.Type == "" || image.Type == access.Virtualization) {
			templates = append(templates, image)
		}
	}
	delete(sources, "images")
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"container": container, "usage": sources["usage"], "history": sources["history"], "traffic": sources["traffic"],
		"templates": templates, "errors": errorsBySource,
	}})
}

func (p *customerPortal) serviceCredential(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	access, _, err := p.serviceClient(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	password := ""
	if len(access.RootPasswordCiphertext) > 0 {
		password, err = p.box.Open(access.RootPasswordCiphertext)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "credential_unavailable", "message": "实例凭据无法解密"})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"username": "root", "password": password, "stored": password != ""}})
}

func validRootPassword(value string) bool {
	if len(value) < 8 || len(value) > 64 {
		return false
	}
	hasLetter, hasDigit := false, false
	for _, char := range value {
		hasLetter = hasLetter || (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
		hasDigit = hasDigit || char >= '0' && char <= '9'
	}
	return hasLetter && hasDigit
}

func (p *customerPortal) resetServicePassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	var input struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Password = strings.TrimSpace(input.Password)
	if input.Password != "" && !validRootPassword(input.Password) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_password", "message": "密码需为 8-64 位并同时包含字母和数字"})
		return
	}
	access, client, err := p.serviceClient(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	password, err := client.ResetPassword(r.Context(), access.InstanceName, input.Password)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	ciphertext, err := p.box.Seal(password)
	if err == nil {
		identity := customerPrincipalFromContext(r.Context())
		err = p.store.UpdateRootPassword(r.Context(), identity.AccountID, access.ServiceID, identity.UserID, ciphertext, remoteIP(r), r.UserAgent())
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "credential_store_failed", "message": "密码已在节点修改，但加密保存失败，请立即妥善记录", "data": map[string]string{"password": password}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"username": "root", "password": password}})
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (p *customerPortal) reinstallService(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TemplateID string `json:"template_id"`
		Password   string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.Password = strings.TrimSpace(input.Password)
	access, client, err := p.serviceClient(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	if !containsString(access.AllowedTemplateIDs, input.TemplateID) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "template_not_allowed", "message": "所选系统不在当前套餐允许的模板中"})
		return
	}
	if !validRootPassword(input.Password) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_password", "message": "请设置 8-64 位且同时包含字母和数字的新 root 密码"})
		return
	}
	taskID, err := client.Reinstall(r.Context(), access.InstanceName, clicd.ReinstallSpec{TemplateID: input.TemplateID, SSHAuthMode: "password", SSHPassword: input.Password})
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	ciphertext, err := p.box.Seal(input.Password)
	identity := customerPrincipalFromContext(r.Context())
	if err == nil {
		err = p.store.SaveRootPassword(r.Context(), identity.AccountID, access.ServiceID, ciphertext)
	}
	if err == nil {
		err = p.store.RecordServiceOperation(r.Context(), identity.UserID, access.ServiceID, "service.reinstall", remoteIP(r), r.UserAgent(), map[string]any{"template_id": input.TemplateID, "external_task_id": taskID})
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"data": map[string]string{"task_id": taskID, "status": "queued"}})
}

func (p *customerPortal) addPortMapping(w http.ResponseWriter, r *http.Request) {
	p.mutatePortMapping(w, r, "add")
}
func (p *customerPortal) updatePortMapping(w http.ResponseWriter, r *http.Request) {
	p.mutatePortMapping(w, r, "update")
}
func (p *customerPortal) deletePortMapping(w http.ResponseWriter, r *http.Request) {
	p.mutatePortMapping(w, r, "delete")
}

func (p *customerPortal) mutatePortMapping(w http.ResponseWriter, r *http.Request, action string) {
	access, client, err := p.serviceClient(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	index := -1
	if action != "add" {
		index, err = strconv.Atoi(r.PathValue("index"))
		if err != nil || index < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_mapping"})
			return
		}
	}
	var mappings []clicd.PortMapping
	if action == "delete" {
		container, callErr := client.GetContainer(r.Context(), access.InstanceName)
		if callErr != nil || index >= len(container.PortMappings) || strings.EqualFold(container.PortMappings[index].Description, "SSH") {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "mapping_protected", "message": "SSH 管理端口不能删除"})
			return
		}
		mappings, err = client.DeletePortMapping(r.Context(), access.InstanceName, index)
	} else {
		var input struct {
			ContainerPort int    `json:"container_port"`
			Protocol      string `json:"protocol"`
			Description   string `json:"description"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		input.Protocol = strings.ToLower(strings.TrimSpace(input.Protocol))
		input.Description = strings.TrimSpace(input.Description)
		if input.ContainerPort < 1 || input.ContainerPort > 65535 || (input.Protocol != "tcp" && input.Protocol != "udp") || len(input.Description) > 80 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_mapping", "message": "端口需为 1-65535，协议仅支持 TCP/UDP，备注最多 80 字符"})
			return
		}
		mapping := clicd.PortMapping{ContainerPort: input.ContainerPort, Protocol: input.Protocol, Description: input.Description}
		if action == "add" {
			mapping.HostPort, err = client.RandomPort(r.Context(), access.InstanceName)
			if err == nil && mapping.HostPort == 0 {
				err = errors.New("节点没有可用的 NAT 端口")
			}
			if err == nil {
				mappings, err = client.AddPortMapping(r.Context(), access.InstanceName, mapping)
			}
		} else {
			container, callErr := client.GetContainer(r.Context(), access.InstanceName)
			if callErr != nil || index >= len(container.PortMappings) {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "mapping_not_found"})
				return
			}
			existing := container.PortMappings[index]
			mapping.HostPort, mapping.HostIP = existing.HostPort, existing.HostIP
			if strings.EqualFold(existing.Description, "SSH") {
				mapping.Protocol, mapping.Description = existing.Protocol, "SSH"
			}
			mappings, err = client.UpdatePortMapping(r.Context(), access.InstanceName, index, mapping)
		}
	}
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	_ = p.store.RecordServiceOperation(r.Context(), identity.UserID, access.ServiceID, "service.port_mapping."+action, remoteIP(r), r.UserAgent(), map[string]any{"index": index})
	writeJSON(w, http.StatusOK, map[string]any{"data": mappings})
}

func (p *customerPortal) consoleTicket(w http.ResponseWriter, r *http.Request) {
	access, client, err := p.serviceClient(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	kind := r.PathValue("kind")
	if kind == "vnc" && access.Virtualization != "kvm" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "console_unavailable", "message": "VNC 控制台仅适用于 KVM 实例"})
		return
	}
	ticket, err := client.ConsoleTicket(r.Context(), access.InstanceName, kind, r.UserAgent())
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"ticket": ticket, "websocket_path": fmt.Sprintf("/api/v1/customer/services/%s/console/%s", access.ServiceID, kind)}})
}

func (p *customerPortal) consoleProxy(w http.ResponseWriter, r *http.Request) {
	access, client, err := p.serviceClient(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	target, err := client.ConsoleTarget(access.InstanceName, r.PathValue("kind"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "console_unavailable"})
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	proxy.Director = func(request *http.Request) {
		request.URL.Scheme = target.Scheme
		request.URL.Host = target.Host
		request.URL.Path = target.Path
		request.URL.RawPath = ""
		request.URL.RawQuery = target.RawQuery
		request.Host = target.Host
		request.Header.Del("Cookie")
		request.Header.Del("X-CSRF-Token")
		request.Header.Del("X-API-Key")
	}
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(writer, "console proxy unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}

func (p *customerPortal) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, postgres.ErrServiceNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "service_not_found"})
	case errors.Is(err, postgres.ErrServiceActionUnavailable):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "action_unavailable", "message": "当前服务状态不允许此操作"})
	default:
		var apiErr *clicd.APIError
		if errors.As(err, &apiErr) {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "node_request_failed", "message": apiErr.Message})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "node_unavailable", "message": "节点暂时无法完成请求"})
	}
}

func (p *customerPortal) catalogData(w http.ResponseWriter, r *http.Request) {
	plans, err := p.catalog.ListPlans(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	available := make([]postgres.Plan, 0)
	for _, plan := range plans {
		if plan.Enabled && len(plan.Prices) > 0 {
			available = append(available, plan)
		}
	}
	regions, err := p.billing.ListRegions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"plans": available, "regions": regions, "checkout_enabled": p.settings.Current().PaymentGateway.Type != "disabled"}})
}

func (p *customerPortal) listOrders(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	rows, err := p.billing.ListOrdersByAccount(r.Context(), identity.AccountID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (p *customerPortal) createOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Items []postgres.OrderItemInput `json:"items"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	order, err := p.billing.CreateOrder(r.Context(), postgres.CreateOrderInput{AccountID: identity.AccountID, Items: input.Items, ActorType: "customer", ActorID: identity.UserID})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "order_create_failed", "message": "地区、套餐、价格或配置无效"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": order})
}

func (p *customerPortal) checkout(w http.ResponseWriter, r *http.Request) {
	runtime := p.settings.Current()
	config := runtime.PaymentGateway
	if config.Type == "" || config.Type == "disabled" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "checkout_unavailable", "message": "商家尚未配置在线支付网关"})
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	intent, err := p.billing.PreparePaymentIntent(r.Context(), identity.AccountID, r.PathValue("id"), config.Type)
	if errors.Is(err, postgres.ErrInvoiceUnavailable) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "invoice_unavailable", "message": "账单不存在、已支付或当前不可付款"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	if (config.Type == "alipay_f2f" || config.Type == "epay") && intent.Currency != "CNY" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "currency_unsupported", "message": "当前支付网关仅支持人民币账单"})
		return
	}
	if intent.CheckoutURL == "" {
		request := payment.CheckoutRequest{MerchantReference: intent.MerchantReference, InvoiceNumber: intent.InvoiceNumber, AmountMinor: intent.AmountMinor, Currency: intent.Currency, ExpiresAt: intent.ExpiresAt}
		switch config.Type {
		case "generic":
			intent.CheckoutURL, err = (payment.GenericGateway{BaseURL: config.GenericBaseURL, PublicURL: runtime.PublicURL, Secret: config.GenericSecret}).CheckoutURL(request)
		case "epay":
			intent.CheckoutURL, err = (payment.EpayGateway{APIURL: config.EpayAPIURL, PartnerID: config.EpayPartnerID, MerchantKey: config.EpayMerchantKey, PaymentType: config.EpayPaymentType, PublicURL: runtime.PublicURL, SiteName: runtime.AppName}).CheckoutURL(request)
		case "alipay_f2f":
			intent.CheckoutURL, err = (payment.AlipayGateway{AppID: config.AlipayAppID, PrivateKey: config.AlipayPrivateKey, PublicKey: config.AlipayPublicKey, GatewayURL: config.AlipayGatewayURL, PublicURL: runtime.PublicURL}).CheckoutURL(r.Context(), request)
		default:
			err = errors.New("unsupported payment gateway")
		}
		if err == nil {
			err = p.billing.SetPaymentIntentCheckoutURL(r.Context(), intent.ID, identity.AccountID, intent.CheckoutURL)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "checkout_create_failed"})
			return
		}
		intent.Status = "redirected"
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": intent})
}
