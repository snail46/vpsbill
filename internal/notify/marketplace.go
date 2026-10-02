package notify

import (
	"context"
	"fmt"
	"time"

	"vpsbill/internal/store/postgres"
)

// HostNodeOffline warns a host that its node is unreachable and will be
// cleared unless it comes back or staff grant a hold.
func (n *Notifier) HostNodeOffline(ctx context.Context, node postgres.OfflineHostedNode, clearAfter time.Duration) {
	if !n.enabled() || node.OwnerEmail == "" {
		return
	}
	deadline := node.LastSeenAt.Add(clearAfter).In(n.location()).Format("2006-01-02 15:04")
	subject := fmt.Sprintf("[%s] 托管母机 %s 已离线", n.siteName(), node.Name)
	body := fmt.Sprintf("您好，%s：\n\n您托管的母机 %s 自 %s 起无法连接。\n如果到 %s 仍未恢复，系统将按托管准则自动清退：受影响实例的剩余价值退还给买家，并从您的余额按剩余价值额外赔付一份（余额不足时扣到 0 为止）。\n\n如有特殊原因，请尽快提交工单联系管理员说明。\n\n托管中心：%s\n",
		node.OwnerName, node.Name, node.LastSeenAt.In(n.location()).Format("2006-01-02 15:04"), deadline, n.link("/portal/hosting"))
	n.enqueue(ctx, node.OwnerEmail, subject, body, fmt.Sprintf("host-offline:%s:%d", node.ID, node.LastSeenAt.Unix()))
}

// NodeCleared tells the host and every affected buyer how a clearance was
// settled.
func (n *Notifier) NodeCleared(ctx context.Context, result postgres.ClearanceResult) {
	if !n.enabled() {
		return
	}
	if result.HostEmail != "" {
		lines := ""
		for _, item := range result.Services {
			lines += fmt.Sprintf("- %s（买家 %s）：剩余价值 %s，您赔付 %s\n", item.InstanceName, item.BuyerName, money(item.RemainingMinor, result.Currency), money(item.PenaltyMinor, result.Currency))
		}
		if lines == "" {
			lines = "（没有受影响的实例）\n"
		}
		subject := fmt.Sprintf("[%s] 托管母机 %s 已清退", n.siteName(), result.NodeName)
		body := fmt.Sprintf("您好，%s：\n\n您托管的母机 %s 已清退。原因：%s\n\n%s\n合计从余额扣除 %s。赔付以清退时您的账户余额为限，不会扣成负数。\n\n托管中心：%s\n",
			result.HostName, result.NodeName, result.Reason, lines, money(result.PenaltyMinor, result.Currency), n.link("/portal/hosting"))
		n.enqueue(ctx, result.HostEmail, subject, body, "node-cleared:"+result.NodeID+":host")
	}
	for _, item := range result.Services {
		to := item.BuyerEmail
		if to == "" {
			continue
		}
		subject := fmt.Sprintf("[%s] 实例 %s 所在母机已清退", n.siteName(), item.InstanceName)
		body := fmt.Sprintf("您好，%s：\n\n您的实例 %s 所在的托管母机 %s 已清退，实例已停止服务。原因：%s\n\n%s，合计 %s 已存入您的账户余额，可用于本平台消费。\n\n查看余额：%s\n",
			item.BuyerName, item.InstanceName, result.NodeName, result.Reason, clearanceSplit(item, result.Currency), money(item.RefundMinor, result.Currency), n.link("/portal/wallet"))
		n.enqueue(ctx, to, subject, body, "node-cleared:"+item.ServiceID+":buyer")
	}
}

// ServiceRefunded confirms a buyer's refund and tells the host an instance
// on the node was cancelled.
func (n *Notifier) ServiceRefunded(ctx context.Context, result postgres.RefundResult) {
	if !n.enabled() {
		return
	}
	kind := "按剩余天数比例退款"
	if result.Full {
		kind = "早期全额退款"
	}
	if result.BuyerEmail != "" {
		subject := fmt.Sprintf("[%s] 实例 %s 已退款", n.siteName(), result.InstanceName)
		body := fmt.Sprintf("您好，%s：\n\n您的实例 %s（%s）已按您的申请取消，实例会从母机上删除。\n\n退款方式：%s\n退款金额：%s，已存入您的账户余额，可用于本平台消费。\n\n查看余额：%s\n",
			result.BuyerName, result.InstanceName, result.PlanName, kind, money(result.RefundMinor, result.Currency), n.link("/portal/wallet"))
		n.enqueue(ctx, result.BuyerEmail, subject, body, "service-refunded:"+result.ServiceID+":buyer")
	}
	if result.HostEmail != "" {
		subject := fmt.Sprintf("[%s] 买家取消了实例 %s", n.siteName(), result.InstanceName)
		body := fmt.Sprintf("您好，%s：\n\n买家 %s 申请退款并取消了您母机上的实例 %s（%s），实例会从母机上删除。\n\n退款方式：%s，退还买家 %s。\n已使用部分的收益 %s 已结算到您的余额。\n\n托管中心：%s\n",
			result.HostName, result.BuyerName, result.InstanceName, result.PlanName, kind, money(result.RefundMinor, result.Currency), money(result.HostMinor, result.Currency), n.link("/portal/hosting"))
		n.enqueue(ctx, result.HostEmail, subject, body, "service-refunded:"+result.ServiceID+":host")
	}
}

