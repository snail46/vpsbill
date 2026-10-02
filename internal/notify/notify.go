// Package notify sends notification mail to customers and merchant staff:
// ticket activity as it happens, and expiry and traffic reminders from a
// periodic scan. Mail goes through the mail_queue table so it survives
// restarts and SMTP outages.
package notify

import (
	"context"
	"encoding/json"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"vpsbill/internal/clock"
	"vpsbill/internal/mail"
	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
	"vpsbill/internal/telegram"
)

const (
	sendInterval  = 15 * time.Second
	sendBatch     = 20
	maxAttempts   = 8
	scanInterval  = 10 * time.Minute
	firstScanWait = time.Minute
)

type Notifier struct {
	store *postgres.MailStore
	// catalog finds new and restocked plans and who waits for them.
	catalog  *postgres.CatalogStore
	settings *settings.Manager
	box      *security.SecretBox
	logger   *slog.Logger
	send     func(ctx context.Context, config mail.Config, to, subject, body string) error
	// sendTelegram delivers one message to a Telegram chat.
	sendTelegram func(ctx context.Context, config settings.TelegramSettings, chatID int64, text string, buttons [][]telegram.Button) error
	now          func() time.Time
}

func New(store *postgres.MailStore, runtime *settings.Manager, box *security.SecretBox, logger *slog.Logger) *Notifier {
	return &Notifier{store: store, catalog: postgres.NewCatalogStore(store.Pool()), settings: runtime, box: box, logger: logger, send: mail.Send, sendTelegram: sendTelegram, now: time.Now}
}

// enabled reports whether anything can be sent at all; without SMTP or a
// Telegram bot nothing is queued, so enabling one later does not flush a
// backlog of stale reminders.
func (n *Notifier) enabled() bool {
	current := n.settings.Current()
	return current.SMTP.Configured() || current.Telegram.Ready()
}

// enqueue queues a notice for the user with this address, through the
// channels they chose: mail, their linked Telegram account, or both. Mail
// is the fallback while Telegram cannot be used.
func (n *Notifier) enqueue(ctx context.Context, to, subject, body, dedupKey string) {
	n.enqueueWith(ctx, to, subject, body, dedupKey, nil)
}

// adminChat stands for the staff group among the merchant's recipients.
const adminChat = "telegram:staff-group"

func encodeButtons(buttons [][]telegram.Button) ([]byte, error) {
	if len(buttons) == 0 {
		return nil, nil
	}
	return json.Marshal(buttons)
}

// enqueueWith is enqueue with buttons under the Telegram message; mail
// carries the same links in its text.
func (n *Notifier) enqueueWith(ctx context.Context, to, subject, body, dedupKey string, buttons [][]telegram.Button) {
	current := n.settings.Current()
	markup, err := encodeButtons(buttons)
	if err != nil {
		n.logger.Error("encode notification buttons", "error", err)
	}
	if to == adminChat {
		if current.Telegram.Ready() && current.Telegram.AdminChatID != 0 {
			if _, err := n.store.EnqueueTelegram(ctx, current.Telegram.AdminChatID, subject, body, dedupKey, markup); err != nil {
				n.logger.Error("queue staff group notice", "error", err)
			}
		}
		return
	}
	target, err := n.store.NotifyTarget(ctx, to)
	if err != nil {
		n.logger.Error("read notification channels", "to", to, "error", err)
		target = postgres.NotifyTarget{Email: true}
	}
	viaTelegram := target.Telegram && target.TelegramID != 0 && current.Telegram.Ready()
	if viaTelegram {
		key := dedupKey
		if key != "" {
			key += ":telegram"
		}
		if _, err := n.store.EnqueueTelegram(ctx, target.TelegramID, subject, body, key, markup); err != nil {
			n.logger.Error("queue telegram notification", "to", to, "error", err)
		}
	}
	if (target.Email || !viaTelegram) && current.SMTP.Configured() {
		if _, err := n.store.EnqueueMail(ctx, to, subject, body, dedupKey); err != nil {
			n.logger.Error("queue notification mail", "to", to, "error", err)
		}
	}
}

// lang is the language to write to an address in: the one its user reads
// the site in, else the site's default.
func (n *Notifier) lang(ctx context.Context, email string) string {
	locale := ""
	if n.store != nil && email != "" {
		locale, _ = n.store.UserLocale(ctx, email)
	}
	return n.settings.Current().Lang(locale)
}

