package payment

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/url"
	"testing"
)

func TestVerifyAlipay(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	values := url.Values{"app_id": {"123"}, "out_trade_no": {"PAY-1"}, "total_amount": {"19.00"}}
	values.Set("sign", signRSA2(canonical(values), key))
	if !VerifyAlipay(values, publicPEM) {
		t.Fatal("valid signature rejected")
	}
	values.Set("total_amount", "20.00")
	if VerifyAlipay(values, publicPEM) {
		t.Fatal("tampered callback accepted")
	}
	values.Set("sign", base64.StdEncoding.EncodeToString([]byte("bad")))
	if VerifyAlipay(values, publicPEM) {
		t.Fatal("invalid signature accepted")
	}
}
