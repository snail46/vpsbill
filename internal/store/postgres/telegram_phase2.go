package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"vpsbill/internal/clock"
)

// ---- Staff on Telegram ----

// StaffTelegram is a staff member's linked Telegram account and what their
// role lets them do.
type StaffTelegram struct {
	UserID      string    `json:"-"`
	Name        string    `json:"-"`
	Permissions []string  `json:"-"`
	TelegramID  int64     `json:"telegram_id"`
	Username    string    `json:"username"`
	FirstName   string    `json:"first_name"`
	LinkedAt    time.Time `json:"linked_at"`
}

// Can reports whether the staff member's role grants a permission.
func (s StaffTelegram) Can(permission string) bool {
	for _, granted := range s.Permissions {
		if granted == "*" || granted == permission {
			return true
		}
	}
	return false
}

var ErrNotStaff = errors.New("not an active staff member")

const staffTelegramSelect = `
	SELECT l.user_id,u.display_name,r.permissions,l.telegram_id,l.username,l.first_name,l.linked_at
	FROM telegram_staff_links l JOIN users u ON u.id=l.user_id AND u.status='active'
	JOIN staff_members sm ON sm.user_id=l.user_id JOIN staff_roles r ON r.id=sm.role_id`

func scanStaffTelegram(row pgx.Row) (StaffTelegram, error) {
	var link StaffTelegram
	err := row.Scan(&link.UserID, &link.Name, &link.Permissions, &link.TelegramID, &link.Username, &link.FirstName, &link.LinkedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffTelegram{}, ErrTelegramNotLinked
	}
	return link, err
}

// StaffByTelegram finds the active staff member who linked a Telegram
// account.
func (s *TelegramStore) StaffByTelegram(ctx context.Context, telegramID int64) (StaffTelegram, error) {
	return scanStaffTelegram(s.db.QueryRow(ctx, staffTelegramSelect+` WHERE l.telegram_id=$1`, telegramID))
}

// StaffLink is the staff member's own link, for the admin console.
func (s *TelegramStore) StaffLink(ctx context.Context, userID string) (StaffTelegram, error) {
	return scanStaffTelegram(s.db.QueryRow(ctx, staffTelegramSelect+` WHERE l.user_id=$1`, userID))
}

// CreateStaffBindCode replaces the staff member's code for linking.
func (s *TelegramStore) CreateStaffBindCode(ctx context.Context, userID string, codeHash []byte, expiresAt time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var staff, linked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM staff_members WHERE user_id=$1), EXISTS(SELECT 1 FROM telegram_staff_links WHERE user_id=$1)`, userID).Scan(&staff, &linked); err != nil {
		return err
	}
	switch {
	case !staff:
		return ErrNotStaff
	case linked:
		return ErrTelegramAlreadyLinked
	}
	if _, err := tx.Exec(ctx, `DELETE FROM telegram_staff_codes WHERE user_id=$1 OR expires_at<now()`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO telegram_staff_codes(code_hash,user_id,expires_at) VALUES($1,$2,$3)`, codeHash, userID, expiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// BindStaff links the Telegram account to the staff member who made the
