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
	// authorAccountID decides Mine per viewer; it is not sent to clients.
	authorAccountID string
}

// ForViewer marks the message as the viewer's own.
func (m ChatMessage) ForViewer(accountID, userID string) ChatMessage {
	m.Mine = accountID != "" && m.authorAccountID == accountID
	if m.AuthorType == "staff" && userID != "" {
		m.Mine = m.authorAccountID == "staff:"+userID
	}
	return m
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
