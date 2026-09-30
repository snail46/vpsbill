package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"vpsbill/internal/clock"
	"vpsbill/internal/mail"
)

// SiteView is what the admin site settings page shows. Secrets are reported
// only as configured or not.
type SiteView struct {
	AppName                  string              `json:"app_name"`
	PublicURL                string              `json:"public_url"`
	AdminURL                 string              `json:"admin_url"`
	Timezone                 string              `json:"timezone"`
	WorkerPollInterval       string              `json:"worker_poll_interval"`
	ReconcileInterval        string              `json:"reconcile_interval"`
	LifecycleInterval        string              `json:"lifecycle_interval"`
	RenewalLeadTime          string              `json:"renewal_lead_time"`
	OverdueGracePeriod       string              `json:"overdue_grace_period"`
	TerminationRetention     string              `json:"termination_retention"`
	NotificationWebhookURL   string              `json:"notification_webhook_url"`
	NotificationSecretSet    bool                `json:"notification_webhook_secret_configured"`
	SMTPHost                 string              `json:"smtp_host"`
	SMTPPort                 int                 `json:"smtp_port"`
	SMTPUsername             string              `json:"smtp_username"`
	SMTPPasswordConfigured   bool                `json:"smtp_password_configured"`
	SMTPFrom                 string              `json:"smtp_from"`
	SMTPSecurity             string              `json:"smtp_security"`
	PasswordResetMailEnabled bool                `json:"password_reset_mail_enabled"`
	MailNotifications        MailNotifications   `json:"mail_notifications"`
	TicketAttachmentMaxMB    int                 `json:"ticket_attachment_max_mb"`
	Marketplace              MarketplaceSettings `json:"marketplace"`
	// Logo is the logo shown (see Runtime.Logo); LogoExternalURL is set when
	// it comes from another site.
	Logo            string `json:"logo_url"`
	LogoExternalURL string `json:"logo_external_url"`
	LogoMode        string `json:"logo_mode"`
	// ProxyWarning describes a reverse proxy set up so that visitors' real
	// addresses are lost; the HTTP layer fills it in.
	ProxyWarning string `json:"proxy_warning,omitempty"`
}

// SiteInput updates the site settings. Empty secrets keep the stored value;
// ClearSMTPPassword removes the SMTP password.
type SiteInput struct {
	AppName                   string              `json:"app_name"`
	PublicURL                 string              `json:"public_url"`
	AdminURL                  string              `json:"admin_url"`
	Timezone                  string              `json:"timezone"`
	WorkerPollInterval        string              `json:"worker_poll_interval"`
	ReconcileInterval         string              `json:"reconcile_interval"`
	LifecycleInterval         string              `json:"lifecycle_interval"`
	RenewalLeadTime           string              `json:"renewal_lead_time"`
	OverdueGracePeriod        string              `json:"overdue_grace_period"`
	TerminationRetention      string              `json:"termination_retention"`
	NotificationWebhookURL    string              `json:"notification_webhook_url"`
	NotificationWebhookSecret string              `json:"notification_webhook_secret"`
	SMTPHost                  string              `json:"smtp_host"`
	SMTPPort                  int                 `json:"smtp_port"`
	SMTPUsername              string              `json:"smtp_username"`
	SMTPPassword              string              `json:"smtp_password"`
	ClearSMTPPassword         bool                `json:"clear_smtp_password"`
	SMTPFrom                  string              `json:"smtp_from"`
	SMTPSecurity              string              `json:"smtp_security"`
	MailNotifications         MailNotifications   `json:"mail_notifications"`
	TicketAttachmentMaxMB     int                 `json:"ticket_attachment_max_mb"`
	Marketplace               MarketplaceSettings `json:"marketplace"`
	// LogoMode is auto (decided by the image's shape), icon (the site
	// name stays beside the logo) or wordmark (the logo already carries
	// the brand name and takes its place); empty keeps the current one.
	LogoMode string `json:"logo_mode"`
}

func (m *Manager) SiteView() SiteView {
	c := m.Current()
	return SiteView{
		AppName: c.AppName, PublicURL: c.PublicURL, AdminURL: c.AdminURL, Timezone: c.Timezone,
		WorkerPollInterval: compactDuration(c.WorkerPollInterval), ReconcileInterval: compactDuration(c.ReconcileInterval),
		LifecycleInterval: compactDuration(c.LifecycleInterval), RenewalLeadTime: compactDuration(c.RenewalLeadTime),
		OverdueGracePeriod: compactDuration(c.OverdueGracePeriod), TerminationRetention: compactDuration(c.TerminationRetention),
		NotificationWebhookURL: c.NotificationWebhookURL, NotificationSecretSet: c.NotificationWebhookSecret != "",
		SMTPHost: c.SMTP.Host, SMTPPort: c.SMTP.Port, SMTPUsername: c.SMTP.Username, SMTPPasswordConfigured: c.SMTP.Password != "",
		SMTPFrom: c.SMTP.From, SMTPSecurity: c.SMTP.Security, PasswordResetMailEnabled: c.SMTP.Configured(),
		MailNotifications: c.MailNotifications, TicketAttachmentMaxMB: c.TicketAttachmentMaxMB, Marketplace: c.Marketplace,
		Logo: c.Logo(), LogoExternalURL: c.LogoURL, LogoMode: c.LogoMode,
	}
}

