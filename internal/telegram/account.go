package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"vpsbill/internal/clock"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// What a linked customer does with the bot in a private chat: look at
// services, balance and unpaid invoices, and pay an invoice from the
// balance. Paying always asks for confirmation first.

// Callback data of the buttons: the invoice id follows the prefix.
const (
	callbackInvoice = "inv:"
	callbackPay     = "pay:"
	callbackCancel  = "x"
)

// InvoiceButton is the button that starts paying an invoice from the
// balance, for notices about it.
func InvoiceButton(text, invoiceID string) Button {
	return Button{Text: text, Data: callbackInvoice + invoiceID}
}

var (
	msgServicesNone = text{"你还没有 VPS。到 <a href=\"%s\">%s</a> 选购。", "You have no VPS yet. Choose one at <a href=\"%s\">%s</a>."}
	msgServicesHead = text{"🖥 你的 VPS（%d 台）：", "🖥 Your VPS (%d):"}
	msgServiceLine  = text{"\n\n<b>%s</b> · %s\n状态：%s%s%s", "\n\n<b>%s</b> · %s\nStatus: %s%s%s"}
	msgServiceDue   = text{"\n到期：%s", "\nExpires: %s"}
	msgServiceUsed  = text{"\n本月流量：%s / %d GB", "\nTraffic this month: %s / %d GB"}
	msgServicesMore = text{"\n\n还有 %d 台，请到网站查看。", "\n\n%d more; see them on the site."}
	msgBalance      = text{"💰 余额：<b>%s</b>", "💰 Balance: <b>%s</b>"}
	msgBalanceLine  = text{"\n%s %s %s", "\n%s %s %s"}
	msgBalanceHead  = text{"\n\n最近的变动：", "\n\nLatest entries:"}
	msgInvoicesNone = text{"没有待支付的账单。", "No unpaid invoices."}
	msgInvoicesHead = text{"🧾 待支付的账单（余额 %s）：", "🧾 Unpaid invoices (balance %s):"}
	msgInvoiceLine  = text{"\n\n<b>%s</b> %s · %s", "\n\n<b>%s</b> %s · %s"}
	msgPayButton    = text{"余额支付 %s", "Pay %s from balance"}
	msgConfirm      = text{
		"确认用余额支付账单 <b>%s</b>？\n\n%s金额：<b>%s</b>\n当前余额：%s\n支付后余额：%s",
		"Pay invoice <b>%s</b> from your balance?\n\n%sAmount: <b>%s</b>\nBalance now: %s\nBalance afterwards: %s",
	}
	msgConfirmRenew = text{"续费实例：%s%s\n", "Renews: %s%s\n"}
	msgRenewUntil   = text{"（到期日延长至 %s）", " (until %s)"}
	msgConfirmYes   = text{"确认支付", "Confirm"}
	msgConfirmNo    = text{"取消", "Cancel"}
	msgCancelled    = text{"已取消，没有扣款。", "Cancelled; nothing was charged."}
	msgShort        = text{
		"余额不足：账单 <b>%s</b> 需要 %s，当前余额 %s，还差 %s。请先充值再支付。",
		"Not enough balance: invoice <b>%s</b> needs %s, your balance is %s, %s short. Top up first.",
	}
	msgTopup       = text{"去充值", "Top up"}
	msgOpenSite    = text{"打开网站", "Open the site"}
	msgPaid        = text{"✅ 账单 <b>%s</b> 已用余额支付 %s，当前余额 %s。", "✅ Invoice <b>%s</b> was paid from your balance: %s. Balance now %s."}
	msgPaidGone    = text{"这张账单已经支付或已失效。", "This invoice has been paid already or is no longer valid."}
	msgPayFailed   = text{"支付没有成功，请稍后再试或到网站操作。", "The payment did not go through. Try again later or pay on the site."}
	msgPrivateOnly = text{"请私聊 @%s 使用这个功能。", "Use this in a private chat with @%s."}
	kindNames      = map[string]text{
		"initial": {"新购", "new order"}, "renewal": {"续费", "renewal"},
	}
	statusNames = map[string]text{
		"active": {"运行中", "active"}, "pending": {"开通中", "being set up"}, "provisioning": {"开通中", "being set up"},
		"overdue": {"已逾期", "overdue"}, "suspended": {"已暂停", "suspended"}, "error": {"开通失败", "failed"},
		"review": {"待人工处理", "under review"}, "terminating": {"回收中", "being recycled"},
	}
	entryNames = map[string]text{
		"topup": {"充值", "top-up"}, "earning": {"托管收益", "hosting income"}, "payment": {"余额支付", "payment"},
		"clearance_refund": {"清退补偿", "clearance refund"}, "clearance_penalty": {"清退赔付", "clearance penalty"},
		"adjustment": {"管理员调整", "adjustment"}, "refund": {"退款", "refund"}, "trade_purchase": {"交易市场购买", "trade purchase"},
		"trade_sale": {"交易市场售出", "trade sale"}, "reward": {"活动奖励", "reward"},
	}
)