// code.
func (s *TelegramStore) BindStaff(ctx context.Context, codeHash []byte, who TelegramUser) (StaffTelegram, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return StaffTelegram{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID string
	err = tx.QueryRow(ctx, `DELETE FROM telegram_staff_codes WHERE code_hash=$1 AND expires_at>now() RETURNING user_id`, codeHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return StaffTelegram{}, ErrTelegramCodeInvalid
	}
	if err != nil {
		return StaffTelegram{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO telegram_staff_links(user_id,telegram_id,username,first_name) VALUES($1,$2,$3,$4)`, userID, who.ID, who.Username, who.FirstName); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return StaffTelegram{}, ErrTelegramTaken
		}
		return StaffTelegram{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',$1,'telegram.staff_linked','user',$1,jsonb_build_object('telegram_id',$2::bigint,'username',$3::text))`, userID, who.ID, who.Username); err != nil {
		return StaffTelegram{}, err
	}
	link, err := scanStaffTelegram(tx.QueryRow(ctx, staffTelegramSelect+` WHERE l.user_id=$1`, userID))
	if err != nil {
		return StaffTelegram{}, err
	}
	return link, tx.Commit(ctx)
}

// UnlinkStaff removes a staff member's link.
func (s *TelegramStore) UnlinkStaff(ctx context.Context, userID string) error {
	command, err := s.db.Exec(ctx, `DELETE FROM telegram_staff_links WHERE user_id=$1`, userID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrTelegramNotLinked
	}
	_, err = s.db.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id) VALUES('staff',$1,'telegram.staff_unlinked','user',$1)`, userID)
	return err
}

// ---- Tickets on Telegram ----

// TicketRef is the ticket a message of the bot is about.
type TicketRef struct {
	TicketID      string
	Number        string
	Subject       string
	Status        string
	AccountID     string
	HostAccountID string
}

// SaveMessageRef remembers that a message the bot sent is about a ticket.
func (s *TelegramStore) SaveMessageRef(ctx context.Context, chatID, messageID int64, ticketID string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO telegram_message_refs(chat_id,message_id,ticket_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, chatID, messageID, ticketID)
	return err
}

var ErrNoTicketRef = errors.New("the message is about no ticket")

// TicketByMessage is the ticket a message of the bot is about.
func (s *TelegramStore) TicketByMessage(ctx context.Context, chatID, messageID int64) (TicketRef, error) {
	return s.ticketRef(ctx, `t.id=(SELECT ticket_id FROM telegram_message_refs WHERE chat_id=$1 AND message_id=$2)`, chatID, messageID)
}

// TicketRefByID loads a ticket for the bot.
func (s *TelegramStore) TicketRefByID(ctx context.Context, ticketID string) (TicketRef, error) {
	return s.ticketRef(ctx, `t.id=$1`, ticketID)
}

func (s *TelegramStore) ticketRef(ctx context.Context, where string, args ...any) (TicketRef, error) {
	var ref TicketRef
	err := s.db.QueryRow(ctx, `SELECT t.id,t.number,t.subject,t.status,t.account_id,coalesce(t.host_account_id::text,'') FROM support_tickets t WHERE `+where, args...).
		Scan(&ref.TicketID, &ref.Number, &ref.Subject, &ref.Status, &ref.AccountID, &ref.HostAccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ref, ErrNoTicketRef
	}
	return ref, err
}

// ClaimTicket assigns a ticket to a staff member.
func (s *TelegramStore) ClaimTicket(ctx context.Context, ticketID, staffUserID string) error {
	command, err := s.db.Exec(ctx, `UPDATE support_tickets SET assigned_staff_id=$2,updated_at=now() WHERE id=$1 AND status<>'closed'`, ticketID, staffUserID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrTicketClosed
	}
	_, err = s.db.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',$1,'ticket.claimed','ticket',$2,'{"via":"telegram"}'::jsonb)`, staffUserID, ticketID)
	return err
}

// SaveTelegramRef is SaveMessageRef for the notifier, which sends ticket
// notices.
func (s *MailStore) SaveTelegramRef(ctx context.Context, chatID, messageID int64, ticketID string) error {
	return NewTelegramStore(s.db).SaveMessageRef(ctx, chatID, messageID, ticketID)
}

// ---- Red packets ----

