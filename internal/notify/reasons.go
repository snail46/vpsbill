package notify

import (
	"regexp"
	"strings"
)

// Reasons the system writes into the data (why a host was cleared, why a
// node stopped selling, why a listing was closed) are stored in Chinese.
// English mail gives the ones the system wrote itself in English; reasons
// typed by people stay as they were typed.
var reasonRules = []struct {
	pattern *regexp.Regexp
	english string
}{
	{regexp.MustCompile(`^母机离线超过 (\d+) 小时，系统自动清退$`), "the host was offline for more than $1 hours and was cleared automatically"},
	{regexp.MustCompile(`^机主主动下架母机$`), "the owner withdrew the host"},
	{regexp.MustCompile(`^母机长时间离线$`), "the host was offline for a long time"},
	{regexp.MustCompile(`^母机清退$`), "the host was cleared"},
	{regexp.MustCompile(`^母机已清退$`), "the host was cleared"},
	{regexp.MustCompile(`^实例已不是正常运行状态$`), "the instance is no longer running normally"},
	{regexp.MustCompile(`^实例已退款$`), "the instance was refunded"},
	{regexp.MustCompile(`^卖家下架$`), "delisted by the seller"},
	{regexp.MustCompile(`^管理员下架$`), "delisted by staff"},
	{regexp.MustCompile(`^违反交易规则$`), "it broke the trading rules"},
	{regexp.MustCompile(`^可用内存持续低于 (\d+)%（(\d+) / (\d+) MB）$`), "available memory has stayed below $1% ($2 / $3 MB)"},
	{regexp.MustCompile(`^宿主机磁盘 (.+?) 已用 (\d+)%$`), "host disk $1 is $2% full"},
	{regexp.MustCompile(`^(.+?) 实例存储已用 (\d+)%$`), "the $1 instance storage is $2% full"},
	{regexp.MustCompile(`^负载持续 24 小时超过核数的 (\d+) 倍（([\d.]+) / (\d+) 核）$`), "load has stayed above $1 times the core count for 24 hours ($2 / $3 cores)"},
	{regexp.MustCompile(`^(.+?) 无法限制实例硬盘：(.*)$`), "$1 cannot limit instance disks: $2"},
}

// reason gives a stored reason in the mail's language.
func reason(lang, text string) string {
	text = strings.TrimSpace(text)
	if lang != "en" {
		return text
	}
	for _, rule := range reasonRules {
		if rule.pattern.MatchString(text) {
			return rule.pattern.ReplaceAllString(text, rule.english)
		}
	}
	return text
}
