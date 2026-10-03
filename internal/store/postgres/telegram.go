package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"vpsbill/internal/clock"
)

var (
	ErrTelegramCodeInvalid   = errors.New("telegram bind code is invalid or expired")
	ErrTelegramTaken         = errors.New("telegram account is linked to another user")
	ErrTelegramAlreadyLinked = errors.New("user already linked a telegram account")
	ErrTelegramNotLinked     = errors.New("telegram account is not linked")
	ErrTelegramCooldown      = errors.New("telegram link is too recent to remove")
	ErrEmailUnverified       = errors.New("email address is not verified")
)

// TelegramUnlinkCooldown is how long a link must stand before it may be
// removed, so one Telegram account cannot hop between site accounts.
const TelegramUnlinkCooldown = 7 * 24 * time.Hour

// telegramInviteExpiry voids invitations that never met the conditions.
const telegramInviteExpiry = 30 * 24 * time.Hour

// telegramRewardLock serializes reward payments, so the daily budget
// cannot be overspent by rewards paid at the same moment.
const telegramRewardLock = 74210946

type TelegramStore struct {
	db *pgxpool.Pool
}

func NewTelegramStore(db *pgxpool.Pool) *TelegramStore {
	return &TelegramStore{db: db}
}

// TelegramUser is who the bot is talking to.
type TelegramUser struct {
	ID        int64
	Username  string
	FirstName string
}

// TelegramLink is a site user's linked Telegram account.
type TelegramLink struct {
	UserID      string    `json:"-"`
	AccountID   string    `json:"-"`
	TelegramID  int64     `json:"telegram_id"`
	Username    string    `json:"username"`
	FirstName   string    `json:"first_name"`
	LinkedAt    time.Time `json:"linked_at"`
	Email       string    `json:"-"`
	DisplayName string    `json:"-"`
	Locale      string    `json:"-"`
}

const telegramLinkSelect = `
	SELECT l.user_id,l.account_id,l.telegram_id,l.username,l.first_name,l.linked_at,u.email,u.display_name,u.locale
	FROM telegram_links l JOIN users u ON u.id=l.user_id`

func scanTelegramLink(row pgx.Row) (TelegramLink, error) {
	var link TelegramLink
	err := row.Scan(&link.UserID, &link.AccountID, &link.TelegramID, &link.Username, &link.FirstName, &link.LinkedAt, &link.Email, &link.DisplayName, &link.Locale)
	if errors.Is(err, pgx.ErrNoRows) {
		return TelegramLink{}, ErrTelegramNotLinked
	}
	return link, err
}

func (s *TelegramStore) LinkByTelegram(ctx context.Context, telegramID int64) (TelegramLink, error) {
	return scanTelegramLink(s.db.QueryRow(ctx, telegramLinkSelect+` WHERE l.telegram_id=$1`, telegramID))
}

func (s *TelegramStore) LinkByUser(ctx context.Context, userID string) (TelegramLink, error) {
	return scanTelegramLink(s.db.QueryRow(ctx, telegramLinkSelect+` WHERE l.user_id=$1`, userID))
}

// LinkByAccount is the link of the account's owner.
func (s *TelegramStore) LinkByAccount(ctx context.Context, accountID string) (TelegramLink, error) {
	return scanTelegramLink(s.db.QueryRow(ctx, telegramLinkSelect+` WHERE l.account_id=$1 ORDER BY l.linked_at LIMIT 1`, accountID))
}

// Balance is the account's balance in minor units.
func (s *TelegramStore) Balance(ctx context.Context, accountID string) (int64, error) {
	var balance int64
	err := s.db.QueryRow(ctx, `SELECT balance_minor FROM accounts WHERE id=$1`, accountID).Scan(&balance)
	return balance, err
}

