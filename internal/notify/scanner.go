package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"vpsbill/internal/clock"
	"vpsbill/internal/provider"
	"vpsbill/internal/telegram"
)

// RunScanner measures service traffic and queues expiry and traffic
// reminders every scanInterval until ctx ends.
func (n *Notifier) RunScanner(ctx context.Context) {
	wait := firstScanWait
	for {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		n.Scan(ctx)
		wait = scanInterval
	}
}

// Scan runs one pass. Traffic is measured even when mail is off so the admin
// node list can show this month's usage.
func (n *Notifier) Scan(ctx context.Context) {
	n.collectTraffic(ctx)
	if !n.enabled() {
		return
	}
	n.remindExpiringServices(ctx)
	n.notifyClosedListings(ctx)
	n.remindNodes(ctx)
	n.warnHeldNodes(ctx)
}

func (n *Notifier) remindExpiringServices(ctx context.Context) {
	preferences := n.settings.Current().MailNotifications
	if !preferences.CustomerExpiry {
		return
	}
	services, err := n.store.ExpiringServices(ctx, time.Duration(preferences.ExpiryReminderDays)*24*time.Hour)
	if err != nil {
		n.logger.Error("list expiring services", "error", err)
		return
	}
	location := n.location()
	for _, service := range services {
		// One reminder when the window opens, another in the final day.
		stage := "window"
		if service.DueAt.Sub(n.now()) <= 24*time.Hour {
			stage = "final"
		}
		due := service.DueAt.In(location).Format("2006-01-02 15:04")
		lang := n.lang(ctx, service.Email)
		subject := fmt.Sprintf(say(lang, "[%s] 实例 %s 将于 %s 到期", "[%s] Instance %s expires on %s"), n.siteName(), service.InstanceName, due[:10])
		body := fmt.Sprintf(say(lang,
			"您好，%s：\n\n您的实例 %s（%s）将于 %s 到期，续费账单 %s 待支付 %s。\n\n"+
				"请在到期前完成支付，逾期后实例会在宽限期结束时暂停。\n\n前往支付：%s\n",
			"Hello %s,\n\nYour instance %s (%s) expires at %s. The renewal invoice %s for %s is unpaid.\n\n"+
				"Pay it before the instance expires; after that the instance is suspended once the grace period ends.\n\nPay now: %s\n"),
			service.CustomerName, service.InstanceName, service.PlanName, due, service.InvoiceNumber,
			money(service.AmountMinor, service.Currency), n.link("/portal/billing"))
		// On Telegram the reminder carries a button that pays the renewal
		// from the balance, after a confirmation.
		n.enqueueWith(ctx, service.Email, subject, body, fmt.Sprintf("service-expiry:%s:%s:%s", service.ServiceID, service.DueAt.UTC().Format(time.RFC3339), stage), [][]telegram.Button{
			{telegram.InvoiceButton(say(lang, "用余额续费", "Renew from balance"), service.InvoiceID)},
			{{Text: say(lang, "打开网站", "Open the site"), URL: n.link("/portal/billing")}},
		})
	}
}

