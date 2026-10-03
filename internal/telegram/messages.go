package telegram

import (
	"fmt"
	"html"
	"strings"

	"vpsbill/internal/settings"
)

// text is one message in both languages.
type text struct{ zh, en string }

func (t text) in(lang string) string {
	if lang == "en" {
		return t.en
	}
	return t.zh
}

func say(lang string, t text, args ...any) string {
	return fmt.Sprintf(t.in(lang), args...)
}

var (
	msgDisabled = text{
		"活动暂未开启。",
		"Rewards are not open yet.",
	}
	msgBound = text{
		"✅ 绑定成功！Telegram 已绑定到站点账号 <b>%s</b>。",
		"✅ Linked! This Telegram account now belongs to site account <b>%s</b>.",
	}
	msgBoundBonus = text{
		"\n🎁 绑定奖励 <b>%s</b> 已存入余额，当前余额 %s。",
		"\n🎁 A link bonus of <b>%s</b> was added to your balance, which is now %s.",
	}
	msgBindInvalid = text{
		"绑定链接无效或已过期（10 分钟内有效）。请回到网站重新点击「绑定 Telegram」。",
		"This link is invalid or has expired (it works for 10 minutes). Go back to the site and choose “Link Telegram” again.",
	}
	msgBindTaken = text{
		"这个 Telegram 账号已经绑定了另一个站点账号。请先在那个账号的「账户资料」里解绑。",
		"This Telegram account is already linked to another site account. Unlink it there first, under Profile.",
	}
	msgBindAlready = text{
		"你的站点账号已经绑定过 Telegram 了。",
		"Your site account already has a Telegram account linked.",
	}
	msgBindUnverified = text{
		"请先在网站完成邮箱验证，再绑定 Telegram。",
		"Verify your email address on the site first, then link Telegram.",
	}
	msgNotLinked = text{
		"你还没有绑定站点账号。登录 <a href=\"%s\">%s</a>，在「账户资料」里点「绑定 Telegram」，绑定后就能签到领余额。",
		"You have not linked a site account yet. Sign in at <a href=\"%s\">%s</a>, open Profile and choose “Link Telegram”; then you can check in for balance.",
	}
	msgCheckinDone = text{
		"✅ %s 签到成功，<b>+%s</b>，余额 %s，已连续签到 %d 天。",
		"✅ %s checked in: <b>+%s</b>, balance %s, %d day(s) in a row.",
	}
	msgCheckinAlready = text{
		"%s 今天已经签到过了，明天再来。已连续签到 %d 天。",
		"%s, you already checked in today. Come back tomorrow — %d day(s) in a row so far.",
	}
	msgCheckinExhausted = text{
		"今天的签到奖励已经发完了，明天早点来。",
		"Today's check-in rewards are used up. Come earlier tomorrow.",
	}
	msgCheckinInGroup = text{
		"签到要在群里发送「签到」或 /checkin。%s",
		"Check in by sending /checkin in the group. %s",
	}
	msgInvite = text{
		"🔗 你的专属邀请链接：\n%s\n\n新成员通过这个链接进群%s，你将获得 <b>%s</b> 余额。\n已奖励 %d 人，待结算 %d 人。",
		"🔗 Your invite link:\n%s\n\nWhen a new member joins through it%s, you earn <b>%s</b> of balance.\nRewarded: %d, pending: %d.",
	}
	msgInviteHold   = text{"并留在群里满 %d 小时", " and stays for %d hours"}
	msgInviteLinked = text{
		"、绑定站点账号",
		", and links a site account",
	}
	msgInviteCap = text{
		"\n每人每天最多奖励 %d 次邀请，超出的顺延到次日。",
		"\nAt most %d invitations are rewarded per day; the rest are paid on the following days.",
	}
	msgInviteOff = text{
		"邀请奖励暂未开启。",
		"Invite rewards are not open yet.",
	}
	msgInviteUnavailable = text{
		"暂时无法生成邀请链接，请联系管理员（机器人需要有群的「邀请用户」管理权限）。",
		"The invite link cannot be made right now. Ask an administrator: the bot needs the “Invite users” right in the group.",
	}
	msgSentPrivately = text{
		"已私聊发给你了。",
		"Sent to you in a private message.",
	}
	msgStartBotFirst = text{
		"请先私聊 @%s 点「开始」，再发送这个命令。",
		"Open a private chat with @%s and press Start first, then send this command again.",
	}
	msgMe = text{
		"👤 站点账号：%s\n💰 余额：%s\n📅 累计签到 %d 天%s\n🤝 邀请已奖励 %d 人，待结算 %d 人\n🎁 累计获得奖励 %s",
		"👤 Site account: %s\n💰 Balance: %s\n📅 Checked in %d day(s)%s\n🤝 Invitations rewarded: %d, pending: %d\n🎁 Rewards earned: %s",
	}
	msgMeToday = text{"（今天已签到）", " (done today)"}
	msgHelp    = text{
		"<b>%s</b> 机器人\n\n• 在网站「账户资料」里绑定 Telegram%s\n• 在群里发送「签到」或 /checkin，每天领 %s 余额\n• /invite 获取专属邀请链接%s\n• /me 查看签到和奖励\n• 私聊发送 /services 查看 VPS、/balance 查看余额、/invoices 支付待付账单\n\n余额可用于在 <a href=\"%s\">%s</a> 购买和续费，不能提现。",
		"<b>%s</b> bot\n\n• Link Telegram on the site under Profile%s\n• Send /checkin in the group every day for %s of balance\n• /invite gives you your own invite link%s\n• /me shows your check-ins and rewards\n• In a private chat: /services lists your VPS, /balance shows your balance, /invoices pays unpaid invoices\n\nBalance pays for purchases and renewals at <a href=\"%s\">%s</a>; it cannot be withdrawn.",
	}
	msgHelpBind   = text{"，奖励 %s", " and get %s"}
	msgHelpRank   = text{"\n• /rank 查看本周签到榜和邀请榜", "\n• /rank shows this week's check-in and invite boards"}
	msgHelpTicket = text{"\n• 私聊发送 /ticket 主题 提交工单；直接回复机器人发来的工单消息即可回复", "\n• In a private chat, /ticket subject opens a ticket; reply to the bot's ticket messages to answer"}
	msgHelpInvite = text{"，每邀请一人奖励 %s", ", %s per member invited"}
	msgWelcome    = text{
		"👋 欢迎 %s！在 <a href=\"%s\">%s</a> 绑定 Telegram 后，每天在群里发「签到」可领余额，/invite 邀请好友还有奖励。",
		"👋 Welcome, %s! Link Telegram at <a href=\"%s\">%s</a>, then send /checkin here every day for balance; /invite earns more for bringing friends.",
	}
	msgInviteRewarded = text{
		"🎉 你邀请的 %s 已满足条件，奖励 <b>%s</b> 已存入余额。",
		"🎉 %s, whom you invited, now counts: <b>%s</b> was added to your balance.",
	}
	msgGroupLink       = text{"群：%s", "Group: %s"}
	msgPeriodBoth      = text{"⏰ 活动时间：%s 至 %s（北京时间）。", "⏰ Rewards run from %s to %s (UTC+8)."}
	msgPeriodFrom      = text{"⏰ 活动自 %s 开始（北京时间）。", "⏰ Rewards start at %s (UTC+8)."}
	msgPeriodUntil     = text{"⏰ 活动截止 %s（北京时间）。", "⏰ Rewards end at %s (UTC+8)."}
	msgRewardsUpcoming = text{
		"活动还没有开始，暂时不发放奖励。%s",
		"Rewards have not started yet; nothing is paid for now. %s",
	}
	msgRewardsEnded = text{
		"活动已经结束，不再发放奖励。%s",
		"Rewards have ended; nothing is paid any more. %s",
	}
	msgHelpRebate = text{
		"\n• 你邀请的成员首次充值或在线付款后，你再得该笔金额 %d%% 的返利",
		"\n• When a member you invited first tops up or pays online, you get %d%% of that payment",
	}
	msgRebatePaid = text{
		"🎉 你邀请的 %s 完成了首次付款，返利 <b>%s</b> 已存入余额。",
		"🎉 %s, whom you invited, made a first payment: a rebate of <b>%s</b> was added to your balance.",
	}
)

// amountRange prints the check-in reward: one amount, or the two ends.
func amountRange(cfg settings.TelegramSettings, money func(int64) string) string {
	if cfg.CheckinMinMinor == cfg.CheckinMaxMinor {
		return money(cfg.CheckinMinMinor)
	}
	return money(cfg.CheckinMinMinor) + "–" + money(cfg.CheckinMaxMinor)
}

// mention is a member's name as text safe to put into a message.
func mention(user User) string {
	name := strings.TrimSpace(user.FirstName)
	if name == "" {
		name = user.Username
	}
	if name == "" {
		name = fmt.Sprint(user.ID)
	}
	return html.EscapeString(name)
}

// maskEmail hides most of an address, for messages others may read.
func maskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return html.EscapeString(email)
	}
	local := []rune(email[:at])
	keep := 2
	if len(local) <= 2 {
		keep = 1
	}
	return html.EscapeString(string(local[:keep]) + "***" + email[at:])
}

// siteHost is the site's address without the scheme, as a link label.
func siteHost(publicURL string) string {
	host := strings.TrimPrefix(strings.TrimPrefix(publicURL, "https://"), "http://")
	return html.EscapeString(strings.TrimRight(host, "/"))
}