// CreateBindCode replaces the user's bind code. Only verified addresses
// may link, so rewards cannot be farmed with throwaway sign-ups.
func (s *TelegramStore) CreateBindCode(ctx context.Context, userID string, codeHash []byte, expiresAt time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var verified, linked bool
	if err := tx.QueryRow(ctx, `SELECT email_verified_at IS NOT NULL, EXISTS(SELECT 1 FROM telegram_links WHERE user_id=$1) FROM users WHERE id=$1`, userID).Scan(&verified, &linked); err != nil {
		return err
	}
	switch {
	case linked:
		return ErrTelegramAlreadyLinked
	case !verified:
		return ErrEmailUnverified
	}
	if _, err := tx.Exec(ctx, `DELETE FROM telegram_bind_codes WHERE user_id=$1 OR expires_at<now()`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO telegram_bind_codes(code_hash,user_id,expires_at) VALUES($1,$2,$3)`, codeHash, userID, expiresAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TelegramBudget limits what rewards may cost in one UTC+8 day; 0 means no
// limit.
type TelegramBudget struct {
	DailyMinor int64
	Now        time.Time
}

func (b TelegramBudget) dayStart() time.Time {
	local := b.Now.In(clock.Zone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, clock.Zone)
}

// reserve locks reward payments for this transaction and reports whether
// amount still fits in today's budget.
func (b TelegramBudget) reserve(ctx context.Context, tx pgx.Tx, amount int64) (bool, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(telegramRewardLock)); err != nil {
		return false, err
	}
	if b.DailyMinor <= 0 {
		return true, nil
	}
	var spent int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(sum(amount_minor),0) FROM wallet_entries WHERE kind='reward' AND created_at>=$1`, b.dayStart()).Scan(&spent); err != nil {
		return false, err
	}
	return spent+amount <= b.DailyMinor, nil
}

// TelegramBindResult is a new link and the bonus paid for it (0 when the
// bonus was paid before, is off or did not fit in today's budget).
type TelegramBindResult struct {
	Link         TelegramLink
	BonusMinor   int64
	BalanceMinor int64
}

// Bind links the Telegram account to the user who made the code.
func (s *TelegramStore) Bind(ctx context.Context, codeHash []byte, who TelegramUser, bonusMinor int64, budget TelegramBudget) (TelegramBindResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TelegramBindResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID string
	err = tx.QueryRow(ctx, `DELETE FROM telegram_bind_codes WHERE code_hash=$1 AND expires_at>now() RETURNING user_id`, codeHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TelegramBindResult{}, ErrTelegramCodeInvalid
	}
	if err != nil {
		return TelegramBindResult{}, err
	}
	var accountID string
	var verified bool
	err = tx.QueryRow(ctx, `
		SELECT m.account_id,u.email_verified_at IS NOT NULL
		FROM users u JOIN memberships m ON m.user_id=u.id JOIN accounts a ON a.id=m.account_id
		WHERE u.id=$1 AND u.status='active' AND a.status='active'
		ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END,m.created_at LIMIT 1
	`, userID).Scan(&accountID, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return TelegramBindResult{}, ErrTelegramCodeInvalid
	}
	if err != nil {
		return TelegramBindResult{}, err
	}
	if !verified {
		return TelegramBindResult{}, ErrEmailUnverified
	}
	var taken, linked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telegram_links WHERE telegram_id=$1), EXISTS(SELECT 1 FROM telegram_links WHERE user_id=$2)`, who.ID, userID).Scan(&taken, &linked); err != nil {
		return TelegramBindResult{}, err
	}
	switch {
	case linked:
		return TelegramBindResult{}, ErrTelegramAlreadyLinked
	case taken:
		return TelegramBindResult{}, ErrTelegramTaken
	}
	if _, err := tx.Exec(ctx, `INSERT INTO telegram_links(user_id,account_id,telegram_id,username,first_name) VALUES($1,$2,$3,$4,$5)`, userID, accountID, who.ID, who.Username, who.FirstName); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return TelegramBindResult{}, ErrTelegramTaken
		}
		return TelegramBindResult{}, err
	}
	result := TelegramBindResult{}
	if bonusMinor > 0 {
		fits, err := budget.reserve(ctx, tx, bonusMinor)
		if err != nil {
			return TelegramBindResult{}, err
		}
		if fits {
			command, err := tx.Exec(ctx, `INSERT INTO telegram_bind_rewards(telegram_id,account_id,amount_minor) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, who.ID, accountID, bonusMinor)
			if err != nil {
				return TelegramBindResult{}, err
			}
			if command.RowsAffected() == 1 {
				if _, err := applyWalletChange(ctx, tx, walletChange{AccountID: accountID, Kind: "reward", AmountMinor: bonusMinor, Description: "绑定 Telegram 奖励", DedupKey: fmt.Sprintf("tg-bind:%d", who.ID)}); err != nil {
					return TelegramBindResult{}, err
				}
				result.BonusMinor = bonusMinor
			}
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('customer',$1,'telegram.linked','user',$1,jsonb_build_object('telegram_id',$2::bigint,'username',$3::text))`, userID, who.ID, who.Username); err != nil {
		return TelegramBindResult{}, err
	}
	// A member who joined through an invite link before linking counts for
	// the inviter from now on (see SettleInvites).
	if result.Link, err = scanTelegramLink(tx.QueryRow(ctx, telegramLinkSelect+` WHERE l.user_id=$1`, userID)); err != nil {
		return TelegramBindResult{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT balance_minor FROM accounts WHERE id=$1`, accountID).Scan(&result.BalanceMinor); err != nil {
		return TelegramBindResult{}, err
	}
	return result, tx.Commit(ctx)
}

