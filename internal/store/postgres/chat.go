package postgres

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type ChatMessage struct {
	ID         int64     `json:"id"`
	NodeID     string    `json:"node_id"`
	AuthorType string    `json:"author_type"`
	AuthorName string    `json:"author_name"`
	Mine       bool      `json:"mine"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
	// AuthorAccountID is only sent to staff, who can mute the author.
	AuthorAccountID string `json:"author_account_id,omitempty"`
	// authorAccountID decides Mine per viewer; it is not sent to clients.
	authorAccountID string
}

// ForViewer marks the message as the viewer's own.
func (m ChatMessage) ForViewer(accountID, userID string) ChatMessage {
	m.Mine = accountID != "" && m.authorAccountID == accountID
	if m.AuthorType == "staff" && userID != "" {
		m.Mine = m.authorAccountID == "staff:"+userID
	}
	if accountID == "" && m.AuthorType != "staff" {
		m.AuthorAccountID = m.authorAccountID
	}
	return m
}

// ChatLimitError explains why a customer cannot post right now.
type ChatLimitError struct{ Message string }

func (e *ChatLimitError) Error() string { return e.Message }

// Chat flood limits for customers: 5 messages per 30 seconds and 60 per
// 10 minutes in a room.
const (
	chatBurst       = 5
	chatBurstWindow = 30 * time.Second
	chatHourly      = 60
	chatHourlyWin   = 10 * time.Minute
)

// MuteChat stops an account posting in a room until the given time.
func (m *MarketplaceStore) MuteChat(ctx context.Context, nodeID, accountID, staffID, reason string, until time.Time) error {
	_, err := m.db.Exec(ctx, `
		INSERT INTO node_chat_mutes(node_id,account_id,until,reason,created_by) VALUES($1,$2,$3,$4,nullif($5,'')::uuid)
		ON CONFLICT(node_id,account_id) DO UPDATE SET until=excluded.until,reason=excluded.reason,created_by=excluded.created_by,created_at=now()
	`, nodeID, accountID, until, strings.TrimSpace(reason), staffID)
	if err == nil {
		_, err = m.db.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',nullif($1,'')::uuid,'chat.muted','node',$2,jsonb_build_object('account_id',$3::text,'until',$4::timestamptz,'reason',$5::text))`,
			staffID, nodeID, accountID, until, reason)
	}
	return err
}

// UnmuteChat lifts a mute.
func (m *MarketplaceStore) UnmuteChat(ctx context.Context, nodeID, accountID string) error {
	_, err := m.db.Exec(ctx, `DELETE FROM node_chat_mutes WHERE node_id=$1 AND account_id=$2`, nodeID, accountID)
	return err
}

// ChatMute is an active mute in a room.
type ChatMute struct {
	AccountID   string    `json:"account_id"`
	AccountName string    `json:"account_name"`
	Until       time.Time `json:"until"`
	Reason      string    `json:"reason"`
}

func (m *MarketplaceStore) ChatMutes(ctx context.Context, nodeID string) ([]ChatMute, error) {
	rows, err := m.db.Query(ctx, `SELECT c.account_id,a.display_name,c.until,c.reason FROM node_chat_mutes c JOIN accounts a ON a.id=c.account_id WHERE c.node_id=$1 AND c.until>now() ORDER BY c.until`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ChatMute, 0)
	for rows.Next() {
		var mute ChatMute
		if err := rows.Scan(&mute.AccountID, &mute.AccountName, &mute.Until, &mute.Reason); err != nil {
			return nil, err
		}
		result = append(result, mute)
	}
	return result, rows.Err()
}

type ChatRoom struct {
	NodeID      string       `json:"node_id"`
	NodeName    string       `json:"node_name"`
	HostName    string       `json:"host_name"`
	Role        string       `json:"role"`
	Retired     bool         `json:"retired"`
	Members     int          `json:"members"`
	LastMessage *ChatMessage `json:"last_message,omitempty"`
}

const chatMemberServiceStatuses = "('provisioning','active','overdue','suspended')"

// ChatRole returns "host" or "buyer" for members of a hosted node's room and
// "" for everyone else.
func (m *MarketplaceStore) ChatRole(ctx context.Context, nodeID, accountID string) (string, bool, error) {
	var role string
	var retired bool
	err := m.db.QueryRow(ctx, `
		SELECT CASE WHEN n.owner_account_id::text=$2 THEN 'host'
		            WHEN EXISTS(SELECT 1 FROM services s JOIN plans p ON p.id=s.plan_id WHERE p.node_id=n.id AND s.account_id::text=$2 AND s.status IN `+chatMemberServiceStatuses+`) THEN 'buyer'
		            ELSE '' END, n.retired_at IS NOT NULL
		FROM nodes n WHERE n.id=$1 AND n.owner_account_id IS NOT NULL
	`, nodeID, accountID).Scan(&role, &retired)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrHostedNodeNotFound
	}
	return role, retired, err
}

