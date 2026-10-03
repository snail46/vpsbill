package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
	"unicode"

	"vpsbill/internal/security"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// Tickets on Telegram: a customer opens one with /ticket in a private chat
// and answers by replying to the bot's message about it; a host answers
// the tickets of their instances the same way; staff who linked their
// Telegram account answer in the staff group by replying to the notice,
// or claim it with its button. A reply to a message that is about no
// ticket is refused, never guessed.

// TicketEvents hears about tickets opened or answered through the bot, so
// the other side is told as when it happens on the site.
type TicketEvents interface {
	TicketCreated(ctx context.Context, ticketID, messageID, body string)
	TicketReplied(ctx context.Context, ticketID, messageID, authorType, body string, internal bool)
}

// SetTicketEvents sets who hears about tickets made through the bot.
func (b *Bot) SetTicketEvents(events TicketEvents) { b.events = events }

// callbackClaim starts the data of a ticket's claim button.
const callbackClaim = "tk:"

// ClaimButton claims a ticket for the staff member who presses it.
func ClaimButton(text, ticketID string) Button {
	return Button{Text: text, Data: callbackClaim + ticketID}
}

var (
	msgTicketsOff = text{"在 Telegram 里提交工单的功能没有开启，请到网站提交：<a href=\"%s\">%s</a>", "Tickets cannot be opened on Telegram here; open one on the site: <a href=\"%s\">%s</a>"}
	msgTicketHelp = text{
		"发送 <code>/ticket 主题</code>，换行后写问题描述，就会创建一张工单，例如：\n<code>/ticket 无法连接 SSH\n实例 svc-xxx 从今天早上开始连不上……</code>\n可以把图片和 /ticket 放在同一条消息里（图片说明里写 /ticket）。之后直接回复机器人发来的工单消息即可继续沟通。",
		"Send <code>/ticket subject</code>, then the description on the next lines, to open a ticket, for example:\n<code>/ticket Cannot reach SSH\nInstance svc-xxx stopped answering this morning…</code>\nAn image can carry /ticket in its caption. Then reply to the bot's ticket messages to carry on.",
	}
	msgTicketCreated  = text{"✅ 已创建工单 <b>%s</b>：%s\n\n客服回复后会通知你。直接回复（引用）这条消息可以补充内容或图片。", "✅ Ticket <b>%s</b> opened: %s\n\nYou will be told when support answers. Reply to this message to add text or images."}
	msgTicketFailed   = text{"工单没有提交成功，请稍后再试或到网站提交。", "The ticket could not be saved. Try again later or use the site."}
	msgTicketReplied  = text{"✅ 已回复工单 <b>%s</b>。", "✅ Replied to ticket <b>%s</b>."}
	msgTicketClosed   = text{"工单 <b>%s</b> 已关闭，不能再回复。需要的话请新建工单。", "Ticket <b>%s</b> is closed. Open a new one if needed."}
	msgTicketNoRef    = text{"这条消息对应不到工单。要回复工单，请回复（引用）机器人发来的那条工单消息；要新建工单请发送 /ticket。", "That message belongs to no ticket. To answer a ticket, reply to the bot's message about it; to open one, send /ticket."}
	msgTicketNotYour  = text{"这张工单不属于你绑定的账号。", "This ticket does not belong to your linked account."}
	msgTicketHow      = text{"要回复工单，请回复（引用）机器人发来的工单消息；新建工单发送 /ticket 主题。发送 /help 查看全部命令。", "To answer a ticket, reply to the bot's message about it; to open one, send /ticket subject. Send /help for all commands."}
	msgStaffNotLinked = text{"你还没有把 Telegram 绑定到管理员账号：请在后台「安全中心 → Telegram」绑定。", "Your Telegram account is not linked to a staff account: link it under Security → Telegram in the admin console."}
	msgStaffNoRight   = text{"你的管理员角色没有这个权限。", "Your staff role does not allow this."}
	msgClaimed        = text{"👤 已由 %s 认领", "👤 Claimed by %s"}
	msgClaimDone      = text{"已认领", "Claimed"}
	msgAttachmentType = text{"只支持 PNG、JPEG、GIF、WebP 图片附件。", "Only PNG, JPEG, GIF and WebP images can be attached."}
	msgAttachmentSize = text{"图片超过 %d MB，没有提交。", "The image is larger than %d MB and was not sent."}
	msgStaffBound     = text{"✅ 已绑定管理员账号 <b>%s</b>。现在可以在管理群里回复和认领工单%s。", "✅ Linked to staff account <b>%s</b>. You can now answer and claim tickets in the staff group%s."}
	msgStaffBoundRP   = text{"，并在交流群里用 /redpacket 发红包", ", and hand out red packets in the group with /redpacket"}
)

