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
