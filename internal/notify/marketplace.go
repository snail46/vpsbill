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
	body := fmt.Sprintf("您好，%s：\n\n您托管的母机 %s 自 %s 起无法连接。\n如果到 %s 仍未恢复，系统将按托管准则自动清退：受影响的实例按剩余价值的 2 倍补偿给买家，其中一份从您的余额扣除。\n\n如有特殊原因，请尽快提交工单联系管理员说明。\n\n托管中心：%s\n",
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
			lines += fmt.Sprintf("- %s（买家 %s）：剩余价值 %s，赔付 %s\n", item.InstanceName, item.BuyerName, money(item.RemainingMinor, result.Currency), money(item.PenaltyMinor, result.Currency))
		}
		if lines == "" {
			lines = "（没有受影响的实例）\n"
		}
		subject := fmt.Sprintf("[%s] 托管母机 %s 已清退", n.siteName(), result.NodeName)
		body := fmt.Sprintf("您好，%s：\n\n您托管的母机 %s 已清退。原因：%s\n\n%s\n合计从余额扣除 %s。余额为负时不能发布新的母机，之后的托管收益会先用于抵扣。\n\n托管中心：%s\n",
			result.HostName, result.NodeName, result.Reason, lines, money(result.PenaltyMinor, result.Currency), n.link("/portal/hosting"))
		n.enqueue(ctx, result.HostEmail, subject, body, "node-cleared:"+result.NodeID+":host")
	}
	for _, item := range result.Services {
		to := item.BuyerEmail
		if to == "" {
			continue
		}
		subject := fmt.Sprintf("[%s] 实例 %s 所在母机已清退", n.siteName(), item.InstanceName)
		body := fmt.Sprintf("您好，%s：\n\n您的实例 %s 所在的托管母机 %s 已清退，实例已停止服务。原因：%s\n\n实例剩余价值 %s，按 %d 倍补偿 %s，已存入您的账户余额，可用于本平台消费。\n\n查看余额：%s\n",
			item.BuyerName, item.InstanceName, result.NodeName, result.Reason, money(item.RemainingMinor, result.Currency), result.Multiplier, money(item.RefundMinor, result.Currency), n.link("/portal/wallet"))
		n.enqueue(ctx, to, subject, body, "node-cleared:"+item.ServiceID+":buyer")
	}
}
