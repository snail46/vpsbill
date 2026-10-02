package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"vpsbill/internal/payment"
	"vpsbill/internal/provider"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

type customerPortal struct {
	store    *postgres.PortalStore
	billing  *postgres.BillingStore
	catalog  *postgres.CatalogStore
	settings *settings.Manager
	box      *security.SecretBox
	tickets  consoleTickets
}

func newCustomerPortal(runtime *settings.Manager, store *postgres.PortalStore, billing *postgres.BillingStore, catalog *postgres.CatalogStore, box *security.SecretBox, tickets consoleTickets) *customerPortal {
	return &customerPortal{store: store, billing: billing, catalog: catalog, settings: runtime, box: box, tickets: tickets}
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
	case errors.Is(err, postgres.ErrServiceListed):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "service_listed", "message": "实例正在交易市场挂售，挂售期间不能使用，下架后才能操作"})
	case errors.Is(err, postgres.ErrTrafficLocked):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "traffic_locked", "message": "本月流量（上行加下行）已用尽，实例已停止，下月 1 日自动恢复"})
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

// serviceDriver opens the node of a customer's usable service for an
// operation; listed instances are frozen for their seller.
func (p *customerPortal) serviceDriver(r *http.Request) (postgres.CustomerServiceAccess, provider.Driver, error) {
	return p.openService(r, false)
}

// openService is serviceDriver with an option to allow read-only access to
// a listed instance (monitoring).
func (p *customerPortal) openService(r *http.Request, readOnly bool) (postgres.CustomerServiceAccess, provider.Driver, error) {
	identity := customerPrincipalFromContext(r.Context())
	access, err := p.store.ServiceAccess(r.Context(), identity.AccountID, r.PathValue("id"))
	if err != nil {
		return access, nil, err
	}
	if access.Listed && !readOnly {
		return access, nil, postgres.ErrServiceListed
	}
	if _, registered := provider.Lookup(access.ProviderType); !postgres.CustomerUsable(access.Status) || !registered {
		return access, nil, postgres.ErrServiceActionUnavailable
	}
	driver, err := provider.OpenSealed(p.box, access.Sealed(), 20*time.Second)
	return access, driver, err
}

func (p *customerPortal) serviceRuntime(w http.ResponseWriter, r *http.Request) {
	access, driver, err := p.openService(r, true)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	type source struct {
		name string
		load func() (any, error)
	}
	type result struct {
		name  string
		value any
		err   error
	}
	ctx, name := r.Context(), access.InstanceName
	brief := r.URL.Query().Get("brief") == "1"
	sources := []source{{"container", func() (any, error) { return driver.GetInstance(ctx, name) }}}
	metrics, hasMetrics := driver.(provider.Metrics)
	if hasMetrics {
		sources = append(sources,
			source{"usage", func() (any, error) { return metrics.InstanceUsage(ctx, name) }},
			source{"traffic", func() (any, error) { return metrics.InstanceTraffic(ctx, name) }})
	}
	if !brief {
		if hasMetrics {
			sources = append(sources, source{"history", func() (any, error) { return metrics.InstanceHistory(ctx, name) }})
		}
		sources = append(sources, source{"images", func() (any, error) { return driver.Images(ctx) }})
	}
	results := make(chan result, len(sources))
	for _, source := range sources {
		go func() {
			value, callErr := source.load()
			results <- result{source.name, value, callErr}
		}()
	}
	values := map[string]any{}
	errorsBySource := map[string]string{}
	var instance provider.Instance
	var images []provider.Image
	for range sources {
		row := <-results
		if row.err != nil {
			errorsBySource[row.name] = row.err.Error()
			continue
		}
		values[row.name] = row.value
		switch value := row.value.(type) {
		case provider.Instance:
			instance = value
		case []provider.Image:
			images = value
		}
	}
	if instance.Name == "" {
		p.writeServiceError(w, errors.New(errorsBySource["container"]))
		return
	}
	switch status := provider.NormalizeStatus(instance.Status); status {
	case "running", "stopped", "suspended":
		_ = p.store.ObserveRuntime(ctx, access.ServiceID, status)
	}
	allowed := make(map[string]bool, len(access.AllowedTemplateIDs))
	for _, id := range access.AllowedTemplateIDs {
		allowed[id] = true
	}
	templates := make([]provider.Image, 0)
	for _, image := range images {
		if allowed[image.ID] && image.Sellable(access.Virtualization) {
			templates = append(templates, image)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"container": instance, "usage": values["usage"], "history": values["history"], "traffic": values["traffic"],
		"templates": templates, "errors": errorsBySource, "capabilities": provider.CapabilitiesOf(driver),
	}})
}

