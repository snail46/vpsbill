package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"vpsbill/internal/clock"
	"vpsbill/internal/config"
	"vpsbill/internal/mail"
	"vpsbill/internal/security"
	"vpsbill/internal/store/postgres"
)

var (
	ErrAlreadyInstalled = errors.New("system installation is already complete")
	ErrInvalidSettings  = errors.New("invalid installation settings")
)

type Runtime struct {
	Installed                    bool
	AppName, PublicURL, Timezone string
	// AdminURL is where staff reach the admin console when it is served on
	// its own address; empty means PublicURL.
	AdminURL                  string
	PaymentProviderName       string
	PaymentCheckoutURL        string
	PaymentWebhookSecret      string
	NotificationWebhookURL    string
	NotificationWebhookSecret string
	MetricsToken              string
	WorkerPollInterval        time.Duration
	ReconcileInterval         time.Duration
	LifecycleInterval         time.Duration
	RenewalLeadTime           time.Duration
	OverdueGracePeriod        time.Duration
	TerminationRetention      time.Duration
	PaymentGateway            PaymentGatewayConfig
	SMTP                      mail.Config
	MailNotifications         MailNotifications
	TicketAttachmentMaxMB     int
	Marketplace               MarketplaceSettings
	// LogoURL is an external logo address; LogoVersion is set instead when
	// a logo image was uploaded (see Logo).
	LogoURL, LogoVersion string
	// LogoDarkURL and LogoDarkVersion are the same for the dark theme.
	LogoDarkURL, LogoDarkVersion string
	// LogoFaviconURL and LogoFaviconVersion are the browser tab icon's.
	LogoFaviconURL, LogoFaviconVersion string
	// LogoMode is how the logo sits beside the site name: auto, icon or
	// wordmark (see SiteInput).
	LogoMode string
	// ContactIntro and ContactLinks fill the portal's "联系我们" page.
	ContactIntro string
	ContactLinks []ContactLink
}

// MarketplaceSettings controls the hosting center: whether customers can
// publish and buy hosted nodes, the platform fee on each hosted sale and how
// long a hosted node may stay offline before it is cleared.
type MarketplaceSettings struct {
	Enabled      bool    `json:"enabled"`
	FeePercent   float64 `json:"fee_percent"`
	OfflineHours int     `json:"offline_hours"`
	// TradeFeePercent is taken from each trading market sale.
	TradeFeePercent float64 `json:"trade_fee_percent"`
	// TradeHoldDays is how long an owner must hold an instance before
	// listing it in the trading market.
	TradeHoldDays int `json:"trade_hold_days"`
	// MaxOvercommit* cap the oversell ratio any node may declare.
	MaxOvercommitCPU     float64 `json:"max_overcommit_cpu"`
	MaxOvercommitRAM     float64 `json:"max_overcommit_ram"`
	MaxOvercommitDisk    float64 `json:"max_overcommit_disk"`
	MaxOvercommitTraffic float64 `json:"max_overcommit_traffic"`
}

func DefaultMarketplaceSettings() MarketplaceSettings {
	return MarketplaceSettings{Enabled: true, FeePercent: 20, OfflineHours: 24, TradeFeePercent: 20, TradeHoldDays: 31,
		MaxOvercommitCPU: 4, MaxOvercommitRAM: 1.5, MaxOvercommitDisk: 2, MaxOvercommitTraffic: 3}
}

type PaymentGatewayConfig struct {
	Type             string `json:"type"`
	GenericBaseURL   string `json:"generic_base_url,omitempty"`
	GenericSecret    string `json:"generic_secret,omitempty"`
	AlipayAppID      string `json:"alipay_app_id,omitempty"`
	AlipayPrivateKey string `json:"alipay_private_key,omitempty"`
	AlipayPublicKey  string `json:"alipay_public_key,omitempty"`
	AlipayGatewayURL string `json:"alipay_gateway_url,omitempty"`
	EpayAPIURL       string `json:"epay_api_url,omitempty"`
	EpayPartnerID    string `json:"epay_partner_id,omitempty"`
	EpayMerchantKey  string `json:"epay_merchant_key,omitempty"`
	EpayPaymentType  string `json:"epay_payment_type,omitempty"`
}

