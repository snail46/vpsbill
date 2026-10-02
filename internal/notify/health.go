package notify

import (
	"context"
	"fmt"
)

// warnHeldNodes tells a host (or the staff, for platform nodes) once per
// hold that a node stopped selling because its machine is overloaded.
func (n *Notifier) warnHeldNodes(ctx context.Context) {
	nodes, err := n.store.HeldNodes(ctx)
	if err != nil {
		n.logger.Error("list held nodes", "error", err)
		return
	}
	var admins []string
	for _, node := range nodes {
		recipients, manage, greeting := admins, n.adminLink("/admin/nodes"), ""
		if node.OwnerMail != "" {
			recipients, manage, greeting = []string{node.OwnerMail}, n.link("/portal/hosting"), node.OwnerName
		} else if admins == nil {
			admins = n.adminRecipients(ctx)
			recipients = admins
		}
		for _, to := range recipients {
			lang := n.lang(ctx, to)
			name := greeting
			if name == "" {
				name = say(lang, "管理员", "administrator")
			}
			subject := fmt.Sprintf(say(lang, "[%s] 母机 %s 负载过高，已暂停销售", "[%s] Host %s is overloaded; sales are paused"), n.siteName(), node.Name)
			body := fmt.Sprintf(say(lang,
				"您好，%s：\n\n母机 %s 自 %s 起暂停销售新实例，原因：%s。\n\n已售实例不受影响。负载恢复正常后会自动恢复销售；如果长期不恢复，管理员可能按托管准则清退。请检查母机的内存、硬盘和 CPU 负载，或调低超售倍数。\n\n管理：%s\n",
				"Hello %s,\n\nHost %s stopped selling new instances at %s, because %s.\n\nInstances already sold are not affected. Sales resume by themselves once the load is back to normal; if it does not recover for a long time, an administrator may clear the host under the hosting rules. Check the host's memory, disk and CPU load, or lower its oversell ratios.\n\nManage: %s\n"),
				name, node.Name, node.Since.In(n.location()).Format("2006-01-02 15:04"), reason(lang, node.Reason), manage)
			n.enqueue(ctx, to, subject, body, fmt.Sprintf("node-hold:%s:%d:%s", node.ID, node.Since.Unix(), to))
		}
	}
}