func named(names map[string]text, key, lang string) string {
	if name, ok := names[key]; ok {
		return name.in(lang)
	}
	return html.EscapeString(key)
}

func day(value time.Time) string { return value.In(clock.Zone).Format("2006-01-02") }

func gigabytes(bytes int64) string { return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30)) }

// maxListed keeps the lists short enough for one message.
const maxListed = 10

func (b *Bot) services(ctx context.Context, link postgres.TelegramLink, lang string) string {
	site := b.settings.Current().PublicURL
	services, err := b.portal.ListServices(ctx, link.AccountID)
	if err != nil {
		b.logger.Error("telegram services", "error", err)
		return say(lang, msgPayFailed)
	}
	live := services[:0]
	for _, service := range services {
		if service.Status != "terminated" {
			live = append(live, service)
		}
	}
	if len(live) == 0 {
		return say(lang, msgServicesNone, site+"/portal/shop", siteHost(site))
	}
	var out strings.Builder
	out.WriteString(say(lang, msgServicesHead, len(live)))
	for index, service := range live {
		if index == maxListed {
			out.WriteString(say(lang, msgServicesMore, len(live)-maxListed))
			break
		}
		due, used := "", ""
		if service.NextDueAt != nil {
			due = say(lang, msgServiceDue, day(*service.NextDueAt))
		}
		if service.TrafficUsedBytes != nil && service.TrafficGB > 0 {
			used = say(lang, msgServiceUsed, gigabytes(*service.TrafficUsedBytes), service.TrafficGB)
		}
		out.WriteString(say(lang, msgServiceLine, html.EscapeString(service.InstanceName), html.EscapeString(service.PlanName), named(statusNames, service.Status, lang), due, used))
	}
	return out.String()
}

func (b *Bot) balance(ctx context.Context, link postgres.TelegramLink, lang string) string {
	wallet, err := b.billing.Wallet(ctx, link.AccountID, 5)
	if err != nil {
		b.logger.Error("telegram balance", "error", err)
		return say(lang, msgPayFailed)
	}
	var out strings.Builder
	out.WriteString(say(lang, msgBalance, b.money(wallet.BalanceMinor)))
	if len(wallet.Entries) > 0 {
		out.WriteString(say(lang, msgBalanceHead))
	}
	for _, entry := range wallet.Entries {
		amount := b.money(entry.AmountMinor)
		if entry.AmountMinor > 0 {
			amount = "+" + amount
		}
		out.WriteString(say(lang, msgBalanceLine, day(entry.CreatedAt), named(entryNames, entry.Kind, lang), amount))
	}
	return out.String()
}

// invoices lists what the balance can pay, with a button for each.
func (b *Bot) invoices(ctx context.Context, link postgres.TelegramLink, lang string) (string, [][]Button) {
	invoices, err := b.store.PayableInvoices(ctx, link.AccountID)
	if err != nil {
		b.logger.Error("telegram invoices", "error", err)
		return say(lang, msgPayFailed), nil
	}
	if len(invoices) == 0 {
		return say(lang, msgInvoicesNone), nil
	}
	var out strings.Builder
	out.WriteString(say(lang, msgInvoicesHead, b.money(invoices[0].BalanceMinor)))
	buttons := make([][]Button, 0, len(invoices))
	for _, invoice := range invoices {
		what := named(kindNames, invoice.Kind, lang)
		if invoice.InstanceName != "" {
			what += " " + html.EscapeString(invoice.InstanceName)
		}
		out.WriteString(say(lang, msgInvoiceLine, html.EscapeString(invoice.Number), what, b.money(invoice.AmountMinor)))
		buttons = append(buttons, []Button{InvoiceButton(say(lang, msgPayButton, invoice.Number), invoice.ID)})
	}
	return out.String(), buttons
}

// account answers the commands about the sender's own account, in a
// private chat only: what they show is nobody else's business.
func (b *Bot) account(ctx context.Context, client API, cfg settings.TelegramSettings, message *Message, name, lang string) {
	if message.Chat.Type != "private" {
		b.groupReply(ctx, client, cfg, message.MessageID, say(lang, msgPrivateOnly, cfg.BotUsername))
		return
	}
	link, err := b.store.LinkByTelegram(ctx, message.From.ID)
	if err != nil {
		_, _ = client.SendMessage(ctx, message.Chat.ID, b.notLinked(lang), 0)
		return
	}
	lang = b.settings.Current().Lang(firstNonEmpty(link.Locale, lang))
	var text string
	var buttons [][]Button
	switch name {
	case "services":
		text = b.services(ctx, link, lang)
	case "balance":
		text = b.balance(ctx, link, lang)
	case "invoices":
		text, buttons = b.invoices(ctx, link, lang)
	}
	_, _ = client.SendButtons(ctx, message.Chat.ID, text, buttons)
}