type PaymentGatewayInput struct {
	Type             string `json:"type"`
	GenericBaseURL   string `json:"generic_base_url"`
	GenericSecret    string `json:"generic_secret"`
	AlipayAppID      string `json:"alipay_app_id"`
	AlipayPrivateKey string `json:"alipay_private_key"`
	AlipayPublicKey  string `json:"alipay_public_key"`
	AlipayGatewayURL string `json:"alipay_gateway_url"`
	EpayAPIURL       string `json:"epay_api_url"`
	EpayPartnerID    string `json:"epay_partner_id"`
	EpayMerchantKey  string `json:"epay_merchant_key"`
	EpayPaymentType  string `json:"epay_payment_type"`
}

type PaymentGatewayView struct {
	Type                       string `json:"type"`
	GenericBaseURL             string `json:"generic_base_url"`
	GenericSecretConfigured    bool   `json:"generic_secret_configured"`
	AlipayAppID                string `json:"alipay_app_id"`
	AlipayGatewayURL           string `json:"alipay_gateway_url"`
	AlipayPrivateKeyConfigured bool   `json:"alipay_private_key_configured"`
	AlipayPublicKeyConfigured  bool   `json:"alipay_public_key_configured"`
	EpayAPIURL                 string `json:"epay_api_url"`
	EpayPartnerID              string `json:"epay_partner_id"`
	EpayPaymentType            string `json:"epay_payment_type"`
	EpayMerchantKeyConfigured  bool   `json:"epay_merchant_key_configured"`
}

type InstallInput struct {
	AppName                   string `json:"app_name"`
	PublicURL                 string `json:"public_url"`
	Timezone                  string `json:"timezone"`
	PaymentProviderName       string `json:"payment_provider_name"`
	PaymentCheckoutURL        string `json:"payment_checkout_url"`
	PaymentWebhookSecret      string `json:"payment_webhook_secret"`
	NotificationWebhookURL    string `json:"notification_webhook_url"`
	NotificationWebhookSecret string `json:"notification_webhook_secret"`
	MetricsToken              string `json:"metrics_token"`
	WorkerPollInterval        string `json:"worker_poll_interval"`
	ReconcileInterval         string `json:"reconcile_interval"`
	LifecycleInterval         string `json:"lifecycle_interval"`
	RenewalLeadTime           string `json:"renewal_lead_time"`
	OverdueGracePeriod        string `json:"overdue_grace_period"`
	TerminationRetention      string `json:"termination_retention"`
	AdminDisplayName          string `json:"admin_display_name"`
	AdminEmail                string `json:"admin_email"`
	AdminPassword             string `json:"admin_password"`
	AdminPasswordHash         string `json:"-"`
}

type InstallResult struct {
	Identity                    postgres.StaffIdentity
	GeneratedPaymentSecret      bool
	GeneratedNotificationSecret bool
	GeneratedMetricsToken       bool
}

type Manager struct {
	db      *pgxpool.Pool
	box     *security.SecretBox
	mu      sync.Mutex
	current atomic.Pointer[Runtime]
}

