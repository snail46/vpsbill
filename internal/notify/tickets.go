package notify

import (
	"context"
	"fmt"
)

var priorityNames = map[string]string{"low": "低", "normal": "普通", "high": "高", "urgent": "紧急"}

// TicketCreated tells merchant staff about a new customer ticket.
func (n *Notifier) TicketCreated(ctx context.Context, ticketID, messageID, body string) {
	if !n.enabled() || !n.settings.Current().MailNotifications.AdminTicket {
		return
	}
	ticket, err := n.store.TicketMailContext(ctx, ticketID)
	if err != nil {
		n.logger.Error("load ticket for notification", "ticket_id", ticketID, "error", err)
		return
	}
	subject := fmt.Sprintf("[%s] 新工单 %s：%s", n.siteName(), ticket.Number, ticket.Subject)
	text := fmt.Sprintf("客户 %s 提交了新工单。\n\n编号：%s\n主题：%s\n优先级：%s\n\n%s\n\n处理工单：%s\n",
		ticket.CustomerName, ticket.Number, ticket.Subject, priorityNames[ticket.Priority], excerpt(body, 800), n.link("/admin/support"))
	for _, to := range n.adminRecipients(ctx) {
		n.enqueue(ctx, to, subject, text, "ticket-message:"+messageID+":"+to)
	}
}

// TicketReplied mails the other side of the conversation: staff replies go
// to the customer, customer replies to merchant staff. Internal notes never
// leave the admin console.
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
	case "staff":
		if !preferences.CustomerTicketReply {
			return
		}
		subject := fmt.Sprintf("[%s] 您的工单 %s 有新回复", n.siteName(), ticket.Number)
		text := fmt.Sprintf("您好，%s：\n\n您的工单「%s」收到了客服回复：\n\n%s\n\n查看并回复：%s\n",
			ticket.CustomerName, ticket.Subject, excerpt(body, 1500), n.link("/portal/support"))
		n.enqueue(ctx, ticket.RequesterEmail, subject, text, "ticket-message:"+messageID+":"+ticket.RequesterEmail)
	case "customer":
		if !preferences.AdminTicket {
			return
		}
		subject := fmt.Sprintf("[%s] 工单 %s 客户回复：%s", n.siteName(), ticket.Number, ticket.Subject)
		text := fmt.Sprintf("客户 %s 回复了工单 %s。\n\n%s\n\n处理工单：%s\n",
			ticket.CustomerName, ticket.Number, excerpt(body, 800), n.link("/admin/support"))
		for _, to := range n.adminRecipients(ctx) {
			n.enqueue(ctx, to, subject, text, "ticket-message:"+messageID+":"+to)
		}
	}
}