// Unlink removes a user's Telegram link once it is old enough.
func (s *TelegramStore) Unlink(ctx context.Context, userID string, now time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var linkedAt time.Time
	var telegramID int64
	err = tx.QueryRow(ctx, `SELECT linked_at,telegram_id FROM telegram_links WHERE user_id=$1 FOR UPDATE`, userID).Scan(&linkedAt, &telegramID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTelegramNotLinked
	}
	if err != nil {
		return err
	}
	if now.Sub(linkedAt) < TelegramUnlinkCooldown {
		return ErrTelegramCooldown
	}
	if _, err := tx.Exec(ctx, `DELETE FROM telegram_links WHERE user_id=$1`, userID); err != nil {
		return err
	}
	// Notices that went to Telegram go to the mailbox again.
	if _, err := tx.Exec(ctx, `UPDATE users SET notify_email=true,notify_telegram=false WHERE id=$1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('customer',$1,'telegram.unlinked','user',$1,jsonb_build_object('telegram_id',$2::bigint))`, userID, telegramID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NotifyChannels is where a user's notices go.
type NotifyChannels struct {
	Email    bool `json:"email"`
	Telegram bool `json:"telegram"`
}

var ErrNotifyChannels = errors.New("at least one notification channel is required")

func (s *TelegramStore) NotifyChannels(ctx context.Context, userID string) (NotifyChannels, error) {
	var channels NotifyChannels
	err := s.db.QueryRow(ctx, `SELECT notify_email,notify_telegram FROM users WHERE id=$1`, userID).Scan(&channels.Email, &channels.Telegram)
	return channels, err
}

// SetNotifyChannels stores a user's choice: at least one channel, and
// Telegram only with a linked account.
func (s *TelegramStore) SetNotifyChannels(ctx context.Context, userID string, channels NotifyChannels) error {
	if !channels.Email && !channels.Telegram {
		return ErrNotifyChannels
	}
	command, err := s.db.Exec(ctx, `
		UPDATE users SET notify_email=$2,notify_telegram=$3,updated_at=now()
		WHERE id=$1 AND (NOT $3 OR EXISTS(SELECT 1 FROM telegram_links l WHERE l.user_id=$1))
	`, userID, channels.Email, channels.Telegram)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrTelegramNotLinked
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('customer',$1,'notifications.channels_changed','user',$1,jsonb_build_object('email',$2::boolean,'telegram',$3::boolean))`, userID, channels.Email, channels.Telegram); err != nil {
		return err
	}
	return nil
}

// TelegramCheckin is the outcome of a check-in.
type TelegramCheckin struct {
	// Already is set when today's check-in was made before.
	Already bool
	// Exhausted is set when today's reward budget is spent.
	Exhausted    bool
	AmountMinor  int64
	BalanceMinor int64
	// Streak counts consecutive days checked in, today included.
	Streak int
	// BonusMinor is what today's streak paid on top, for BonusDays in a
	// row (0 when nothing).
	BonusMinor int64
	BonusDays  int
}

// StreakRule pays AmountMinor when a check-in makes a streak of Days, or
// of a multiple of it.
type StreakRule struct {
	Days        int
	AmountMinor int64
}

// Checkin pays today's check-in reward to the linked account, once per
// site account and Telegram account a day.
func (s *TelegramStore) Checkin(ctx context.Context, link TelegramLink, amountMinor int64, budget TelegramBudget, streaks ...StreakRule) (TelegramCheckin, error) {
	day := budget.Now.In(clock.Zone).Format("2006-01-02")
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TelegramCheckin{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result := TelegramCheckin{}
	fits, err := budget.reserve(ctx, tx, amountMinor)
	if err != nil {
		return TelegramCheckin{}, err
	}
	var done bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telegram_checkins WHERE day=$1::date AND (account_id=$2 OR telegram_id=$3))`, day, link.AccountID, link.TelegramID).Scan(&done); err != nil {
		return TelegramCheckin{}, err
	}
	switch {
	case done:
		result.Already = true
	case !fits || amountMinor <= 0:
		result.Exhausted = true
	default:
		if _, err := tx.Exec(ctx, `INSERT INTO telegram_checkins(account_id,day,telegram_id,amount_minor) VALUES($1,$2::date,$3,$4)`, link.AccountID, day, link.TelegramID, amountMinor); err != nil {
			return TelegramCheckin{}, err
		}
		if _, err := applyWalletChange(ctx, tx, walletChange{AccountID: link.AccountID, Kind: "reward", AmountMinor: amountMinor, Description: "Telegram 群签到奖励（" + day + "）", DedupKey: "tg-checkin:" + link.AccountID + ":" + day}); err != nil {
			return TelegramCheckin{}, err
		}
		result.AmountMinor = amountMinor
	}
	if err := tx.QueryRow(ctx, `SELECT balance_minor FROM accounts WHERE id=$1`, link.AccountID).Scan(&result.BalanceMinor); err != nil {
		return TelegramCheckin{}, err
	}
	// Days in a row up to today: each earlier day is one further back.
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT day, row_number() OVER (ORDER BY day DESC) AS position
			FROM telegram_checkins WHERE account_id=$1 AND day<=$2::date AND day>$2::date-400
		) recent WHERE day=$2::date-(position-1)::int
	`, link.AccountID, day).Scan(&result.Streak); err != nil {
		return TelegramCheckin{}, err
	}
	// A streak bonus for the longest rule today's streak reaches, when the
	// check-in itself was paid.
	if result.AmountMinor > 0 {
		best := StreakRule{}
		for _, rule := range streaks {
			if rule.Days > 1 && rule.AmountMinor > 0 && result.Streak%rule.Days == 0 && rule.Days > best.Days {
				best = rule
			}
		}
		if best.Days > 0 {
			fits, err := budget.reserve(ctx, tx, best.AmountMinor)
			if err != nil {
				return TelegramCheckin{}, err
			}
			if fits {
				paid, err := streakBonus(ctx, tx, link, day, best.Days, best.AmountMinor)
				if err != nil {
					return TelegramCheckin{}, err
				}
				if paid {
					result.BonusMinor, result.BonusDays = best.AmountMinor, best.Days
					result.BalanceMinor += best.AmountMinor
				}
			}
		}
	}
	return result, tx.Commit(ctx)
}

// InviteLink is the account's own invite link to the chat, or "".
func (s *TelegramStore) InviteLink(ctx context.Context, accountID string, chatID int64) (string, error) {
	var link string
	err := s.db.QueryRow(ctx, `SELECT invite_link FROM telegram_invite_links WHERE account_id=$1 AND chat_id=$2`, accountID, chatID).Scan(&link)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return link, err
}

// SaveInviteLink keeps the account's invite link, replacing one made for
// another chat.
func (s *TelegramStore) SaveInviteLink(ctx context.Context, accountID string, chatID int64, link string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO telegram_invite_links(account_id,chat_id,invite_link) VALUES($1,$2,$3)
		ON CONFLICT (account_id) DO UPDATE SET chat_id=EXCLUDED.chat_id,invite_link=EXCLUDED.invite_link,created_at=now()
	`, accountID, chatID, link)
	return err
}