func NewManager(ctx context.Context, db *pgxpool.Pool, box *security.SecretBox, fallback config.Config) (*Manager, error) {
	m := &Manager{db: db, box: box}
	m.current.Store(&Runtime{
		AppName: fallback.AppName, PublicURL: fallback.PublicURL, Timezone: "Asia/Shanghai",
		PaymentProviderName: fallback.PaymentProviderName, PaymentCheckoutURL: fallback.PaymentCheckoutURL,
		PaymentWebhookSecret: fallback.PaymentWebhookSecret, NotificationWebhookURL: fallback.NotificationWebhookURL,
		NotificationWebhookSecret: fallback.NotificationWebhookSecret, MetricsToken: fallback.MetricsToken,
		WorkerPollInterval: fallback.WorkerPollInterval, ReconcileInterval: fallback.ReconcileInterval,
		LifecycleInterval: fallback.LifecycleInterval, RenewalLeadTime: fallback.RenewalLeadTime,
		OverdueGracePeriod: fallback.OverdueGracePeriod, TerminationRetention: fallback.TerminationRetention,
		PaymentGateway:    legacyPaymentConfig(fallback.PaymentProviderName, fallback.PaymentCheckoutURL, fallback.PaymentWebhookSecret),
		MailNotifications: DefaultMailNotifications(), TicketAttachmentMaxMB: 5, Marketplace: DefaultMarketplaceSettings(),
	})
	if err := m.reload(ctx); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if !m.Current().Installed {
		var hasAdmin bool
		if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_members)`).Scan(&hasAdmin); err != nil {
			return nil, err
		}
		if hasAdmin {
			if err := m.seedLegacy(ctx, fallback); err != nil {
				return nil, err
			}
			if err := m.reload(ctx); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

func (m *Manager) Current() Runtime { return *m.current.Load() }

func (m *Manager) PaymentGatewayView() PaymentGatewayView {
	c := m.Current().PaymentGateway
	return PaymentGatewayView{Type: c.Type, GenericBaseURL: c.GenericBaseURL, GenericSecretConfigured: c.GenericSecret != "",
		AlipayAppID: c.AlipayAppID, AlipayGatewayURL: c.AlipayGatewayURL, AlipayPrivateKeyConfigured: c.AlipayPrivateKey != "", AlipayPublicKeyConfigured: c.AlipayPublicKey != "",
		EpayAPIURL: c.EpayAPIURL, EpayPartnerID: c.EpayPartnerID, EpayPaymentType: c.EpayPaymentType, EpayMerchantKeyConfigured: c.EpayMerchantKey != ""}
}

func (m *Manager) UpdatePaymentGateway(ctx context.Context, in PaymentGatewayInput, actorID string) (PaymentGatewayView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.Current()
	config := PaymentGatewayConfig{Type: strings.TrimSpace(in.Type), GenericBaseURL: strings.TrimSpace(in.GenericBaseURL), GenericSecret: strings.TrimSpace(in.GenericSecret),
		AlipayAppID: strings.TrimSpace(in.AlipayAppID), AlipayPrivateKey: strings.TrimSpace(in.AlipayPrivateKey), AlipayPublicKey: strings.TrimSpace(in.AlipayPublicKey), AlipayGatewayURL: strings.TrimSpace(in.AlipayGatewayURL),
		EpayAPIURL: strings.TrimSpace(in.EpayAPIURL), EpayPartnerID: strings.TrimSpace(in.EpayPartnerID), EpayMerchantKey: strings.TrimSpace(in.EpayMerchantKey), EpayPaymentType: strings.TrimSpace(in.EpayPaymentType)}
	old := current.PaymentGateway
	if config.GenericSecret == "" {
		config.GenericSecret = old.GenericSecret
	}
	if config.AlipayPrivateKey == "" {
		config.AlipayPrivateKey = old.AlipayPrivateKey
	}
	if config.AlipayPublicKey == "" {
		config.AlipayPublicKey = old.AlipayPublicKey
	}
	if config.EpayMerchantKey == "" {
		config.EpayMerchantKey = old.EpayMerchantKey
	}
	if config.AlipayGatewayURL == "" {
		config.AlipayGatewayURL = "https://openapi.alipay.com/gateway.do"
	}
	if config.EpayPaymentType == "" {
		config.EpayPaymentType = "alipay"
	}
	if err := validatePaymentGateway(config); err != nil {
		return PaymentGatewayView{}, fmt.Errorf("%w: %v", ErrInvalidSettings, err)
	}
	plain, err := json.Marshal(config)
	if err != nil {
		return PaymentGatewayView{}, err
	}
	sealed, err := m.box.Seal(string(plain))
	if err != nil {
		return PaymentGatewayView{}, err
	}
	legacyURL, legacySecret := "", current.PaymentWebhookSecret
	if config.Type == "generic" {
		legacyURL, legacySecret = config.GenericBaseURL, config.GenericSecret
	}
	legacySecretEncrypted, err := m.box.Seal(legacySecret)
	if err != nil {
		return PaymentGatewayView{}, err
	}
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return PaymentGatewayView{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE system_settings SET payment_gateway_type=$1,payment_gateway_config_encrypted=$2,payment_provider_name=$1,payment_checkout_url=$3,payment_webhook_secret_encrypted=$4,updated_at=now() WHERE singleton=true`, config.Type, sealed, legacyURL, legacySecretEncrypted); err != nil {
		return PaymentGatewayView{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',$1,'payment_gateway.updated','system_settings','singleton',jsonb_build_object('type',$2::text))`, actorID, config.Type); err != nil {
		return PaymentGatewayView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PaymentGatewayView{}, err
	}
	current.PaymentGateway, current.PaymentProviderName, current.PaymentCheckoutURL, current.PaymentWebhookSecret = config, config.Type, legacyURL, legacySecret
	m.current.Store(&current)
	return m.PaymentGatewayView(), nil
}