// collectTraffic reads each running service's monthly traffic from its node,
// stores it, and warns the customer when the plan allowance is nearly used.
func (n *Notifier) collectTraffic(ctx context.Context) {
	targets, err := n.store.TrafficTargets(ctx)
	if err != nil {
		n.logger.Error("list traffic targets", "error", err)
		return
	}
	preferences := n.settings.Current().MailNotifications
	month := n.now().In(n.location()).Format("2006-01")
	drivers := map[string]provider.Driver{}
	for _, target := range targets {
		key := target.ProviderType + "|" + target.BaseURL
		driver, ok := drivers[key]
		if !ok {
			opened, err := provider.OpenSealed(n.box, target.Sealed(), 20*time.Second)
			if err != nil {
				drivers[key] = nil
				continue
			}
			driver, drivers[key] = opened, opened
		}
		metrics, ok := driver.(provider.Metrics)
		if driver == nil || !ok {
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		value, err := metrics.InstanceTraffic(requestCtx, target.InstanceName)
		cancel()
		traffic, found := provider.ParseTraffic(value)
		if err != nil || !found {
			continue
		}
		used := traffic.TotalBytes
		var rx, tx *int64
		if traffic.Split {
			rx, tx = &traffic.RXBytes, &traffic.TXBytes
		}
		if err := n.store.RecordServiceTraffic(ctx, target.ServiceID, used, rx, tx); err != nil {
			n.logger.Error("record service traffic", "service_id", target.ServiceID, "error", err)
			continue
		}
		// Traffic counts both directions. An instance over its allowance is
		// stopped until the month (UTC+8, the platform calendar) changes.
		lockMonth := clock.Month(n.now())
		if target.LockedMonth != "" && target.LockedMonth != lockMonth {
			if err := n.store.UnlockTraffic(ctx, target.ServiceID, lockMonth); err != nil {
				n.logger.Error("lift traffic lock", "service_id", target.ServiceID, "error", err)
			}
		}
		if target.TrafficGB > 0 && used > int64(target.TrafficGB)<<30 {
			if locked, err := n.store.LockForTraffic(ctx, target.ServiceID, lockMonth); err != nil {
				n.logger.Error("stop over-limit instance", "service_id", target.ServiceID, "error", err)
			} else if locked {
				n.logger.Warn("instance stopped for exceeding its traffic", "service_id", target.ServiceID, "used_bytes", used)
			}
		}
		if target.TrafficGB <= 0 || !preferences.CustomerTraffic || !n.enabled() {
			continue
		}
		threshold, crossed := crossedThreshold(used, int64(target.TrafficGB)<<30, preferences.TrafficAlertPercent)
		if !crossed {
			continue
		}
		lang := n.lang(ctx, target.Email)
		subject := fmt.Sprintf(say(lang, "[%s] 实例 %s 本月流量已用 %d%%", "[%s] Instance %s has used %d%% of this month's traffic"), n.siteName(), target.InstanceName, threshold)
		advice := say(lang, "流量按上行加下行双向计算，超出后实例会被停止，下月 1 日自动恢复。",
			"Traffic counts upload plus download. Once it is used up the instance is stopped, and it resumes on the 1st of next month.")
		if threshold >= 100 {
			subject = fmt.Sprintf(say(lang, "[%s] 实例 %s 本月流量已用尽", "[%s] Instance %s has used up this month's traffic"), n.siteName(), target.InstanceName)
			advice = say(lang, "本月流量（上行加下行）已用尽，实例已停止，下月 1 日自动重置并开机。如需帮助请提交工单。",
				"This month's traffic (upload plus download) is used up and the instance is stopped. On the 1st of next month the count starts over and the instance starts again. Open a ticket if you need help.")
		}
		detail := ""
		if traffic.Split {
			detail = fmt.Sprintf(say(lang, "（下行 %s，上行 %s）", " (download %s, upload %s)"), formatBytes(traffic.RXBytes), formatBytes(traffic.TXBytes))
		}
		body := fmt.Sprintf(say(lang,
			"您好，%s：\n\n您的实例 %s（%s）本月已使用 %s%s，套餐月流量 %d GB。\n%s\n\n查看实例：%s\n",
			"Hello %s,\n\nYour instance %s (%s) has used %s%s this month; the plan's monthly traffic is %d GB.\n%s\n\nSee the instance: %s\n"),
			target.CustomerName, target.InstanceName, target.PlanName, formatBytes(used), detail, target.TrafficGB, advice, n.link("/portal/services"))
		n.enqueue(ctx, target.Email, subject, body, fmt.Sprintf("service-traffic:%s:%s:%d", target.ServiceID, month, threshold))
	}
}

// remindNodes warns merchant staff about host rentals that are about to
// expire and hosts whose services used most of the monthly allowance.
func (n *Notifier) remindNodes(ctx context.Context) {
	preferences := n.settings.Current().MailNotifications
	if !preferences.AdminNodeExpiry && !preferences.AdminNodeTraffic {
		return
	}
	nodes, err := n.store.NodeWatches(ctx)
	if err != nil {
		n.logger.Error("list node watches", "error", err)
		return
	}
	location := n.location()
	now := n.now().In(location)
	month := now.Format("2006-01")
	var admins []string
	for _, node := range nodes {
		// Hosted nodes remind their host; platform nodes the merchant staff.
		recipients, manage := admins, n.adminLink("/admin/nodes")
		if node.OwnerEmail != "" {
			recipients, manage = []string{node.OwnerEmail}, n.link("/portal/hosting")
		} else if admins == nil {
			admins = n.adminRecipients(ctx)
			recipients = admins
		}
		if preferences.AdminNodeExpiry && node.ExpiresAt != nil {
			date := node.ExpiresAt.In(clock.Zone).Format("2006-01-02")
			expires, _ := time.ParseInLocation("2006-01-02", date, location)
			remaining := expires.Sub(now)
			if remaining <= time.Duration(preferences.NodeExpiryReminderDays)*24*time.Hour {
				stage := "window"
				switch {
				case remaining <= 0:
					stage = "expired"
				case remaining <= 24*time.Hour:
					stage = "final"
				}
				for _, to := range recipients {
					lang := n.lang(ctx, to)
					subject := fmt.Sprintf(say(lang, "[%s] 母鸡 %s 将于 %s 到期", "[%s] Host %s expires on %s"), n.siteName(), node.Name, date)
					if stage == "expired" {
						subject = fmt.Sprintf(say(lang, "[%s] 母鸡 %s 已于 %s 到期", "[%s] Host %s expired on %s"), n.siteName(), node.Name, date)
					}
					body := fmt.Sprintf(say(lang,
						"母鸡 %s 的租期到 %s。请及时续费，或提前迁出上面的实例，避免客户服务中断。\n\n节点管理：%s\n",
						"The lease of host %s runs to %s. Renew it in time, or move its instances away beforehand, so that customers' service is not interrupted.\n\nNodes: %s\n"),
						node.Name, date, manage)
					if node.OwnerEmail != "" {
						body = fmt.Sprintf(say(lang,
							"您托管的母机 %s 租期到 %s。续租后请在托管中心更新到期日期；如果不再续租，受影响的实例将按托管准则清退：剩余价值退还买家，并从您的余额按剩余价值额外赔付一份（余额不足时扣到 0 为止）。\n\n托管中心：%s\n",
							"The lease of your hosted server %s runs to %s. After renewing it, update the expiry date in the hosting center. If you do not renew, the affected instances are cleared under the hosting rules: the remaining value returns to the buyers, and the same amount again is paid from your balance as compensation (down to a balance of 0 at most).\n\nHosting center: %s\n"),
							node.Name, date, manage)
					}
					n.enqueue(ctx, to, subject, body, fmt.Sprintf("node-expiry:%s:%s:%s:%s", node.ID, date, stage, to))
				}
			}
		}
		if preferences.AdminNodeTraffic && node.TrafficQuotaGB > 0 {
			threshold, crossed := crossedThreshold(node.UsedBytes, int64(node.TrafficQuotaGB)<<30, preferences.TrafficAlertPercent)
			if crossed {
				for _, to := range recipients {
					lang := n.lang(ctx, to)
					subject := fmt.Sprintf(say(lang, "[%s] 母鸡 %s 本月流量已用 %d%%", "[%s] Host %s has used %d%% of this month's traffic"), n.siteName(), node.Name, threshold)
					body := fmt.Sprintf(say(lang,
						"母鸡 %s 上的实例本月合计使用 %s，月流量限额 %d GB。\n统计的是各实例流量之和，不含宿主机自身流量。\n\n节点管理：%s\n",
						"The instances on host %s have used %s in total this month; the monthly traffic quota is %d GB.\nThis is the sum of the instances' traffic, without the host's own.\n\nNodes: %s\n"),
						node.Name, formatBytes(node.UsedBytes), node.TrafficQuotaGB, manage)
					n.enqueue(ctx, to, subject, body, fmt.Sprintf("node-traffic:%s:%s:%d:%s", node.ID, month, threshold, to))
				}
			}
		}
	}
}

// crossedThreshold returns the highest alert level reached: 100 when the
// allowance is used up, otherwise alertPercent.
func crossedThreshold(used, limit int64, alertPercent int) (int, bool) {
	if limit <= 0 {
		return 0, false
	}
	switch {
	case used >= limit:
		return 100, true
	case used*100 >= limit*int64(alertPercent):
		return alertPercent, true
	default:
		return 0, false
	}
}

// TrafficUsedBytes is the two-way total of a provider traffic document.
func TrafficUsedBytes(value any) (int64, bool) {
	traffic, ok := provider.ParseTraffic(value)
	return traffic.TotalBytes, ok
}

func formatBytes(value int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(value)
	index := 0
	for size >= 1024 && index < len(units)-1 {
		size /= 1024
		index++
	}
	if index == 0 {
		return fmt.Sprintf("%d B", value)
	}
	return fmt.Sprintf("%.2f %s", size, units[index])
}

func money(amountMinor int64, currency string) string {
	symbol := strings.TrimSpace(currency) + " "
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "CNY":
		symbol = "¥"
	case "USD":
		symbol = "$"
	}
	return fmt.Sprintf("%s%.2f", symbol, float64(amountMinor)/100)
}
