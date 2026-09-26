package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"vpsbill/internal/security"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrPaymentDuplicate = errors.New("payment event already processed")
	ErrPaymentMismatch  = errors.New("payment does not match invoice")
)

type BillingStore struct {
	db *pgxpool.Pool
}

func NewBillingStore(db *pgxpool.Pool) *BillingStore {
	return &BillingStore{db: db}
}

type Account struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	Status          string    `json:"status"`
	DisplayName     string    `json:"display_name"`
	BillingEmail    string    `json:"billing_email"`
	LegalName       string    `json:"legal_name,omitempty"`
	TaxID           string    `json:"tax_id,omitempty"`
	CountryCode     string    `json:"country_code,omitempty"`
	DefaultCurrency string    `json:"default_currency"`
	CreatedAt       time.Time `json:"created_at"`
}

func (s *BillingStore) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, kind, status, display_name, billing_email, coalesce(legal_name,''),
		       coalesce(tax_id,''), coalesce(country_code,''), default_currency, created_at
		FROM accounts ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := make([]Account, 0)
	for rows.Next() {
		var account Account
		if err := rows.Scan(&account.ID, &account.Kind, &account.Status, &account.DisplayName, &account.BillingEmail, &account.LegalName, &account.TaxID, &account.CountryCode, &account.DefaultCurrency, &account.CreatedAt); err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

func (s *BillingStore) CreateAccount(ctx context.Context, input Account) (Account, error) {
	input.DefaultCurrency = strings.ToUpper(strings.TrimSpace(input.DefaultCurrency))
	input.CountryCode = strings.ToUpper(strings.TrimSpace(input.CountryCode))
	err := s.db.QueryRow(ctx, `
		INSERT INTO accounts(kind, status, display_name, billing_email, legal_name, tax_id, country_code, default_currency)
		VALUES($1, 'active', $2, lower($3), nullif($4,''), nullif($5,''), nullif($6,''), $7)
		RETURNING id, status, created_at
	`, input.Kind, strings.TrimSpace(input.DisplayName), strings.TrimSpace(input.BillingEmail), strings.TrimSpace(input.LegalName), strings.TrimSpace(input.TaxID), input.CountryCode, input.DefaultCurrency).Scan(&input.ID, &input.Status, &input.CreatedAt)
	return input, err
}

func (s *BillingStore) UpdateAccountStatus(ctx context.Context, accountID, status, actorID string) error {
	if status != "active" && status != "suspended" {
		return errors.New("invalid account status")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, "UPDATE accounts SET status=$2,updated_at=now() WHERE id=$1", accountID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(actor_type,actor_id,action,target_type,target_id,metadata) VALUES('staff',$1,'account.status_changed','account',$2,jsonb_build_object('status',$3::text))`, actorID, accountID, status); err != nil {
		return err
	}
	if status == "suspended" {
		if _, err = tx.Exec(ctx, `DELETE FROM login_sessions WHERE user_id IN (SELECT user_id FROM memberships WHERE account_id=$1)`, accountID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type RegionSummary struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

func (s *BillingStore) ListRegions(ctx context.Context) ([]RegionSummary, error) {
	rows, err := s.db.Query(ctx, "SELECT id, code, name FROM regions WHERE enabled=true ORDER BY code")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	regions := make([]RegionSummary, 0)
	for rows.Next() {
		var region RegionSummary
		if err := rows.Scan(&region.ID, &region.Code, &region.Name); err != nil {
			return nil, err
		}
		regions = append(regions, region)
	}
	return regions, rows.Err()
}

type OrderItemInput struct {
	PlanID        string         `json:"plan_id"`
	RegionID      string         `json:"region_id"`
	BillingCycle  string         `json:"billing_cycle"`
	Quantity      int            `json:"quantity"`
	Configuration map[string]any `json:"configuration"`
}

type CreateOrderInput struct {
	AccountID string           `json:"account_id"`
	Items     []OrderItemInput `json:"items"`
	ActorType string           `json:"-"`
	ActorID   string           `json:"-"`
}

type Order struct {
	ID            string    `json:"id"`
	Number        string    `json:"number"`
	AccountID     string    `json:"account_id"`
	CustomerName  string    `json:"customer_name"`
	Status        string    `json:"status"`
	Currency      string    `json:"currency"`
	SubtotalMinor int64     `json:"subtotal_minor"`
	TaxMinor      int64     `json:"tax_minor"`
	TotalMinor    int64     `json:"total_minor"`
	InvoiceID     string    `json:"invoice_id,omitempty"`
	InvoiceNumber string    `json:"invoice_number,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type pricedItem struct {
	Input              OrderItemInput
	PlanName           string
	Virtualization     string
	PlanVersion        int
	VCPU               int
	RAMMB              int
	DiskGB             int
	TrafficGB          int
	NetworkDownMbps    int
	NetworkUpMbps      int
	SnapshotLimit      int
	AssignNAT          bool
	PortMappingCount   int
	AssignIPv4         bool
	IPv4Count          int
	AssignIPv6         bool
	IPv6Count          int
	DefaultTemplateID  string
	AllowedTemplateIDs []string
	UnitAmountMinor    int64
	SetupFeeMinor      int64
}

func (s *BillingStore) CreateOrder(ctx context.Context, input CreateOrderInput) (Order, error) {
	if len(input.Items) == 0 || len(input.Items) > 20 {
		return Order{}, errors.New("order requires at least one item")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Order{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var order Order
	if err := tx.QueryRow(ctx, "SELECT display_name, default_currency FROM accounts WHERE id=$1 AND status='active'", input.AccountID).Scan(&order.CustomerName, &order.Currency); err != nil {
		return Order{}, fmt.Errorf("load customer: %w", err)
	}
	items := make([]pricedItem, 0, len(input.Items))
	for _, item := range input.Items {
		if item.Quantity < 1 || item.Quantity > 100 {
			return Order{}, errors.New("item quantity must be between 1 and 100")
		}
		cleanConfiguration, err := sanitizeOrderConfiguration(item.Configuration)
		if err != nil {
			return Order{}, err
		}
		item.Configuration = cleanConfiguration
		var enabled bool
		if err := tx.QueryRow(ctx, "SELECT enabled FROM regions WHERE id=$1", item.RegionID).Scan(&enabled); err != nil || !enabled {
			return Order{}, errors.New("selected region is unavailable")
		}
		priced := pricedItem{Input: item}
		if err := tx.QueryRow(ctx, `
			SELECT p.name, p.virtualization, p.version, p.vcpu, p.ram_mb, p.disk_gb,
			       p.traffic_gb, p.network_down_mbps, p.network_up_mbps, p.snapshot_limit,
		       p.assign_nat, p.port_mapping_count, p.assign_ipv4, p.ipv4_count, p.assign_ipv6, p.ipv6_count,
		       p.default_template_id, p.allowed_template_ids, pp.amount_minor, pp.setup_fee_minor
			FROM plans p
			JOIN plan_prices pp ON pp.plan_id=p.id
			WHERE p.id=$1 AND p.enabled=true AND pp.currency=$2 AND pp.billing_cycle=$3
			  AND pp.active_from <= now() AND (pp.active_until IS NULL OR pp.active_until > now())
			ORDER BY pp.active_from DESC LIMIT 1
		`, item.PlanID, order.Currency, item.BillingCycle).Scan(&priced.PlanName, &priced.Virtualization, &priced.PlanVersion, &priced.VCPU, &priced.RAMMB, &priced.DiskGB, &priced.TrafficGB, &priced.NetworkDownMbps, &priced.NetworkUpMbps, &priced.SnapshotLimit,
			&priced.AssignNAT, &priced.PortMappingCount, &priced.AssignIPv4, &priced.IPv4Count, &priced.AssignIPv6, &priced.IPv6Count,
			&priced.DefaultTemplateID, &priced.AllowedTemplateIDs, &priced.UnitAmountMinor, &priced.SetupFeeMinor); err != nil {
			return Order{}, fmt.Errorf("load active plan price: %w", err)
		}
		templateID, _ := item.Configuration["template_id"].(string)
		if !stringAllowed(templateID, priced.AllowedTemplateIDs) {
			return Order{}, errors.New("selected template is unavailable for this plan")
		}
		quantity := int64(item.Quantity)
		if priced.UnitAmountMinor > math.MaxInt64/quantity || priced.SetupFeeMinor > math.MaxInt64/quantity {
			return Order{}, errors.New("order amount is too large")
		}
		lineTotal := priced.UnitAmountMinor * quantity
		setupTotal := priced.SetupFeeMinor * quantity
		if lineTotal > math.MaxInt64-setupTotal || order.SubtotalMinor > math.MaxInt64-lineTotal-setupTotal {
			return Order{}, errors.New("order amount is too large")
		}
		order.SubtotalMinor += lineTotal + setupTotal
		items = append(items, priced)
	}
	order.TotalMinor = order.SubtotalMinor + order.TaxMinor
	order.AccountID = input.AccountID
	order.Number = newDocumentNumber("ORD")
	order.Status = "pending_payment"
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders(number, account_id, status, currency, subtotal_minor, tax_minor, total_minor)
		VALUES($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`, order.Number, order.AccountID, order.Status, order.Currency, order.SubtotalMinor, order.TaxMinor, order.TotalMinor).Scan(&order.ID, &order.CreatedAt); err != nil {
		return Order{}, fmt.Errorf("create order: %w", err)
	}

	invoiceNumber := newDocumentNumber("INV")
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices(number, account_id, order_id, status, currency, subtotal_minor, tax_minor, total_minor, balance_minor, issued_at, due_at)
		VALUES($1, $2, $3, 'open', $4, $5, $6, $7, $7, now(), now() + interval '1 day')
		RETURNING id
	`, invoiceNumber, order.AccountID, order.ID, order.Currency, order.SubtotalMinor, order.TaxMinor, order.TotalMinor).Scan(&order.InvoiceID); err != nil {
		return Order{}, fmt.Errorf("create invoice: %w", err)
	}
	order.InvoiceNumber = invoiceNumber

	for _, item := range items {
		configuration := map[string]any{
			"billing_cycle":      item.Input.BillingCycle,
			"plan_version":       item.PlanVersion,
			"virtualization":     item.Virtualization,
			"vcpu":               item.VCPU,
			"ram_mb":             item.RAMMB,
			"disk_gb":            item.DiskGB,
			"traffic_gb":         item.TrafficGB,
			"network_down_mbps":  item.NetworkDownMbps,
			"network_up_mbps":    item.NetworkUpMbps,
			"snapshot_limit":     item.SnapshotLimit,
			"assign_nat":         item.AssignNAT,
			"port_mapping_count": item.PortMappingCount,
			"assign_ipv4":        item.AssignIPv4,
			"ipv4_count":         item.IPv4Count,
			"assign_ipv6":        item.AssignIPv6,
			"ipv6_count":         item.IPv6Count,
		}
		for key, value := range item.Input.Configuration {
			configuration[key] = value
		}
		configBody, _ := json.Marshal(configuration)
		var orderItemID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_items(order_id, plan_id, region_id, description, quantity, unit_amount_minor, configuration)
			VALUES($1, $2, $3, $4, $5, $6, $7)
			RETURNING id
		`, order.ID, item.Input.PlanID, item.Input.RegionID, item.PlanName, item.Input.Quantity, item.UnitAmountMinor, configBody).Scan(&orderItemID); err != nil {
			return Order{}, fmt.Errorf("create order item: %w", err)
		}
		lineTotal := item.UnitAmountMinor * int64(item.Input.Quantity)
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoice_lines(invoice_id, order_item_id, description, quantity, unit_amount_minor, tax_minor, total_minor)
			VALUES($1, $2, $3, $4, $5, 0, $6)
		`, order.InvoiceID, orderItemID, item.PlanName+" / "+item.Input.BillingCycle, item.Input.Quantity, item.UnitAmountMinor, lineTotal); err != nil {
			return Order{}, fmt.Errorf("create invoice line: %w", err)
		}
		if item.SetupFeeMinor > 0 {
			setupTotal := item.SetupFeeMinor * int64(item.Input.Quantity)
			if _, err := tx.Exec(ctx, `
				INSERT INTO invoice_lines(invoice_id, order_item_id, description, quantity, unit_amount_minor, tax_minor, total_minor)
				VALUES($1, $2, $3, $4, $5, 0, $6)
			`, order.InvoiceID, orderItemID, item.PlanName+" / setup", item.Input.Quantity, item.SetupFeeMinor, setupTotal); err != nil {
				return Order{}, fmt.Errorf("create setup fee invoice line: %w", err)
			}
		}
	}
	eventPayload, _ := json.Marshal(map[string]any{"order_number": order.Number, "invoice_id": order.InvoiceID, "invoice_number": order.InvoiceNumber, "total_minor": order.TotalMinor, "currency": order.Currency})
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events(aggregate_type, aggregate_id, event_type, deduplication_key, payload)
		VALUES('order', $1, 'order.created', $2, $3)
	`, order.ID, order.ID+":created:v1", eventPayload); err != nil {
		return Order{}, fmt.Errorf("write order outbox event: %w", err)
	}
	actorType := strings.TrimSpace(input.ActorType)
	if actorType == "" {
		actorType = "system"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs(actor_type, actor_id, action, target_type, target_id, metadata)
		VALUES($1, nullif($2,''), 'order.created', 'order', $3, jsonb_build_object('invoice_id',$4::text,'total_minor',$5::bigint,'currency',$6::text))
	`, actorType, strings.TrimSpace(input.ActorID), order.ID, order.InvoiceID, order.TotalMinor, order.Currency); err != nil {
		return Order{}, fmt.Errorf("write order audit log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Order{}, err
	}
	return order, nil
}

func stringAllowed(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func sanitizeOrderConfiguration(input map[string]any) (map[string]any, error) {
	result := make(map[string]any)
	for key, value := range input {
		switch key {
		case "template_id":
			text, ok := value.(string)
			text = strings.TrimSpace(text)
			if !ok || text == "" || len(text) > 128 {
				return nil, errors.New("invalid template_id")
			}
			result[key] = text
		case "ssh_auth_mode":
			text, ok := value.(string)
			if !ok || (text != "password" && text != "key") {
				return nil, errors.New("invalid ssh_auth_mode")
			}
			result[key] = text
		case "ssh_public_key":
			text, ok := value.(string)
			text = strings.TrimSpace(text)
			if !ok || len(text) > 16384 {
				return nil, errors.New("invalid ssh_public_key")
			}
			result[key] = text
		default:
			return nil, fmt.Errorf("unsupported configuration field %q", key)
		}
	}
	if _, ok := result["template_id"]; !ok {
		return nil, errors.New("template_id is required")
	}
	return result, nil
}

func (s *BillingStore) ListOrders(ctx context.Context) ([]Order, error) {
	rows, err := s.db.Query(ctx, `
		SELECT o.id, o.number, o.account_id, a.display_name, o.status, o.currency,
		       o.subtotal_minor, o.tax_minor, o.total_minor, coalesce(i.id::text,''), coalesce(i.number,''), o.created_at
		FROM orders o JOIN accounts a ON a.id=o.account_id
		LEFT JOIN invoices i ON i.order_id=o.id
		ORDER BY o.created_at DESC LIMIT 500
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := make([]Order, 0)
	for rows.Next() {
		var order Order
		if err := rows.Scan(&order.ID, &order.Number, &order.AccountID, &order.CustomerName, &order.Status, &order.Currency, &order.SubtotalMinor, &order.TaxMinor, &order.TotalMinor, &order.InvoiceID, &order.InvoiceNumber, &order.CreatedAt); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

func (s *BillingStore) ListOrdersByAccount(ctx context.Context, accountID string) ([]Order, error) {
	rows, err := s.db.Query(ctx, `
		SELECT o.id,o.number,o.account_id,a.display_name,o.status,o.currency,o.subtotal_minor,o.tax_minor,o.total_minor,
		       coalesce(i.id::text,''),coalesce(i.number,''),o.created_at
		FROM orders o JOIN accounts a ON a.id=o.account_id LEFT JOIN invoices i ON i.order_id=o.id
		WHERE o.account_id=$1 ORDER BY o.created_at DESC LIMIT 500
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Order, 0)
	for rows.Next() {
		var row Order
		if err := rows.Scan(&row.ID, &row.Number, &row.AccountID, &row.CustomerName, &row.Status, &row.Currency, &row.SubtotalMinor, &row.TaxMinor, &row.TotalMinor, &row.InvoiceID, &row.InvoiceNumber, &row.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

type Invoice struct {
	ID           string     `json:"id"`
	Number       string     `json:"number"`
	AccountID    string     `json:"account_id"`
	CustomerName string     `json:"customer_name"`
	OrderID      *string    `json:"order_id"`
	ServiceID    *string    `json:"service_id,omitempty"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	Currency     string     `json:"currency"`
	TotalMinor   int64      `json:"total_minor"`
	BalanceMinor int64      `json:"balance_minor"`
	DueAt        time.Time  `json:"due_at"`
	PaidAt       *time.Time `json:"paid_at"`
	PeriodStart  *time.Time `json:"period_start,omitempty"`
	PeriodEnd    *time.Time `json:"period_end,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func (s *BillingStore) ListInvoices(ctx context.Context) ([]Invoice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT i.id, i.number, i.account_id, a.display_name, i.order_id,i.service_id,i.kind, i.status, i.currency,
		       i.total_minor, i.balance_minor, i.due_at, i.paid_at,i.period_start,i.period_end, i.created_at
		FROM invoices i JOIN accounts a ON a.id=i.account_id
		ORDER BY i.created_at DESC LIMIT 500
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	invoices := make([]Invoice, 0)
	for rows.Next() {
		var invoice Invoice
		if err := rows.Scan(&invoice.ID, &invoice.Number, &invoice.AccountID, &invoice.CustomerName, &invoice.OrderID, &invoice.ServiceID, &invoice.Kind, &invoice.Status, &invoice.Currency, &invoice.TotalMinor, &invoice.BalanceMinor, &invoice.DueAt, &invoice.PaidAt, &invoice.PeriodStart, &invoice.PeriodEnd, &invoice.CreatedAt); err != nil {
			return nil, err
		}
		invoices = append(invoices, invoice)
	}
	return invoices, rows.Err()
}

func (s *BillingStore) InvoiceByID(ctx context.Context, id string) (Invoice, error) {
	var invoice Invoice
	err := s.db.QueryRow(ctx, `
		SELECT i.id, i.number, i.account_id, a.display_name, i.order_id,i.service_id,i.kind, i.status, i.currency,
		       i.total_minor, i.balance_minor, i.due_at, i.paid_at,i.period_start,i.period_end, i.created_at
		FROM invoices i JOIN accounts a ON a.id=i.account_id WHERE i.id=$1
	`, id).Scan(&invoice.ID, &invoice.Number, &invoice.AccountID, &invoice.CustomerName, &invoice.OrderID, &invoice.ServiceID, &invoice.Kind, &invoice.Status, &invoice.Currency, &invoice.TotalMinor, &invoice.BalanceMinor, &invoice.DueAt, &invoice.PaidAt, &invoice.PeriodStart, &invoice.PeriodEnd, &invoice.CreatedAt)
	return invoice, err
}

type PaymentEvent struct {
	Provider              string          `json:"provider"`
	ProviderEventID       string          `json:"id"`
	EventType             string          `json:"type"`
	ProviderTransactionID string          `json:"transaction_id"`
	InvoiceNumber         string          `json:"invoice_number"`
	AmountMinor           int64           `json:"amount_minor"`
	Currency              string          `json:"currency"`
	Payload               json.RawMessage `json:"-"`
}

type Transaction struct {
	ID                    string    `json:"id"`
	AccountID             string    `json:"account_id"`
	CustomerName          string    `json:"customer_name"`
	InvoiceID             *string   `json:"invoice_id"`
	InvoiceNumber         string    `json:"invoice_number,omitempty"`
	Provider              string    `json:"provider"`
	ProviderTransactionID string    `json:"provider_transaction_id"`
	Type                  string    `json:"type"`
	Status                string    `json:"status"`
	Currency              string    `json:"currency"`
	AmountMinor           int64     `json:"amount_minor"`
	CreatedAt             time.Time `json:"created_at"`
}

func (s *BillingStore) ListTransactions(ctx context.Context) ([]Transaction, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.id, t.account_id, a.display_name, t.invoice_id, coalesce(i.number,''), t.provider,
		       coalesce(t.provider_transaction_id,''), t.type, t.status, t.currency, t.amount_minor, t.created_at
		FROM transactions t
		JOIN accounts a ON a.id=t.account_id
		LEFT JOIN invoices i ON i.id=t.invoice_id
		ORDER BY t.created_at DESC LIMIT 500
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	transactions := make([]Transaction, 0)
	for rows.Next() {
		var transaction Transaction
		if err := rows.Scan(&transaction.ID, &transaction.AccountID, &transaction.CustomerName, &transaction.InvoiceID, &transaction.InvoiceNumber, &transaction.Provider, &transaction.ProviderTransactionID, &transaction.Type, &transaction.Status, &transaction.Currency, &transaction.AmountMinor, &transaction.CreatedAt); err != nil {
			return nil, err
		}
		transactions = append(transactions, transaction)
	}
	return transactions, rows.Err()
}

type PaymentResult struct {
	Duplicate     bool     `json:"duplicate"`
	InvoiceID     string   `json:"invoice_id,omitempty"`
	OrderID       string   `json:"order_id,omitempty"`
	ServiceIDs    []string `json:"service_ids,omitempty"`
	TransactionID string   `json:"transaction_id,omitempty"`
}

func (s *BillingStore) ProcessPayment(ctx context.Context, event PaymentEvent, actorType, actorID string) (PaymentResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return PaymentResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var paymentEventID string
	err = tx.QueryRow(ctx, `
		INSERT INTO payment_events(provider, provider_event_id, event_type, payload)
		VALUES($1, $2, $3, $4)
		ON CONFLICT(provider, provider_event_id) DO NOTHING
		RETURNING id
	`, event.Provider, event.ProviderEventID, event.EventType, event.Payload).Scan(&paymentEventID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PaymentResult{Duplicate: true}, tx.Commit(ctx)
	}
	if err != nil {
		return PaymentResult{}, err
	}

	var result PaymentResult
	var accountID, status, currency string
	var orderID, renewalServiceID *string
	var periodEnd *time.Time
	var balance int64
	if err := tx.QueryRow(ctx, `
		SELECT id, account_id, order_id, service_id, period_end, status, currency, balance_minor
		FROM invoices WHERE number=$1 FOR UPDATE
	`, event.InvoiceNumber).Scan(&result.InvoiceID, &accountID, &orderID, &renewalServiceID, &periodEnd, &status, &currency, &balance); err != nil {
		return PaymentResult{}, fmt.Errorf("load invoice: %w", err)
	}
	if status != "open" || balance != event.AmountMinor || !strings.EqualFold(currency, event.Currency) || event.AmountMinor <= 0 {
		return PaymentResult{}, ErrPaymentMismatch
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO transactions(account_id, invoice_id, provider, provider_transaction_id, type, status, currency, amount_minor)
		VALUES($1, $2, $3, $4, 'payment', 'succeeded', $5, $6)
		RETURNING id
	`, accountID, result.InvoiceID, event.Provider, event.ProviderTransactionID, currency, event.AmountMinor).Scan(&result.TransactionID); err != nil {
		return PaymentResult{}, fmt.Errorf("record transaction: %w", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE invoices SET status='paid', balance_minor=0, paid_at=now(), updated_at=now() WHERE id=$1", result.InvoiceID); err != nil {
		return PaymentResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payment_intents SET status='succeeded',external_reference=$3,updated_at=now()
		WHERE invoice_id=$1 AND provider=$2 AND status IN ('pending','redirected')
	`, result.InvoiceID, event.Provider, event.ProviderTransactionID); err != nil {
		return PaymentResult{}, err
	}
	if renewalServiceID != nil {
		if periodEnd == nil {
			return PaymentResult{}, errors.New("renewal invoice has no period end")
		}
		var previousStatus, runtimeStatus string
		var nodeID *string
		if err := tx.QueryRow(ctx, `SELECT status,runtime_status,node_id FROM services WHERE id=$1 AND account_id=$2 FOR UPDATE`, *renewalServiceID, accountID).Scan(&previousStatus, &runtimeStatus, &nodeID); err != nil {
			return PaymentResult{}, err
		}
		if previousStatus == "terminating" || previousStatus == "terminated" {
			return PaymentResult{}, ErrPaymentMismatch
		}
		needsStart := false
		if previousStatus == "suspended" && nodeID != nil {
			// A renewal can race the asynchronous overdue stop. Cancel work that has
			// not started; if it may already have reached CLICD, queue a compensating
			// start so a paid service cannot remain stopped.
			if _, err := tx.Exec(ctx, `UPDATE provisioning_jobs SET status='dead',locked_at=NULL,locked_by=NULL,last_error='cancelled by renewal payment',updated_at=now() WHERE service_id=$1 AND action='stop' AND status IN ('pending','failed') AND payload->>'source'='billing_lifecycle'`, *renewalServiceID); err != nil {
				return PaymentResult{}, err
			}
			var stopMayHaveRun bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provisioning_jobs WHERE service_id=$1 AND action='stop' AND status IN ('running','succeeded') AND payload->>'source'='billing_lifecycle')`, *renewalServiceID).Scan(&stopMayHaveRun); err != nil {
				return PaymentResult{}, err
			}
			needsStart = runtimeStatus != "running" || stopMayHaveRun
		}
		if _, err := tx.Exec(ctx, `UPDATE services SET status='active',next_due_at=$2,expires_at=$2,grace_until=NULL,termination_scheduled_at=NULL,suspended_at=NULL,desired_runtime_status=CASE WHEN $3::boolean THEN 'running' ELSE NULL END,updated_at=now() WHERE id=$1`, *renewalServiceID, *periodEnd, needsStart); err != nil {
			return PaymentResult{}, err
		}
		if needsStart {
			dedup := *renewalServiceID + ":renewal-start:" + result.InvoiceID
			if _, err := tx.Exec(ctx, `INSERT INTO provisioning_jobs(service_id,action,deduplication_key,payload) VALUES($1,'start',$2,jsonb_build_object('source','renewal_payment','invoice_id',$3::text)) ON CONFLICT(deduplication_key) DO NOTHING`, *renewalServiceID, dedup, result.InvoiceID); err != nil {
				return PaymentResult{}, err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_events(aggregate_type,aggregate_id,event_type,deduplication_key,payload) VALUES('service',$1,'service.renewed',$2,jsonb_build_object('invoice_id',$3::text,'next_due_at',$4::timestamptz))`, *renewalServiceID, *renewalServiceID+":renewed:"+result.InvoiceID, result.InvoiceID, *periodEnd); err != nil {
			return PaymentResult{}, err
		}
		result.ServiceIDs = []string{*renewalServiceID}
	} else {
		if orderID == nil {
			return PaymentResult{}, errors.New("invoice is not linked to an order or service")
		}
		result.OrderID = *orderID
		if _, err := tx.Exec(ctx, "UPDATE orders SET status='fulfilling', updated_at=now() WHERE id=$1", result.OrderID); err != nil {
			return PaymentResult{}, err
		}

		rows, err := tx.Query(ctx, `
		SELECT id, plan_id, region_id, quantity, configuration
		FROM order_items WHERE order_id=$1 ORDER BY created_at
	`, result.OrderID)
		if err != nil {
			return PaymentResult{}, err
		}
		type itemRow struct {
			id, planID, regionID string
			quantity             int
			configuration        []byte
		}
		items := make([]itemRow, 0)
		for rows.Next() {
			var item itemRow
			if err := rows.Scan(&item.id, &item.planID, &item.regionID, &item.quantity, &item.configuration); err != nil {
				rows.Close()
				return PaymentResult{}, err
			}
			items = append(items, item)
		}
		rows.Close()

		for _, item := range items {
			var config map[string]any
			_ = json.Unmarshal(item.configuration, &config)
			cycle, _ := config["billing_cycle"].(string)
			for index := 0; index < item.quantity; index++ {
				var serviceID string
				if err := tx.QueryRow(ctx, "SELECT gen_random_uuid()::text").Scan(&serviceID); err != nil {
					return PaymentResult{}, err
				}
				instanceName := "svc-" + strings.ReplaceAll(serviceID, "-", "")[:16]
				nextDue := addBillingCycle(time.Now().UTC(), cycle)
				if _, err := tx.Exec(ctx, `
				INSERT INTO services(id, account_id, order_item_id, plan_id, region_id, status, instance_name, billing_cycle, next_due_at, expires_at)
				VALUES($1, $2, $3, $4, $5, 'provisioning', $6, $7, $8, $8)
			`, serviceID, accountID, item.id, item.planID, item.regionID, instanceName, cycle, nextDue); err != nil {
					return PaymentResult{}, fmt.Errorf("create service: %w", err)
				}
				deduplicationKey := serviceID + ":provision:v1"
				payload, _ := json.Marshal(map[string]any{"order_id": result.OrderID, "order_item_id": item.id, "instance_name": instanceName, "configuration": config})
				if _, err := tx.Exec(ctx, `
				INSERT INTO provisioning_jobs(service_id, action, deduplication_key, payload)
				VALUES($1, 'provision', $2, $3)
			`, serviceID, deduplicationKey, payload); err != nil {
					return PaymentResult{}, fmt.Errorf("queue provisioning: %w", err)
				}
				if _, err := tx.Exec(ctx, `
				INSERT INTO outbox_events(aggregate_type, aggregate_id, event_type, deduplication_key, payload)
				VALUES('service', $1, 'service.provision_requested', $2, $3)
			`, serviceID, deduplicationKey, payload); err != nil {
					return PaymentResult{}, fmt.Errorf("write outbox: %w", err)
				}
				result.ServiceIDs = append(result.ServiceIDs, serviceID)
			}
		}
	}
	if _, err := tx.Exec(ctx, "UPDATE payment_events SET processed_at=now() WHERE id=$1", paymentEventID); err != nil {
		return PaymentResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs(actor_type, actor_id, action, target_type, target_id, metadata)
		VALUES($1, nullif($2,''), 'invoice.payment_recorded', 'invoice', $3, jsonb_build_object('provider',$4::text,'transaction_id',$5::text,'amount_minor',$6::bigint))
	`, actorType, actorID, result.InvoiceID, event.Provider, result.TransactionID, event.AmountMinor); err != nil {
		return PaymentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PaymentResult{}, err
	}
	return result, nil
}

func newDocumentNumber(prefix string) string {
	token, _, _ := security.NewToken()
	if len(token) > 10 {
		token = token[:10]
	}
	return fmt.Sprintf("%s-%s-%s", prefix, time.Now().UTC().Format("20060102"), strings.ToUpper(token))
}

func addBillingCycle(value time.Time, cycle string) time.Time {
	switch cycle {
	case "quarterly":
		return addMonthsClamped(value, 3)
	case "semiannual":
		return addMonthsClamped(value, 6)
	case "annual":
		return addMonthsClamped(value, 12)
	default:
		return addMonthsClamped(value, 1)
	}
}

func addMonthsClamped(value time.Time, months int) time.Time {
	target := time.Date(value.Year(), value.Month()+time.Month(months), 1, value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), value.Location())
	lastDay := time.Date(target.Year(), target.Month()+1, 0, 0, 0, 0, 0, target.Location()).Day()
	day := value.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(target.Year(), target.Month(), day, value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), value.Location())
}