func (p *customerPortal) serviceCredential(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	access, _, err := p.serviceDriver(r)
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
	access, driver, err := p.serviceDriver(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	resetter, ok := driver.(provider.PasswordResetter)
	if !ok {
		p.writeServiceError(w, provider.ErrUnsupported)
		return
	}
	password, err := resetter.ResetPassword(r.Context(), access.InstanceName, input.Password)
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

// reinstallService starts a reinstall and answers at once; it runs in the
// background, and the page follows it through reinstallStatus. An empty
// password keeps the instance's stored root password.
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
	access, driver, err := p.serviceDriver(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	reinstaller, ok := driver.(provider.Reinstaller)
	if !ok {
		p.writeServiceError(w, provider.ErrUnsupported)
		return
	}
	if !containsString(access.AllowedTemplateIDs, input.TemplateID) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "template_not_allowed", "message": "所选系统不在当前套餐允许的模板中"})
		return
	}
	password, newPassword := input.Password, input.Password != ""
	if newPassword && !validRootPassword(password) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_password", "message": "新 root 密码需为 8-64 位且同时包含字母和数字，或者留空沿用当前密码"})
		return
	}
	if !newPassword {
		if len(access.RootPasswordCiphertext) > 0 {
			password, err = p.box.Open(access.RootPasswordCiphertext)
		}
		if err != nil || password == "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "password_required", "message": "这台实例没有留存 root 密码，请设置新的 root 密码"})
			return
		}
	}
	identity := customerPrincipalFromContext(r.Context())
	state, err := p.store.StartReinstall(r.Context(), identity.AccountID, access.ServiceID, input.TemplateID)
	if errors.Is(err, postgres.ErrReinstallRunning) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "reinstall_running", "message": "这台实例正在重装，请等待完成"})
		return
	}
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	ip, userAgent := remoteIP(r), r.UserAgent()
	// The reinstall outlives this request.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	go func() {
		defer cancel()
		failure := ""
		if err := p.runReinstall(ctx, access, reinstaller, driver, identity, input.TemplateID, password, newPassword, ip, userAgent); err != nil {
			failure = serviceErrorMessage(err)
		}
		if err := p.store.FinishReinstall(context.WithoutCancel(ctx), access.ServiceID, failure); err != nil {
			slog.Warn("record reinstall result", "service", access.ServiceID, "error", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"data": state})
}

func (p *customerPortal) runReinstall(ctx context.Context, access postgres.CustomerServiceAccess, reinstaller provider.Reinstaller, driver provider.Driver,
	identity postgres.CustomerSessionIdentity, templateID, password string, newPassword bool, ip, userAgent string) error {
	taskID, err := reinstaller.Reinstall(ctx, access.InstanceName, provider.ReinstallSpec{TemplateID: templateID, Password: password})
	if err != nil {
		return err
	}
	if newPassword {
		ciphertext, err := p.box.Seal(password)
		if err == nil {
			err = p.store.SaveRootPassword(ctx, identity.AccountID, access.ServiceID, ciphertext)
		}
		if err != nil {
			return customerMessage("系统已重装，但新密码保存失败，请重置密码")
		}
	}
	_ = p.store.SetServiceTemplate(ctx, identity.AccountID, access.ServiceID, templateID)
	_ = p.store.RecordServiceOperation(ctx, identity.UserID, access.ServiceID, "service.reinstall", ip, userAgent, map[string]any{"template_id": templateID, "external_task_id": taskID})
	if taskID == "" {
		return nil
	}
	// Backends that queue the work (LXDAPI, CLICD) answer at once; wait
	// until the instance runs again.
	for {
		select {
		case <-ctx.Done():
			return customerMessage("节点仍在重装，已超过 15 分钟，请稍后查看实例状态")
		case <-time.After(5 * time.Second):
		}
		if instance, err := driver.GetInstance(ctx, access.InstanceName); err == nil && provider.NormalizeStatus(instance.Status) == "running" {
			return nil
		}
	}
}

