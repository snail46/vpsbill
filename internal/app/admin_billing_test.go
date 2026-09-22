package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestValidPaymentSignature(t *testing.T) {
	body := []byte(`{"id":"evt_1","type":"payment.succeeded"}`)
	secret := "test-webhook-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !validPaymentSignature(body, signature, secret) {
		t.Fatal("expected valid signature")
	}
	if validPaymentSignature([]byte("tampered"), signature, secret) {
		t.Fatal("tampered payload must fail validation")
	}
}

func TestParseAmountMinor(t *testing.T) {
	for input, expected := range map[string]int64{"19": 1900, "19.0": 1900, "19.05": 1905, "0.01": 1} {
		actual, err := parseAmountMinor(input)
		if err != nil || actual != expected {
			t.Fatalf("%s: got %d, %v", input, actual, err)
		}
	}
	for _, input := range []string{"", "-1.00", "1.001", "abc"} {
		if _, err := parseAmountMinor(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