// MemberJoined records someone entering the group. It reports true when
// that started an invitation: the member was never in the group before and
// came through another account's invite link.
func (s *TelegramStore) MemberJoined(ctx context.Context, who TelegramUser, inviteLink string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `INSERT INTO telegram_members(telegram_id) VALUES($1) ON CONFLICT DO NOTHING`, who.ID)
	if err != nil {
		return false, err
	}
	first := command.RowsAffected() == 1
	if !first {
		if _, err := tx.Exec(ctx, `UPDATE telegram_members SET in_group=true,updated_at=now() WHERE telegram_id=$1`, who.ID); err != nil {
			return false, err
		}
		// Coming back does not revive a voided invitation, but one that
		// is still pending counts again.
		if _, err := tx.Exec(ctx, `UPDATE telegram_invites SET left_at=NULL WHERE invitee_telegram_id=$1 AND status='pending'`, who.ID); err != nil {
			return false, err
		}
	}
	invited := false
	if first && inviteLink != "" {
		var inviter string
		err := tx.QueryRow(ctx, `SELECT account_id FROM telegram_invite_links WHERE invite_link=$1`, inviteLink).Scan(&inviter)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
		if err == nil {
			// Inviting one's own Telegram account earns nothing.
			var own bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telegram_links WHERE telegram_id=$1 AND account_id=$2)`, who.ID, inviter).Scan(&own); err != nil {
				return false, err
			}
			if !own {
				name := who.FirstName
				if who.Username != "" {
					name = "@" + who.Username
				}
				command, err := tx.Exec(ctx, `INSERT INTO telegram_invites(invitee_telegram_id,inviter_account_id,invitee_name) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, who.ID, inviter, name)
				if err != nil {
					return false, err
				}
				invited = command.RowsAffected() == 1
			}
		}
	}
	return invited, tx.Commit(ctx)
}