// attachmentTypes are the images a ticket takes, as the site does.
var attachmentTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// attachments downloads the image a message carries, for a ticket; the
// second result is what to tell the sender when it cannot be taken.
func (b *Bot) attachments(ctx context.Context, client API, message *Message, lang string) ([]postgres.AttachmentUpload, string) {
	maxMB := b.settings.Current().TicketAttachmentMaxMB
	if maxMB < 1 {
		maxMB = 5
	}
	var fileID, name string
	switch {
	case len(message.Photo) > 0:
		// The last size is the largest.
		fileID, name = message.Photo[len(message.Photo)-1].FileID, fmt.Sprintf("telegram-%d.jpg", message.MessageID)
	case message.Document != nil:
		if !strings.HasPrefix(message.Document.MimeType, "image/") {
			return nil, say(lang, msgAttachmentType)
		}
		fileID, name = message.Document.FileID, message.Document.FileName
		if name == "" {
			name = fmt.Sprintf("telegram-%d", message.MessageID)
		}
	default:
		return nil, ""
	}
	data, err := client.Download(ctx, fileID, int64(maxMB)<<20)
	switch {
	case errors.Is(err, ErrFileTooLarge):
		return nil, say(lang, msgAttachmentSize, maxMB)
	case err != nil:
		b.logger.Warn("download a telegram attachment", "error", err)
		return nil, say(lang, msgTicketFailed)
	}
	contentType := http.DetectContentType(data)
	if !attachmentTypes[contentType] {
		return nil, say(lang, msgAttachmentType)
	}
	return []postgres.AttachmentUpload{{FileName: name, ContentType: contentType, Data: data}}, ""
}

// tell runs a ticket event in the background, as the site does.
func (b *Bot) tell(event func(ctx context.Context, events TicketEvents)) {
	if b.events == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		event(ctx, b.events)
	}()
}

// ticketSubject splits what follows /ticket into a subject (its first
// line) and a body (the rest, or the subject again).
func ticketSubject(content string) (string, string) {
	content = strings.TrimSpace(content)
	// Drop the command word, which a space or a new line ends.
	rest := ""
	if end := strings.IndexFunc(content, unicode.IsSpace); end >= 0 {
		rest = content[end:]
	}
	subject, body, _ := strings.Cut(strings.TrimSpace(rest), "\n")
	subject, body = strings.TrimSpace(subject), strings.TrimSpace(body)
	if runes := []rune(subject); len(runes) > 50 {
		subject = string(runes[:50])
	}
	if body == "" {
		body = subject
	}
	return subject, body
}

// newTicket opens a ticket from /ticket in a private chat.
func (b *Bot) newTicket(ctx context.Context, client API, cfg settings.TelegramSettings, message *Message, lang string) {
	site := b.settings.Current().PublicURL
	answer := func(text string) { _, _ = client.SendMessage(ctx, message.Chat.ID, text, message.MessageID) }
	if !cfg.Tickets {
		answer(say(lang, msgTicketsOff, site+"/portal/support", siteHost(site)))
		return
	}
	link, err := b.store.LinkByTelegram(ctx, message.From.ID)
	if err != nil {
		answer(b.notLinked(lang))
		return
	}
	lang = b.settings.Current().Lang(firstNonEmpty(link.Locale, lang))
	subject, body := ticketSubject(message.Content())
	if len(subject) < 3 {
		answer(say(lang, msgTicketHelp))
		return
	}
	uploads, problem := b.attachments(ctx, client, message, lang)
	if problem != "" {
		answer(problem)
		return
	}
	detail, err := b.ops.CreateTicket(ctx, link.AccountID, link.UserID, "", subject, "normal", body, "", "telegram", uploads...)
	if err != nil {
		b.logger.Error("open a ticket from telegram", "error", err)
		answer(say(lang, msgTicketFailed))
		return
	}
	sent, err := client.SendMessage(ctx, message.Chat.ID, say(lang, msgTicketCreated, html.EscapeString(detail.Ticket.Number), html.EscapeString(subject)), message.MessageID)
	if err == nil {
		_ = b.store.SaveMessageRef(ctx, message.Chat.ID, sent.MessageID, detail.Ticket.ID)
	}
	first := detail.Messages[0]
	b.tell(func(ctx context.Context, events TicketEvents) {
		events.TicketCreated(ctx, detail.Ticket.ID, first.ID, first.Body)
	})
}

