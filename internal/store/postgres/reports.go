package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrReportNotFound = errors.New("report not found")

// ReportReasons are the reasons a customer can give.
var ReportReasons = map[string]string{
	"resources":  "实际资源与宣传不符",
	"oversell":   "性能严重不足（超出公开的超售倍数）",
	"false_info": "位置、线路等信息不实",
	"abuse":      "辱骂或骚扰",
	"spam":       "广告或刷屏",
	"other":      "其他",
}

// Report is a customer's complaint about a hosted node or a chat message.
type Report struct {
	ID           string     `json:"id"`
	TargetType   string     `json:"target_type"`
	NodeID       string     `json:"node_id"`
	NodeName     string     `json:"node_name"`
	HostName     string     `json:"host_name"`
	MessageID    *int64     `json:"message_id,omitempty"`
	MessageBody  string     `json:"message_body,omitempty"`
	MessageBy    string     `json:"message_author,omitempty"`
	MessageByID  string     `json:"message_author_account_id,omitempty"`
	Reason       string     `json:"reason"`
	Detail       string     `json:"detail"`
	ReporterName string     `json:"reporter_name"`
	Status       string     `json:"status"`
	Resolution   string     `json:"resolution,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}

// ReportInput is a new report. A chat message report must come from a
// member of the room; a node can be reported by anyone who can see it in
// the market.
type ReportInput struct {
	TargetType string `json:"target_type"`
	NodeID     string `json:"node_id"`
	MessageID  int64  `json:"message_id"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail"`
}

// CreateReport files a report. At most 10 reports per account a day.
func (m *MarketplaceStore) CreateReport(ctx context.Context, accountID, userID string, in ReportInput) (string, error) {
	in.Detail = strings.TrimSpace(in.Detail)
	if _, ok := ReportReasons[in.Reason]; !ok || len([]rune(in.Detail)) > 1000 || (in.TargetType != "node" && in.TargetType != "chat_message") {
		return "", &HostedOrderError{"举报内容无效"}
	}
	var today int
	if err := m.db.QueryRow(ctx, `SELECT count(*) FROM reports WHERE reporter_account_id=$1 AND created_at>now()-interval '1 day'`, accountID).Scan(&today); err != nil {
		return "", err
	}
	if today >= 10 {
		return "", &HostedOrderError{"今天的举报次数已达上限"}
	}
	var owner string
	err := m.db.QueryRow(ctx, `SELECT owner_account_id::text FROM nodes WHERE id=$1 AND owner_account_id IS NOT NULL`, in.NodeID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrHostedNodeNotFound
	}
	if err != nil {
		return "", err
	}
	if owner == accountID && in.TargetType == "node" {
		return "", &HostedOrderError{"不能举报自己的母机"}
	}
	var messageID *int64
	if in.TargetType == "chat_message" {
		role, _, err := m.ChatRole(ctx, in.NodeID, accountID)
		if err != nil || role == "" {
			return "", ErrNotChatMember
		}
		var found bool
		if err := m.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM node_chat_messages WHERE id=$1 AND node_id=$2)`, in.MessageID, in.NodeID).Scan(&found); err != nil || !found {
			return "", &HostedOrderError{"消息不存在"}
		}
		messageID = &in.MessageID
	}
	var id string
	err = m.db.QueryRow(ctx, `
		INSERT INTO reports(reporter_account_id,reporter_user_id,target_type,node_id,message_id,reason,detail)
		VALUES($1,nullif($2,'')::uuid,$3,$4,$5,$6,$7) RETURNING id
	`, accountID, userID, in.TargetType, in.NodeID, messageID, in.Reason, in.Detail).Scan(&id)
	return id, err
}

// Reports lists reports for staff, open ones first.
func (m *MarketplaceStore) Reports(ctx context.Context) ([]Report, error) {
	rows, err := m.db.Query(ctx, `
		SELECT r.id,r.target_type,r.node_id,n.name,h.display_name,r.message_id,coalesce(c.body,''),coalesce(c.author_name,''),coalesce(c.author_account_id::text,''),
		       r.reason,r.detail,a.display_name,r.status,coalesce(r.resolution,''),r.created_at,r.resolved_at
		FROM reports r JOIN nodes n ON n.id=r.node_id JOIN accounts h ON h.id=n.owner_account_id JOIN accounts a ON a.id=r.reporter_account_id
		LEFT JOIN node_chat_messages c ON c.id=r.message_id
		ORDER BY (r.status='open') DESC, r.created_at DESC LIMIT 300
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Report, 0)
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.TargetType, &r.NodeID, &r.NodeName, &r.HostName, &r.MessageID, &r.MessageBody, &r.MessageBy, &r.MessageByID,
			&r.Reason, &r.Detail, &r.ReporterName, &r.Status, &r.Resolution, &r.CreatedAt, &r.ResolvedAt); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ResolveReport closes a report as resolved or dismissed.
func (m *MarketplaceStore) ResolveReport(ctx context.Context, id, staffID, status, resolution string) error {
	if status != "resolved" && status != "dismissed" {
		return &HostedOrderError{"处理结果无效"}
	}
	command, err := m.db.Exec(ctx, `UPDATE reports SET status=$2,resolution=nullif($3,''),resolved_by=nullif($4,'')::uuid,resolved_at=now() WHERE id=$1 AND status='open'`,
		id, status, strings.TrimSpace(resolution), staffID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrReportNotFound
	}
	return nil
}

// CapacityCap limits what a hosted node may sell whatever its agent
// reports; nil fields lift the cap.
type CapacityCap struct {
	VCPU   *int   `json:"vcpu"`
	RAMMB  *int64 `json:"ram_mb"`
	DiskGB *int64 `json:"disk_gb"`
}

func (m *MarketplaceStore) SetCapacityCap(ctx context.Context, nodeID, staffID string, limit CapacityCap) error {
	command, err := m.db.Exec(ctx, `
		UPDATE nodes SET capacity_cap_vcpu=$2,capacity_cap_ram_mb=$3,capacity_cap_disk_gb=$4,updated_at=now()
		WHERE id=$1 AND owner_account_id IS NOT NULL
	`, nodeID, limit.VCPU, limit.RAMMB, limit.DiskGB)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrHostedNodeNotFound
	}
	if _, err := m.db.Exec(ctx, `UPDATE nodes SET `+sellableCapacitySQL+` WHERE id=$1`, nodeID); err != nil {
		return err
	}
	_, err = m.db.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',nullif($1,'')::uuid,'hosting.capacity_capped','node',$2,jsonb_build_object('vcpu',$3::int,'ram_mb',$4::bigint,'disk_gb',$5::bigint))`,
		staffID, nodeID, limit.VCPU, limit.RAMMB, limit.DiskGB)
	return err
}
