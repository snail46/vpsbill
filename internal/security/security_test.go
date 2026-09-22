package security

import (
	"bytes"
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("expected password to match: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong password")
	if err != nil || ok {
		t.Fatalf("expected password not to match: ok=%v err=%v", ok, err)
	}
}

func TestTokenAndSecretBox(t *testing.T) {
	token, hash, err := NewToken()
	if err != nil || token == "" || !bytes.Equal(hash, HashToken(token)) {
		t.Fatal("token generation failed")
	}
	box, err := NewSecretBox(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal("api-secret")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := box.Open(sealed)
	if err != nil || opened != "api-secret" {
		t.Fatalf("unexpected secret result: %q %v", opened, err)
	}
}
