package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrTicketNotFound = errors.New("ticket not found")
	ErrTicketClosed   = errors.New("ticket is closed")
)

type OperationsStore struct{ db *pgxpool.Pool }

func NewOperationsStore(db *pgxpool.Pool) *OperationsStore { return &OperationsStore{db: db} }

type Ticket struct {
	ID            string  `json:"id"`
	Number        string  `json:"number"`
	AccountID     string  `json:"account_id"`
	CustomerName  string  `json:"customer_name"`
	ServiceID     *string `json:"service_id,omitempty"`
	InstanceName  string  `json:"instance_name,omitempty"`
	Subject       string  `json:"subject"`
	Priority      string  `json:"priority"`
	Status        string  `json:"status"`
	AssignedStaff string  `json:"assigned_staff,omitempty"`
	// HostAccountID is set on tickets about a hosted instance; the host
	// answers first and staff can step in.
	HostAccountID string    `json:"host_account_id,omitempty"`
	HostName      string    `json:"host_name,omitempty"`
	MessageCount  int       `json:"message_count"`
	LastReplyAt   time.Time `json:"last_reply_at"`
	CreatedAt     time.Time `json:"created_at"`
}

type TicketMessage struct {
	ID          string             `json:"id"`
	AuthorType  string             `json:"author_type"`
	AuthorName  string             `json:"author_name"`
	Body        string             `json:"body"`
	Internal    bool               `json:"internal"`
	CreatedAt   time.Time          `json:"created_at"`
	Attachments []TicketAttachment `json:"attachments"`
}

type TicketDetail struct {
	Ticket   Ticket          `json:"ticket"`
	Messages []TicketMessage `json:"messages"`
}

func (s *OperationsStore) ListCustomerTickets(ctx context.Context, accountID string) ([]Ticket, error) {
	return s.listTickets(ctx, "WHERE t.account_id=$1", accountID)
}

func (s *OperationsStore) ListHostTickets(ctx context.Context, hostAccountID string) ([]Ticket, error) {
	return s.listTickets(ctx, "WHERE t.host_account_id=$1", hostAccountID)
}

func (s *OperationsStore) ListTickets(ctx context.Context) ([]Ticket, error) {
	return s.listTickets(ctx, "", nil)
}

