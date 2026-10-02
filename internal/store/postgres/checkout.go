package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"vpsbill/internal/security"
)

var (
	ErrInvoiceUnavailable = errors.New("invoice is unavailable for checkout")
	// ErrBalanceOnly marks an invoice the payment gateway does not take.
	ErrBalanceOnly = errors.New("invoice can only be paid from the balance")
)

// invoiceGatewaySQL tells whether invoice i may be paid through the payment
// gateway: top-ups and the platform's own products. Everything sold by a
// host is paid from the balance.
const invoiceGatewaySQL = `(i.kind='topup' OR NOT (
	EXISTS(SELECT 1 FROM order_items oi JOIN plans gp ON gp.id=oi.plan_id WHERE oi.order_id=i.order_id AND gp.owner_account_id IS NOT NULL)
	OR EXISTS(SELECT 1 FROM services gs JOIN plans gp ON gp.id=gs.plan_id WHERE gs.id=i.service_id AND gp.owner_account_id IS NOT NULL)))`

type PaymentIntent struct {
	ID                string    `json:"id"`
	AccountID         string    `json:"account_id"`
	InvoiceID         string    `json:"invoice_id"`
	InvoiceNumber     string    `json:"invoice_number"`
	Provider          string    `json:"provider"`
	MerchantReference string    `json:"merchant_reference"`
	Status            string    `json:"status"`
	Currency          string    `json:"currency"`
	AmountMinor       int64     `json:"amount_minor"`
	CheckoutURL       string    `json:"checkout_url"`
	ExpiresAt         time.Time `json:"expires_at"`
}

func (s *BillingStore) PreparePaymentIntent(ctx context.Context, accountID, invoiceID, provider string) (PaymentIntent, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return PaymentIntent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "UPDATE payment_intents SET status='expired',updated_at=now() WHERE invoice_id=$1 AND status IN ('pending','redirected') AND expires_at<=now()", invoiceID); err != nil {
		return PaymentIntent{}, err
	}
	var result PaymentIntent
	var invoiceStatus string
	var gateway bool
	err = tx.QueryRow(ctx, `
		SELECT i.id,i.number,i.status,i.currency,i.balance_minor,`+invoiceGatewaySQL+` FROM invoices i
		WHERE i.id=$1 AND i.account_id=$2 FOR UPDATE OF i
	`, invoiceID, accountID).Scan(&result.InvoiceID, &result.InvoiceNumber, &invoiceStatus, &result.Currency, &result.AmountMinor, &gateway)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (invoiceStatus != "open" || result.AmountMinor <= 0)) {
		return PaymentIntent{}, ErrInvoiceUnavailable
	}
	if err != nil {
		return PaymentIntent{}, err
	}
	if !gateway {
		return PaymentIntent{}, ErrBalanceOnly
	}
	err = tx.QueryRow(ctx, `
		SELECT id,account_id,provider,merchant_reference,status,coalesce(checkout_url,''),expires_at
		FROM payment_intents WHERE invoice_id=$1 AND provider=$2 AND status IN ('pending','redirected') AND expires_at>now()
		LIMIT 1
	`, invoiceID, provider).Scan(&result.ID, &result.AccountID, &result.Provider, &result.MerchantReference, &result.Status, &result.CheckoutURL, &result.ExpiresAt)
	if err == nil {
		return result, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return PaymentIntent{}, err
	}
	token, _, err := security.NewToken()
	if err != nil {
		return PaymentIntent{}, err
	}
	token = strings.ToUpper(token)
	if len(token) > 20 {
		token = token[:20]
	}
	result.AccountID = accountID
	result.Provider = provider
	result.MerchantReference = "PAY-" + token
	result.Status = "pending"
	result.ExpiresAt = time.Now().UTC().Add(30 * time.Minute)
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_intents(account_id,invoice_id,provider,merchant_reference,status,currency,amount_minor,expires_at)
		VALUES($1,$2,$3,$4,'pending',$5,$6,$7) RETURNING id
	`, result.AccountID, result.InvoiceID, result.Provider, result.MerchantReference, result.Currency, result.AmountMinor, result.ExpiresAt).Scan(&result.ID)
	if err != nil {
		return PaymentIntent{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PaymentIntent{}, err
	}
	return result, nil
}

func (s *BillingStore) SetPaymentIntentCheckoutURL(ctx context.Context, id, accountID, checkoutURL string) error {
	command, err := s.db.Exec(ctx, `
		UPDATE payment_intents SET checkout_url=$3,status='redirected',updated_at=now()
		WHERE id=$1 AND account_id=$2 AND status IN ('pending','redirected') AND expires_at>now()
	`, id, accountID, checkoutURL)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrInvoiceUnavailable
	}
	return nil
}

func (s *BillingStore) PaymentIntentByMerchantReference(ctx context.Context, reference, provider string) (PaymentIntent, error) {
	var result PaymentIntent
	err := s.db.QueryRow(ctx, `SELECT p.id,p.account_id,p.invoice_id,i.number,p.provider,p.merchant_reference,p.status,p.currency,p.amount_minor,coalesce(p.checkout_url,''),p.expires_at
		FROM payment_intents p JOIN invoices i ON i.id=p.invoice_id WHERE p.merchant_reference=$1 AND p.provider=$2`, reference, provider).
		Scan(&result.ID, &result.AccountID, &result.InvoiceID, &result.InvoiceNumber, &result.Provider, &result.MerchantReference, &result.Status, &result.Currency, &result.AmountMinor, &result.CheckoutURL, &result.ExpiresAt)
	return result, err
}