// TradeCompleted tells both sides of a trading market sale what happened.
func (n *Notifier) TradeCompleted(ctx context.Context, result postgres.TradeResult) {
	if !n.enabled() || !n.settings.Current().MailNotifications.CustomerTrade {
		return
	}
	price := money(result.PriceMinor, result.Currency)
	if result.SellerEmail != "" {
		subject := fmt.Sprintf("[%s] 你挂售的实例 %s 已售出", n.siteName(), result.InstanceName)
		body := fmt.Sprintf("您好，%s：\n\n你在交易市场挂售的实例 %s（%s）已售出，成交价 %s 已存入你的账户余额，可用于本平台消费。\n实例已转给买家，你的账户里不再显示它。\n\n查看余额：%s\n",
			result.SellerName, result.InstanceName, result.PlanName, price, n.link("/portal/wallet"))
		n.enqueue(ctx, result.SellerEmail, subject, body, "trade:"+result.ListingID+":seller")
	}
	if result.BuyerEmail != "" {
		subject := fmt.Sprintf("[%s] 你已买下实例 %s", n.siteName(), result.InstanceName)
		body := fmt.Sprintf("您好，%s：\n\n你在交易市场以 %s 买下了实例 %s（%s），实例已转入你的账户。\n\n为了安全，请尽快在「我的 VPS」重置 root 密码；如果不确定原主人是否留下了其他登录方式，建议重装系统。\n交易市场的交易不退款。\n\n我的 VPS：%s\n",
			result.BuyerName, price, result.InstanceName, result.PlanName, n.link("/portal/services"))
		n.enqueue(ctx, result.BuyerEmail, subject, body, "trade:"+result.ListingID+":buyer")
	}
}

// clearanceSplit says what a buyer got back from a clearance.
func clearanceSplit(item postgres.ClearedService, currency string) string {
	if item.PenaltyMinor > 0 {
		return fmt.Sprintf("退还实例剩余价值 %s，另获机主赔付 %s", money(item.RemainingMinor, currency), money(item.PenaltyMinor, currency))
	}
	return fmt.Sprintf("退还实例剩余价值 %s", money(item.RemainingMinor, currency))
}

// closedListingWindow is how far back closed listings are looked at; mail
// deduplication keeps each notice to one.
const closedListingWindow = 10 * 24 * time.Hour

// notifyClosedListings tells sellers about listings they did not close
// themselves: expiry (with the renewal window, and later the recycling),
// staff and the system.
func (n *Notifier) notifyClosedListings(ctx context.Context) {
	if !n.settings.Current().MailNotifications.CustomerTrade {
		return
	}
	rows, err := n.store.ClosedListings(ctx, closedListingWindow)
	if err != nil {
		n.logger.Error("list closed trade listings", "error", err)
		return
	}
	for _, row := range rows {
		name := fmt.Sprintf("%s（%s）", row.InstanceName, row.PlanName)
		switch row.ClosedBy {
		case "expiry":
			if row.ServiceStatus == "terminating" || row.ServiceStatus == "terminated" {
				subject := fmt.Sprintf("[%s] 实例 %s 已被回收", n.siteName(), row.InstanceName)
				body := fmt.Sprintf("您好，%s：\n\n你在交易市场挂售的实例 %s 到期后没有在缓冲期内续费，系统已回收该实例，实例和数据已删除。\n\n我的 VPS：%s\n",
					row.CustomerName, name, n.link("/portal/services"))
				n.enqueue(ctx, row.Email, subject, body, "trade-recycled:"+row.ListingID)
				continue
			}
			deadline := "3 天内"
			if row.TerminationAt != nil {
				deadline = row.TerminationAt.In(n.location()).Format("2006-01-02 15:04") + " 前"
			}
			subject := fmt.Sprintf("[%s] 挂售的实例 %s 已到期下架", n.siteName(), row.InstanceName)
			body := fmt.Sprintf("您好，%s：\n\n你在交易市场挂售的实例 %s 已到期，挂售已自动下架，实例已退回你的账户并暂停使用。\n\n请在 %s 支付续费账单，支付后实例自动恢复运行；逾期未续费，系统将回收实例并删除数据。\n\n前往支付：%s\n",
				row.CustomerName, name, deadline, n.link("/portal/billing"))
			n.enqueue(ctx, row.Email, subject, body, "trade-closed:"+row.ListingID)
		default:
			who := "系统"
			if row.ClosedBy == "staff" {
				who = "管理员"
			}
			reason := row.Reason
			if reason == "" {
				reason = "未说明"
			}
			subject := fmt.Sprintf("[%s] 挂售的实例 %s 已被下架", n.siteName(), row.InstanceName)
			body := fmt.Sprintf("您好，%s：\n\n你在交易市场挂售的实例 %s（挂售价 %s）已被%s下架。原因：%s\n\n实例仍在你的账户中；挂售期间实例是停机的，需要使用请在「我的 VPS」开机，也可以重新挂售。\n\n交易市场：%s\n",
				row.CustomerName, name, money(row.PriceMinor, row.Currency), who, reason, n.link("/portal/trade"))
			n.enqueue(ctx, row.Email, subject, body, "trade-closed:"+row.ListingID)
		}
	}
}