// RedPacket is one red packet and how much of it is left.
type RedPacket struct {
	ID             string     `json:"id"`
	CreatedBy      string     `json:"created_by"`
	TotalMinor     int64      `json:"total_minor"`
	Count          int        `json:"count"`
	RemainingMinor int64      `json:"remaining_minor"`
	RemainingCount int        `json:"remaining_count"`
	Password       string     `json:"password"`
	RequireSpent   bool       `json:"require_spent"`
	MinLinkedDays  int        `json:"min_linked_days"`
	ChatID         int64      `json:"chat_id"`
	MessageID      int64      `json:"message_id"`
	Status         string     `json:"status"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	BestName       string     `json:"best_name,omitempty"`
	BestMinor      int64      `json:"best_minor,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

// RedPacketInput is a new red packet.
type RedPacketInput struct {
	CreatedBy     string
	TotalMinor    int64
	Count         int
	Password      string
	RequireSpent  bool
	MinLinkedDays int
	ExpiresAt     time.Time
}

// Red packet limits: up to 100 shares, each at least one minor unit, at
// most 10000 of the currency in all.
const (
	MaxRedPacketCount = 100
	MaxRedPacketMinor = 1_000_000
)

var (
	ErrRedPacketInvalid  = errors.New("red packet is invalid")
	ErrRedPacketPassword = errors.New("another active red packet has this password")
	ErrRedPacketGone     = errors.New("red packet is no longer active")
)

const redPacketSelect = `
	SELECT p.id,coalesce(u.display_name,''),p.total_minor,p.count,p.remaining_minor,p.remaining_count,coalesce(p.password,''),p.require_spent,p.min_linked_days,
	       p.chat_id,p.message_id,p.status,p.expires_at,p.created_at,
	       coalesce(best.name,''),coalesce(best.amount_minor,0)
	FROM telegram_red_packets p LEFT JOIN users u ON u.id=p.created_by
	LEFT JOIN LATERAL (
		SELECT coalesce(nullif(l.first_name,''),'@'||l.username,a.display_name) AS name,c.amount_minor
		FROM telegram_red_packet_claims c JOIN accounts a ON a.id=c.account_id LEFT JOIN telegram_links l ON l.telegram_id=c.telegram_id
		WHERE c.packet_id=p.id ORDER BY c.amount_minor DESC,c.created_at LIMIT 1
	) best ON true`

func scanRedPacket(row pgx.Row) (RedPacket, error) {
	var packet RedPacket
	err := row.Scan(&packet.ID, &packet.CreatedBy, &packet.TotalMinor, &packet.Count, &packet.RemainingMinor, &packet.RemainingCount, &packet.Password,
		&packet.RequireSpent, &packet.MinLinkedDays, &packet.ChatID, &packet.MessageID, &packet.Status, &packet.ExpiresAt, &packet.CreatedAt, &packet.BestName, &packet.BestMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return packet, ErrRedPacketGone
	}
	return packet, err
}

// CreateRedPacket stores a new red packet.
func (s *TelegramStore) CreateRedPacket(ctx context.Context, in RedPacketInput) (RedPacket, error) {
	in.Password = strings.TrimSpace(in.Password)
	switch {
	case in.Count < 1 || in.Count > MaxRedPacketCount, in.TotalMinor < int64(in.Count), in.TotalMinor > MaxRedPacketMinor,
		in.MinLinkedDays < 0 || in.MinLinkedDays > 365, len([]rune(in.Password)) > 32,
		in.Password != "" && len([]rune(in.Password)) < 2:
		return RedPacket{}, ErrRedPacketInvalid
	}
	var id string
	err := s.db.QueryRow(ctx, `
		INSERT INTO telegram_red_packets(created_by,total_minor,count,remaining_minor,remaining_count,password,require_spent,min_linked_days,expires_at)
		VALUES(nullif($1,'')::uuid,$2,$3,$2,$3,nullif($4,''),$5,$6,$7) RETURNING id
	`, in.CreatedBy, in.TotalMinor, in.Count, in.Password, in.RequireSpent, in.MinLinkedDays, in.ExpiresAt).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return RedPacket{}, ErrRedPacketPassword
		}
		return RedPacket{}, err
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',nullif($1,'')::uuid,'telegram.red_packet_created','red_packet',$2,jsonb_build_object('total_minor',$3::bigint,'count',$4::int))`, in.CreatedBy, id, in.TotalMinor, in.Count); err != nil {
		return RedPacket{}, err
	}
	return s.RedPacket(ctx, id)
}

func (s *TelegramStore) RedPacket(ctx context.Context, id string) (RedPacket, error) {
	return scanRedPacket(s.db.QueryRow(ctx, redPacketSelect+` WHERE p.id=$1`, id))
}

// SetRedPacketMessage remembers where the packet was posted.
func (s *TelegramStore) SetRedPacketMessage(ctx context.Context, id string, chatID, messageID int64) error {
	_, err := s.db.Exec(ctx, `UPDATE telegram_red_packets SET chat_id=$2,message_id=$3 WHERE id=$1`, id, chatID, messageID)
	return err
}

