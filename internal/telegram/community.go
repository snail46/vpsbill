package telegram

import (
	"context"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vpsbill/internal/clock"
	"vpsbill/internal/settings"
	"vpsbill/internal/store/postgres"
)

// The community side of the bot: streak bonuses, weekly leaderboards,
// holding new members until they pass a check, and keeping links and
// blocked words out of the group.

var (
	msgStreakBonus  = text{"\n🔥 连续签到 %d 天，额外奖励 <b>%s</b>！", "\n🔥 %d days in a row: a bonus of <b>%s</b>!"}
	msgStreakRules  = text{"\n🔥 连续签到奖励：%s", "\n🔥 Streak bonuses: %s"}
	msgStreakRule   = text{"满 %d 天 %s", "%d days %s"}
	msgBoardTitle   = text{"🏆 <b>%s</b>（%s – %s）", "🏆 <b>%s</b> (%s – %s)"}
	msgBoardLine    = text{"\n%d. %s — %d%s", "\n%d. %s — %d%s"}
	msgBoardPrize   = text{"，奖励 %s", ", prize %s"}
	msgBoardEmpty   = text{"\n还没有人上榜。", "\nNobody is on it yet."}
	msgBoardWon     = text{"🏆 恭喜！你在上周%s排第 %d 名，奖励 <b>%s</b> 已存入余额。", "🏆 Congratulations! You came %[2]d on last week's %[1]s; a prize of <b>%[3]s</b> was added to your balance."}
	msgBoardThisWk  = text{"本周", "This week"}
	msgBoardLastWk  = text{"上周", "Last week"}
	msgBoardRules   = text{"\n🏆 每周一公布上周签到榜和邀请榜，前 %d 名有奖：%s", "\n🏆 Every Monday the check-in and invite boards of the week before are posted; the top %d win: %s"}
	msgVerifyButton = text{
		"👋 欢迎 %s！请在 %d 分钟内点击下面的按钮完成验证，验证前暂时不能发言。",
		"👋 Welcome, %s! Press the button below within %d minutes to be let in; until then you cannot write.",
	}
	msgVerifyLink = text{
		"👋 欢迎 %s！本群仅限绑定了站点账号的成员发言：请在 %d 分钟内到 <a href=\"%s\">%s</a> 绑定 Telegram，绑定后自动解除限制（或点「我已绑定」）。",
		"👋 Welcome, %s! Only members with a linked site account may write here: within %d minutes, link Telegram at <a href=\"%s\">%s</a>; you are let in once linked (or press “I have linked”).",
	}
	msgVerifyHuman     = text{"✅ 我不是机器人", "✅ I am human"}
	msgVerifyBind      = text{"绑定站点账号", "Link a site account"}
	msgVerifyLinked    = text{"✅ 我已绑定", "✅ I have linked"}
	msgVerifyNotYou    = text{"这不是给你的验证。", "This check is not for you."}
	msgVerifyNotLinked = text{"还没有绑定站点账号，请先在网站上绑定 Telegram。", "No site account is linked yet; link Telegram on the site first."}
	msgVerifyPassed    = text{"验证通过，欢迎！", "Verified, welcome!"}
	msgFilteredLink    = text{"%s 未绑定站点账号的成员不能发链接，消息已删除。", "%s Members without a linked site account cannot post links; the message was removed."}
	msgFilteredWord    = text{"%s 消息包含屏蔽词，已删除。", "%s The message contained a blocked word and was removed."}
	msgFilteredRate    = text{"%s 发言太快了，请稍后再发（绑定站点账号后不受限制）。", "%s Slow down, please (members with a linked site account have no limit)."}
)

// streakRules are the settings' streak bonuses for the store.
func streakRules(cfg settings.TelegramSettings) []postgres.StreakRule {
	rules := make([]postgres.StreakRule, 0, len(cfg.StreakBonuses))
	for _, bonus := range cfg.StreakBonuses {
		rules = append(rules, postgres.StreakRule{Days: bonus.Days, AmountMinor: bonus.AmountMinor})
	}
	return rules
}

