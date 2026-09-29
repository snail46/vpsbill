package app

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"vpsbill/internal/hatch/gateway"
	"vpsbill/internal/hatch/protocol"
	"vpsbill/internal/provider"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// AgentEnroller lists agents that connect with an enroll key under its
// owner, keeping their token sealed so the owner can add the host without
// copying it.
func AgentEnroller(catalog *postgres.CatalogStore, box *security.SecretBox, logger *slog.Logger) gateway.EnrollFunc {
	return func(ctx context.Context, endpoint, token string, hello protocol.Hello, r *http.Request) bool {
		sealed, err := box.Seal(token)
		if err != nil {
			return false
		}
		ok, err := catalog.Enroll(ctx, hello.EnrollKey, endpoint, sealed, hello.Hostname, hello.AgentVersion, hello.Runtimes, remoteIP(r))
		if err != nil {
			logger.Warn("record agent enrollment", "error", err)
		}
		return ok
	}
}

// agentIP tells an agent the address it connects from, which is its public
// IPv4 when the host itself only has a private one (1:1 NAT). An agent that
// reaches the site over a private address (the billing server's own
// machine, seen through the Docker bridge, or the same LAN) shares the
// site's public address, taken from the public URL.
func agentIP(runtime *settings.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		address := remoteIP(r)
		if ip := net.ParseIP(address); ip == nil || ip.IsLoopback() || ip.IsPrivate() || cgnat.Contains(ip) {
			address = ""
			if site, err := url.Parse(runtime.Current().PublicURL); err == nil {
				if ip := net.ParseIP(site.Hostname()); ip != nil {
					address = ip.String()
				} else if ips, err := net.DefaultResolver.LookupIP(r.Context(), "ip4", site.Hostname()); err == nil && len(ips) > 0 {
					address = ips[0].String()
				}
			}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(address))
	}
}

// cgnat is the carrier-grade NAT range, private in practice.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// installCommand is the one command every host of an owner runs.
func installCommand(publicURL, key string) string {
	server := strings.TrimRight(publicURL, "/")
	return "curl -fsSL " + server + "/api/v1/agent/download/install.sh | sh -s -- --server " + server + " --enroll " + key
}

// pendingAgentView is a pending agent with what it reports about its host,
// when it is connected.
type pendingAgentView struct {
	postgres.PendingAgent
	Online     bool               `json:"online"`
	PublicIPv4 string             `json:"public_ipv4,omitempty"`
	Capacity   *provider.Capacity `json:"capacity,omitempty"`
}

// pendingAgents lists an owner's pending agents, asking the connected ones
// for their host facts in parallel.
func pendingAgents(ctx context.Context, catalog *postgres.CatalogStore, box *security.SecretBox, accountID string) ([]pendingAgentView, error) {
	agents, err := catalog.PendingAgents(ctx, accountID)
	if err != nil {
		return nil, err
	}
	views := make([]pendingAgentView, len(agents))
	var wg sync.WaitGroup
	for i, agent := range agents {
		views[i].PendingAgent = agent
		wg.Add(1)
		go func(view *pendingAgentView) {
			defer wg.Done()
			ciphertext, err := catalog.EnrollmentToken(ctx, view.ID, accountID)
			if err != nil {
				return
			}
			token, err := box.Open(ciphertext)
			if err != nil {
				return
			}
			driver, err := provider.Open("hatch", provider.Config{BaseURL: view.Endpoint, Credential: token, Timeout: 4 * time.Second})
			if err != nil {
				return
			}
			callCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			info, err := driver.HostInfo(callCtx)
			if err != nil {
				return
			}
			view.Online = true
			capacity := info.Capacity
			view.Capacity = &capacity
			if address, _ := info.Raw["public_ipv4"].(string); address != "" {
				view.PublicIPv4 = address
			}
		}(&views[i])
	}
	wg.Wait()
	return views, nil
}