// RedPackets lists the newest red packets.
func (s *TelegramStore) RedPackets(ctx context.Context, limit int) ([]RedPacket, error) {
	rows, err := s.db.Query(ctx, redPacketSelect+` ORDER BY p.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RedPacket, 0)
	for rows.Next() {
		packet, err := scanRedPacket(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, packet)
	}
	return result, rows.Err()
}

// ActivePasswords maps the passwords of the active packets (lower case)
// to their packets.
func (s *TelegramStore) ActivePasswords(ctx context.Context, now time.Time) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `SELECT lower(password),id FROM telegram_red_packets WHERE status='active' AND password IS NOT NULL AND expires_at>$1`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var password, id string
		if err := rows.Scan(&password, &id); err != nil {
			return nil, err
		}
		result[password] = id
	}
	return result, rows.Err()
}

// CancelRedPacket stops a packet; what was not grabbed is never paid.
func (s *TelegramStore) CancelRedPacket(ctx context.Context, id, actorID string) (RedPacket, error) {
	command, err := s.db.Exec(ctx, `UPDATE telegram_red_packets SET status='cancelled' WHERE id=$1 AND status='active'`, id)
	if err != nil {
		return RedPacket{}, err
	}
	if command.RowsAffected() == 0 {
		return RedPacket{}, ErrRedPacketGone
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id) VALUES('staff',nullif($1,'')::uuid,'telegram.red_packet_cancelled','red_packet',$2)`, actorID, id); err != nil {
		return RedPacket{}, err
	}
	return s.RedPacket(ctx, id)
}

// ExpireRedPackets closes the packets whose time ran out and returns them.
func (s *TelegramStore) ExpireRedPackets(ctx context.Context, now time.Time) ([]RedPacket, error) {
	rows, err := s.db.Query(ctx, `UPDATE telegram_red_packets SET status='expired' WHERE status='active' AND expires_at<=$1 RETURNING id`, now)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]RedPacket, 0, len(ids))
	for _, id := range ids {
		packet, err := s.RedPacket(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, packet)
	}
	return result, nil
}

// Why a member did not get a share of a red packet.
const (
	RedPacketAlready   = "already"
	RedPacketEmpty     = "empty"
	RedPacketNotSpent  = "not_spent"
	RedPacketTooNew    = "too_new"
	RedPacketNotActive = "not_active"
)

// RedPacketClaim is the outcome of grabbing a red packet.
type RedPacketClaim struct {
	// Refused is one of the RedPacket* reasons, or "" when paid.
	Refused      string
	AmountMinor  int64
	BalanceMinor int64
	Packet       RedPacket
}