// reinstallStatus reports the service's latest reinstall.
func (p *customerPortal) reinstallStatus(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	state, err := p.store.LatestReinstall(r.Context(), identity.AccountID, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": state})
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
	access, driver, err := p.serviceDriver(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	mapper, ok := driver.(provider.PortMapper)
	if !ok {
		p.writeServiceError(w, provider.ErrUnsupported)
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
	var mappings []provider.PortMapping
	if action == "delete" {
		instance, callErr := driver.GetInstance(r.Context(), access.InstanceName)
		if callErr != nil || index >= len(instance.PortMappings) || strings.EqualFold(instance.PortMappings[index].Description, "SSH") {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "mapping_protected", "message": "SSH 管理端口不能删除"})
			return
		}
		mappings, err = mapper.DeletePortMapping(r.Context(), access.InstanceName, index)
	} else {
		var input struct {
			ContainerPort int `json:"container_port"`
			// PublicPort is the port on the node's public address; 0 picks
			// a free one (new rules) or keeps the current one (changes).
			PublicPort  int    `json:"public_port"`
			Protocol    string `json:"protocol"`
			Description string `json:"description"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		input.Protocol = strings.ToLower(strings.TrimSpace(input.Protocol))
		input.Description = strings.TrimSpace(input.Description)
		if input.PublicPort < 0 || input.PublicPort > 65535 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_mapping", "message": "公网端口需为 1-65535，留空自动分配"})
			return
		}
		if input.Protocol == "tcp+udp" {
			input.Protocol = "both"
		}
		if input.ContainerPort < 1 || input.ContainerPort > 65535 || (input.Protocol != "tcp" && input.Protocol != "udp" && input.Protocol != "both") || len(input.Description) > 80 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_mapping", "message": "端口需为 1-65535，协议仅支持 TCP、UDP 或 TCP+UDP，备注最多 80 字符"})
			return
		}
		// Hatch forwards both protocols in one rule; other backends get a
		// TCP and a UDP rule on the same public port.
		_, native := driver.(interface{ BothProtocols() bool })
		split := input.Protocol == "both" && !native
		if split && action != "add" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid_mapping", "message": "该节点的一条规则只能是 TCP 或 UDP；需要两种协议时请再添加一条映射"})
			return
		}
		mapping := provider.PortMapping{ContainerPort: input.ContainerPort, Protocol: input.Protocol, Description: input.Description}
		if split {
			mapping.Protocol = "tcp"
		}
		if action == "add" && input.PublicPort > 0 {
			mapping.HostPort = input.PublicPort
			mappings, err = mapper.AddPortMapping(r.Context(), access.InstanceName, mapping)
		} else if action == "add" {
			mapping.HostPort, err = mapper.FreePort(r.Context(), access.InstanceName)
			if err == nil && mapping.HostPort == 0 {
				err = errors.New("节点没有可用的 NAT 端口")
			}
			if err == nil {
				mappings, err = mapper.AddPortMapping(r.Context(), access.InstanceName, mapping)
			}
		} else {
			instance, callErr := driver.GetInstance(r.Context(), access.InstanceName)
			if callErr != nil || index >= len(instance.PortMappings) {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": "mapping_not_found"})
				return
			}
			existing := instance.PortMappings[index]
			mapping.HostPort, mapping.HostIP = existing.HostPort, existing.HostIP
			if input.PublicPort > 0 {
				mapping.HostPort = input.PublicPort
			}
			if strings.EqualFold(existing.Description, "SSH") {
				mapping.Protocol, mapping.Description = existing.Protocol, "SSH"
			}
			mappings, err = mapper.UpdatePortMapping(r.Context(), access.InstanceName, index, mapping)
		}
		if split && err == nil {
			mappings, err = addUDPTwin(r.Context(), mapper, access.InstanceName, mapping, mappings)
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
	access, driver, err := p.serviceDriver(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	kind := r.PathValue("kind")
	path := fmt.Sprintf("/api/v1/customer/services/%s/console/%s", access.ServiceID, kind)
	if _, bridged := driver.(provider.Terminal); bridged && kind == "ssh" {
		if _, native := driver.(provider.Console); !native {
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"ticket": p.tickets.issue(access.ServiceID, kind), "websocket_path": path}})
			return
		}
	}
	console, ok := driver.(provider.Console)
	if !ok || !containsString(console.ConsoleKinds(), kind) {
		p.writeServiceError(w, provider.ErrUnsupported)
		return
	}
	if kind == "vnc" && access.Virtualization != "kvm" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "console_unavailable", "message": "VNC 控制台仅适用于 KVM 实例"})
		return
	}
	ticket, err := console.ConsoleTicket(r.Context(), access.InstanceName, kind, r.UserAgent())
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]string{"ticket": ticket, "websocket_path": path}})
}

func (p *customerPortal) consoleProxy(w http.ResponseWriter, r *http.Request) {
	access, driver, err := p.serviceDriver(r)
	if err != nil {
		p.writeServiceError(w, err)
		return
	}
	// A console stays open for hours; close it once the customer no longer
	// may use the instance (sold, listed, terminated).
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(ctx)
	go p.watchAccess(ctx, cancel, customerPrincipalFromContext(ctx).AccountID, access.ServiceID)
	console, ok := driver.(provider.Console)
	if terminal, bridged := driver.(provider.Terminal); !ok && bridged && r.PathValue("kind") == "ssh" {
		p.bridgeTerminal(w, r, access, terminal)
		return
	}
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "console_unavailable"})
		return
	}
	target, err := console.ConsoleTarget(access.InstanceName, r.PathValue("kind"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "console_unavailable"})
		return
	}
	// Console sessions outlive the server's read/write timeouts.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
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
	case errors.Is(err, postgres.ErrServiceListed):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "service_listed", "message": "实例正在交易市场挂售，挂售期间不能使用，下架后才能操作"})
	case errors.Is(err, postgres.ErrServiceActionUnavailable):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "action_unavailable", "message": "当前服务状态不允许此操作"})
	case errors.Is(err, provider.ErrUnsupported):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "feature_unsupported", "message": "该实例所在节点不支持此功能"})
	default:
		var apiErr *provider.Error
		if errors.As(err, &apiErr) {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "node_request_failed", "message": apiErr.Message})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "node_unavailable", "message": "节点暂时无法完成请求"})
	}
}

// customerMessage is an error written for the customer.
type customerMessage string

func (m customerMessage) Error() string { return string(m) }

// serviceErrorMessage is what writeServiceError would tell the customer
// about err, for work that ends after the reply.
func serviceErrorMessage(err error) string {
	var apiErr *provider.Error
	switch {
	case errors.As(err, &apiErr) && apiErr.Message != "":
		return apiErr.Message
	case errors.Is(err, context.DeadlineExceeded):
		return "节点处理超时，请稍后查看实例状态"
	case errors.As(err, new(customerMessage)):
		return err.Error()
	default:
		return "节点暂时无法完成重装，请稍后重试"
	}
}

func (p *customerPortal) catalogData(w http.ResponseWriter, r *http.Request) {
	plans, err := p.catalog.ListPlans(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	available := make([]postgres.Plan, 0)
	inUse := map[string]bool{}
	for _, plan := range postgres.PlatformPlans(plans) {
		if plan.Enabled && len(plan.Prices) > 0 {
			// Which nodes sell the plan is the operator's business; the
			// regions it can be ordered in are enough.
			plan.NodeIDs = nil
			available = append(available, plan)
			inUse[plan.CategoryID] = true
		}
	}
	regions, err := p.billing.ListRegions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	allCategories, err := p.catalog.ListPlanCategories(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	categories := make([]postgres.PlanCategory, 0, len(allCategories))
	for _, category := range allCategories {
		if inUse[category.ID] {
			category.Plans = 0
			categories = append(categories, category)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"plans": available, "regions": regions, "categories": categories, "checkout_enabled": p.settings.Current().PaymentGateway.Type != "disabled", "checkout_currency": p.checkoutCurrency()}})
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
		Items      []postgres.OrderItemInput `json:"items"`
		CouponCode string                    `json:"coupon_code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	identity := customerPrincipalFromContext(r.Context())
	order, err := p.billing.CreateOrder(r.Context(), postgres.CreateOrderInput{AccountID: identity.AccountID, Items: input.Items, CouponCode: input.CouponCode, ActorType: "customer", ActorID: identity.UserID})
	var hosted *postgres.HostedOrderError
	if errors.As(err, &hosted) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "hosted_unavailable", "message": hosted.Message})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "order_create_failed", "message": "地区、套餐、价格或配置无效"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": order})
}

// checkoutCurrency is the currency online payments are charged in: CNY
// with Alipay and Epay, the ledger's otherwise.
func (p *customerPortal) checkoutCurrency() string {
	runtime := p.settings.Current()
	if runtime.PaymentGateway.Type == "alipay_f2f" || runtime.PaymentGateway.Type == "epay" {
		return "CNY"
	}
	return runtime.Locale.Ledger()
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
	if errors.Is(err, postgres.ErrBalanceOnly) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "balance_only", "message": "这张账单只能用账户余额支付，余额不足时请先充值"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	if intent.CheckoutURL == "" {
		// Alipay and Epay take CNY: an invoice in another currency is
		// charged its worth in CNY at the rate of this moment, which the
		// intent keeps for checking the gateway's notification.
		intent.ChargeCurrency, intent.ChargeMinor = "", 0
		if (config.Type == "alipay_f2f" || config.Type == "epay") && intent.Currency != "CNY" {
			intent.ChargeCurrency, intent.ChargeMinor = "CNY", max(runtime.Locale.Convert(intent.AmountMinor, intent.Currency, "CNY"), 1)
		}
		charge, chargeCurrency := intent.Charged()
		request := payment.CheckoutRequest{MerchantReference: intent.MerchantReference, InvoiceNumber: intent.InvoiceNumber, AmountMinor: charge, Currency: chargeCurrency, ExpiresAt: intent.ExpiresAt}
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
			err = p.billing.SetPaymentIntentCheckoutURL(r.Context(), intent.ID, identity.AccountID, intent.CheckoutURL, intent.ChargeCurrency, intent.ChargeMinor)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "checkout_create_failed"})
			return
		}
		intent.Status = "redirected"
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": intent})
}

