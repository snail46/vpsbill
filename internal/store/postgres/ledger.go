package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The ledger has one currency at a time, CNY or USD: every balance, price,
// invoice and entry is in it. LedgerCurrencySQL reads it where rows are
// created, so a row never gets another currency than the ledger's.
const LedgerCurrencySQL = `coalesce((SELECT nullif(locale_settings->>'ledger_currency','') FROM system_settings WHERE singleton=true),'CNY')`

var (
	ErrLedgerCurrency = errors.New("the ledger is not in the expected currency")
	// ErrLedgerBusy means online payments are under way; their amounts
	// were agreed in the current currency.
	ErrLedgerBusy = errors.New("online payments are in progress")
)

// ledgerAmounts lists every stored amount, by table. The integration test
// checks that no money column is missing here.
var ledgerAmounts = []struct {
	table   string
	columns []string
	// currency is the table's currency column, if it has one.
	currency string
}{
	{"accounts", []string{"balance_minor"}, "default_currency"},
	{"plan_prices", []string{"amount_minor", "setup_fee_minor"}, "currency"},
	{"orders", []string{"subtotal_minor", "tax_minor", "total_minor", "discount_minor"}, "currency"},
	{"order_items", []string{"unit_amount_minor", "discount_unit_minor"}, ""},
	{"invoices", []string{"subtotal_minor", "tax_minor", "total_minor", "balance_minor"}, "currency"},
	{"invoice_lines", []string{"unit_amount_minor", "tax_minor", "total_minor"}, ""},
	{"transactions", []string{"amount_minor"}, "currency"},
	{"payment_intents", []string{"amount_minor"}, "currency"},
	{"wallet_entries", []string{"amount_minor", "balance_after_minor"}, "currency"},
	{"marketplace_escrows", []string{"gross_minor", "fee_minor", "host_share_minor", "released_gross_minor", "released_host_minor", "refunded_minor"}, "currency"},
	{"service_listings", []string{"price_minor", "fee_minor", "seller_proceeds_minor"}, "currency"},
	{"services", []string{"renewal_price_minor", "list_price_minor"}, ""},
	{"coupon_redemptions", []string{"discount_minor"}, ""},
	{"telegram_bind_rewards", []string{"amount_minor"}, ""},
	{"telegram_checkins", []string{"amount_minor"}, ""},
	{"telegram_invites", []string{"reward_minor"}, ""},
	{"telegram_rebates", []string{"paid_minor", "amount_minor"}, ""},
}

// LedgerConversion counts what a switch converted.
type LedgerConversion struct {
	Accounts      int64 `json:"accounts"`
	PlanPrices    int64 `json:"plan_prices"`
	Invoices      int64 `json:"invoices"`
	WalletEntries int64 `json:"wallet_entries"`
}

