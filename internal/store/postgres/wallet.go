package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrInvalidAmount       = errors.New("invalid amount")
)

// Top-ups are limited so a typo cannot create an absurd invoice.
const (
	MinTopupMinor = 100
	MaxTopupMinor = 10_000_000
)

type WalletEntry struct {
	ID                string    `json:"id"`
	Kind              string    `json:"kind"`
	AmountMinor       int64     `json:"amount_minor"`
	BalanceAfterMinor int64     `json:"balance_after_minor"`
	Currency          string    `json:"currency"`
	Description       string    `json:"description"`
	ReferenceType     string    `json:"reference_type,omitempty"`
	ReferenceID       string    `json:"reference_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type Wallet struct {
	BalanceMinor int64         `json:"balance_minor"`
	Currency     string        `json:"currency"`
	Entries      []WalletEntry `json:"entries"`
}

// walletChange is one balance movement. Earnings and refunds are positive,
// payments and penalties negative.
type walletChange struct {
	AccountID     string
	Kind          string
	AmountMinor   int64
	Description   string
	ReferenceType string
	ReferenceID   string
	DedupKey      string
	ActorUserID   string
	// AllowNegative lets clearance penalties push a host into debt, which
	// later earnings pay back. Payments never overdraw.
	AllowNegative bool
}

// applyWalletChange moves a balance inside the caller's transaction. It locks
// the account row, so entries for one account are serialized and the
// deduplication check cannot race. It reports false when the deduplication
// key was already applied.
func applyWalletChange(ctx context.Context, tx pgx.Tx, change walletChange) (bool, error) {
	if change.AmountMinor == 0 {
		return false, nil
	}
	var balance int64
	var currency string
	if err := tx.QueryRow(ctx, `SELECT balance_minor, default_currency FROM accounts WHERE id=$1 FOR UPDATE`, change.AccountID).Scan(&balance, &currency); err != nil {
		return false, fmt.Errorf("lock wallet: %w", err)
	}
	if change.DedupKey != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wallet_entries WHERE deduplication_key=$1)`, change.DedupKey).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return false, nil
		}
	}
	next := balance + change.AmountMinor
	if change.AmountMinor < 0 && next < 0 && !change.AllowNegative {
		return false, ErrInsufficientBalance
	}
	description := strings.TrimSpace(change.Description)
	if len([]rune(description)) > 300 {
		description = string([]rune(description)[:300])
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO wallet_entries(account_id,kind,amount_minor,balance_after_minor,currency,description,reference_type,reference_id,deduplication_key,actor_user_id)
		VALUES($1,$2,$3,$4,$5,$6,nullif($7,''),nullif($8,'')::uuid,nullif($9,''),nullif($10,'')::uuid)
	`, change.AccountID, change.Kind, change.AmountMinor, next, currency, description, change.ReferenceType, change.ReferenceID, change.DedupKey, change.ActorUserID); err != nil {
		return false, fmt.Errorf("record wallet entry: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET balance_minor=$2, updated_at=now() WHERE id=$1`, change.AccountID, next); err != nil {
		return false, err
	}
	return true, nil
}