func (m *Manager) reload(ctx context.Context) error {
	var v Runtime
	var payment, notification, metrics, gatewayConfig, smtpPassword, mailNotifications, contactLinks []byte
	var gatewayType string
	var poll, reconcile, lifecycle, lead, grace, retention int
	err := m.db.QueryRow(ctx, `SELECT app_name,public_url,timezone,payment_provider_name,payment_checkout_url,
		payment_webhook_secret_encrypted,notification_webhook_url,notification_webhook_secret_encrypted,metrics_token_encrypted,
		worker_poll_interval_seconds,reconcile_interval_seconds,lifecycle_interval_seconds,renewal_lead_seconds,overdue_grace_seconds,termination_retention_seconds,
		payment_gateway_type,payment_gateway_config_encrypted,
		smtp_host,smtp_port,smtp_username,smtp_password_encrypted,smtp_from,smtp_security,
		mail_notifications,ticket_attachment_max_mb,
		marketplace_enabled,marketplace_fee_percent::float8,marketplace_offline_hours,trade_fee_percent::float8,admin_url,
		max_overcommit_cpu::float8,max_overcommit_ram::float8,max_overcommit_disk::float8,max_overcommit_traffic::float8,
		logo_url,logo_version,logo_mode,trade_hold_days,contact_intro,contact_links,logo_dark_url,logo_dark_version,logo_favicon_url,logo_favicon_version
		FROM system_settings WHERE singleton=true`).Scan(&v.AppName, &v.PublicURL, &v.Timezone, &v.PaymentProviderName, &v.PaymentCheckoutURL,
		&payment, &v.NotificationWebhookURL, &notification, &metrics, &poll, &reconcile, &lifecycle, &lead, &grace, &retention, &gatewayType, &gatewayConfig,
		&v.SMTP.Host, &v.SMTP.Port, &v.SMTP.Username, &smtpPassword, &v.SMTP.From, &v.SMTP.Security,
		&mailNotifications, &v.TicketAttachmentMaxMB,
		&v.Marketplace.Enabled, &v.Marketplace.FeePercent, &v.Marketplace.OfflineHours, &v.Marketplace.TradeFeePercent, &v.AdminURL,
		&v.Marketplace.MaxOvercommitCPU, &v.Marketplace.MaxOvercommitRAM, &v.Marketplace.MaxOvercommitDisk, &v.Marketplace.MaxOvercommitTraffic,
		&v.LogoURL, &v.LogoVersion, &v.LogoMode, &v.Marketplace.TradeHoldDays, &v.ContactIntro, &contactLinks, &v.LogoDarkURL, &v.LogoDarkVersion, &v.LogoFaviconURL, &v.LogoFaviconVersion)
	if err != nil {
		return err
	}
	v.ContactLinks = []ContactLink{}
	_ = json.Unmarshal(contactLinks, &v.ContactLinks)
	if v.PaymentWebhookSecret, err = m.box.Open(payment); err != nil {
		return fmt.Errorf("decrypt payment webhook secret: %w", err)
	}
	if len(notification) > 0 {
		if v.NotificationWebhookSecret, err = m.box.Open(notification); err != nil {
			return fmt.Errorf("decrypt notification webhook secret: %w", err)
		}
	}
	if v.MetricsToken, err = m.box.Open(metrics); err != nil {
		return fmt.Errorf("decrypt metrics token: %w", err)
	}
	if len(smtpPassword) > 0 {
		if v.SMTP.Password, err = m.box.Open(smtpPassword); err != nil {
			return fmt.Errorf("decrypt smtp password: %w", err)
		}
	}
	v.PaymentGateway = legacyPaymentConfig(gatewayType, v.PaymentCheckoutURL, v.PaymentWebhookSecret)
	if len(gatewayConfig) > 0 {
		plain, openErr := m.box.Open(gatewayConfig)
		if openErr != nil {
			return fmt.Errorf("decrypt payment gateway configuration: %w", openErr)
		}
		if jsonErr := json.Unmarshal([]byte(plain), &v.PaymentGateway); jsonErr != nil {
			return fmt.Errorf("decode payment gateway configuration: %w", jsonErr)
		}
	}
	v.MailNotifications = decodeMailNotifications(mailNotifications)
	v.Installed = true
	v.WorkerPollInterval, v.ReconcileInterval, v.LifecycleInterval = time.Duration(poll)*time.Second, time.Duration(reconcile)*time.Second, time.Duration(lifecycle)*time.Second
	v.RenewalLeadTime, v.OverdueGracePeriod, v.TerminationRetention = time.Duration(lead)*time.Second, time.Duration(grace)*time.Second, time.Duration(retention)*time.Second
	m.current.Store(&v)
	return nil
}

