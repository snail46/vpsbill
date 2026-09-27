package settings

import (
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
)

// MailNotifications chooses which reminder mails are sent. Keys missing
// from the stored document keep their defaults, so new kinds start enabled.
type MailNotifications struct {
	// AdminEmails receives merchant notices; empty means every active staff
	// member's login address.
	AdminEmails            string `json:"admin_emails"`
	CustomerExpiry         bool   `json:"customer_expiry"`
	CustomerTraffic        bool   `json:"customer_traffic"`
	CustomerTicketReply    bool   `json:"customer_ticket_reply"`
	AdminNodeExpiry        bool   `json:"admin_node_expiry"`
	AdminNodeTraffic       bool   `json:"admin_node_traffic"`
	AdminTicket            bool   `json:"admin_ticket"`
	ExpiryReminderDays     int    `json:"expiry_reminder_days"`
	NodeExpiryReminderDays int    `json:"node_expiry_reminder_days"`
	TrafficAlertPercent    int    `json:"traffic_alert_percent"`
}

func DefaultMailNotifications() MailNotifications {
	return MailNotifications{
		CustomerExpiry: true, CustomerTraffic: true, CustomerTicketReply: true,
		AdminNodeExpiry: true, AdminNodeTraffic: true, AdminTicket: true,
		ExpiryReminderDays: 3, NodeExpiryReminderDays: 7, TrafficAlertPercent: 80,
	}
}

func decodeMailNotifications(raw []byte) MailNotifications {
	value := DefaultMailNotifications()
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

func (n MailNotifications) validate() error {
	if n.ExpiryReminderDays < 1 || n.ExpiryReminderDays > 30 || n.NodeExpiryReminderDays < 1 || n.NodeExpiryReminderDays > 60 {
		return errors.New("到期提醒天数超出范围（实例 1–30 天，母鸡 1–60 天）")
	}
	if n.TrafficAlertPercent < 50 || n.TrafficAlertPercent > 99 {
		return errors.New("流量告警阈值必须在 50%–99% 之间")
	}
	for _, address := range n.AdminRecipients() {
		if _, err := mail.ParseAddress(address); err != nil {
			return errors.New("管理员通知邮箱格式无效：" + address)
		}
	}
	return nil
}

// AdminRecipients splits the configured admin addresses.
func (n MailNotifications) AdminRecipients() []string {
	var result []string
	for _, part := range strings.FieldsFunc(n.AdminEmails, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
