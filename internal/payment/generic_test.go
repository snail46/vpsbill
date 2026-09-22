package payment

import (
	"net/url"
	"testing"
	"time"
)

func TestGenericCheckoutURL(t *testing.T) {
	gateway := GenericGateway{BaseURL: "https://pay.example/checkout", PublicURL: "https://billing.example/", Secret: "secret"}
	result, err := gateway.CheckoutURL(CheckoutRequest{MerchantReference: "PAY-1", InvoiceNumber: "INV-1", AmountMinor: 1900, Currency: "cny", ExpiresAt: time.Unix(1000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(result)
	query := parsed.Query()
	if query.Get("amount_minor") != "1900" || query.Get("notify_url") != "https://billing.example/api/v1/webhooks/payments/generic" || query.Get("signature") == "" {
		t.Fatalf("unexpected checkout URL: %s", result)
	}
}

func TestGenericCheckoutURLRejectsInvalidBase(t *testing.T) {
	if _, err := (GenericGateway{BaseURL: "javascript:alert(1)"}).CheckoutURL(CheckoutRequest{}); err == nil {
		t.Fatal("expected invalid URL error")
	}
}