func (m *Manager) seedLegacy(ctx context.Context, cfg config.Config) error {
	payment := cfg.PaymentWebhookSecret
	if len(payment) < 32 {
		payment = randomSecret()
	}
	metrics := cfg.MetricsToken
	if len(metrics) < 32 {
		metrics = randomSecret()
	}
	notification := cfg.NotificationWebhookSecret
	if cfg.NotificationWebhookURL != "" && len(notification) < 32 {
		notification = randomSecret()
	}
	paymentEnc, err := m.box.Seal(payment)
	if err != nil {
		return err
	}
	metricsEnc, err := m.box.Seal(metrics)
	if err != nil {
		return err
	}
	var notificationEnc []byte
	if notification != "" {
		notificationEnc, err = m.box.Seal(notification)
		if err != nil {
			return err
		}
	}
	legacy := legacyPaymentConfig(cfg.PaymentProviderName, cfg.PaymentCheckoutURL, payment)
	gatewayJSON, _ := json.Marshal(legacy)
	gatewayEnc, err := m.box.Seal(string(gatewayJSON))
	if err != nil {
		return err
	}
	_, err = m.db.Exec(ctx, insertSQL, cfg.AppName, cfg.PublicURL, "Asia/Shanghai", cfg.PaymentProviderName, cfg.PaymentCheckoutURL, paymentEnc,
		cfg.NotificationWebhookURL, notificationEnc, metricsEnc, seconds(cfg.WorkerPollInterval), seconds(cfg.ReconcileInterval), seconds(cfg.LifecycleInterval), seconds(cfg.RenewalLeadTime), seconds(cfg.OverdueGracePeriod), seconds(cfg.TerminationRetention), legacy.Type, gatewayEnc)
	return err
}

func (m *Manager) Install(ctx context.Context, in InstallInput) (InstallResult, error) {
	v, generated, err := validate(in)
	if err != nil {
		return InstallResult{}, fmt.Errorf("%w: %v", ErrInvalidSettings, err)
	}
	paymentEnc, err := m.box.Seal(v.PaymentWebhookSecret)
	if err != nil {
		return InstallResult{}, err
	}
	metricsEnc, err := m.box.Seal(v.MetricsToken)
	if err != nil {
		return InstallResult{}, err
	}
	var notificationEnc []byte
	if v.NotificationWebhookSecret != "" {
		notificationEnc, err = m.box.Seal(v.NotificationWebhookSecret)
		if err != nil {
			return InstallResult{}, err
		}
	}
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return InstallResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(74210921)`); err != nil {
		return InstallResult{}, err
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM system_settings) OR EXISTS(SELECT 1 FROM staff_members)`).Scan(&exists); err != nil {
		return InstallResult{}, err
	}
	if exists {
		return InstallResult{}, ErrAlreadyInstalled
	}
	if _, err = tx.Exec(ctx, insertSQL, v.AppName, v.PublicURL, v.Timezone, v.PaymentProviderName, v.PaymentCheckoutURL, paymentEnc,
		v.NotificationWebhookURL, notificationEnc, metricsEnc, seconds(v.WorkerPollInterval), seconds(v.ReconcileInterval), seconds(v.LifecycleInterval), seconds(v.RenewalLeadTime), seconds(v.OverdueGracePeriod), seconds(v.TerminationRetention), "disabled", nil); err != nil {
		return InstallResult{}, err
	}
	identity, err := createAdmin(ctx, tx, in.AdminEmail, in.AdminDisplayName, in.AdminPasswordHash)
	if err != nil {
		return InstallResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return InstallResult{}, err
	}
	m.current.Store(&v)
	generated.Identity = identity
	return generated, nil
}