func (s *BillingStore) Wallet(ctx context.Context, accountID string, limit int) (Wallet, error) {
	var wallet Wallet
	if err := s.db.QueryRow(ctx, `SELECT balance_minor, default_currency FROM accounts WHERE id=$1`, accountID).Scan(&wallet.BalanceMinor, &wallet.Currency); err != nil {
		return Wallet{}, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT id,kind,amount_minor,balance_after_minor,currency,description,coalesce(reference_type,''),coalesce(reference_id::text,''),created_at
		FROM wallet_entries WHERE account_id=$1 ORDER BY created_at DESC, id LIMIT $2
	`, accountID, limit)
	if err != nil {
		return Wallet{}, err
	}
	defer rows.Close()
	wallet.Entries = make([]WalletEntry, 0)
	for rows.Next() {
		var entry WalletEntry
		if err := rows.Scan(&entry.ID, &entry.Kind, &entry.AmountMinor, &entry.BalanceAfterMinor, &entry.Currency, &entry.Description, &entry.ReferenceType, &entry.ReferenceID, &entry.CreatedAt); err != nil {
			return Wallet{}, err
		}
		wallet.Entries = append(wallet.Entries, entry)
	}
	return wallet, rows.Err()
}

type TopupInvoice struct {
	ID         string    `json:"id"`
	Number     string    `json:"number"`
	Currency   string    `json:"currency"`
	TotalMinor int64     `json:"total_minor"`
	DueAt      time.Time `json:"due_at"`
	CreatedAt  time.Time `json:"created_at"`
}

// CreateTopupInvoice opens an invoice that credits the balance once it is
// paid through a gateway or confirmed by staff.
func (s *BillingStore) CreateTopupInvoice(ctx context.Context, accountID, userID string, amountMinor int64) (TopupInvoice, error) {
	if amountMinor < MinTopupMinor || amountMinor > MaxTopupMinor {
		return TopupInvoice{}, ErrInvalidAmount
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return TopupInvoice{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var invoice TopupInvoice
	if err := tx.QueryRow(ctx, `SELECT default_currency FROM accounts WHERE id=$1 AND status='active'`, accountID).Scan(&invoice.Currency); err != nil {
		return TopupInvoice{}, fmt.Errorf("load account: %w", err)
	}
	invoice.Number = newDocumentNumber("TOP")
	invoice.TotalMinor = amountMinor
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices(number,account_id,kind,status,currency,subtotal_minor,tax_minor,total_minor,balance_minor,issued_at,due_at)
		VALUES($1,$2,'topup','open',$3,$4,0,$4,$4,now(),now()+interval '3 days')
		RETURNING id,due_at,created_at
	`, invoice.Number, accountID, invoice.Currency, amountMinor).Scan(&invoice.ID, &invoice.DueAt, &invoice.CreatedAt); err != nil {
		return TopupInvoice{}, fmt.Errorf("create top-up invoice: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO invoice_lines(invoice_id,description,quantity,unit_amount_minor,tax_minor,total_minor) VALUES($1,'账户余额充值',1,$2,0,$2)`, invoice.ID, amountMinor); err != nil {
		return TopupInvoice{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('customer',nullif($1,'')::uuid,'wallet.topup_requested','invoice',$2,jsonb_build_object('amount_minor',$3::bigint))`, userID, invoice.ID, amountMinor); err != nil {
		return TopupInvoice{}, err
	}
	return invoice, tx.Commit(ctx)
}

// PayInvoiceWithBalance settles an open invoice from the account balance and
// then runs the same fulfilment as any other payment.
func (s *BillingStore) PayInvoiceWithBalance(ctx context.Context, accountID, invoiceID, userID string) (PaymentResult, error) {
	return s.payInvoiceWithBalance(ctx, accountID, invoiceID, "customer", userID)
}

// payInvoiceWithBalance pays for the customer (actorType "customer") or for
// automatic renewal ("system", with no user).
func (s *BillingStore) payInvoiceWithBalance(ctx context.Context, accountID, invoiceID, actorType, userID string) (PaymentResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return PaymentResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var number, kind, status, currency string
	var balance int64
	err = tx.QueryRow(ctx, `SELECT number,kind,status,currency,balance_minor FROM invoices WHERE id=$1 AND account_id=$2 FOR UPDATE`, invoiceID, accountID).Scan(&number, &kind, &status, &currency, &balance)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (status != "open" || kind == "topup" || balance <= 0)) {
		return PaymentResult{}, ErrInvoiceUnavailable
	}
	if err != nil {
		return PaymentResult{}, err
	}
	if _, err := applyWalletChange(ctx, tx, walletChange{
		AccountID: accountID, Kind: "payment", AmountMinor: -balance, Description: "余额支付账单 " + number,
		ReferenceType: "invoice", ReferenceID: invoiceID, DedupKey: "invoice-payment:" + invoiceID, ActorUserID: userID,
	}); err != nil {
		return PaymentResult{}, err
	}
	result, err := s.processPayment(ctx, tx, PaymentEvent{
		Provider: "balance", ProviderEventID: "balance:" + invoiceID, EventType: "payment.succeeded",
		ProviderTransactionID: "balance:" + invoiceID, InvoiceNumber: number, AmountMinor: balance, Currency: currency,
		Payload: []byte(`{"source":"wallet"}`),
	}, actorType, userID)
	if err != nil {
		return PaymentResult{}, err
	}
	if result.Duplicate {
		return PaymentResult{}, ErrInvoiceUnavailable
	}
	return result, tx.Commit(ctx)
}

// AdjustWallet is a staff correction; the reason is kept on the entry and in
// the audit log.
func (s *BillingStore) AdjustWallet(ctx context.Context, accountID, staffID string, amountMinor int64, reason string) (Wallet, error) {
	reason = strings.TrimSpace(reason)
	if amountMinor == 0 || amountMinor > MaxTopupMinor || amountMinor < -MaxTopupMinor || reason == "" {
		return Wallet{}, ErrInvalidAmount
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Wallet{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := applyWalletChange(ctx, tx, walletChange{AccountID: accountID, Kind: "adjustment", AmountMinor: amountMinor, Description: "管理员调整：" + reason, ActorUserID: staffID}); err != nil {
		return Wallet{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',$1,'wallet.adjusted','account',$2,jsonb_build_object('amount_minor',$3::bigint,'reason',$4::text))`, staffID, accountID, amountMinor, reason); err != nil {
		return Wallet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Wallet{}, err
	}
	return s.Wallet(ctx, accountID, 100)
}