// ChatRooms lists the rooms an account belongs to; an empty account lists
// every hosted node's room for staff.
func (m *MarketplaceStore) ChatRooms(ctx context.Context, accountID string) ([]ChatRoom, error) {
	query := `
		SELECT n.id,n.name,a.display_name,
		       CASE WHEN $1='' THEN 'staff' WHEN n.owner_account_id::text=$1 THEN 'host' ELSE 'buyer' END,
		       n.retired_at IS NOT NULL,
		       1 + (SELECT count(DISTINCT s.account_id) FROM services s JOIN plans p ON p.id=s.plan_id WHERE p.node_id=n.id AND s.status IN ` + chatMemberServiceStatuses + `)::int
		FROM nodes n JOIN accounts a ON a.id=n.owner_account_id
		WHERE n.owner_account_id IS NOT NULL AND ($1='' OR n.owner_account_id::text=$1 OR EXISTS(
		      SELECT 1 FROM services s JOIN plans p ON p.id=s.plan_id WHERE p.node_id=n.id AND s.account_id::text=$1 AND s.status IN ` + chatMemberServiceStatuses + `))
		ORDER BY n.retired_at NULLS FIRST, n.name`
	rows, err := m.db.Query(ctx, query, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rooms := make([]ChatRoom, 0)
	for rows.Next() {
		var room ChatRoom
		if err := rows.Scan(&room.NodeID, &room.NodeName, &room.HostName, &room.Role, &room.Retired, &room.Members); err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range rooms {
		messages, err := m.ChatMessages(ctx, rooms[i].NodeID, 0, 0, 1)
		if err != nil {
			return nil, err
		}
		if len(messages) > 0 {
			rooms[i].LastMessage = &messages[0]
		}
	}
	return rooms, nil
}

// ChatMessages returns messages in ascending order: those after afterID, or
// the latest ones before beforeID (0 = newest).
func (m *MarketplaceStore) ChatMessages(ctx context.Context, nodeID string, afterID, beforeID int64, limit int) ([]ChatMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	var rows pgx.Rows
	var err error
	columns := `id,node_id,author_type,author_name,body,created_at,coalesce(author_account_id::text,''),coalesce(author_user_id::text,'')`
	if afterID > 0 {
		rows, err = m.db.Query(ctx, `SELECT `+columns+` FROM node_chat_messages WHERE node_id=$1 AND id>$2 ORDER BY id LIMIT $3`, nodeID, afterID, limit)
	} else {
		if beforeID <= 0 {
			beforeID = 1<<62 - 1
		}
		rows, err = m.db.Query(ctx, `SELECT * FROM (SELECT `+columns+` FROM node_chat_messages WHERE node_id=$1 AND id<$2 ORDER BY id DESC LIMIT $3) latest ORDER BY id`, nodeID, beforeID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ChatMessage, 0)
	for rows.Next() {
		message, err := scanChatMessage(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, message)
	}
	return result, rows.Err()
}

func scanChatMessage(row pgx.Row) (ChatMessage, error) {
	var message ChatMessage
	var accountID, userID string
	if err := row.Scan(&message.ID, &message.NodeID, &message.AuthorType, &message.AuthorName, &message.Body, &message.CreatedAt, &accountID, &userID); err != nil {
		return ChatMessage{}, err
	}
	message.authorAccountID = accountID
	if message.AuthorType == "staff" {
		message.authorAccountID = "staff:" + userID
	}
	return message, nil
}

func (m *MarketplaceStore) ChatMessage(ctx context.Context, id int64) (ChatMessage, error) {
	return scanChatMessage(m.db.QueryRow(ctx, `SELECT id,node_id,author_type,author_name,body,created_at,coalesce(author_account_id::text,''),coalesce(author_user_id::text,'') FROM node_chat_messages WHERE id=$1`, id))
}

// PostChatMessage stores a message and wakes every API process that has
// subscribers in the room.
func (m *MarketplaceStore) PostChatMessage(ctx context.Context, nodeID, authorType, userID, accountID, authorName, body string) (ChatMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > 2000 {
		return ChatMessage{}, errors.New("invalid chat message")
	}
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return ChatMessage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if authorType != "staff" && accountID != "" {
		var mutedUntil *time.Time
		var burst, recent int
		if err := tx.QueryRow(ctx, `
			SELECT (SELECT until FROM node_chat_mutes WHERE node_id=$1 AND account_id=$2 AND until>now()),
			       count(*) FILTER (WHERE created_at > now() - make_interval(secs => $3)),
			       count(*) FILTER (WHERE created_at > now() - make_interval(secs => $4))
			FROM node_chat_messages WHERE node_id=$1 AND author_account_id=$2 AND created_at > now() - make_interval(secs => $4)
		`, nodeID, accountID, chatBurstWindow.Seconds(), chatHourlyWin.Seconds()).Scan(&mutedUntil, &burst, &recent); err != nil {
			return ChatMessage{}, err
		}
		switch {
		case mutedUntil != nil:
			return ChatMessage{}, &ChatLimitError{"你已被管理员禁言至 " + mutedUntil.Format("2006-01-02 15:04") + "（UTC）"}
		case burst >= chatBurst || recent >= chatHourly:
			return ChatMessage{}, &ChatLimitError{"发言太频繁，请稍后再发"}
		}
	}
	message, err := scanChatMessage(tx.QueryRow(ctx, `
		INSERT INTO node_chat_messages(node_id,author_type,author_user_id,author_account_id,author_name,body)
		VALUES($1,$2,nullif($3,'')::uuid,nullif($4,'')::uuid,$5,$6)
		RETURNING id,node_id,author_type,author_name,body,created_at,coalesce(author_account_id::text,''),coalesce(author_user_id::text,'')
	`, nodeID, authorType, userID, accountID, authorName, body))
	if err != nil {
		return ChatMessage{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1,$2)`, ChatNotifyChannel, strconv.FormatInt(message.ID, 10)); err != nil {
		return ChatMessage{}, err
	}
	return message, tx.Commit(ctx)
}

// ChatAuthorName is the name shown for a customer in a room.
func (m *MarketplaceStore) ChatAuthorName(ctx context.Context, accountID string) (string, error) {
	var name string
	err := m.db.QueryRow(ctx, `SELECT display_name FROM accounts WHERE id=$1`, accountID).Scan(&name)
	return name, err
}