func (s *OperationsStore) listTickets(ctx context.Context, where string, argument any) ([]Ticket, error) {
	query := `SELECT t.id,t.number,t.account_id,a.display_name,t.service_id,coalesce(s.instance_name,''),t.subject,t.priority,t.status,
		coalesce(u.display_name,''),count(m.id),t.last_reply_at,t.created_at,coalesce(t.host_account_id::text,''),coalesce(h.display_name,'')
		FROM support_tickets t JOIN accounts a ON a.id=t.account_id LEFT JOIN accounts h ON h.id=t.host_account_id
		LEFT JOIN services s ON s.id=t.service_id LEFT JOIN users u ON u.id=t.assigned_staff_id
		LEFT JOIN support_messages m ON m.ticket_id=t.id ` + where + `
		GROUP BY t.id,a.display_name,s.instance_name,u.display_name,h.display_name ORDER BY t.updated_at DESC LIMIT 500`
	var rows pgx.Rows
	var err error
	if where == "" {
		rows, err = s.db.Query(ctx, query)
	} else {
		rows, err = s.db.Query(ctx, query, argument)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Ticket, 0)
	for rows.Next() {
		var row Ticket
		if err := rows.Scan(&row.ID, &row.Number, &row.AccountID, &row.CustomerName, &row.ServiceID, &row.InstanceName, &row.Subject, &row.Priority, &row.Status, &row.AssignedStaff, &row.MessageCount, &row.LastReplyAt, &row.CreatedAt, &row.HostAccountID, &row.HostName); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *OperationsStore) CreateTicket(ctx context.Context, accountID, userID, serviceID, subject, priority, body, ip, userAgent string, attachments ...AttachmentUpload) (TicketDetail, error) {
	subject, body, priority = strings.TrimSpace(subject), strings.TrimSpace(body), strings.TrimSpace(priority)
	if body == "" && len(attachments) > 0 {
		body = attachmentPlaceholder
	}
	if len(subject) < 3 || len(subject) > 160 || body == "" || len(body) > 10000 {
		return TicketDetail{}, errors.New("invalid ticket content")
	}
	if priority != "low" && priority != "normal" && priority != "high" && priority != "urgent" {
		return TicketDetail{}, errors.New("invalid ticket priority")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TicketDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var hostAccountID string
	if serviceID != "" {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM services WHERE id=$1 AND account_id=$2)", serviceID, accountID).Scan(&exists); err != nil || !exists {
			return TicketDetail{}, errors.New("invalid service")
		}
		if err := tx.QueryRow(ctx, "SELECT coalesce(p.owner_account_id::text,'') FROM services s JOIN plans p ON p.id=s.plan_id WHERE s.id=$1", serviceID).Scan(&hostAccountID); err != nil {
			return TicketDetail{}, err
		}
	}
	var result TicketDetail
	result.Ticket.Number = newDocumentNumber("TKT")
	err = tx.QueryRow(ctx, `INSERT INTO support_tickets(number,account_id,requester_user_id,service_id,subject,priority,host_account_id)
		VALUES($1,$2,$3,nullif($4,'')::uuid,$5,$6,nullif($7,'')::uuid) RETURNING id,status,last_reply_at,created_at`, result.Ticket.Number, accountID, userID, serviceID, subject, priority, hostAccountID).Scan(&result.Ticket.ID, &result.Ticket.Status, &result.Ticket.LastReplyAt, &result.Ticket.CreatedAt)
	if err != nil {
		return TicketDetail{}, err
	}
	var message TicketMessage
	err = tx.QueryRow(ctx, `INSERT INTO support_messages(ticket_id,author_type,author_id,body) VALUES($1,'customer',$2,$3) RETURNING id,created_at`, result.Ticket.ID, userID, body).Scan(&message.ID, &message.CreatedAt)
	if err != nil {
		return TicketDetail{}, err
	}
	if message.Attachments, err = insertAttachments(ctx, tx, result.Ticket.ID, message.ID, attachments); err != nil {
		return TicketDetail{}, err
	}
	message.AuthorType, message.Body = "customer", body
	result.Messages = []TicketMessage{message}
	result.Ticket.AccountID = accountID
	result.Ticket.HostAccountID = hostAccountID
	result.Ticket.Subject = subject
	result.Ticket.Priority = priority
	result.Ticket.MessageCount = 1
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,ip,user_agent,metadata) VALUES('customer',$1,'ticket.created','ticket',$2,nullif($3,'')::inet,$4,jsonb_build_object('priority',$5::text))`, userID, result.Ticket.ID, ip, userAgent, priority); err != nil {
		return TicketDetail{}, err
	}
	payload, _ := json.Marshal(map[string]any{"ticket_number": result.Ticket.Number, "subject": subject, "priority": priority, "account_id": accountID})
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('ticket',$1,'ticket.created',$2,$3)`, result.Ticket.ID, result.Ticket.ID+":created:v1", payload); err != nil {
		return TicketDetail{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return TicketDetail{}, err
	}
	return result, nil
}

func (s *OperationsStore) TicketDetail(ctx context.Context, ticketID, accountID string, includeInternal bool) (TicketDetail, error) {
	return s.ticketDetail(ctx, ticketID, "t.account_id", accountID, includeInternal)
}

// HostTicketDetail shows a hosted-instance ticket to its host, without staff
// notes.
func (s *OperationsStore) HostTicketDetail(ctx context.Context, ticketID, hostAccountID string) (TicketDetail, error) {
	if hostAccountID == "" {
		return TicketDetail{}, ErrTicketNotFound
	}
	return s.ticketDetail(ctx, ticketID, "t.host_account_id", hostAccountID, false)
}