// ConvertLedger re-denominates the whole ledger inside the caller's
// transaction: every amount is converted at usdRate (CNY for one USD) and
// every currency mark changes with it. Amounts that were not zero stay at
// least one minor unit, so nothing becomes free by rounding.
func ConvertLedger(ctx context.Context, tx pgx.Tx, from, to string, usdRate float64) (LedgerConversion, error) {
	var result LedgerConversion
	if from == to || (from != "CNY" && from != "USD") || (to != "CNY" && to != "USD") || usdRate <= 0 {
		return result, ErrLedgerCurrency
	}
	operator := "/"
	if from == "USD" {
		operator = "*"
	}
	// conv is the converted value of an expression holding the old amount.
	conv := func(old string) string {
		return fmt.Sprintf(`(CASE WHEN %[1]s IS NULL OR %[1]s=0 THEN %[1]s ELSE sign(%[1]s)::bigint*greatest(1, round(abs(%[1]s)::numeric %[2]s $1::numeric)::bigint) END)`, old, operator)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout='20s'`); err != nil {
		return result, err
	}
	tables := make([]string, 0, len(ledgerAmounts)+1)
	for _, item := range ledgerAmounts {
		tables = append(tables, item.table)
	}
	// Nothing may move money while the amounts change under it.
	if _, err := tx.Exec(ctx, `LOCK TABLE `+strings.Join(tables, ",")+`,coupons IN ACCESS EXCLUSIVE MODE`); err != nil {
		return result, fmt.Errorf("lock the ledger: %w", err)
	}
	var live int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM payment_intents WHERE status IN ('pending','redirected') AND expires_at>now()`).Scan(&live); err != nil {
		return result, err
	}
	if live > 0 {
		return result, ErrLedgerBusy
	}
	if _, err := tx.Exec(ctx, `UPDATE payment_intents SET status='expired',updated_at=now() WHERE status IN ('pending','redirected')`); err != nil {
		return result, err
	}
	// Transactions and wallet entries are immutable except for this
	// re-denomination (see migration 000048); the setting ends with the
	// transaction.
	if _, err := tx.Exec(ctx, `SELECT set_config('vpsbill.ledger_conversion','on',true)`); err != nil {
		return result, err
	}

	for _, item := range ledgerAmounts {
		sets := make([]string, 0, len(item.columns)+1)
		for _, column := range item.columns {
			value := conv(column)
			switch item.table + "." + column {
			case "invoices.subtotal_minor":
				// Keep subtotal + tax = total where it held.
				value = fmt.Sprintf(`(CASE WHEN subtotal_minor+tax_minor=total_minor THEN %s-%s ELSE %s END)`, conv("total_minor"), conv("tax_minor"), conv("subtotal_minor"))
			case "invoices.balance_minor":
				value = fmt.Sprintf(`least(%s, %s)`, conv("balance_minor"), conv("total_minor"))
			case "marketplace_escrows.host_share_minor":
				// The host's share is what is left of the gross after the fee.
				value = fmt.Sprintf(`(CASE WHEN host_share_minor=gross_minor-fee_minor THEN %s-%s ELSE %s END)`, conv("gross_minor"), conv("fee_minor"), conv("host_share_minor"))
			case "marketplace_escrows.released_gross_minor":
				value = fmt.Sprintf(`least(%s, %s)`, conv("released_gross_minor"), conv("gross_minor"))
			case "marketplace_escrows.released_host_minor":
				value = fmt.Sprintf(`least(%s, (CASE WHEN host_share_minor=gross_minor-fee_minor THEN %s-%s ELSE %s END))`, conv("released_host_minor"), conv("gross_minor"), conv("fee_minor"), conv("host_share_minor"))
			case "service_listings.price_minor":
				// The table allows prices of 1.00 to 100000.00.
				value = fmt.Sprintf(`least(greatest(%s, 100), 10000000)`, conv("price_minor"))
			}
			sets = append(sets, column+"="+value)
		}
		where := ""
		if item.currency != "" {
			sets = append(sets, item.currency+"=$3")
			where = " WHERE " + item.currency + "=$2"
		}
		query := `UPDATE ` + item.table + ` SET ` + strings.Join(sets, ",") + where
		args := []any{usdRate}
		if item.currency != "" {
			args = append(args, from, to)
		}
		command, err := tx.Exec(ctx, query, args...)
		if err != nil {
			return result, fmt.Errorf("convert %s: %w", item.table, err)
		}
		switch item.table {
		case "accounts":
			result.Accounts = command.RowsAffected()
		case "plan_prices":
			result.PlanPrices = command.RowsAffected()
		case "invoices":
			result.Invoices = command.RowsAffected()
		case "wallet_entries":
			result.WalletEntries = command.RowsAffected()
		}
	}
	// Discounts that are an amount rather than a percentage, and the list
	// price kept with an order item.
	for _, query := range []string{
		`UPDATE coupons SET discount_value=` + conv("discount_value") + ` WHERE discount_type='amount'`,
		`UPDATE services SET renewal_discount_value=` + conv("renewal_discount_value") + ` WHERE renewal_discount_type='amount'`,
		`UPDATE order_items SET renewal_discount_value=` + conv("renewal_discount_value") + ` WHERE renewal_discount_type='amount'`,
		`UPDATE order_items SET configuration=jsonb_set(configuration,'{list_amount_minor}',to_jsonb(` + conv("(configuration->>'list_amount_minor')::bigint") + `)) WHERE configuration ? 'list_amount_minor'`,
	} {
		if _, err := tx.Exec(ctx, query, usdRate); err != nil {
			return result, fmt.Errorf("convert discounts: %w", err)
		}
	}
	return result, nil
}

// LedgerSwitch is one recorded change of the ledger currency.
type LedgerSwitch struct {
	From      string           `json:"from"`
	To        string           `json:"to"`
	USDRate   float64          `json:"usd_rate"`
	Counts    LedgerConversion `json:"counts"`
	CreatedAt time.Time        `json:"created_at"`
}

// LedgerState is what staff see before switching.
type LedgerState struct {
	// Balances is how many accounts hold a balance, BalanceMinor their sum.
	Balances     int64 `json:"balances"`
	BalanceMinor int64 `json:"balance_minor"`
	PlanPrices   int64 `json:"plan_prices"`
	OpenInvoices int64 `json:"open_invoices"`
	// LivePayments are online payments under way, which block a switch.
	LivePayments int64          `json:"live_payments"`
	History      []LedgerSwitch `json:"history"`
}

func ReadLedgerState(ctx context.Context, db *pgxpool.Pool) (LedgerState, error) {
	var state LedgerState
	err := db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM accounts WHERE balance_minor<>0),
		       (SELECT coalesce(sum(balance_minor),0)::bigint FROM accounts),
		       (SELECT count(*) FROM plan_prices WHERE active_until IS NULL OR active_until>now()),
		       (SELECT count(*) FROM invoices WHERE status='open'),
		       (SELECT count(*) FROM payment_intents WHERE status IN ('pending','redirected') AND expires_at>now())
	`).Scan(&state.Balances, &state.BalanceMinor, &state.PlanPrices, &state.OpenInvoices, &state.LivePayments)
	if err != nil {
		return state, err
	}
	rows, err := db.Query(ctx, `SELECT from_currency,to_currency,usd_rate::float8,counts,created_at FROM ledger_switches ORDER BY created_at DESC LIMIT 10`)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	state.History = make([]LedgerSwitch, 0)
	for rows.Next() {
		var item LedgerSwitch
		if err := rows.Scan(&item.From, &item.To, &item.USDRate, &item.Counts, &item.CreatedAt); err != nil {
			return state, err
		}
		state.History = append(state.History, item)
	}
	return state, rows.Err()
}

// LedgerAmountColumns names every column that holds money, by table: the
// ones ConvertLedger converts, and payment_intents.charge_minor, which is
// what a gateway charged in its own currency and stays as it is.
func LedgerAmountColumns() map[string][]string {
	columns := map[string][]string{
		"coupons":         {"discount_value"},
		"services":        {"renewal_discount_value"},
		"order_items":     {"renewal_discount_value"},
		"payment_intents": {"charge_minor"},
	}
	for _, item := range ledgerAmounts {
		columns[item.table] = append(columns[item.table], item.columns...)
	}
	return columns
}