// MemberLeft records someone leaving the group; an invitation that was
// not rewarded yet is void.
func (s *TelegramStore) MemberLeft(ctx context.Context, telegramID int64) error {
	_, err := s.db.Exec(ctx, `
		WITH gone AS (
			INSERT INTO telegram_members(telegram_id,in_group) VALUES($1,false)
			ON CONFLICT (telegram_id) DO UPDATE SET in_group=false,updated_at=now()
		)
		UPDATE telegram_invites SET left_at=now(),status='void',void_reason='left' WHERE invitee_telegram_id=$1 AND status='pending'
	`, telegramID)
	return err
}

// TelegramInviteRules are the conditions an invitation is rewarded under.
type TelegramInviteRules struct {
	RewardMinor int64
	// Hold is how long the member must have stayed in the group.
	Hold time.Duration
	// RequireLink asks the member to link a verified site account too.
	RequireLink bool
	// DailyCap is how many invitations one account is paid for per day
	// (0 = no cap); the rest wait for the next day.
	DailyCap int
}

// SettledInvite is one invitation that was just rewarded.
type SettledInvite struct {
	InviterAccountID string
	InviteeName      string
	RewardMinor      int64
}

// SettleInvites rewards the invitations that now meet the rules and voids
// the ones that never did.
func (s *TelegramStore) SettleInvites(ctx context.Context, rules TelegramInviteRules, budget TelegramBudget) ([]SettledInvite, error) {
	if _, err := s.db.Exec(ctx, `UPDATE telegram_invites SET status='void',void_reason='expired' WHERE status='pending' AND joined_at<$1`, budget.Now.Add(-telegramInviteExpiry)); err != nil {
		return nil, err
	}
	if rules.RewardMinor <= 0 {
		return nil, nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT i.invitee_telegram_id,i.inviter_account_id,i.invitee_name,coalesce(l.account_id::text,''),
		       coalesce(l.account_id=i.inviter_account_id,false),
		       EXISTS(SELECT 1 FROM telegram_invites other WHERE other.invitee_account_id=l.account_id AND other.invitee_telegram_id<>i.invitee_telegram_id)
		FROM telegram_invites i
		JOIN accounts inviter ON inviter.id=i.inviter_account_id AND inviter.status='active'
		LEFT JOIN telegram_links l ON l.telegram_id=i.invitee_telegram_id
		LEFT JOIN users u ON u.id=l.user_id
		WHERE i.status='pending' AND i.left_at IS NULL AND i.joined_at<=$1
		  AND ($2::boolean=false OR (l.user_id IS NOT NULL AND u.email_verified_at IS NOT NULL))
		ORDER BY i.joined_at LIMIT 200
		FOR UPDATE OF i SKIP LOCKED
	`, budget.Now.Add(-rules.Hold), rules.RequireLink)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		invitee              int64
		inviter, name, owner string
		self, counted        bool
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.invitee, &item.inviter, &item.name, &item.owner, &item.self, &item.counted); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	paidToday := map[string]int{}
	var settled []SettledInvite
	for _, item := range candidates {
		void := func(reason string) error {
			_, err := tx.Exec(ctx, `UPDATE telegram_invites SET status='void',void_reason=$2 WHERE invitee_telegram_id=$1`, item.invitee, reason)
			return err
		}
		if item.self {
			if err := void("self"); err != nil {
				return nil, err
			}
			continue
		}
		if item.counted {
			// The member's site account already counted under another
			// Telegram account.
			if err := void("account_counted"); err != nil {
				return nil, err
			}
			continue
		}
		if rules.DailyCap > 0 {
			count, ok := paidToday[item.inviter]
			if !ok {
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM telegram_invites WHERE inviter_account_id=$1 AND status='rewarded' AND rewarded_at>=$2`, item.inviter, budget.dayStart()).Scan(&count); err != nil {
					return nil, err
				}
			}
			if count >= rules.DailyCap {
				paidToday[item.inviter] = count
				continue
			}
			paidToday[item.inviter] = count + 1
		}
		fits, err := budget.reserve(ctx, tx, rules.RewardMinor)
		if err != nil {
			return nil, err
		}
		if !fits {
			break
		}
		if _, err := tx.Exec(ctx, `UPDATE telegram_invites SET status='rewarded',reward_minor=$2,rewarded_at=$3,invitee_account_id=nullif($4,'')::uuid WHERE invitee_telegram_id=$1`, item.invitee, rules.RewardMinor, budget.Now, item.owner); err != nil {
			return nil, err
		}
		if _, err := applyWalletChange(ctx, tx, walletChange{AccountID: item.inviter, Kind: "reward", AmountMinor: rules.RewardMinor, Description: "邀请 " + item.name + " 加入 Telegram 群奖励", DedupKey: fmt.Sprintf("tg-invite:%d", item.invitee)}); err != nil {
			return nil, err
		}
		settled = append(settled, SettledInvite{InviterAccountID: item.inviter, InviteeName: item.name, RewardMinor: rules.RewardMinor})
	}
	return settled, tx.Commit(ctx)
}