const insertSQL = `INSERT INTO system_settings(app_name,public_url,timezone,payment_provider_name,payment_checkout_url,payment_webhook_secret_encrypted,
	notification_webhook_url,notification_webhook_secret_encrypted,metrics_token_encrypted,worker_poll_interval_seconds,reconcile_interval_seconds,lifecycle_interval_seconds,
	renewal_lead_seconds,overdue_grace_seconds,termination_retention_seconds,payment_gateway_type,payment_gateway_config_encrypted) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`

func validate(in InstallInput) (Runtime, InstallResult, error) {
	v := Runtime{Installed: true, AppName: strings.TrimSpace(in.AppName), PublicURL: strings.TrimRight(strings.TrimSpace(in.PublicURL), "/"), Timezone: clock.DatabaseZone,
		PaymentProviderName: strings.TrimSpace(in.PaymentProviderName), PaymentCheckoutURL: strings.TrimSpace(in.PaymentCheckoutURL), PaymentWebhookSecret: strings.TrimSpace(in.PaymentWebhookSecret),
		NotificationWebhookURL: strings.TrimSpace(in.NotificationWebhookURL), NotificationWebhookSecret: strings.TrimSpace(in.NotificationWebhookSecret), MetricsToken: strings.TrimSpace(in.MetricsToken)}
	v.PaymentGateway = PaymentGatewayConfig{Type: "disabled", AlipayGatewayURL: "https://openapi.alipay.com/gateway.do", EpayPaymentType: "alipay"}
	v.MailNotifications, v.TicketAttachmentMaxMB, v.Marketplace = DefaultMailNotifications(), 5, DefaultMarketplaceSettings()
	if v.AppName == "" || strings.TrimSpace(in.AdminDisplayName) == "" || strings.TrimSpace(in.AdminEmail) == "" {
		return Runtime{}, InstallResult{}, errors.New("站点名称和管理员信息不能为空")
	}
	if err := absoluteURL(v.PublicURL, true); err != nil {
		return Runtime{}, InstallResult{}, errors.New("公开访问地址必须是完整的 HTTP(S) URL")
	}
	if err := absoluteURL(v.PaymentCheckoutURL, false); err != nil {
		return Runtime{}, InstallResult{}, errors.New("收银台地址必须是完整的 HTTP(S) URL")
	}
	if err := absoluteURL(v.NotificationWebhookURL, false); err != nil {
		return Runtime{}, InstallResult{}, errors.New("通知地址必须是完整的 HTTP(S) URL")
	}
	if v.PaymentProviderName == "" {
		v.PaymentProviderName = "generic"
	}
	result := InstallResult{}
	if len(v.PaymentWebhookSecret) < 32 {
		v.PaymentWebhookSecret = randomSecret()
		result.GeneratedPaymentSecret = true
	}
	if v.NotificationWebhookURL != "" && len(v.NotificationWebhookSecret) < 32 {
		v.NotificationWebhookSecret = randomSecret()
		result.GeneratedNotificationSecret = true
	}
	if len(v.MetricsToken) < 32 {
		v.MetricsToken = randomSecret()
		result.GeneratedMetricsToken = true
	}
	parse := func(value string, fallback time.Duration) (time.Duration, error) {
		if strings.TrimSpace(value) == "" {
			return fallback, nil
		}
		d, e := time.ParseDuration(value)
		if e != nil || d <= 0 {
			return 0, errors.New("时间参数必须使用 3s、5m 或 168h 等正数格式")
		}
		return d, nil
	}
	var err error
	if v.WorkerPollInterval, err = parse(in.WorkerPollInterval, 3*time.Second); err != nil {
		return Runtime{}, InstallResult{}, err
	}
	if v.ReconcileInterval, err = parse(in.ReconcileInterval, 5*time.Minute); err != nil {
		return Runtime{}, InstallResult{}, err
	}
	if v.LifecycleInterval, err = parse(in.LifecycleInterval, time.Minute); err != nil {
		return Runtime{}, InstallResult{}, err
	}
	if v.RenewalLeadTime, err = parse(in.RenewalLeadTime, 7*24*time.Hour); err != nil {
		return Runtime{}, InstallResult{}, err
	}
	if v.OverdueGracePeriod, err = parse(in.OverdueGracePeriod, 3*24*time.Hour); err != nil {
		return Runtime{}, InstallResult{}, err
	}
	if v.TerminationRetention, err = parse(in.TerminationRetention, 7*24*time.Hour); err != nil {
		return Runtime{}, InstallResult{}, err
	}
	return v, result, nil
}