// ClaimRedPacket pays the member a share of the packet. pick chooses a
// share between the two bounds. Shares count towards the day's reward
// budget but are not held back by it: staff chose to give this much.
func (s *TelegramStore) ClaimRedPacket(ctx context.Context, packetID string, link TelegramLink, budget TelegramBudget, pick func(min, max int64) int64) (RedPacketClaim, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return RedPacketClaim{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := budget.reserve(ctx, tx, 0); err != nil {
		return RedPacketClaim{}, err
	}
	var packet RedPacket
	err = tx.QueryRow(ctx, `SELECT id,remaining_minor,remaining_count,require_spent,min_linked_days,status,expires_at FROM telegram_red_packets WHERE id=$1 FOR UPDATE`, packetID).
		Scan(&packet.ID, &packet.RemainingMinor, &packet.RemainingCount, &packet.RequireSpent, &packet.MinLinkedDays, &packet.Status, &packet.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RedPacketClaim{Refused: RedPacketNotActive}, nil
	}
	if err != nil {
		return RedPacketClaim{}, err
	}
	result := RedPacketClaim{}
	var already, spent bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM telegram_red_packet_claims WHERE packet_id=$1 AND (account_id=$2 OR telegram_id=$3)),
		       EXISTS(SELECT 1 FROM invoices WHERE account_id=$2 AND status='paid' AND kind<>'topup' AND total_minor>0)
	`, packetID, link.AccountID, link.TelegramID).Scan(&already, &spent); err != nil {
		return RedPacketClaim{}, err
	}
	switch {
	case already:
		result.Refused = RedPacketAlready
	case packet.Status == "finished" || packet.RemainingCount <= 0:
		result.Refused = RedPacketEmpty
	case packet.Status != "active" || !packet.ExpiresAt.After(budget.Now):
		result.Refused = RedPacketNotActive
	case packet.RequireSpent && !spent:
		result.Refused = RedPacketNotSpent
	case packet.MinLinkedDays > 0 && budget.Now.Sub(link.LinkedAt) < time.Duration(packet.MinLinkedDays)*24*time.Hour:
		result.Refused = RedPacketTooNew
	}
	if result.Refused == "" {
		amount := packet.RemainingMinor
		if packet.RemainingCount > 1 {
			// Twice the average at most, leaving every later share at
			// least one minor unit.
			most := 2 * packet.RemainingMinor / int64(packet.RemainingCount)
			if room := packet.RemainingMinor - int64(packet.RemainingCount-1); room < most {
				most = room
			}
			if most < 1 {
				most = 1
			}
			amount = pick(1, most)
			if amount < 1 || amount > most {
				amount = most
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO telegram_red_packet_claims(packet_id,account_id,telegram_id,amount_minor) VALUES($1,$2,$3,$4)`, packetID, link.AccountID, link.TelegramID, amount); err != nil {
			return RedPacketClaim{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE telegram_red_packets SET remaining_minor=remaining_minor-$2,remaining_count=remaining_count-1,
			status=CASE WHEN remaining_count-1<=0 THEN 'finished' ELSE status END WHERE id=$1`, packetID, amount); err != nil {
			return RedPacketClaim{}, err
		}
		if _, err := applyWalletChange(ctx, tx, walletChange{AccountID: link.AccountID, Kind: "reward", AmountMinor: amount, Description: "Telegram 红包", DedupKey: "tg-redpacket:" + packetID + ":" + link.AccountID}); err != nil {
			return RedPacketClaim{}, err
		}
		result.AmountMinor = amount
		if err := tx.QueryRow(ctx, `SELECT balance_minor FROM accounts WHERE id=$1`, link.AccountID).Scan(&result.BalanceMinor); err != nil {
			return RedPacketClaim{}, err
		}
	}
	if result.Packet, err = scanRedPacket(tx.QueryRow(ctx, redPacketSelect+` WHERE p.id=$1`, packetID)); err != nil {
		return RedPacketClaim{}, err
	}
	return result, tx.Commit(ctx)
}

// ---- Streaks and leaderboards ----

// StreakBonus pays bonusMinor for a streak of days reached today, once.
func streakBonus(ctx context.Context, tx pgx.Tx, link TelegramLink, day string, days int, bonusMinor int64) (bool, error) {
	command, err := tx.Exec(ctx, `INSERT INTO telegram_streak_bonuses(account_id,day,days,amount_minor) VALUES($1,$2::date,$3,$4) ON CONFLICT DO NOTHING`, link.AccountID, day, days, bonusMinor)
	if err != nil || command.RowsAffected() == 0 {
		return false, err
	}
	_, err = applyWalletChange(ctx, tx, walletChange{AccountID: link.AccountID, Kind: "reward", AmountMinor: bonusMinor,
		Description: fmt.Sprintf("Telegram 连续签到 %d 天奖励", days), DedupKey: fmt.Sprintf("tg-streak:%s:%s:%d", link.AccountID, day, days)})
	return err == nil, err
}

// Leaderboards.
const (
	BoardCheckins = "checkins"
	BoardInvites  = "invites"
)

// BoardEntry is one place on a weekly leaderboard.
type BoardEntry struct {
	Rank       int
	AccountID  string
	Name       string
	TelegramID int64
	Count      int
	PrizeMinor int64
}

// WeekStart is the Monday (UTC+8) of the week a moment is in.
func WeekStart(now time.Time) time.Time {
	local := now.In(clock.Zone)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, clock.Zone)
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

// WeeklyBoard ranks accounts by check-ins or rewarded invitations in the
// week from start; who got there first ranks higher on a tie.
func (s *TelegramStore) WeeklyBoard(ctx context.Context, board string, start time.Time, limit int) ([]BoardEntry, error) {
	var query string
	switch board {
	case BoardCheckins:
		query = `SELECT c.account_id,count(*),max(c.created_at) FROM telegram_checkins c WHERE c.day>=$1::date AND c.day<$1::date+7 AND $2::timestamptz IS NOT NULL GROUP BY c.account_id`
	case BoardInvites:
		query = `SELECT i.inviter_account_id,count(*),max(i.rewarded_at) FROM telegram_invites i WHERE i.status='rewarded' AND i.rewarded_at>=$2::timestamptz AND i.rewarded_at<$2::timestamptz+interval '7 days' AND $1::date IS NOT NULL GROUP BY i.inviter_account_id`
	default:
		return nil, errors.New("unknown board")
	}
	rows, err := s.db.Query(ctx, `
		SELECT b.account_id,coalesce(nullif(l.first_name,''),'@'||nullif(l.username,''),a.display_name),coalesce(l.telegram_id,0),b.count
		FROM (`+query+`) b(account_id,count,last) JOIN accounts a ON a.id=b.account_id
		LEFT JOIN LATERAL (SELECT * FROM telegram_links WHERE account_id=b.account_id ORDER BY linked_at LIMIT 1) l ON true
		ORDER BY b.count DESC,b.last LIMIT $3
	`, start.Format("2006-01-02"), start, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]BoardEntry, 0)
	for rows.Next() {
		entry := BoardEntry{Rank: len(result) + 1}
		if err := rows.Scan(&entry.AccountID, &entry.Name, &entry.TelegramID, &entry.Count); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

// AwardBoard marks a week's board as posted and pays its prizes by rank.
// It reports false when the board was done before. Prizes count towards
// the day's reward budget but are not held back by it.
func (s *TelegramStore) AwardBoard(ctx context.Context, board string, week time.Time, entries []BoardEntry, prizes []int64, budget TelegramBudget) ([]BoardEntry, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	day := week.Format("2006-01-02")
	command, err := tx.Exec(ctx, `INSERT INTO telegram_leaderboards(week,board) VALUES($1::date,$2) ON CONFLICT DO NOTHING`, day, board)
	if err != nil || command.RowsAffected() == 0 {
		return nil, false, err
	}
	if _, err := budget.reserve(ctx, tx, 0); err != nil {
		return nil, false, err
	}
	awarded := make([]BoardEntry, len(entries))
	copy(awarded, entries)
	for i, entry := range awarded {
		if i >= len(prizes) || prizes[i] <= 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO telegram_leaderboard_prizes(week,board,rank,account_id,count,amount_minor) VALUES($1::date,$2,$3,$4,$5,$6)`, day, board, entry.Rank, entry.AccountID, entry.Count, prizes[i]); err != nil {
			return nil, false, err
		}
		boardName := map[string]string{BoardCheckins: "签到榜", BoardInvites: "邀请榜"}[board]
		if _, err := applyWalletChange(ctx, tx, walletChange{AccountID: entry.AccountID, Kind: "reward", AmountMinor: prizes[i],
			Description: fmt.Sprintf("Telegram %s %s 第 %d 名奖励", day, boardName, entry.Rank), DedupKey: fmt.Sprintf("tg-board:%s:%s:%d", day, board, entry.Rank)}); err != nil {
			return nil, false, err
		}
		awarded[i].PrizeMinor = prizes[i]
	}
	return awarded, true, tx.Commit(ctx)
}