// enrollmentToken opens the agent token of the owner's pending agent.
func enrollmentToken(ctx context.Context, catalog *postgres.CatalogStore, box *security.SecretBox, id, accountID string) (string, error) {
	ciphertext, err := catalog.EnrollmentToken(ctx, id, accountID)
	if err != nil {
		return "", err
	}
	return box.Open(ciphertext)
}

// ---- Hosting center ----

func (a *marketplaceAPI) dismissHostedAgent(w http.ResponseWriter, r *http.Request) {
	identity := customerPrincipalFromContext(r.Context())
	if err := a.catalog.DismissEnrollment(r.Context(), r.PathValue("id"), identity.AccountID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "待接入的母机不存在"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveRegion fills in regionID from a typed region name, creating the
// region when it does not exist yet. It writes the error response itself.
func resolveRegion(w http.ResponseWriter, ctx context.Context, catalog *postgres.CatalogStore, regionID *string, regionName string) bool {
	if strings.TrimSpace(*regionID) != "" {
		return true
	}
	region, err := catalog.EnsureRegion(ctx, regionName)
	switch {
	case errors.Is(err, postgres.ErrRegionDisabled):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "region_disabled", "message": "该地域已被管理员停用，请选择其他地域"})
		return false
	case errors.Is(err, postgres.ErrRegionInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "请选择或填写地域（1-40 个字符）"})
		return false
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return false
	}
	*regionID = region.ID
	return true
}

// ---- Administration ----

func (a *adminCatalog) listEnrollments(w http.ResponseWriter, r *http.Request) {
	key, err := a.store.EnrollKey(r.Context(), "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	agents, err := pendingAgents(r.Context(), a.store, a.box, "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"install_command": installCommand(a.settings.Current().PublicURL, key),
		"agents":          agents,
	}})
}

func (a *adminCatalog) dismissEnrollment(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DismissEnrollment(r.Context(), r.PathValue("id"), ""); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "待接入的 Agent 不存在"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *adminCatalog) listAllRegions(w http.ResponseWriter, r *http.Request) {
	regions, err := a.store.AllRegions(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": regions})
}

func (a *adminCatalog) createRegion(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var id string
	if !resolveRegion(w, r.Context(), a.store, &id, input.Name) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": map[string]string{"id": id}})
}

func (a *adminCatalog) updateRegion(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	err := a.store.UpdateRegion(r.Context(), r.PathValue("id"), input.Name, input.Enabled)
	switch {
	case errors.Is(err, postgres.ErrRegionInvalid):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "地域名称需为 1-40 个字符"})
	case errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
	case err != nil:
		writeJSON(w, http.StatusConflict, map[string]any{"error": "update_failed", "message": "地域名称可能与其他地域重复"})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// tlsProbe reads the certificate a node's HTTPS endpoint presents, so the
// administrator can pin a self-signed one (LXDAPI) without running openssl.
func (a *adminCatalog) tlsProbe(w http.ResponseWriter, r *http.Request) {
	var input struct {
		BaseURL string `json:"base_url"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	target, err := url.Parse(strings.TrimSpace(input.BaseURL))
	if err != nil || target.Scheme != "https" || target.Hostname() == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "validation_error", "message": "请先填写 https:// 开头的接口地址"})
		return
	}
	port := target.Port()
	if port == "" {
		port = "443"
	}
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 8 * time.Second}, Config: &tls.Config{InsecureSkipVerify: true, ServerName: target.Hostname()}} // #nosec G402 -- only reads the certificate
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Hostname(), port))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "node_unreachable", "message": "无法连接 " + target.Host + "：" + err.Error()})
		return
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "node_unreachable", "message": "对方没有提供证书"})
		return
	}
	leaf := state.PeerCertificates[0]
	sum := sha256.Sum256(leaf.Raw)
	intermediates := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	_, verifyErr := leaf.Verify(x509.VerifyOptions{DNSName: target.Hostname(), Intermediates: intermediates})
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"fingerprint": hex.EncodeToString(sum[:]),
		"subject":     leaf.Subject.String(),
		"not_after":   leaf.NotAfter,
		"trusted":     verifyErr == nil,
	}})
}