// say picks a text by language.
func say(lang, zh, en string) string {
	if lang == "en" {
		return en
	}
	return zh
}

// adminRecipients returns the configured merchant addresses, or every active
// staff member when none are configured, and the staff group on Telegram
// when one is set.
func (n *Notifier) adminRecipients(ctx context.Context) []string {
	current := n.settings.Current()
	recipients := current.MailNotifications.AdminRecipients()
	if len(recipients) == 0 {
		emails, err := n.store.StaffEmails(ctx)
		if err != nil {
			n.logger.Error("list staff emails", "error", err)
		}
		recipients = emails
	}
	if current.Telegram.Ready() && current.Telegram.AdminChatID != 0 {
		recipients = append(append([]string(nil), recipients...), adminChat)
	}
	return recipients
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
	current := n.settings.Current()
	if config := current.SMTP; config.Configured() {
		n.deliver(ctx, postgres.ChannelMail, func(ctx context.Context, item postgres.QueuedMail) error {
			return n.send(ctx, config, item.Recipient, item.Subject, item.Body)
		})
	}
	if config := current.Telegram; config.Ready() {
		n.deliver(ctx, postgres.ChannelTelegram, func(ctx context.Context, item postgres.QueuedMail) error {
			chatID, err := strconv.ParseInt(item.Recipient, 10, 64)
			if err != nil {
				return finalError{err}
			}
			var buttons [][]telegram.Button
			if len(item.Buttons) > 0 {
				_ = json.Unmarshal(item.Buttons, &buttons)
			}
			err = n.sendTelegram(ctx, config, chatID, telegramText(item.Subject, item.Body), buttons)
			if telegram.Forbidden(err) {
				// The user blocked the bot; trying again will not help.
				return finalError{err}
			}
			return err
		})
	}
}

// finalError marks a failure that a retry cannot fix.
type finalError struct{ error }

// deliver sends the due messages of one channel.
func (n *Notifier) deliver(ctx context.Context, channel string, send func(context.Context, postgres.QueuedMail) error) {
	batch, err := n.store.ClaimMail(ctx, channel, sendBatch)
	if err != nil {
		n.logger.Error("claim notifications", "channel", channel, "error", err)
		return
	}
	for _, item := range batch {
		sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := send(sendCtx, item)
		cancel()
		if err == nil {
			if err := n.store.CompleteMail(ctx, item.ID); err != nil {
				n.logger.Error("complete notification", "id", item.ID, "error", err)
			}
			continue
		}
		n.logger.Warn("send notification", "channel", channel, "id", item.ID, "to", item.Recipient, "attempt", item.Attempts, "error", err)
		attempts := item.Attempts
		if _, final := err.(finalError); final {
			attempts = maxAttempts
		}
		retry := time.Duration(1<<min(item.Attempts, 8)) * time.Minute
		if err := n.store.FailMail(ctx, item.ID, attempts, maxAttempts, retry, err.Error()); err != nil {
			n.logger.Error("reschedule notification", "id", item.ID, "error", err)
		}
	}
}

// telegramText is a notice as a Telegram message: the subject in bold over
// the text, cut to what one message holds.
func telegramText(subject, body string) string {
	return "<b>" + html.EscapeString(subject) + "</b>\n\n" + html.EscapeString(excerpt(body, 3200))
}

func sendTelegram(ctx context.Context, config settings.TelegramSettings, chatID int64, text string, buttons [][]telegram.Button) error {
	client := telegram.NewClient(config.APIBase, config.BotToken)
	if len(buttons) > 0 {
		if _, err := client.SendButtons(ctx, chatID, text, buttons); err == nil {
			return nil
		}
		// Telegram refuses buttons whose address it does not like (a
		// site without a public address); the text has the links too, so
		// the buttons that open one are left out.
		var actions [][]telegram.Button
		for _, row := range buttons {
			var kept []telegram.Button
			for _, button := range row {
				if button.URL == "" {
					kept = append(kept, button)
				}
			}
			if len(kept) > 0 {
				actions = append(actions, kept)
			}
		}
		if len(actions) > 0 {
			if _, err := client.SendButtons(ctx, chatID, text, actions); err == nil {
				return nil
			}
		}
	}
	_, err := client.SendMessage(ctx, chatID, text, 0)
	return err
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