// ---- Joining the group ----

// Verification is a new member who must pass the group's check.
type Verification struct {
	TelegramID int64
	ChatID     int64
	MessageID  int64
	Deadline   time.Time
}

// AddVerification starts or restarts a member's check.
func (s *TelegramStore) AddVerification(ctx context.Context, v Verification) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO telegram_verifications(telegram_id,chat_id,message_id,deadline) VALUES($1,$2,$3,$4)
		ON CONFLICT (telegram_id) DO UPDATE SET chat_id=EXCLUDED.chat_id,message_id=EXCLUDED.message_id,deadline=EXCLUDED.deadline,created_at=now()
	`, v.TelegramID, v.ChatID, v.MessageID, v.Deadline)
	return err
}

// TakeVerification ends a member's check, returning it; ok is false when
// there was none.
func (s *TelegramStore) TakeVerification(ctx context.Context, telegramID int64) (Verification, bool, error) {
	v := Verification{TelegramID: telegramID}
	err := s.db.QueryRow(ctx, `DELETE FROM telegram_verifications WHERE telegram_id=$1 RETURNING chat_id,message_id,deadline`, telegramID).Scan(&v.ChatID, &v.MessageID, &v.Deadline)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}

// PendingVerification reports whether a member still has to pass.
func (s *TelegramStore) PendingVerification(ctx context.Context, telegramID int64) (bool, error) {
	var pending bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telegram_verifications WHERE telegram_id=$1)`, telegramID).Scan(&pending)
	return pending, err
}

// OverdueVerifications ends the checks whose time ran out and returns them.
func (s *TelegramStore) OverdueVerifications(ctx context.Context, now time.Time) ([]Verification, error) {
	rows, err := s.db.Query(ctx, `DELETE FROM telegram_verifications WHERE deadline<=$1 RETURNING telegram_id,chat_id,message_id,deadline`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Verification
	for rows.Next() {
		var v Verification
		if err := rows.Scan(&v.TelegramID, &v.ChatID, &v.MessageID, &v.Deadline); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
