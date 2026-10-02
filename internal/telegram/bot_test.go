package telegram

import (
	"reflect"
	"strings"
	"testing"

	"vpsbill/internal/settings"
)

func TestCommand(t *testing.T) {
	cases := []struct{ text, name, argument string }{
		{"/start bind_abc", "start", "bind_abc"},
		{"/checkin@Site_Bot", "checkin", ""},
		{"/checkin@other_bot", "", ""},
		{"/INVITE", "invite", ""},
		{"签到", "checkin", ""},
		{" 打卡 ", "checkin", ""},
		{"今天 签到 了吗", "", ""},
		{"邀请", "invite", ""},
		{"hello", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		name, argument := command(c.text, "site_bot")
		if name != c.name || argument != c.argument {
			t.Errorf("command(%q) = %q %q, want %q %q", c.text, name, argument, c.name, c.argument)
		}
	}
}

func TestMoneyAndNames(t *testing.T) {
	cny := settings.LocaleSettings{USDEnabled: true, USDRate: 7, DefaultCurrency: "CNY"}
	usd := settings.LocaleSettings{USDEnabled: true, USDRate: 7, DefaultCurrency: "USD"}
	if got := cny.Money(5); got != "¥0.05" {
		t.Errorf("cny.Money(5) = %s", got)
	}
	if got := cny.Money(12345); got != "¥123.45" {
		t.Errorf("cny.Money(12345) = %s", got)
	}
	if got := usd.Money(700); got != "$1.00" {
		t.Errorf("usd.Money(700) = %s", got)
	}
	if got := amountRange(settings.TelegramSettings{CheckinMinMinor: 30, CheckinMaxMinor: 30}, cny.Money); got != "¥0.30" {
		t.Errorf("fixed range = %s", got)
	}
	// Rewards typed in USD are paid in the ledger currency and read the
	// same when shown in USD again.
	rewards := settings.TelegramSettings{Currency: "USD", BindRewardMinor: 100, CheckinMinMinor: 10, CheckinMaxMinor: 50, DailyBudgetMinor: 10000}
	ledger := rewards.In(usd, "CNY")
	if ledger.BindRewardMinor != 700 || ledger.CheckinMinMinor != 70 || ledger.DailyBudgetMinor != 70000 || ledger.Currency != "CNY" {
		t.Errorf("ledger rewards = %+v", ledger)
	}
	if got := amountRange(ledger, usd.Money); got != "$0.10–$0.50" {
		t.Errorf("usd range = %s", got)
	}
	if back := ledger.In(usd, "USD"); back.BindRewardMinor != 100 || back.CheckinMaxMinor != 50 {
		t.Errorf("round trip = %+v", back)
	}
	if got := maskEmail("a@example.com"); got != "a***@example.com" {
		t.Errorf("maskEmail = %s", got)
	}
	if got := mention(User{ID: 7}); got != "7" {
		t.Errorf("mention without a name = %s", got)
	}
	if got := mention(User{FirstName: "<b>x</b>"}); strings.Contains(got, "<") {
		t.Errorf("mention is not escaped: %s", got)
	}
}

// Both languages of a message must take the same arguments.
func TestMessagesTakeTheSameArguments(t *testing.T) {
	verbs := func(format string) []string {
		var found []string
		for i := 0; i < len(format)-1; i++ {
			if format[i] != '%' {
				continue
			}
			if format[i+1] != '%' {
				found = append(found, format[i:i+2])
			}
			i++
		}
		return found
	}
	for _, message := range []text{
		msgDisabled, msgBound, msgBoundBonus, msgBindInvalid, msgBindTaken, msgBindAlready, msgBindUnverified, msgNotLinked,
		msgCheckinDone, msgCheckinAlready, msgCheckinExhausted, msgCheckinInGroup, msgInvite, msgInviteHold, msgInviteLinked,
		msgInviteCap, msgInviteOff, msgInviteUnavailable, msgSentPrivately, msgStartBotFirst, msgMe, msgMeToday, msgHelp,
		msgHelpBind, msgHelpInvite, msgWelcome, msgInviteRewarded, msgGroupLink,
	} {
		if zh, en := verbs(message.zh), verbs(message.en); !reflect.DeepEqual(zh, en) {
			t.Errorf("arguments differ: %v in %q, %v in %q", zh, message.zh, en, message.en)
		}
	}
}

func TestPresent(t *testing.T) {
	for status, want := range map[string]bool{"creator": true, "administrator": true, "member": true, "left": false, "kicked": false, "restricted": false} {
		if got := (ChatMember{Status: status}).Present(); got != want {
			t.Errorf("Present(%s) = %v", status, got)
		}
	}
	if !(ChatMember{Status: "restricted", IsMember: true}).Present() {
		t.Error("a restricted member who is in the chat is present")
	}
}
