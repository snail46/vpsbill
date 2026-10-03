package telegram

import (
	"context"
	"errors"
	"html"
	"math"
	"strconv"
	"strings"
	"time"

	"vpsbill/internal/clock"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// Red packets: staff hand out a total split at random among the first
// members who grab it, with the button under the packet or, for a
// password packet, by sending its password in the group. Members need a
// linked site account; a packet may also ask for one that paid for
// something, or one linked for some days. Shares count towards the day's
// reward budget but are not held back by it.

const callbackRedPacket = "rp:"

// RedPacketTTL is how long a packet sent from the group lasts.
const RedPacketTTL = 24 * time.Hour

var (
	msgRedPacket = text{
		"🧧 <b>%s</b> 发了一个红包：共 <b>%s</b>，%d 个\n%s%s\n已领 %d/%d，%s 前有效",
		"🧧 <b>%s</b> sent a red packet: <b>%s</b> in %d shares\n%s%s\n%d/%d taken, valid until %s",
	}
	msgRedPacketButton   = text{"点下面的按钮领取。", "Press the button below to take a share."}
	msgRedPacketPassword = text{"在群里发送口令「<b>%s</b>」领取。", "Send the password “<b>%s</b>” in the group to take a share."}
	msgRedPacketSpent    = text{"仅限消费过的账号。", "Only for accounts that paid for something."}
	msgRedPacketDays     = text{"仅限绑定满 %d 天的账号。", "Only for accounts linked at least %d days."}
	msgRedPacketGrab     = text{"🧧 抢红包", "🧧 Take a share"}
	msgRedPacketDone     = text{"🧧 红包 %s（%d 个）已被领完。手气最佳：%s（%s）", "🧧 The red packet of %s (%d shares) is all taken. Luckiest: %s (%s)"}
	msgRedPacketOver     = text{"🧧 红包已结束：领取了 %d/%d 个，未领取的 %s 不再发放。", "🧧 The red packet is over: %d/%d shares were taken; the %s left is not paid."}
	msgRedPacketGot      = text{"领到 %s，已存入余额（当前 %s）。", "You got %s, added to your balance (now %s)."}
	msgRedPacketGotGroup = text{"🧧 %s 领到红包 %s", "🧧 %s got %s from the red packet"}
	msgRedPacketAlready  = text{"你已经领过这个红包了。", "You took a share of this one already."}
	msgRedPacketEmpty    = text{"红包已经被领完了。", "The red packet is all taken."}
	msgRedPacketInactive = text{"红包已经结束了。", "The red packet is over."}
	msgRedPacketNotSpent = text{"这个红包仅限消费过的账号领取。", "This red packet is only for accounts that paid for something."}
	msgRedPacketTooNew   = text{"这个红包仅限绑定满 %d 天的账号领取。", "This red packet is only for accounts linked at least %d days."}
	msgRedPacketNeedLink = text{"领红包需要先绑定站点账号：%s", "Link a site account to take red packets: %s"}
	msgRedPacketUsage    = text{
		"用法：<code>/redpacket 总金额 个数</code>，例如 <code>/redpacket 10 5</code>；加上口令就是口令红包：<code>/redpacket 10 5 新年快乐</code>。金额按 %s 计，每个至少 0.01，最多 %d 个。",
		"Usage: <code>/redpacket total shares</code>, e.g. <code>/redpacket 10 5</code>; add a password for a password packet: <code>/redpacket 10 5 happy new year</code>. Amounts are in %s, at least 0.01 a share, %d shares at most.",
	}
	msgRedPacketStaff   = text{"只有绑定了 Telegram 且有财务权限的管理员可以发红包。", "Only staff with a linked Telegram account and billing rights can send red packets."}
	msgRedPacketInvalid = text{"红包金额或个数无效：每个至少 0.01，最多 %d 个，总额不超过 %s。", "Invalid amount or count: at least 0.01 a share, %d shares at most, %s in all at most."}
	msgRedPacketTaken   = text{"已有一个进行中的红包用了这个口令，请换一个。", "An active red packet already uses this password; choose another."}
	msgRedPacketFailed  = text{"红包没有发出，请稍后再试。", "The red packet could not be sent. Try again later."}
)

// redPacketText describes a packet in its current state.
func (b *Bot) redPacketText(packet postgres.RedPacket, lang string) string {
	switch packet.Status {
	case "finished":
		return say(lang, msgRedPacketDone, b.money(packet.TotalMinor), packet.Count, html.EscapeString(packet.BestName), b.money(packet.BestMinor))
	case "expired", "cancelled":
		return say(lang, msgRedPacketOver, packet.Count-packet.RemainingCount, packet.Count, b.money(packet.RemainingMinor))
	}
	how := say(lang, msgRedPacketButton)
	if packet.Password != "" {
		how = say(lang, msgRedPacketPassword, html.EscapeString(packet.Password))
	}
	conditions := ""
	if packet.RequireSpent {
		conditions += "\n" + say(lang, msgRedPacketSpent)
	}
	if packet.MinLinkedDays > 0 {
		conditions += "\n" + say(lang, msgRedPacketDays, packet.MinLinkedDays)
	}
	sender := packet.CreatedBy
	if sender == "" {
		sender = b.settings.Current().AppName
	}
	return say(lang, msgRedPacket, html.EscapeString(sender), b.money(packet.TotalMinor), packet.Count, how, conditions,
		packet.Count-packet.RemainingCount, packet.Count, packet.ExpiresAt.In(clock.Zone).Format("01-02 15:04"))
}

func (b *Bot) redPacketButtons(packet postgres.RedPacket, lang string) [][]Button {
	if packet.Status != "active" || packet.Password != "" {
		return nil
	}
	return [][]Button{{{Text: say(lang, msgRedPacketGrab), Data: callbackRedPacket + packet.ID}}}
}

// refreshRedPacket shows a packet's current state where it was posted.
func (b *Bot) refreshRedPacket(ctx context.Context, client API, packet postgres.RedPacket) {
	if packet.ChatID == 0 || packet.MessageID == 0 {
		return
	}
	lang := b.settings.Current().Lang("")
	_ = client.EditMessage(ctx, packet.ChatID, packet.MessageID, b.redPacketText(packet, lang), b.redPacketButtons(packet, lang))
}

// CreateRedPacket stores a packet and posts it in the group. A password
// packet is posted only when post is set: its password may be meant for
// an announcement elsewhere.
func (b *Bot) CreateRedPacket(ctx context.Context, input postgres.RedPacketInput, post bool) (postgres.RedPacket, error) {
	cfg := b.settings.Current().TelegramLedger()
	if !cfg.Ready() {
		return postgres.RedPacket{}, ErrNotReady
	}
	return b.createRedPacket(ctx, b.client(cfg), cfg, input, post || strings.TrimSpace(input.Password) == "")
}

func (b *Bot) createRedPacket(ctx context.Context, client API, cfg settings.TelegramSettings, input postgres.RedPacketInput, post bool) (postgres.RedPacket, error) {
	packet, err := b.store.CreateRedPacket(ctx, input)
	if err != nil {
		return packet, err
	}
	if post {
		lang := b.settings.Current().Lang("")
		sent, err := client.SendButtons(ctx, cfg.ChatID, b.redPacketText(packet, lang), b.redPacketButtons(packet, lang))
		if err != nil {
			// Nothing was taken; the packet goes away with the post.
			_, _ = b.store.CancelRedPacket(ctx, packet.ID, input.CreatedBy)
			return postgres.RedPacket{}, err
		}
		_ = b.store.SetRedPacketMessage(ctx, packet.ID, cfg.ChatID, sent.MessageID)
		packet.ChatID, packet.MessageID = cfg.ChatID, sent.MessageID
	}
	b.loadPasswords(ctx)
	return packet, nil
}

// CancelRedPacket stops a packet and updates its post.
func (b *Bot) CancelRedPacket(ctx context.Context, id, actorID string) (postgres.RedPacket, error) {
	packet, err := b.store.CancelRedPacket(ctx, id, actorID)
	if err != nil {
		return packet, err
	}
	if cfg := b.settings.Current().TelegramLedger(); cfg.CanSend() {
		b.refreshRedPacket(ctx, b.client(cfg), packet)
	}
	b.loadPasswords(ctx)
	return packet, nil
}

// loadPasswords reads the passwords of the active packets.
func (b *Bot) loadPasswords(ctx context.Context) {
	passwords, err := b.store.ActivePasswords(ctx, b.now())
	if err != nil {
		b.logger.Warn("read red packet passwords", "error", err)
		return
	}
	b.mu.Lock()
	b.passwords = passwords
	b.mu.Unlock()
}

func (b *Bot) passwordPacket(text string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.passwords[strings.ToLower(strings.TrimSpace(text))]
}

// redPacketCommand sends a packet from /redpacket in the group.
func (b *Bot) redPacketCommand(ctx context.Context, client API, cfg settings.TelegramSettings, message *Message, argument, lang string) {
	site := b.settings.Current()
	staff, err := b.store.StaffByTelegram(ctx, message.From.ID)
	if err != nil || !staff.Can("billing:write") {
		b.groupReply(ctx, client, cfg, message.MessageID, say(lang, msgRedPacketStaff))
		return
	}
	fields := strings.Fields(argument)
	currency := site.Locale.Currency()
	if len(fields) < 2 {
		b.groupReply(ctx, client, cfg, message.MessageID, say(lang, msgRedPacketUsage, currency, postgres.MaxRedPacketCount))
		return
	}
	amount, amountErr := strconv.ParseFloat(fields[0], 64)
	count, countErr := strconv.Atoi(fields[1])
	if amountErr != nil || countErr != nil || amount <= 0 || math.IsInf(amount, 0) || amount > postgres.MaxRedPacketMinor {
		b.groupReply(ctx, client, cfg, message.MessageID, say(lang, msgRedPacketUsage, currency, postgres.MaxRedPacketCount))
		return
	}
	// Typed in the site's currency, paid in the ledger's.
	total := site.Locale.Convert(int64(math.Round(amount*100)), currency, site.Locale.Ledger())
	input := postgres.RedPacketInput{
		CreatedBy: staff.UserID, TotalMinor: total, Count: count, Password: strings.Join(fields[2:], " "),
		RequireSpent: cfg.RedPacketRequireSpent, MinLinkedDays: cfg.RedPacketMinLinkedDays, ExpiresAt: b.now().Add(RedPacketTTL),
	}
	_, err = b.createRedPacket(ctx, client, cfg, input, true)
	switch {
	case errors.Is(err, postgres.ErrRedPacketInvalid):
		b.groupReply(ctx, client, cfg, message.MessageID, say(lang, msgRedPacketInvalid, postgres.MaxRedPacketCount, b.money(postgres.MaxRedPacketMinor)))
	case errors.Is(err, postgres.ErrRedPacketPassword):
		b.groupReply(ctx, client, cfg, message.MessageID, say(lang, msgRedPacketTaken))
	case err != nil:
		b.logger.Error("send a red packet", "error", err)
		b.groupReply(ctx, client, cfg, message.MessageID, say(lang, msgRedPacketFailed))
	}
}

// claimRedPacket gives the member a share and says how it went: the
// first result is for the member alone, the second for everyone (empty
// when nothing was paid).
func (b *Bot) claimRedPacket(ctx context.Context, client API, cfg settings.TelegramSettings, packetID string, user User) (string, string) {
	lang := b.langOf(ctx, user)
	link, err := b.store.LinkByTelegram(ctx, user.ID)
	if err != nil {
		return say(lang, msgRedPacketNeedLink, b.settings.Current().PublicURL+"/portal/profile"), ""
	}
	lang = b.settings.Current().Lang(firstNonEmpty(link.Locale, lang))
	claim, err := b.store.ClaimRedPacket(ctx, packetID, link, b.budget(cfg), b.between)
	if err != nil {
		b.logger.Error("take a red packet share", "error", err)
		return say(lang, msgRedPacketFailed), ""
	}
	switch claim.Refused {
	case postgres.RedPacketAlready:
		return say(lang, msgRedPacketAlready), ""
	case postgres.RedPacketEmpty:
		return say(lang, msgRedPacketEmpty), ""
	case postgres.RedPacketNotActive:
		return say(lang, msgRedPacketInactive), ""
	case postgres.RedPacketNotSpent:
		return say(lang, msgRedPacketNotSpent), ""
	case postgres.RedPacketTooNew:
		return say(lang, msgRedPacketTooNew, claim.Packet.MinLinkedDays), ""
	}
	b.refreshRedPacket(ctx, client, claim.Packet)
	if claim.Packet.Status != "active" {
		b.loadPasswords(ctx)
	}
	site := b.settings.Current().Lang("")
	return say(lang, msgRedPacketGot, b.money(claim.AmountMinor), b.money(claim.BalanceMinor)),
		say(site, msgRedPacketGotGroup, mention(user), b.money(claim.AmountMinor))
}

// redPacketCallback is a press of a packet's button.
func (b *Bot) redPacketCallback(ctx context.Context, client API, cfg settings.TelegramSettings, query *CallbackQuery, packetID string) {
	mine, _ := b.claimRedPacket(ctx, client, cfg, packetID, query.From)
	_ = client.AnswerCallback(ctx, query.ID, strip(mine))
}

// redPacketPassword takes a share for a member who sent a packet's
// password; it reports whether the text was one.
func (b *Bot) redPacketPassword(ctx context.Context, client API, cfg settings.TelegramSettings, message *Message) bool {
	packetID := b.passwordPacket(message.Content())
	if packetID == "" {
		return false
	}
	mine, everyone := b.claimRedPacket(ctx, client, cfg, packetID, *message.From)
	if message.Chat.Type == "private" {
		_, _ = client.SendMessage(ctx, message.Chat.ID, mine, message.MessageID)
		return true
	}
	if everyone != "" {
		mine = everyone
	}
	b.groupReply(ctx, client, cfg, message.MessageID, mine)
	return true
}

// expireRedPackets closes the packets whose time ran out.
func (b *Bot) expireRedPackets(ctx context.Context, client API) {
	expired, err := b.store.ExpireRedPackets(ctx, b.now())
	if err != nil {
		b.logger.Error("expire red packets", "error", err)
		return
	}
	for _, packet := range expired {
		b.refreshRedPacket(ctx, client, packet)
	}
}