// TelegramAccountStats is what one account earned through Telegram.
type TelegramAccountStats struct {
	Checkins        int   `json:"checkins"`
	CheckedInToday  bool  `json:"checked_in_today"`
	InvitesRewarded int   `json:"invites_rewarded"`
	InvitesPending  int   `json:"invites_pending"`
	EarnedMinor     int64 `json:"earned_minor"`
}

func (s *TelegramStore) AccountStats(ctx context.Context, accountID string, now time.Time) (TelegramAccountStats, error) {
	var stats TelegramAccountStats
	err := s.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM telegram_checkins WHERE account_id=$1),
		       EXISTS(SELECT 1 FROM telegram_checkins WHERE account_id=$1 AND day=$2::date),
		       (SELECT count(*) FROM telegram_invites WHERE inviter_account_id=$1 AND status='rewarded'),
		       (SELECT count(*) FROM telegram_invites WHERE inviter_account_id=$1 AND status='pending'),
		       (SELECT coalesce(sum(amount_minor),0) FROM wallet_entries WHERE account_id=$1 AND kind='reward')
	`, accountID, now.In(clock.Zone).Format("2006-01-02")).Scan(&stats.Checkins, &stats.CheckedInToday, &stats.InvitesRewarded, &stats.InvitesPending, &stats.EarnedMinor)
	return stats, err
}

// TelegramSiteStats sums up the activity for the admin console.
type TelegramSiteStats struct {
	Links           int   `json:"links"`
	CheckinsToday   int   `json:"checkins_today"`
	RewardedToday   int64 `json:"rewarded_today_minor"`
	RewardedTotal   int64 `json:"rewarded_total_minor"`
	InvitesRewarded int   `json:"invites_rewarded"`
	InvitesPending  int   `json:"invites_pending"`
	Members         int   `json:"members"`
}

func (s *TelegramStore) SiteStats(ctx context.Context, now time.Time) (TelegramSiteStats, error) {
	var stats TelegramSiteStats
	budget := TelegramBudget{Now: now}
	err := s.db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM telegram_links),
		       (SELECT count(*) FROM telegram_checkins WHERE day=$1::date),
		       (SELECT coalesce(sum(amount_minor),0) FROM wallet_entries WHERE kind='reward' AND created_at>=$2),
		       (SELECT coalesce(sum(amount_minor),0) FROM wallet_entries WHERE kind='reward'),
		       (SELECT count(*) FROM telegram_invites WHERE status='rewarded'),
		       (SELECT count(*) FROM telegram_invites WHERE status='pending'),
		       (SELECT count(*) FROM telegram_members WHERE in_group)
	`, now.In(clock.Zone).Format("2006-01-02"), budget.dayStart()).Scan(&stats.Links, &stats.CheckinsToday, &stats.RewardedToday, &stats.RewardedTotal, &stats.InvitesRewarded, &stats.InvitesPending, &stats.Members)
	return stats, err
}

// UpdateOffset is the next Telegram update the bot asks for; it is kept so
// a restart does not handle an update twice.
func (s *TelegramStore) UpdateOffset(ctx context.Context) (int64, error) {
	var offset int64
	err := s.db.QueryRow(ctx, `SELECT telegram_update_offset FROM system_settings WHERE singleton=true`).Scan(&offset)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return offset, err
}

func (s *TelegramStore) SaveUpdateOffset(ctx context.Context, offset int64) error {
	_, err := s.db.Exec(ctx, `UPDATE system_settings SET telegram_update_offset=$1 WHERE singleton=true`, offset)
	return err
}

// PollLock makes one server instance the bot: Telegram allows a single
// reader of a bot's updates. The returned function gives the lock back.
func (s *TelegramStore) PollLock(ctx context.Context) (func(), bool, error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, int64(telegramRewardLock+1)).Scan(&locked); err != nil || !locked {
		conn.Release()
		return nil, false, err
	}
	return func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, int64(telegramRewardLock+1))
		conn.Release()
	}, true, nil
}
