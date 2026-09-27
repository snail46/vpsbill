package security

import (
	"testing"
	"time"
)

func TestVerifyTOTPRFCVector(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	at := time.Unix(59, 0)
	if got := totpCode(secret, at); got != "287082" {
		t.Fatalf("code=%s", got)
	}
	if !VerifyTOTP(secret, "287082", at) {
		t.Fatal("valid code rejected")
	}
	if VerifyTOTP(secret, "287083", at) {
		t.Fatal("invalid code accepted")
	}
}

func TestTOTPUri(t *testing.T) {
	uri := TOTPUri("VPSBill", "user@example.com", "ABC")
	if uri == "" || uri[:7] != "otpauth" {
		t.Fatalf("unexpected uri %q", uri)
	}
}
