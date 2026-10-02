package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the database the store works on, for the other stores the bot
// needs.
func (s *TelegramStore) Pool() *pgxpool.Pool { return s.db }

// PayableInvoice is an open invoice a customer may pay from the balance,
// as the bot shows it before asking for confirmation.
type PayableInvoice struct {
	ID           string
	Number       string
	Kind         string
	Currency     string
	AmountMinor  int64
	BalanceMinor int64
	// InstanceName and PeriodEnd are set for renewals.
	InstanceName string
	PeriodEnd    *time.Time
}

// PayableInvoice loads an account's open invoice; top-ups are not paid
// from the balance.
func (s *TelegramStore) PayableInvoice(ctx context.Context, accountID, invoiceID string) (PayableInvoice, error) {
	var invoice PayableInvoice
	err := s.db.QueryRow(ctx, `
		SELECT i.id,i.number,i.kind,i.currency,i.balance_minor,a.balance_minor,coalesce(sv.instance_name,''),i.period_end
		FROM invoices i JOIN accounts a ON a.id=i.account_id LEFT JOIN services sv ON sv.id=i.service_id
		WHERE i.id=$1 AND i.account_id=$2 AND i.status='open' AND i.kind<>'topup' AND i.balance_minor>0
	`, invoiceID, accountID).Scan(&invoice.ID, &invoice.Number, &invoice.Kind, &invoice.Currency, &invoice.AmountMinor, &invoice.BalanceMinor, &invoice.InstanceName, &invoice.PeriodEnd)
	if errors.Is(err, pgx.ErrNoRows) {
		return invoice, ErrInvoiceUnavailable
	}
	return invoice, err
}

// PayableInvoices lists an account's open invoices that the balance can
// pay, soonest due first.
func (s *TelegramStore) PayableInvoices(ctx context.Context, accountID string) ([]PayableInvoice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT i.id,i.number,i.kind,i.currency,i.balance_minor,a.balance_minor,coalesce(sv.instance_name,''),i.period_end
		FROM invoices i JOIN accounts a ON a.id=i.account_id LEFT JOIN services sv ON sv.id=i.service_id
		WHERE i.account_id=$1 AND i.status='open' AND i.kind<>'topup' AND i.balance_minor>0
		ORDER BY i.due_at LIMIT 10
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PayableInvoice, 0)
	for rows.Next() {
		var invoice PayableInvoice
		if err := rows.Scan(&invoice.ID, &invoice.Number, &invoice.Kind, &invoice.Currency, &invoice.AmountMinor, &invoice.BalanceMinor, &invoice.InstanceName, &invoice.PeriodEnd); err != nil {
			return nil, err
		}
		result = append(result, invoice)
	}
	return result, rows.Err()
}

// TelegramRebateRules say what an inviter gets for an invited member's
// first real payment.
type TelegramRebateRules struct {
	Percent int
	// MaxMinor caps one rebate (0 = no cap).
	MaxMinor int64
	// Delay is how old the payment must be, to outlast refunds.
	Delay time.Duration
}

// SettledRebate is a rebate that was just paid.
type SettledRebate struct {
	InviterAccountID string
	InviteeName      string
	PaidMinor        int64
	RebateMinor      int64
}

