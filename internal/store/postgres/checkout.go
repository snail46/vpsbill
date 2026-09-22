package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"clicd-billing/internal/security"
	"github.com/jackc/pgx/v5"
)

var ErrInvoiceUnavailable = errors.New("invoice is unavailable for checkout")

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
	err = tx.QueryRow(ctx, `
		SELECT id,number,status,currency,balance_minor FROM invoices
		WHERE id=$1 AND account_id=$2 FOR UPDATE
	`, invoiceID, accountID).Scan(&result.InvoiceID, &result.InvoiceNumber, &invoiceStatus, &result.Currency, &result.AmountMinor)
	if errors.Is(err, pgx.ErrNoRows) || invoiceStatus != "open" || result.AmountMinor <= 0 {
		return PaymentIntent{}, ErrInvoiceUnavailable
	}
	if err != nil {
		return PaymentIntent{}, err
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
