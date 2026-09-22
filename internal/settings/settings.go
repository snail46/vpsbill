package settings

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"clicd-billing/internal/config"
	"clicd-billing/internal/security"
	"clicd-billing/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAlreadyInstalled = errors.New("system installation is already complete")
	ErrInvalidSettings  = errors.New("invalid installation settings")
)

type Runtime struct {
	Installed                    bool
	AppName, PublicURL, Timezone string
	PaymentProviderName          string
	PaymentCheckoutURL           string
	PaymentWebhookSecret         string
	NotificationWebhookURL       string
	NotificationWebhookSecret    string
	MetricsToken                 string
	WorkerPollInterval           time.Duration
	ReconcileInterval            time.Duration
	LifecycleInterval            time.Duration
	RenewalLeadTime              time.Duration
	OverdueGracePeriod           time.Duration
	TerminationRetention         time.Duration
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

func (m *Manager) reload(ctx context.Context) error {
	var v Runtime
	var payment, notification, metrics []byte
	var poll, reconcile, lifecycle, lead, grace, retention int
	err := m.db.QueryRow(ctx, `SELECT app_name,public_url,timezone,payment_provider_name,payment_checkout_url,
		payment_webhook_secret_encrypted,notification_webhook_url,notification_webhook_secret_encrypted,metrics_token_encrypted,
		worker_poll_interval_seconds,reconcile_interval_seconds,lifecycle_interval_seconds,renewal_lead_seconds,overdue_grace_seconds,termination_retention_seconds
		FROM system_settings WHERE singleton=true`).Scan(&v.AppName, &v.PublicURL, &v.Timezone, &v.PaymentProviderName, &v.PaymentCheckoutURL,
		&payment, &v.NotificationWebhookURL, &notification, &metrics, &poll, &reconcile, &lifecycle, &lead, &grace, &retention)
	if err != nil {
		return err
	}
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
	_, err = m.db.Exec(ctx, insertSQL, cfg.AppName, cfg.PublicURL, "Asia/Shanghai", cfg.PaymentProviderName, cfg.PaymentCheckoutURL, paymentEnc,
		cfg.NotificationWebhookURL, notificationEnc, metricsEnc, seconds(cfg.WorkerPollInterval), seconds(cfg.ReconcileInterval), seconds(cfg.LifecycleInterval), seconds(cfg.RenewalLeadTime), seconds(cfg.OverdueGracePeriod), seconds(cfg.TerminationRetention))
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
		v.NotificationWebhookURL, notificationEnc, metricsEnc, seconds(v.WorkerPollInterval), seconds(v.ReconcileInterval), seconds(v.LifecycleInterval), seconds(v.RenewalLeadTime), seconds(v.OverdueGracePeriod), seconds(v.TerminationRetention)); err != nil {
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
	renewal_lead_seconds,overdue_grace_seconds,termination_retention_seconds) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`

func validate(in InstallInput) (Runtime, InstallResult, error) {
	v := Runtime{Installed: true, AppName: strings.TrimSpace(in.AppName), PublicURL: strings.TrimRight(strings.TrimSpace(in.PublicURL), "/"), Timezone: strings.TrimSpace(in.Timezone),
		PaymentProviderName: strings.TrimSpace(in.PaymentProviderName), PaymentCheckoutURL: strings.TrimSpace(in.PaymentCheckoutURL), PaymentWebhookSecret: strings.TrimSpace(in.PaymentWebhookSecret),
		NotificationWebhookURL: strings.TrimSpace(in.NotificationWebhookURL), NotificationWebhookSecret: strings.TrimSpace(in.NotificationWebhookSecret), MetricsToken: strings.TrimSpace(in.MetricsToken)}
	if v.AppName == "" || v.Timezone == "" || strings.TrimSpace(in.AdminDisplayName) == "" || strings.TrimSpace(in.AdminEmail) == "" {
		return Runtime{}, InstallResult{}, errors.New("站点名称、时区和管理员信息不能为空")
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
