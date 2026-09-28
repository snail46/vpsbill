// Package notify sends notification mail to customers and merchant staff:
// ticket activity as it happens, and expiry and traffic reminders from a
// periodic scan. Mail goes through the mail_queue table so it survives
// restarts and SMTP outages.
package notify

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"vpsbill/internal/clock"
	"vpsbill/internal/mail"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

const (
	sendInterval  = 15 * time.Second
	sendBatch     = 20
	maxAttempts   = 8
	scanInterval  = 10 * time.Minute
	firstScanWait = time.Minute
)

type Notifier struct {
	store    *postgres.MailStore
	settings *settings.Manager
	box      *security.SecretBox
	logger   *slog.Logger
	send     func(ctx context.Context, config mail.Config, to, subject, body string) error
	now      func() time.Time
}

func New(store *postgres.MailStore, runtime *settings.Manager, box *security.SecretBox, logger *slog.Logger) *Notifier {
	return &Notifier{store: store, settings: runtime, box: box, logger: logger, send: mail.Send, now: time.Now}
}

// enabled reports whether mail can be sent at all; without SMTP nothing is
// queued, so enabling it later does not flush a backlog of stale reminders.
func (n *Notifier) enabled() bool { return n.settings.Current().SMTP.Configured() }

func (n *Notifier) enqueue(ctx context.Context, to, subject, body, dedupKey string) {
	if _, err := n.store.EnqueueMail(ctx, to, subject, body, dedupKey); err != nil {
		n.logger.Error("queue notification mail", "to", to, "error", err)
	}
}

// adminRecipients returns the configured merchant addresses, or every active
// staff member when none are configured.
func (n *Notifier) adminRecipients(ctx context.Context) []string {
	if configured := n.settings.Current().MailNotifications.AdminRecipients(); len(configured) > 0 {
		return configured
	}
	emails, err := n.store.StaffEmails(ctx)
	if err != nil {
		n.logger.Error("list staff emails", "error", err)
		return nil
	}
	return emails
}

func (n *Notifier) link(path string) string {
	return strings.TrimRight(n.settings.Current().PublicURL, "/") + path
}

// adminLink points into the admin console, which may have its own address.
func (n *Notifier) adminLink(path string) string {
	current := n.settings.Current()
	if current.AdminURL != "" {
		return strings.TrimRight(current.AdminURL, "/") + path
	}
	return strings.TrimRight(current.PublicURL, "/") + path
}

// location is the platform calendar; mail shows every time in UTC+8.
func (n *Notifier) location() *time.Location { return clock.Zone }

func (n *Notifier) siteName() string {
	if name := n.settings.Current().AppName; name != "" {
		return name
	}
	return "VPSBill"
}

// RunSender delivers queued mail until ctx ends.
func (n *Notifier) RunSender(ctx context.Context) {
	for {
		n.sendDue(ctx)
		timer := time.NewTimer(sendInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (n *Notifier) sendDue(ctx context.Context) {
	config := n.settings.Current().SMTP
	if !config.Configured() {
		return
	}
	batch, err := n.store.ClaimMail(ctx, sendBatch)
	if err != nil {
		n.logger.Error("claim notification mail", "error", err)
		return
	}
	for _, item := range batch {
		sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := n.send(sendCtx, config, item.Recipient, item.Subject, item.Body)
		cancel()
		if err == nil {
			if err := n.store.CompleteMail(ctx, item.ID); err != nil {
				n.logger.Error("complete notification mail", "id", item.ID, "error", err)
			}
			continue
		}
		n.logger.Warn("send notification mail", "id", item.ID, "to", item.Recipient, "attempt", item.Attempts, "error", err)
		retry := time.Duration(1<<min(item.Attempts, 8)) * time.Minute
		if err := n.store.FailMail(ctx, item.ID, item.Attempts, maxAttempts, retry, err.Error()); err != nil {
			n.logger.Error("reschedule notification mail", "id", item.ID, "error", err)
		}
	}
}

// excerpt shortens message text for a notification body.
func excerpt(text string, limit int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "…"
}