// callback handles a pressed button: showing what paying an invoice would
// do, paying it, or cancelling. Only the linked owner of an invoice gets
// anywhere.
func (b *Bot) callback(ctx context.Context, client API, query *CallbackQuery) {
	lang := b.langOf(ctx, query.From)
	if query.Message == nil || query.Message.Chat.Type != "private" {
		_ = client.AnswerCallback(ctx, query.ID, "")
		return
	}
	chat, message := query.Message.Chat.ID, query.Message.MessageID
	if query.Data == callbackCancel {
		_ = client.AnswerCallback(ctx, query.ID, "")
		_ = client.EditMessage(ctx, chat, message, say(lang, msgCancelled), nil)
		return
	}
	link, err := b.store.LinkByTelegram(ctx, query.From.ID)
	if err != nil {
		_ = client.AnswerCallback(ctx, query.ID, strip(b.notLinked(lang)))
		return
	}
	lang = b.settings.Current().Lang(firstNonEmpty(link.Locale, lang))
	site := strings.TrimRight(b.settings.Current().PublicURL, "/")
	invoiceID, pay := strings.CutPrefix(query.Data, callbackPay)
	if !pay {
		var show bool
		if invoiceID, show = strings.CutPrefix(query.Data, callbackInvoice); !show {
			_ = client.AnswerCallback(ctx, query.ID, "")
			return
		}
	}
	invoice, err := b.store.PayableInvoice(ctx, link.AccountID, invoiceID)
	if err != nil {
		_ = client.AnswerCallback(ctx, query.ID, say(lang, msgPaidGone))
		return
	}
	_ = client.AnswerCallback(ctx, query.ID, "")
	if invoice.BalanceMinor < invoice.AmountMinor {
		short := invoice.AmountMinor - invoice.BalanceMinor
		text := say(lang, msgShort, html.EscapeString(invoice.Number), b.money(invoice.AmountMinor), b.money(invoice.BalanceMinor), b.money(short))
		b.sendWithLinks(ctx, client, chat, text, [][]Button{{{Text: say(lang, msgTopup), URL: fmt.Sprintf("%s/portal/wallet?need=%d#topup", site, short)}}})
		return
	}
	if !pay {
		renews := ""
		if invoice.InstanceName != "" {
			until := ""
			if invoice.PeriodEnd != nil {
				until = say(lang, msgRenewUntil, day(*invoice.PeriodEnd))
			}
			renews = say(lang, msgConfirmRenew, html.EscapeString(invoice.InstanceName), until)
		}
		text := say(lang, msgConfirm, html.EscapeString(invoice.Number), renews, b.money(invoice.AmountMinor), b.money(invoice.BalanceMinor), b.money(invoice.BalanceMinor-invoice.AmountMinor))
		_, _ = client.SendButtons(ctx, chat, text, [][]Button{{
			{Text: say(lang, msgConfirmYes), Data: callbackPay + invoice.ID},
			{Text: say(lang, msgConfirmNo), Data: callbackCancel},
		}})
		return
	}
	_, err = b.billing.PayInvoiceWithBalance(ctx, link.AccountID, invoice.ID, link.UserID)
	switch {
	case errors.Is(err, postgres.ErrInvoiceUnavailable):
		_ = client.EditMessage(ctx, chat, message, say(lang, msgPaidGone), nil)
	case err != nil:
		b.logger.Error("telegram pay invoice", "invoice", invoice.Number, "error", err)
		_ = client.EditMessage(ctx, chat, message, say(lang, msgPayFailed), nil)
	default:
		_ = client.EditMessage(ctx, chat, message, say(lang, msgPaid, html.EscapeString(invoice.Number), b.money(invoice.AmountMinor), b.money(invoice.BalanceMinor-invoice.AmountMinor)), nil)
	}
}

// sendWithLinks sends text with URL buttons, and without them when
// Telegram refuses the address (it takes public http(s) addresses only).
func (b *Bot) sendWithLinks(ctx context.Context, client API, chat int64, text string, buttons [][]Button) {
	if _, err := client.SendButtons(ctx, chat, text, buttons); err != nil {
		_, _ = client.SendMessage(ctx, chat, text, 0)
	}
}

// strip removes the HTML from a message, for a pop-up that shows plain
// text.
func strip(text string) string {
	var out strings.Builder
	inTag := false
	for _, r := range text {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			out.WriteRune(r)
		}
	}
	return html.UnescapeString(out.String())
}