func absoluteURL(value string, required bool) error {
	if value == "" && !required {
		return nil
	}
	u, e := url.Parse(value)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("invalid url")
	}
	return nil
}

func legacyPaymentConfig(provider, checkoutURL, secret string) PaymentGatewayConfig {
	if strings.TrimSpace(checkoutURL) == "" {
		return PaymentGatewayConfig{Type: "disabled", AlipayGatewayURL: "https://openapi.alipay.com/gateway.do", EpayPaymentType: "alipay"}
	}
	return PaymentGatewayConfig{Type: "generic", GenericBaseURL: strings.TrimSpace(checkoutURL), GenericSecret: secret, AlipayGatewayURL: "https://openapi.alipay.com/gateway.do", EpayPaymentType: "alipay"}
}

func validatePaymentGateway(config PaymentGatewayConfig) error {
	switch config.Type {
	case "disabled":
		return nil
	case "generic":
		if absoluteURL(config.GenericBaseURL, true) != nil || len(config.GenericSecret) < 32 {
			return errors.New("通用网关需要完整收银台地址和至少 32 位密钥")
		}
	case "alipay_f2f":
		if config.AlipayAppID == "" || config.AlipayPrivateKey == "" || config.AlipayPublicKey == "" || absoluteURL(config.AlipayGatewayURL, true) != nil {
			return errors.New("支付宝当面付需要 App ID、应用私钥、支付宝公钥和网关地址")
		}
	case "epay":
		if config.EpayPartnerID == "" || config.EpayMerchantKey == "" || absoluteURL(config.EpayAPIURL, true) != nil {
			return errors.New("易支付需要接口地址、商户 ID 和商户密钥")
		}
		if config.EpayPaymentType != "alipay" && config.EpayPaymentType != "wxpay" && config.EpayPaymentType != "qqpay" {
			return errors.New("易支付通道只支持 alipay、wxpay 或 qqpay")
		}
	default:
		return errors.New("不支持的支付网关类型")
	}
	return nil
}
func randomSecret() string {
	token, _, err := security.NewToken()
	if err != nil {
		panic(err)
	}
	return token
}
func seconds(v time.Duration) int64 {
	value := int64(v / time.Second)
	if value < 1 {
		return 1
	}
	return value
}

func createAdmin(ctx context.Context, tx pgx.Tx, email, displayName, passwordHash string) (postgres.StaffIdentity, error) {
	email, displayName = strings.ToLower(strings.TrimSpace(email)), strings.TrimSpace(displayName)
	var identity postgres.StaffIdentity
	if err := tx.QueryRow(ctx, `INSERT INTO users(email,display_name,password_hash,email_verified_at,status) VALUES($1,$2,$3,now(),'active') RETURNING id`, email, displayName, passwordHash).Scan(&identity.UserID); err != nil {
		return identity, err
	}
	var roleID string
	if err := tx.QueryRow(ctx, `SELECT id,name,permissions FROM staff_roles WHERE name='Super Administrator'`).Scan(&roleID, &identity.RoleName, &identity.Permissions); err != nil {
		return identity, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO staff_members(user_id,role_id) VALUES($1,$2)`, identity.UserID, roleID); err != nil {
		return identity, err
	}
	identity.Email, identity.DisplayName, identity.Status = email, displayName, "active"
	return identity, nil
}
