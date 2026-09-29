package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	AppName                   string
	Environment               string
	HTTPAddr                  string
	DatabaseURL               string
	SessionSecret             string
	EncryptionKey             string
	PaymentWebhookSecret      string
	PaymentCheckoutURL        string
	PaymentProviderName       string
	NotificationWebhookURL    string
	NotificationWebhookSecret string
	MetricsToken              string
	PublicURL                 string
	// InternalURL is how other API instances reach this one to forward Hatch
	// agent requests. Empty disables forwarding (single instance); "auto"
	// uses this host's first private IPv4 and the HTTP port.
	InternalURL string
	// AgentDownloadDir holds the Hatch agent release bundled with the image.
	AgentDownloadDir string
	// BackupDir keeps the local copies of backups; mount a volume there.
	BackupDir            string
	WorkerPollInterval   time.Duration
	ReconcileInterval    time.Duration
	LifecycleInterval    time.Duration
	RenewalLeadTime      time.Duration
	OverdueGracePeriod   time.Duration
	TerminationRetention time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		AppName:                   env("APP_NAME", "VPSBill"),
		Environment:               env("APP_ENV", "development"),
		HTTPAddr:                  env("HTTP_ADDR", ":8080"),
		DatabaseURL:               strings.TrimSpace(os.Getenv("DATABASE_URL")),
		SessionSecret:             strings.TrimSpace(os.Getenv("SESSION_SECRET")),
		EncryptionKey:             strings.TrimSpace(os.Getenv("ENCRYPTION_KEY")),
		PaymentWebhookSecret:      strings.TrimSpace(os.Getenv("PAYMENT_WEBHOOK_SECRET")),
		PaymentCheckoutURL:        strings.TrimSpace(os.Getenv("PAYMENT_CHECKOUT_URL")),
		PaymentProviderName:       env("PAYMENT_PROVIDER_NAME", "generic"),
		NotificationWebhookURL:    strings.TrimSpace(os.Getenv("NOTIFICATION_WEBHOOK_URL")),
		NotificationWebhookSecret: strings.TrimSpace(os.Getenv("NOTIFICATION_WEBHOOK_SECRET")),
		MetricsToken:              strings.TrimSpace(os.Getenv("METRICS_TOKEN")),
		PublicURL:                 env("PUBLIC_URL", "http://localhost:8080"),
		InternalURL:               strings.TrimSpace(os.Getenv("INTERNAL_URL")),
		AgentDownloadDir:          env("AGENT_DOWNLOAD_DIR", "/app/hatch-agent"),
		BackupDir:                 env("BACKUP_DIR", "/var/lib/vpsbill/backups"),
	}
	var err error
	cfg.WorkerPollInterval, err = durationEnv("WORKER_POLL_INTERVAL", 3*time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg.ReconcileInterval, err = durationEnv("RECONCILE_INTERVAL", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	cfg.LifecycleInterval, err = durationEnv("LIFECYCLE_INTERVAL", time.Minute)
	if err != nil {
		return Config{}, err
	}
	cfg.RenewalLeadTime, err = durationEnv("RENEWAL_LEAD_TIME", 7*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	cfg.OverdueGracePeriod, err = durationEnv("OVERDUE_GRACE_PERIOD", 3*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	cfg.TerminationRetention, err = durationEnv("TERMINATION_RETENTION", 7*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.Environment == "production" && len(cfg.SessionSecret) < 32 {
		return Config{}, errors.New("SESSION_SECRET must contain at least 32 characters in production")
	}
	if cfg.EncryptionKey == "" && cfg.Environment != "production" {
		hash := sha256.Sum256([]byte("vpsbill-development-encryption-key"))
		cfg.EncryptionKey = hex.EncodeToString(hash[:])
	}
	if len(cfg.EncryptionKey) != 64 {
		return Config{}, errors.New("ENCRYPTION_KEY must contain 64 hexadecimal characters")
	}
	if cfg.PaymentWebhookSecret == "" && cfg.Environment != "production" {
		cfg.PaymentWebhookSecret = "development-payment-webhook-secret"
	}
	if cfg.PaymentCheckoutURL != "" {
		parsed, err := url.Parse(cfg.PaymentCheckoutURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return Config{}, errors.New("PAYMENT_CHECKOUT_URL must be an absolute HTTP(S) URL")
		}
	}
	if cfg.NotificationWebhookURL != "" {
		parsed, parseErr := url.Parse(cfg.NotificationWebhookURL)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return Config{}, errors.New("NOTIFICATION_WEBHOOK_URL must be an absolute HTTP(S) URL")
		}
		if len(cfg.NotificationWebhookSecret) < 32 {
			return Config{}, errors.New("NOTIFICATION_WEBHOOK_SECRET must contain at least 32 characters when notification webhook is enabled")
		}
	}
	return cfg, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, errors.New(key + " must be a positive duration such as 3s or 5m")
	}
	return duration, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// ResolveInternalURL expands INTERNAL_URL=auto to http://<private IPv4>:<port>.
func (c Config) ResolveInternalURL() (string, error) {
	if c.InternalURL != "auto" {
		return strings.TrimRight(c.InternalURL, "/"), nil
	}
	_, port, err := net.SplitHostPort(c.HTTPAddr)
	if err != nil {
		return "", fmt.Errorf("HTTP_ADDR: %w", err)
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok && network.IP.To4() != nil && !network.IP.IsLoopback() {
			return "http://" + net.JoinHostPort(network.IP.String(), port), nil
		}
	}
	return "", errors.New("INTERNAL_URL=auto found no IPv4 address")
}