// streakHelp lists the streak bonuses, or "" without any.
func (b *Bot) streakHelp(cfg settings.TelegramSettings, lang string) string {
	var parts []string
	for _, bonus := range cfg.StreakBonuses {
		if bonus.AmountMinor > 0 {
			parts = append(parts, say(lang, msgStreakRule, bonus.Days, b.money(bonus.AmountMinor)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return say(lang, msgStreakRules, strings.Join(parts, lang2sep(lang)))
}

func lang2sep(lang string) string {
	if lang == "en" {
		return ", "
	}
	return "、"
}

// boardHelp describes the leaderboards, or "" while they are off.
func (b *Bot) boardHelp(cfg settings.TelegramSettings, lang string) string {
	if !cfg.Leaderboards {
		return ""
	}
	var prizes []string
	for i, prize := range cfg.LeaderboardPrizes {
		if prize > 0 {
			prizes = append(prizes, strconv.Itoa(i+1)+". "+b.money(prize))
		}
	}
	if len(prizes) == 0 {
		return ""
	}
	return say(lang, msgBoardRules, len(prizes), strings.Join(prizes, lang2sep(lang)))
}

var boardNames = map[string]text{postgres.BoardCheckins: {"签到榜", "check-in board"}, postgres.BoardInvites: {"邀请榜", "invite board"}}

// boardText is a leaderboard as a message.
func (b *Bot) boardText(board string, start time.Time, entries []postgres.BoardEntry, lang, when string) string {
	end := start.AddDate(0, 0, 6)
	var out strings.Builder
	out.WriteString(say(lang, msgBoardTitle, when+boardNames[board].in(lang), start.Format("01-02"), end.Format("01-02")))
	if len(entries) == 0 {
		out.WriteString(say(lang, msgBoardEmpty))
	}
	for _, entry := range entries {
		prize := ""
		if entry.PrizeMinor > 0 {
			prize = say(lang, msgBoardPrize, b.money(entry.PrizeMinor))
		}
		out.WriteString(say(lang, msgBoardLine, entry.Rank, html.EscapeString(entry.Name), entry.Count, prize))
	}
	return out.String()
}

// rank shows this week's boards so far.
func (b *Bot) rank(ctx context.Context, cfg settings.TelegramSettings, lang string) string {
	start := postgres.WeekStart(b.now())
	var parts []string
	for _, board := range []string{postgres.BoardCheckins, postgres.BoardInvites} {
		if board == postgres.BoardInvites && cfg.InviteRewardMinor <= 0 {
			continue
		}
		entries, err := b.store.WeeklyBoard(ctx, board, start, 10)
		if err != nil {
			b.logger.Error("read a telegram leaderboard", "error", err)
			continue
		}
		parts = append(parts, b.boardText(board, start, entries, lang, say(lang, msgBoardThisWk)))
	}
	return strings.Join(parts, "\n\n") + b.boardHelp(cfg, lang)
}

// boardHour is when, on Monday (UTC+8), last week's boards are posted.
const boardHour = 10

// postLeaderboards posts and pays last week's boards once, on Monday.
func (b *Bot) postLeaderboards(ctx context.Context, client API, cfg settings.TelegramSettings) {
	now := b.now().In(clock.Zone)
	if !cfg.Leaderboards || now.Weekday() != time.Monday || now.Hour() < boardHour {
		return
	}
	last := postgres.WeekStart(now).AddDate(0, 0, -7)
	lang := b.settings.Current().Lang("")
	for _, board := range []string{postgres.BoardCheckins, postgres.BoardInvites} {
		entries, err := b.store.WeeklyBoard(ctx, board, last, 10)
		if err != nil {
			b.logger.Error("read a telegram leaderboard", "error", err)
			continue
		}
		awarded, fresh, err := b.store.AwardBoard(ctx, board, last, entries, cfg.LeaderboardPrizes, b.budget(cfg))
		if err != nil {
			b.logger.Error("award a telegram leaderboard", "error", err)
			continue
		}
		if !fresh || len(awarded) == 0 {
			continue
		}
		if _, err := client.SendMessage(ctx, cfg.ChatID, b.boardText(board, last, awarded, lang, say(lang, msgBoardLastWk)), 0); err != nil {
			b.logger.Warn("post a telegram leaderboard", "error", err)
		}
		for _, entry := range awarded {
			if entry.PrizeMinor <= 0 || entry.TelegramID == 0 {
				continue
			}
			winner := lang
			if link, err := b.store.LinkByAccount(ctx, entry.AccountID); err == nil {
				winner = b.settings.Current().Lang(link.Locale)
			}
			_, _ = client.SendMessage(ctx, entry.TelegramID, say(winner, msgBoardWon, boardNames[board].in(winner), entry.Rank, b.money(entry.PrizeMinor)), 0)
		}
	}
}

// ---- Joining ----

const callbackVerify = "vf:"

// holdNewMember keeps a new member silent until they pass the group's
// check; it reports whether it did. Members with a linked site account,
// and staff, are let in.
func (b *Bot) holdNewMember(ctx context.Context, client API, cfg settings.TelegramSettings, user User) bool {
	if cfg.Verify == "" {
		return false
	}
	if _, err := b.store.LinkByTelegram(ctx, user.ID); err == nil {
		return false
	}
	if _, err := b.store.StaffByTelegram(ctx, user.ID); err == nil {
		return false
	}
	if err := client.RestrictMember(ctx, cfg.ChatID, user.ID, false); err != nil {
		b.logger.Warn("hold a new telegram member", "error", err)
		return false
	}
	lang := b.langOf(ctx, user)
	minutes := max(cfg.VerifyMinutes, 1)
	data := callbackVerify + strconv.FormatInt(user.ID, 10)
	var text string
	var buttons [][]Button
	if cfg.Verify == settings.VerifyLink {
		site := b.settings.Current().PublicURL
		text = say(lang, msgVerifyLink, mention(user), minutes, site+"/portal/profile", siteHost(site))
		buttons = [][]Button{{{Text: say(lang, msgVerifyBind), URL: site + "/portal/profile"}}, {{Text: say(lang, msgVerifyLinked), Data: data}}}
	} else {
		text = say(lang, msgVerifyButton, mention(user), minutes)
		buttons = [][]Button{{{Text: say(lang, msgVerifyHuman), Data: data}}}
	}
	sent, err := client.SendButtons(ctx, cfg.ChatID, text, buttons)
	if err != nil && cfg.Verify == settings.VerifyLink {
		// Telegram refuses a site address it does not like; the text has
		// the link.
		sent, err = client.SendButtons(ctx, cfg.ChatID, text, buttons[1:])
	}
	if err != nil {
		b.logger.Warn("ask a new telegram member to verify", "error", err)
	}
	if err := b.store.AddVerification(ctx, postgres.Verification{TelegramID: user.ID, ChatID: cfg.ChatID, MessageID: sent.MessageID, Deadline: b.now().Add(time.Duration(minutes) * time.Minute)}); err != nil {
		b.logger.Error("record a telegram verification", "error", err)
	}
	return true
}

// letIn lifts a member's hold, if they had one.
func (b *Bot) letIn(ctx context.Context, client API, telegramID int64) bool {
	held, ok, err := b.store.TakeVerification(ctx, telegramID)
	if err != nil || !ok {
		return false
	}
	if err := client.RestrictMember(ctx, held.ChatID, telegramID, true); err != nil {
		b.logger.Warn("let a telegram member in", "error", err)
	}
	if held.MessageID != 0 {
		_ = client.DeleteMessage(ctx, held.ChatID, held.MessageID)
	}
	return true
}

// verifyCallback is a press of the check's button.
func (b *Bot) verifyCallback(ctx context.Context, client API, cfg settings.TelegramSettings, query *CallbackQuery, who string) {
	lang := b.langOf(ctx, query.From)
	if who != strconv.FormatInt(query.From.ID, 10) {
		_ = client.AnswerCallback(ctx, query.ID, say(lang, msgVerifyNotYou))
		return
	}
	if cfg.Verify == settings.VerifyLink {
		if _, err := b.store.LinkByTelegram(ctx, query.From.ID); err != nil {
			_ = client.AnswerCallback(ctx, query.ID, say(lang, msgVerifyNotLinked))
			return
		}
	}
	b.letIn(ctx, client, query.From.ID)
	_ = client.AnswerCallback(ctx, query.ID, say(lang, msgVerifyPassed))
}

// removeUnverified removes the members whose time to pass ran out.
func (b *Bot) removeUnverified(ctx context.Context, client API) {
	overdue, err := b.store.OverdueVerifications(ctx, b.now())
	if err != nil {
		b.logger.Error("read overdue telegram verifications", "error", err)
		return
	}
	for _, held := range overdue {
		if err := client.KickMember(ctx, held.ChatID, held.TelegramID); err != nil {
			b.logger.Warn("remove an unverified telegram member", "error", err)
		}
		if held.MessageID != 0 {
			_ = client.DeleteMessage(ctx, held.ChatID, held.MessageID)
		}
	}
}

// ---- Keeping the group clean ----

var linkPattern = regexp.MustCompile(`(?i)(https?://|www\.|t\.me/|telegram\.me/)`)

func hasLink(message *Message) bool {
	for _, entity := range append(append([]Entity(nil), message.Entities...), message.CaptionEntities...) {
		if entity.Type == "url" || entity.Type == "text_link" {
			return true
		}
	}
	return linkPattern.MatchString(message.Content())
}

// adminsFor is how long the list of group administrators is trusted.
const adminsFor = 10 * time.Minute

// isGroupAdmin reports whether a user administers the group.
func (b *Bot) isGroupAdmin(ctx context.Context, client API, cfg settings.TelegramSettings, userID int64) bool {
	b.mu.Lock()
	fresh := b.now().Sub(b.adminsAt) < adminsFor
	_, admin := b.admins[userID]
	b.mu.Unlock()
	if fresh {
		return admin
	}
	members, err := client.ChatAdministrators(ctx, cfg.ChatID)
	if err != nil {
		return admin
	}
	admins := map[int64]struct{}{}
	for _, member := range members {
		admins[member.User.ID] = struct{}{}
	}
	b.mu.Lock()
	b.admins, b.adminsAt = admins, b.now()
	b.mu.Unlock()
	_, admin = admins[userID]
	return admin
}

// tooFast counts a message of an unlinked member and reports whether it
// is over the limit of the last minute.
func (b *Bot) tooFast(userID int64, limit int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	recent := b.rates[userID][:0]
	for _, at := range b.rates[userID] {
		if now.Sub(at) < time.Minute {
			recent = append(recent, at)
		}
	}
	recent = append(recent, now)
	b.rates[userID] = recent
	return len(recent) > limit
}

// moderate removes a group message that breaks the rules and reports
// whether it did. Group administrators and linked staff are exempt.
func (b *Bot) moderate(ctx context.Context, client API, cfg settings.TelegramSettings, message *Message) bool {
	if !cfg.FilterLinks && len(cfg.BlockedWords) == 0 && cfg.UnlinkedPerMinute <= 0 {
		return false
	}
	user := *message.From
	if b.isGroupAdmin(ctx, client, cfg, user.ID) {
		return false
	}
	if _, err := b.store.StaffByTelegram(ctx, user.ID); err == nil {
		return false
	}
	lang := b.settings.Current().Lang("")
	remove := func(why text) bool {
		if err := client.DeleteMessage(ctx, message.Chat.ID, message.MessageID); err != nil {
			b.logger.Warn("remove a telegram message", "error", err)
			return false
		}
		if why.zh != "" {
			b.groupReply(ctx, client, cfg, 0, say(lang, why, mention(user)))
		}
		return true
	}
	content := strings.ToLower(message.Content())
	for _, word := range cfg.BlockedWords {
		if word != "" && strings.Contains(content, strings.ToLower(word)) {
			return remove(msgFilteredWord)
		}
	}
	if !cfg.FilterLinks && cfg.UnlinkedPerMinute <= 0 {
		return false
	}
	if _, err := b.store.LinkByTelegram(ctx, user.ID); err == nil {
		return false
	}
	if cfg.FilterLinks && hasLink(message) {
		return remove(msgFilteredLink)
	}
	if cfg.UnlinkedPerMinute > 0 && b.tooFast(user.ID, cfg.UnlinkedPerMinute) {
		// Said once, when the limit is first passed.
		b.mu.Lock()
		first := len(b.rates[user.ID]) == cfg.UnlinkedPerMinute+1
		b.mu.Unlock()
		if first {
			return remove(msgFilteredRate)
		}
		return remove(text{})
	}
	return false
}