func (s *OperationsStore) ticketDetail(ctx context.Context, ticketID, scopeColumn, scopeID string, includeInternal bool) (TicketDetail, error) {
	where := "t.id=$1"
	args := []any{ticketID}
	if scopeID != "" {
		where += " AND " + scopeColumn + "=$2"
		args = append(args, scopeID)
	}
	var result TicketDetail
	err := s.db.QueryRow(ctx, `SELECT t.id,t.number,t.account_id,a.display_name,t.service_id,coalesce(s.instance_name,''),t.subject,t.priority,t.status,coalesce(u.display_name,''),
		(SELECT count(*) FROM support_messages WHERE ticket_id=t.id),t.last_reply_at,t.created_at,coalesce(t.host_account_id::text,''),coalesce(h.display_name,'') FROM support_tickets t JOIN accounts a ON a.id=t.account_id LEFT JOIN accounts h ON h.id=t.host_account_id LEFT JOIN services s ON s.id=t.service_id LEFT JOIN users u ON u.id=t.assigned_staff_id WHERE `+where, args...).Scan(&result.Ticket.ID, &result.Ticket.Number, &result.Ticket.AccountID, &result.Ticket.CustomerName, &result.Ticket.ServiceID, &result.Ticket.InstanceName, &result.Ticket.Subject, &result.Ticket.Priority, &result.Ticket.Status, &result.Ticket.AssignedStaff, &result.Ticket.MessageCount, &result.Ticket.LastReplyAt, &result.Ticket.CreatedAt, &result.Ticket.HostAccountID, &result.Ticket.HostName)
	if errors.Is(err, pgx.ErrNoRows) {
		return TicketDetail{}, ErrTicketNotFound
	}
	if err != nil {
		return TicketDetail{}, err
	}
	messageWhere := "ticket_id=$1"
	if !includeInternal {
		messageWhere += " AND internal=false"
	}
	rows, err := s.db.Query(ctx, `SELECT m.id,m.author_type,coalesce(u.display_name,CASE WHEN m.author_type='system' THEN 'System' ELSE '' END),m.body,m.internal,m.created_at FROM support_messages m LEFT JOIN users u ON u.id=m.author_id WHERE `+messageWhere+` ORDER BY m.created_at`, ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	defer rows.Close()
	result.Messages = make([]TicketMessage, 0)
	for rows.Next() {
		var row TicketMessage
		if err := rows.Scan(&row.ID, &row.AuthorType, &row.AuthorName, &row.Body, &row.Internal, &row.CreatedAt); err != nil {
			return TicketDetail{}, err
		}
		result.Messages = append(result.Messages, row)
	}
	if err := rows.Err(); err != nil {
		return TicketDetail{}, err
	}
	attachments, err := s.attachmentsByMessage(ctx, ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	for index := range result.Messages {
		result.Messages[index].Attachments = attachments[result.Messages[index].ID]
		if result.Messages[index].Attachments == nil {
			result.Messages[index].Attachments = []TicketAttachment{}
		}
	}
	return result, nil
}

func (s *OperationsStore) ReplyTicket(ctx context.Context, ticketID, accountID, actorType, actorID, body string, internal bool, attachments ...AttachmentUpload) (TicketMessage, error) {
	body = strings.TrimSpace(body)
	if body == "" && len(attachments) > 0 {
		body = attachmentPlaceholder
	}
	if body == "" || len(body) > 10000 {
		return TicketMessage{}, errors.New("invalid message")
	}
	if actorType != "staff" {
		internal = false
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TicketMessage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	query := "SELECT status FROM support_tickets WHERE id=$1"
	args := []any{ticketID}
	scope := "account_id"
	if actorType == "host" {
		if accountID == "" {
			return TicketMessage{}, ErrTicketNotFound
		}
		scope = "host_account_id"
	}
	if accountID != "" {
		query += " AND " + scope + "=$2"
		args = append(args, accountID)
	}
	query += " FOR UPDATE"
	var status string
	if err = tx.QueryRow(ctx, query, args...).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return TicketMessage{}, ErrTicketNotFound
	}
	if err != nil {
		return TicketMessage{}, err
	}
	if status == "closed" {
		return TicketMessage{}, ErrTicketClosed
	}
	var result TicketMessage
	err = tx.QueryRow(ctx, `INSERT INTO support_messages(ticket_id,author_type,author_id,body,internal) VALUES($1,$2,$3,$4,$5) RETURNING id,created_at`, ticketID, actorType, actorID, body, internal).Scan(&result.ID, &result.CreatedAt)
	if err != nil {
		return TicketMessage{}, err
	}
	if result.Attachments, err = insertAttachments(ctx, tx, ticketID, result.ID, attachments); err != nil {
		return TicketMessage{}, err
	}
	newStatus := "staff_reply"
	if actorType == "customer" {
		newStatus = "customer_reply"
	}
	if internal {
		newStatus = status
	}
	if _, err = tx.Exec(ctx, `UPDATE support_tickets SET status=$2,last_reply_at=now(),updated_at=now(),assigned_staff_id=CASE WHEN $3='staff' THEN $4 ELSE assigned_staff_id END WHERE id=$1`, ticketID, newStatus, actorType, actorID); err != nil {
		return TicketMessage{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES($1,$2,'ticket.replied','ticket',$3,jsonb_build_object('internal',$4::boolean))`, actorType, actorID, ticketID, internal); err != nil {
		return TicketMessage{}, err
	}
	if !internal {
		payload, _ := json.Marshal(map[string]any{"ticket_id": ticketID, "message_id": result.ID, "author_type": actorType, "status": newStatus})
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('ticket',$1,$2,$3,$4)`, ticketID, "ticket."+actorType+"_replied", result.ID+":notify:v1", payload); err != nil {
			return TicketMessage{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return TicketMessage{}, err
	}
	result.AuthorType, result.Body, result.Internal = actorType, body, internal
	return result, nil
}

func (s *OperationsStore) UpdateTicketStatus(ctx context.Context, ticketID, staffID, status string) error {
	if status != "open" && status != "resolved" && status != "closed" {
		return errors.New("invalid ticket status")
	}
	tag, err := s.db.Exec(ctx, `WITH updated AS (UPDATE support_tickets SET status=$2,assigned_staff_id=$3,updated_at=now() WHERE id=$1 RETURNING id)
		INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) SELECT 'staff',$3,'ticket.status_changed','ticket',id,jsonb_build_object('status',$2) FROM updated`, ticketID, status, staffID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTicketNotFound
	}
	return nil
}

type AuditLog struct {
	ID         int64          `json:"id"`
	ActorType  string         `json:"actor_type"`
	ActorID    string         `json:"actor_id,omitempty"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   string         `json:"target_id,omitempty"`
	IP         string         `json:"ip,omitempty"`
	UserAgent  string         `json:"user_agent,omitempty"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"created_at"`
}

func (s *OperationsStore) ListAuditLogs(ctx context.Context) ([]AuditLog, error) {
	rows, err := s.db.Query(ctx, `SELECT id,actor_type,coalesce(actor_id,''),action,target_type,coalesce(target_id,''),coalesce(host(ip),''),coalesce(user_agent,''),metadata,created_at FROM audit_logs ORDER BY created_at DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]AuditLog, 0)
	for rows.Next() {
		var row AuditLog
		if err := rows.Scan(&row.ID, &row.ActorType, &row.ActorID, &row.Action, &row.TargetType, &row.TargetID, &row.IP, &row.UserAgent, &row.Metadata, &row.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