// watchAccess cancels a long-lived session when the account loses the use
// of the service.
func (p *customerPortal) watchAccess(ctx context.Context, cancel context.CancelFunc, accountID, serviceID string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			access, err := p.store.ServiceAccess(ctx, accountID, serviceID)
			if errors.Is(err, postgres.ErrServiceNotFound) || err == nil && (access.Listed || !postgres.CustomerUsable(access.Status)) {
				cancel()
				return
			}
		}
	}
}

// addUDPTwin adds the UDP half of a TCP+UDP mapping on a backend whose rules
// carry one protocol, on the public port the TCP rule got. If it fails the
// TCP rule is removed again, so the customer never keeps half a mapping.
func addUDPTwin(ctx context.Context, mapper provider.PortMapper, name string, tcp provider.PortMapping, mappings []provider.PortMapping) ([]provider.PortMapping, error) {
	index := -1
	for i, existing := range mappings {
		if existing.Protocol == "tcp" && existing.ContainerPort == tcp.ContainerPort && (tcp.HostPort == 0 || existing.HostPort == tcp.HostPort) {
			index = i
		}
	}
	if index < 0 {
		return mappings, errors.New("TCP 规则已添加，但没找到它的公网端口，请手动添加 UDP 规则")
	}
	udp := tcp
	udp.Protocol, udp.HostPort, udp.HostIP = "udp", mappings[index].HostPort, mappings[index].HostIP
	next, err := mapper.AddPortMapping(ctx, name, udp)
	if err != nil {
		if _, undoErr := mapper.DeletePortMapping(ctx, name, index); undoErr != nil {
			return mappings, fmt.Errorf("UDP 规则添加失败，TCP 规则也未能撤销：%w", err)
		}
		return nil, err
	}
	return next, nil
}
