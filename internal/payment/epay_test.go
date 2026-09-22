package payment

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestEpayCheckoutAndSignature(t *testing.T) {
	gateway := EpayGateway{APIURL: "https://pay.example/", PartnerID: "1001", MerchantKey: "secret", PaymentType: "alipay", PublicURL: "https://billing.example", SiteName: "Cloud"}
	checkout, err := gateway.CheckoutURL(CheckoutRequest{MerchantReference: "PAY-1", InvoiceNumber: "INV-1", AmountMinor: 1905, ExpiresAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(checkout)
	if parsed.Path != "/submit.php" || parsed.Query().Get("money") != "19.05" || !VerifyEpay(parsed.Query(), "secret") {
		t.Fatalf("unexpected checkout URL: %s", checkout)
	}
	values := parsed.Query()
	values.Set("money", "1.00")
	if VerifyEpay(values, "secret") {
		t.Fatal("tampered payment was accepted")
	}
}

func TestEpayCanonicalSignature(t *testing.T) {
	values := url.Values{"pid": {"1001"}, "money": {"1.00"}, "sign_type": {"MD5"}}
	if got := EpaySign(values, "abc"); got != "aa3b452b001275ce91e96bd2ee7e1e96" {
		// Keep the expected canonical string visible if the protocol implementation changes.
		t.Fatalf("signature changed for %q: %s", strings.Join([]string{"money=1.00", "pid=1001"}, "&")+"abc", got)
	}
}
