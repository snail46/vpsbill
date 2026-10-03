package notify

import (
	"context"
	"fmt"
)

var priorityNames = map[string][2]string{"low": {"低", "low"}, "normal": {"普通", "normal"}, "high": {"高", "high"}, "urgent": {"紧急", "urgent"}}

func priorityName(lang, priority string) string {
	names, ok := priorityNames[priority]
	if !ok {
		return priority
	}
	return say(lang, names[0], names[1])
}

// TicketCreated tells whoever answers first about a new ticket: the host
// for a hosted instance, merchant staff otherwise.
func (n *Notifier) TicketCreated(ctx context.Context, ticketID, messageID, body string) {
	if !n.enabled() {
		return
	}
	ticket, err := n.store.TicketMailContext(ctx, ticketID)
	if err != nil {
		n.logger.Error("load ticket for notification", "ticket_id", ticketID, "error", err)
		return
	}
	if ticket.HostEmail != "" {
		lang := n.lang(ctx, ticket.HostEmail)
		subject := fmt.Sprintf(say(lang, "[%s] 托管工单 %s：%s", "[%s] Hosting ticket %s: %s"), n.siteName(), ticket.Number, ticket.Subject)
		text := fmt.Sprintf(say(lang,
			"您好，%s：\n\n购买您托管母机实例的用户 %s 提交了工单，请及时处理。\n\n编号：%s\n主题：%s\n优先级：%s\n\n%s\n\n处理工单：%s\n",
			"Hello %s,\n\n%s, who bought an instance on your hosted server, opened a ticket. Please handle it soon.\n\nNumber: %s\nSubject: %s\nPriority: %s\n\n%s\n\nHandle the ticket: %s\n"),
			ticket.HostName, ticket.CustomerName, ticket.Number, ticket.Subject, priorityName(lang, ticket.Priority), excerpt(body, 800), n.link("/portal/hosting"))
		n.enqueueTicket(ctx, ticket.HostEmail, subject, text, "ticket-message:"+messageID+":"+ticket.HostEmail, ticketID)
		return
	}
	if !n.settings.Current().MailNotifications.AdminTicket {
		return
	}
	for _, to := range n.adminRecipients(ctx) {
		lang := n.lang(ctx, to)
		subject := fmt.Sprintf(say(lang, "[%s] 新工单 %s：%s", "[%s] New ticket %s: %s"), n.siteName(), ticket.Number, ticket.Subject)
		text := fmt.Sprintf(say(lang,
			"客户 %s 提交了新工单。\n\n编号：%s\n主题：%s\n优先级：%s\n\n%s\n\n处理工单：%s\n",
			"Customer %s opened a ticket.\n\nNumber: %s\nSubject: %s\nPriority: %s\n\n%s\n\nHandle the ticket: %s\n"),
			ticket.CustomerName, ticket.Number, ticket.Subject, priorityName(lang, ticket.Priority), excerpt(body, 800), n.adminLink("/admin/support"))
		n.enqueueTicket(ctx, to, subject, text, "ticket-message:"+messageID+":"+to, ticketID)
	}
}

// TicketReplied mails the other side of the conversation. Staff and host
// replies go to the customer; customer replies go to the host of a hosted
// instance, or to merchant staff. The host also hears when staff step into
// one of its tickets. Internal notes never leave the admin console.
func (n *Notifier) TicketReplied(ctx context.Context, ticketID, messageID, authorType, body string, internal bool) {
	if !n.enabled() || internal {
		return
	}
	preferences := n.settings.Current().MailNotifications
	ticket, err := n.store.TicketMailContext(ctx, ticketID)
	if err != nil {
		n.logger.Error("load ticket for notification", "ticket_id", ticketID, "error", err)
		return
	}
	switch authorType {
	case "staff", "host":
		if authorType == "staff" && ticket.HostEmail != "" {
			lang := n.lang(ctx, ticket.HostEmail)
			subject := fmt.Sprintf(say(lang, "[%s] 平台客服回复了托管工单 %s", "[%s] Platform support replied to hosting ticket %s"), n.siteName(), ticket.Number)
			text := fmt.Sprintf(say(lang,
				"您好，%s：\n\n平台客服在托管工单「%s」中回复了用户：\n\n%s\n\n查看工单：%s\n",
				"Hello %s,\n\nPlatform support replied to the user in the hosting ticket “%s”:\n\n%s\n\nSee the ticket: %s\n"),
				ticket.HostName, ticket.Subject, excerpt(body, 1500), n.link("/portal/hosting"))
			n.enqueueTicket(ctx, ticket.HostEmail, subject, text, "ticket-message:"+messageID+":"+ticket.HostEmail, ticketID)
		}
		if !preferences.CustomerTicketReply {
			return
		}
		lang := n.lang(ctx, ticket.RequesterEmail)
		who := say(lang, "客服", "support")
		if authorType == "host" {
			who = say(lang, "母机机主", "the host owner")
		}
		subject := fmt.Sprintf(say(lang, "[%s] 您的工单 %s 有新回复", "[%s] New reply to your ticket %s"), n.siteName(), ticket.Number)
		text := fmt.Sprintf(say(lang,
			"您好，%s：\n\n您的工单「%s」收到了%s回复：\n\n%s\n\n查看并回复：%s\n",
			"Hello %s,\n\nYour ticket “%s” has a reply from %s:\n\n%s\n\nRead and reply: %s\n"),
			ticket.CustomerName, ticket.Subject, who, excerpt(body, 1500), n.link("/portal/support"))
		n.enqueueTicket(ctx, ticket.RequesterEmail, subject, text, "ticket-message:"+messageID+":"+ticket.RequesterEmail, ticketID)
	case "customer":
		if ticket.HostEmail != "" {
			lang := n.lang(ctx, ticket.HostEmail)
			subject := fmt.Sprintf(say(lang, "[%s] 托管工单 %s 用户回复：%s", "[%s] The user replied to hosting ticket %s: %s"), n.siteName(), ticket.Number, ticket.Subject)
			text := fmt.Sprintf(say(lang,
				"您好，%s：\n\n用户 %s 回复了托管工单 %s。\n\n%s\n\n处理工单：%s\n",
				"Hello %s,\n\n%s replied to hosting ticket %s.\n\n%s\n\nHandle the ticket: %s\n"),
				ticket.HostName, ticket.CustomerName, ticket.Number, excerpt(body, 800), n.link("/portal/hosting"))
			n.enqueueTicket(ctx, ticket.HostEmail, subject, text, "ticket-message:"+messageID+":"+ticket.HostEmail, ticketID)
			return
		}
		if !preferences.AdminTicket {
			return
		}
		for _, to := range n.adminRecipients(ctx) {
			lang := n.lang(ctx, to)
			subject := fmt.Sprintf(say(lang, "[%s] 工单 %s 客户回复：%s", "[%s] The customer replied to ticket %s: %s"), n.siteName(), ticket.Number, ticket.Subject)
			text := fmt.Sprintf(say(lang,
				"客户 %s 回复了工单 %s。\n\n%s\n\n处理工单：%s\n",
				"Customer %s replied to ticket %s.\n\n%s\n\nHandle the ticket: %s\n"),
				ticket.CustomerName, ticket.Number, excerpt(body, 800), n.adminLink("/admin/support"))
			n.enqueueTicket(ctx, to, subject, text, "ticket-message:"+messageID+":"+to, ticketID)
		}
	}
}