// UpdateSite validates and stores the site settings, then reloads them so
// the workers pick up new intervals on their next tick.
func (m *Manager) UpdateSite(ctx context.Context, in SiteInput, actorID string) (SiteView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.Current()
	appName := strings.TrimSpace(in.AppName)
	publicURL := strings.TrimRight(strings.TrimSpace(in.PublicURL), "/")
	// The platform calendar is fixed at UTC+8; the column is kept for
	// existing installations.
	timezone := clock.DatabaseZone
	invalid := func(message string) (SiteView, error) {
		return SiteView{}, fmt.Errorf("%w: %s", ErrInvalidSettings, message)
	}
	if appName == "" {
		return invalid("站点名称不能为空")
	}
	if absoluteURL(publicURL, true) != nil {
		return invalid("公开访问地址必须是完整的 HTTP(S) URL")
	}
	adminURL := strings.TrimRight(strings.TrimSpace(in.AdminURL), "/")
	if adminURL != "" && absoluteURL(adminURL, true) != nil {
		return invalid("后台访问地址必须是完整的 HTTP(S) URL，或者留空")
	}
	durations := []struct {
		value   string
		current time.Duration
		minimum time.Duration
		target  *time.Duration
	}{
		{in.WorkerPollInterval, current.WorkerPollInterval, time.Second, new(time.Duration)},
		{in.ReconcileInterval, current.ReconcileInterval, 30 * time.Second, new(time.Duration)},
		{in.LifecycleInterval, current.LifecycleInterval, 10 * time.Second, new(time.Duration)},
		{in.RenewalLeadTime, current.RenewalLeadTime, time.Hour, new(time.Duration)},
		{in.OverdueGracePeriod, current.OverdueGracePeriod, time.Minute, new(time.Duration)},
		{in.TerminationRetention, current.TerminationRetention, time.Minute, new(time.Duration)},
	}
	for _, item := range durations {
		if strings.TrimSpace(item.value) == "" {
			*item.target = item.current
			continue
		}
		parsed, err := time.ParseDuration(strings.TrimSpace(item.value))
		if err != nil || parsed < item.minimum {
			return invalid("时间参数必须使用 3s、5m 或 168h 等格式，且不能过短")
		}
		*item.target = parsed
	}
	notificationURL := strings.TrimSpace(in.NotificationWebhookURL)
	if absoluteURL(notificationURL, false) != nil {
		return invalid("通知地址必须是完整的 HTTP(S) URL")
	}
	notificationSecret := strings.TrimSpace(in.NotificationWebhookSecret)
	if notificationSecret == "" {
		notificationSecret = current.NotificationWebhookSecret
	}
	if notificationSecret != "" && len(notificationSecret) < 32 {
		return invalid("通知签名密钥至少 32 位")
	}
	if notificationURL != "" && notificationSecret == "" {
		notificationSecret = randomSecret()
	}
	smtp := mail.Config{
		Host: strings.TrimSpace(in.SMTPHost), Port: in.SMTPPort, Username: strings.TrimSpace(in.SMTPUsername),
		Password: in.SMTPPassword, From: strings.TrimSpace(in.SMTPFrom), Security: strings.TrimSpace(in.SMTPSecurity),
	}
	if smtp.Port == 0 {
		smtp.Port = 587
	}
	if smtp.Security == "" {
		smtp.Security = "starttls"
	}
	if smtp.Password == "" && !in.ClearSMTPPassword {
		smtp.Password = current.SMTP.Password
	}
	if smtp.Host != "" && smtp.From == "" {
		return invalid("填写 SMTP 服务器后还需要填写发件人地址")
	}
	if err := smtp.Validate(); err != nil {
		return invalid(err.Error())
	}
	notifications := in.MailNotifications
	notifications.AdminEmails = strings.Join(notifications.AdminRecipients(), ", ")
	if err := notifications.validate(); err != nil {
		return invalid(err.Error())
	}
	if in.TicketAttachmentMaxMB < 1 || in.TicketAttachmentMaxMB > 20 {
		return invalid("工单附件大小上限必须在 1–20 MB 之间")
	}
	if in.Marketplace.FeePercent < 0 || in.Marketplace.FeePercent > 90 {
		return invalid("托管手续费比例必须在 0–90% 之间")
	}
	if in.Marketplace.OfflineHours < 1 || in.Marketplace.OfflineHours > 720 {
		return invalid("托管母机离线清退时限必须在 1–720 小时之间")
	}
	if in.Marketplace.TradeFeePercent < 0 || in.Marketplace.TradeFeePercent > 90 {
		return invalid("交易市场手续费比例必须在 0–90% 之间")
	}
	if in.Marketplace.TradeHoldDays < 0 || in.Marketplace.TradeHoldDays > 365 {
		return invalid("交易市场挂售门槛必须在 0–365 天之间")
	}
	logoMode := strings.TrimSpace(in.LogoMode)
	if logoMode == "" {
		logoMode = current.LogoMode
	}
	if logoMode != "auto" && logoMode != "icon" && logoMode != "wordmark" {
		return invalid("Logo 显示方式无效")
	}
	for _, ratio := range []float64{in.Marketplace.MaxOvercommitCPU, in.Marketplace.MaxOvercommitRAM, in.Marketplace.MaxOvercommitDisk, in.Marketplace.MaxOvercommitTraffic} {
		if ratio < 1 || ratio > 20 {
			return invalid("超售倍数上限必须在 1–20 之间")
		}
	}
	notificationsJSON, err := json.Marshal(notifications)
	if err != nil {
		return SiteView{}, err
	}
	var notificationEnc, smtpPasswordEnc []byte
	if notificationSecret != "" {
		if notificationEnc, err = m.box.Seal(notificationSecret); err != nil {
			return SiteView{}, err
		}
	}
	if smtp.Password != "" {
		if smtpPasswordEnc, err = m.box.Seal(smtp.Password); err != nil {
			return SiteView{}, err
		}
	}
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return SiteView{}, err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE system_settings SET app_name=$1,public_url=$2,timezone=$3,
		    worker_poll_interval_seconds=$4,reconcile_interval_seconds=$5,lifecycle_interval_seconds=$6,
		    renewal_lead_seconds=$7,overdue_grace_seconds=$8,termination_retention_seconds=$9,
		    notification_webhook_url=$10,notification_webhook_secret_encrypted=$11,
		    smtp_host=$12,smtp_port=$13,smtp_username=$14,smtp_password_encrypted=$15,smtp_from=$16,smtp_security=$17,
		    mail_notifications=$18,ticket_attachment_max_mb=$19,
		    marketplace_enabled=$20,marketplace_fee_percent=$21,marketplace_offline_hours=$22,trade_fee_percent=$23,admin_url=$24,
		    max_overcommit_cpu=$25,max_overcommit_ram=$26,max_overcommit_disk=$27,max_overcommit_traffic=$28,trade_hold_days=$29,logo_mode=$30,updated_at=now()
		WHERE singleton=true
	`, appName, publicURL, timezone,
		seconds(*durations[0].target), seconds(*durations[1].target), seconds(*durations[2].target),
		seconds(*durations[3].target), seconds(*durations[4].target), seconds(*durations[5].target),
		notificationURL, notificationEnc, smtp.Host, smtp.Port, smtp.Username, smtpPasswordEnc, smtp.From, smtp.Security, notificationsJSON, in.TicketAttachmentMaxMB,
		in.Marketplace.Enabled, in.Marketplace.FeePercent, in.Marketplace.OfflineHours, in.Marketplace.TradeFeePercent, adminURL,
		in.Marketplace.MaxOvercommitCPU, in.Marketplace.MaxOvercommitRAM, in.Marketplace.MaxOvercommitDisk, in.Marketplace.MaxOvercommitTraffic,
		in.Marketplace.TradeHoldDays, logoMode)
	if err != nil {
		return SiteView{}, err
	}
	if command.RowsAffected() == 0 {
		return SiteView{}, errors.New("system settings are missing")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id) VALUES('staff',$1,'site_settings.updated','system_settings','singleton')`, actorID); err != nil {
		return SiteView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SiteView{}, err
	}
	if err = m.reload(ctx); err != nil {
		return SiteView{}, err
	}
	return m.SiteView(), nil
}

// compactDuration prints 72h, 5m or 3s instead of 72h0m0s.
func compactDuration(value time.Duration) string {
	switch {
	case value%time.Hour == 0:
		return fmt.Sprintf("%dh", value/time.Hour)
	case value%time.Minute == 0:
		return fmt.Sprintf("%dm", value/time.Minute)
	default:
		return fmt.Sprintf("%ds", value/time.Second)
	}
}
