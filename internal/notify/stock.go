package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"vpsbill/internal/store/postgres"
	"vpsbill/internal/telegram"
)

const (
	stockInterval  = time.Minute
	firstStockWait = 45 * time.Second
)

// RunStock watches for new plans and restocks until ctx ends: customers
// waiting for a plan are told, and the announcement chat hears about it.
func (n *Notifier) RunStock(ctx context.Context) {
	wait := firstStockWait
	for {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if n.settings.Current().Installed {
			n.WatchStock(ctx)
		}
		wait = stockInterval
	}
}

// WatchStock runs one look at what can be bought.
func (n *Notifier) WatchStock(ctx context.Context) {
	changes, err := n.catalog.PlanChanges(ctx, n.now())
	if err != nil {
		n.logger.Error("look for new and restocked plans", "error", err)
		return
	}
	for _, change := range changes {
		if !change.New {
			n.tellWatchers(ctx, change)
		}
		n.announce(ctx, change)
	}
}

// cycleName names a billing cycle after a price: "月", "7 天".
func cycleName(lang, cycle string) string {
	months, days, ok := postgres.ParseBillingCycle(cycle)
	switch {
	case !ok:
		return cycle
	case months == 1:
		return say(lang, "月", "month")
	case months == 3:
		return say(lang, "季", "quarter")
	case months == 6:
		return say(lang, "半年", "half year")
	case months == 12:
		return say(lang, "年", "year")
	case months > 0:
		return fmt.Sprintf(say(lang, "%d 个月", "%d months"), months)
	}
	return fmt.Sprintf(say(lang, "%d 天", "%d days"), days)
}

// planFacts describes a plan in two lines: its size, and its lowest price
// with what is left.
func (n *Notifier) planFacts(lang string, change postgres.PlanChange) string {
	plan := change.Plan
	memory := fmt.Sprintf("%d MB", plan.RAMMB)
	if plan.RAMMB >= 1024 && plan.RAMMB%256 == 0 {
		memory = strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.2f", float64(plan.RAMMB)/1024), "0"), ".0") + " GB"
	}
	size := fmt.Sprintf(say(lang, "%d 核 · %s 内存 · %d GB 硬盘", "%d vCPU · %s RAM · %d GB disk"), plan.VCPU, memory, plan.DiskGB)
	if plan.TrafficGB > 0 {
		size += fmt.Sprintf(say(lang, " · %d GB 月流量", " · %d GB traffic a month"), plan.TrafficGB)
	}
	lines := []string{size}
	if len(plan.Prices) > 0 {
		lowest := plan.Prices[0]
		for _, price := range plan.Prices {
			if price.BillingCycle == "monthly" {
				lowest = price
				break
			}
			if price.AmountMinor < lowest.AmountMinor {
				lowest = price
			}
		}
		lines = append(lines, fmt.Sprintf(say(lang, "价格：%s / %s", "Price: %s per %s"), n.settings.Current().Locale.Money(lowest.AmountMinor), cycleName(lang, lowest.BillingCycle)))
	}
	if change.Left != nil {
		lines = append(lines, fmt.Sprintf(say(lang, "库存：%d 台", "In stock: %d"), *change.Left))
	}
	return strings.Join(lines, "\n")
}

// planLink is where a plan is bought.
func (n *Notifier) planLink(plan postgres.Plan) string {
	if plan.OwnerAccountID != "" {
		return n.link("/portal/hosting")
	}
	return n.link("/portal/shop")
}

// tellWatchers notifies the customers who asked to hear when the plan can
// be bought again; each is told once.
func (n *Notifier) tellWatchers(ctx context.Context, change postgres.PlanChange) {
	// With nothing to send through, the watches are kept for later.
	if !n.enabled() {
		return
	}
	watchers, err := n.catalog.TakePlanWatchers(ctx, change.Plan.ID)
	if err != nil {
		n.logger.Error("list plan watchers", "plan", change.Plan.Code, "error", err)
		return
	}
	stamp := n.now().Unix()
	for _, watcher := range watchers {
		lang := n.lang(ctx, watcher.Email)
		subject := fmt.Sprintf(say(lang, "[%s] %s 到货了", "[%s] %s is back in stock"), n.siteName(), change.Plan.Name)
		body := fmt.Sprintf(say(lang,
			"您好，%s：\n\n您关注的套餐 %s 已经补货，先到先得。\n\n%s\n\n前往购买：%s\n\n这是一次性提醒；如果没买到，可以再次点「到货通知我」。\n",
			"Hello %s,\n\nThe plan %s you asked about is back in stock; first come, first served.\n\n%s\n\nBuy it: %s\n\nYou are told once; if you miss it, choose “Notify me” again.\n"),
			watcher.DisplayName, change.Plan.Name, n.planFacts(lang, change), n.planLink(change.Plan))
		n.enqueueWith(ctx, watcher.Email, subject, body, fmt.Sprintf("plan-restock:%s:%s:%d", change.Plan.ID, watcher.UserID, stamp),
			[][]telegram.Button{{{Text: say(lang, "前往购买", "Buy it"), URL: n.planLink(change.Plan)}}})
	}
}

// announce posts a new plan or a restock to the announcement chat, within
// the daily limit.
func (n *Notifier) announce(ctx context.Context, change postgres.PlanChange) {
	current := n.settings.Current()
	config := current.Telegram
	switch {
	case !config.Ready() || config.AnnounceChatID == 0:
		return
	case change.New && !config.AnnounceNew, !change.New && !config.AnnounceRestock:
		return
	case change.Plan.OwnerAccountID != "" && !config.AnnounceHosted:
		return
	}
	if config.AnnounceDailyCap > 0 {
		local := n.now().In(n.location())
		today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, n.location())
		sent, err := n.store.AnnouncementsSince(ctx, today)
		if err != nil {
			n.logger.Error("count announcements", "error", err)
			return
		}
		if sent >= config.AnnounceDailyCap {
			return
		}
	}
	lang := current.Lang("")
	subject, kind := fmt.Sprintf(say(lang, "🆕 新品上架：%s", "🆕 New plan: %s"), change.Plan.Name), "new"
	if !change.New {
		kind = "restock"
		subject = fmt.Sprintf(say(lang, "📦 补货：%s", "📦 Back in stock: %s"), change.Plan.Name)
	}
	body := n.planFacts(lang, change)
	if change.Plan.OwnerAccountID != "" {
		body += "\n" + say(lang, "由机主托管出售", "Sold by a host")
	}
	buttons := [][]telegram.Button{{{Text: say(lang, "立即购买", "Buy now"), URL: n.planLink(change.Plan)}}}
	payload, err := encodeButtons(buttons)
	if err != nil {
		return
	}
	if _, err := n.store.EnqueueTelegram(ctx, config.AnnounceChatID, subject, body, fmt.Sprintf("%s%s:%s:%d", postgres.AnnouncementKey, kind, change.Plan.ID, n.now().Unix()), payload); err != nil {
		n.logger.Error("queue announcement", "error", err)
	}
}