// SettleRebates pays inviters for the first payment of each member they
// invited: a payment through the gateway or confirmed by staff, made after
// the member joined. Balance payments do not count, so rewards cannot earn
// rewards. Each invited site account counts once.
func (s *TelegramStore) SettleRebates(ctx context.Context, rules TelegramRebateRules, budget TelegramBudget) ([]SettledRebate, error) {
	if rules.Percent <= 0 {
		return nil, nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT i.inviter_account_id,l.account_id,i.invitee_name,t.id,t.amount_minor
		FROM telegram_invites i
		JOIN telegram_links l ON l.telegram_id=i.invitee_telegram_id
		JOIN accounts inviter ON inviter.id=i.inviter_account_id AND inviter.status='active'
		JOIN LATERAL (
			SELECT p.id,p.amount_minor,p.created_at FROM transactions p
			WHERE p.account_id=l.account_id AND p.type='payment' AND p.status='succeeded' AND p.provider<>'balance' AND p.amount_minor>0
			ORDER BY p.created_at LIMIT 1
		) t ON true
		WHERE i.status<>'void' AND l.account_id<>i.inviter_account_id
		  AND t.created_at>=i.joined_at AND t.created_at<=$1
		  AND NOT EXISTS(SELECT 1 FROM telegram_rebates r WHERE r.invitee_account_id=l.account_id)
		ORDER BY t.created_at LIMIT 100
		FOR UPDATE OF i SKIP LOCKED
	`, budget.Now.Add(-rules.Delay))
	if err != nil {
		return nil, err
	}
	type candidate struct {
		inviter, invitee, name, transaction string
		paid                                int64
	}
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.inviter, &item.invitee, &item.name, &item.transaction, &item.paid); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var settled []SettledRebate
	for _, item := range candidates {
		amount := item.paid * int64(rules.Percent) / 100
		if rules.MaxMinor > 0 && amount > rules.MaxMinor {
			amount = rules.MaxMinor
		}
		if amount > 0 {
			fits, err := budget.reserve(ctx, tx, amount)
			if err != nil {
				return nil, err
			}
			if !fits {
				// Today's budget is used up; the rest waits.
				break
			}
		}
		command, err := tx.Exec(ctx, `
			INSERT INTO telegram_rebates(invitee_account_id,inviter_account_id,transaction_id,paid_minor,amount_minor) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT (invitee_account_id) DO NOTHING
		`, item.invitee, item.inviter, item.transaction, item.paid, amount)
		if err != nil {
			return nil, err
		}
		if command.RowsAffected() == 0 || amount == 0 {
			continue
		}
		if _, err := applyWalletChange(ctx, tx, walletChange{
			AccountID: item.inviter, Kind: "reward", AmountMinor: amount, Description: "Telegram 邀请首单返利",
			ReferenceType: "telegram_rebate", DedupKey: "telegram-rebate:" + item.invitee,
		}); err != nil {
			return nil, err
		}
		settled = append(settled, SettledRebate{InviterAccountID: item.inviter, InviteeName: item.name, PaidMinor: item.paid, RebateMinor: amount})
	}
	return settled, tx.Commit(ctx)
}

// ---- New plans, restocks and the customers waiting for them ----

// ErrPlanNotFound means there is no such plan on sale.
var ErrPlanNotFound = errors.New("plan not found")

// PlanChange is a plan that just became buyable.
type PlanChange struct {
	Plan Plan
	// New is true for a plan seen for the first time shortly after it was
	// created; otherwise the plan was sold out and is back.
	New bool
	// Left is how many can be sold (nil = no stock set).
	Left *int
}

// planNewWithin is how soon after its creation a plan counts as new when
// it is first seen.
const planNewWithin = 2 * time.Hour

// PlanChanges compares what can be bought now with the last look and
// returns the plans that are new or back in stock. The first look at a
// plan only records it.
func (s *CatalogStore) PlanChanges(ctx context.Context, now time.Time) ([]PlanChange, error) {
	plans, err := s.ListPlans(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// One instance looks at a time, so a change is reported once.
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(74210923)`).Scan(&locked); err != nil || !locked {
		return nil, err
	}
	known := map[string]bool{}
	rows, err := tx.Query(ctx, `SELECT plan_id,available FROM plan_stock_states`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var available bool
		if err := rows.Scan(&id, &available); err != nil {
			rows.Close()
			return nil, err
		}
		known[id] = available
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var changes []PlanChange
	for _, plan := range plans {
		var left *int
		if plan.StockLimit != nil {
			value := max(*plan.StockLimit-plan.StockHeld, 0)
			left = &value
		}
		available := plan.Enabled && len(plan.Prices) > 0 && (left == nil || *left > 0)
		was, seen := known[plan.ID]
		if seen && was == available {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO plan_stock_states(plan_id,available) VALUES($1,$2)
			ON CONFLICT (plan_id) DO UPDATE SET available=EXCLUDED.available,updated_at=now()
		`, plan.ID, available); err != nil {
			return nil, err
		}
		switch {
		case !available:
		case !seen && now.Sub(plan.CreatedAt) <= planNewWithin:
			changes = append(changes, PlanChange{Plan: plan, New: true, Left: left})
		case seen:
			changes = append(changes, PlanChange{Plan: plan, Left: left})
		}
	}
	return changes, tx.Commit(ctx)
}

// WatchPlan records that a user wants to hear when a plan can be bought.
func (s *CatalogStore) WatchPlan(ctx context.Context, planID, userID string) error {
	command, err := s.db.Exec(ctx, `
		INSERT INTO plan_watches(plan_id,user_id) SELECT p.id,$2 FROM plans p WHERE p.id=$1 AND p.enabled
		ON CONFLICT DO NOTHING
	`, planID, userID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		var exists bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plan_watches WHERE plan_id=$1 AND user_id=$2)`, planID, userID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrPlanNotFound
		}
	}
	return nil
}

func (s *CatalogStore) UnwatchPlan(ctx context.Context, planID, userID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM plan_watches WHERE plan_id=$1 AND user_id=$2`, planID, userID)
	return err
}

// WatchedPlans lists the plans a user is waiting for.
func (s *CatalogStore) WatchedPlans(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT plan_id::text FROM plan_watches WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

// PlanWatcher is a user waiting for a plan.
type PlanWatcher struct {
	UserID      string
	Email       string
	DisplayName string
}

// TakePlanWatchers returns the users waiting for a plan and forgets them:
// each is told once.
func (s *CatalogStore) TakePlanWatchers(ctx context.Context, planID string) ([]PlanWatcher, error) {
	rows, err := s.db.Query(ctx, `
		WITH taken AS (DELETE FROM plan_watches WHERE plan_id=$1 RETURNING user_id)
		SELECT u.id,u.email,u.display_name FROM taken JOIN users u ON u.id=taken.user_id WHERE u.status='active'
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PlanWatcher, 0)
	for rows.Next() {
		var watcher PlanWatcher
		if err := rows.Scan(&watcher.UserID, &watcher.Email, &watcher.DisplayName); err != nil {
			return nil, err
		}
		result = append(result, watcher)
	}
	return result, rows.Err()
}
