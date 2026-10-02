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
	if got := yuan(5); got != "¥0.05" {
		t.Errorf("yuan(5) = %s", got)
	}
	if got := yuan(12345); got != "¥123.45" {
		t.Errorf("yuan(12345) = %s", got)
	}
	if got := amountRange(settings.TelegramSettings{CheckinMinMinor: 30, CheckinMaxMinor: 30}); got != "¥0.30" {
		t.Errorf("fixed range = %s", got)
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