// ticketReply answers a ticket with a reply to the bot's message about it,
// in a private chat (customer or host) or in the staff group (staff). It
// reports whether the message was taken as such.
func (b *Bot) ticketReply(ctx context.Context, client API, cfg settings.TelegramSettings, message *Message, lang string) bool {
	if message.ReplyTo == nil || message.ReplyTo.From == nil || !message.ReplyTo.From.IsBot || !cfg.Tickets {
		return false
	}
	private := message.Chat.Type == "private"
	staffChat := cfg.AdminChatID != 0 && message.Chat.ID == cfg.AdminChatID
	if !private && !staffChat {
		return false
	}
	answer := func(text string) (Message, error) {
		return client.SendMessage(ctx, message.Chat.ID, text, message.MessageID)
	}
	ref, err := b.store.TicketByMessage(ctx, message.Chat.ID, message.ReplyTo.MessageID)
	if errors.Is(err, postgres.ErrNoTicketRef) {
		// In the staff group people talk among themselves; only a
		// private chat is told.
		if private {
			_, _ = answer(say(lang, msgTicketNoRef))
		}
		return private
	}
	if err != nil {
		b.logger.Error("find the ticket of a telegram reply", "error", err)
		return true
	}
	var actor, actorID, scope string
	if staffChat {
		staff, err := b.store.StaffByTelegram(ctx, message.From.ID)
		if err != nil {
			_, _ = answer(say(lang, msgStaffNotLinked))
			return true
		}
		if !staff.Can("tickets:write") {
			_, _ = answer(say(lang, msgStaffNoRight))
			return true
		}
		actor, actorID = "staff", staff.UserID
	} else {
		link, err := b.store.LinkByTelegram(ctx, message.From.ID)
		if err != nil {
			_, _ = answer(b.notLinked(lang))
			return true
		}
		lang = b.settings.Current().Lang(firstNonEmpty(link.Locale, lang))
		switch link.AccountID {
		case ref.AccountID:
			actor, scope = "customer", link.AccountID
		case ref.HostAccountID:
			actor, scope = "host", link.AccountID
		default:
			_, _ = answer(say(lang, msgTicketNotYour))
			return true
		}
		actorID = link.UserID
	}
	uploads, problem := b.attachments(ctx, client, message, lang)
	if problem != "" {
		_, _ = answer(problem)
		return true
	}
	body := strings.TrimSpace(message.Content())
	if body == "" && len(uploads) == 0 {
		return true
	}
	reply, err := b.ops.ReplyTicket(ctx, ref.TicketID, scope, actor, actorID, body, false, uploads...)
	switch {
	case errors.Is(err, postgres.ErrTicketClosed):
		_, _ = answer(say(lang, msgTicketClosed, html.EscapeString(ref.Number)))
		return true
	case errors.Is(err, postgres.ErrTicketNotFound):
		_, _ = answer(say(lang, msgTicketNotYour))
		return true
	case err != nil:
		b.logger.Error("answer a ticket from telegram", "error", err)
		_, _ = answer(say(lang, msgTicketFailed))
		return true
	}
	if sent, err := answer(say(lang, msgTicketReplied, html.EscapeString(ref.Number))); err == nil {
		// Replying to the confirmation carries on in the same ticket.
		_ = b.store.SaveMessageRef(ctx, message.Chat.ID, sent.MessageID, ref.TicketID)
	}
	b.tell(func(ctx context.Context, events TicketEvents) {
		events.TicketReplied(ctx, ref.TicketID, reply.ID, actor, reply.Body, false)
	})
	return true
}

// claimTicket assigns the ticket of a staff group notice to the staff
// member who pressed its button.
func (b *Bot) claimTicket(ctx context.Context, client API, cfg settings.TelegramSettings, query *CallbackQuery, ticketID string) {
	lang := b.settings.Current().Lang("")
	if query.Message == nil || cfg.AdminChatID == 0 || query.Message.Chat.ID != cfg.AdminChatID {
		_ = client.AnswerCallback(ctx, query.ID, "")
		return
	}
	staff, err := b.store.StaffByTelegram(ctx, query.From.ID)
	if err != nil {
		_ = client.AnswerCallback(ctx, query.ID, strip(say(lang, msgStaffNotLinked)))
		return
	}
	if !staff.Can("tickets:write") {
		_ = client.AnswerCallback(ctx, query.ID, say(lang, msgStaffNoRight))
		return
	}
	ref, err := b.store.TicketRefByID(ctx, ticketID)
	if err == nil {
		err = b.store.ClaimTicket(ctx, ticketID, staff.UserID)
	}
	if err != nil {
		_ = client.AnswerCallback(ctx, query.ID, say(lang, msgTicketClosed, ref.Number))
		return
	}
	_ = client.AnswerCallback(ctx, query.ID, "")
	text := html.EscapeString(query.Message.Text) + "\n\n" + say(lang, msgClaimed, html.EscapeString(staff.Name))
	_ = client.EditMessage(ctx, query.Message.Chat.ID, query.Message.MessageID, text, nil)
}

// bindStaff links a staff member's Telegram account from /start staff_….
func (b *Bot) bindStaff(ctx context.Context, user User, code, lang string) string {
	link, err := b.store.BindStaff(ctx, security.HashToken(strings.TrimSpace(code)), postgres.TelegramUser{ID: user.ID, Username: user.Username, FirstName: user.FirstName})
	switch {
	case errors.Is(err, postgres.ErrTelegramCodeInvalid):
		return say(lang, msgBindInvalid)
	case errors.Is(err, postgres.ErrTelegramTaken):
		return say(lang, msgBindTaken)
	case err != nil:
		b.logger.Error("link a staff telegram account", "error", err)
		return say(lang, msgBindInvalid)
	}
	extra := ""
	if link.Can("billing:write") {
		extra = say(lang, msgStaffBoundRP)
	}
	return say(lang, msgStaffBound, html.EscapeString(link.Name), extra)
}
