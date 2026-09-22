package payment

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type AlipayGateway struct {
	AppID, PrivateKey, PublicKey, GatewayURL, PublicURL string
	Client                                              *http.Client
}

func (g AlipayGateway) CheckoutURL(ctx context.Context, request CheckoutRequest) (string, error) {
	privateKey, err := parsePrivateKey(g.PrivateKey)
	if err != nil {
		return "", err
	}
	biz, _ := json.Marshal(map[string]string{"out_trade_no": request.MerchantReference, "total_amount": minorAmount(request.AmountMinor), "subject": "账单 " + request.InvoiceNumber, "timeout_express": "30m"})
	values := url.Values{"app_id": {g.AppID}, "method": {"alipay.trade.precreate"}, "format": {"JSON"}, "charset": {"utf-8"}, "sign_type": {"RSA2"}, "timestamp": {time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05")}, "version": {"1.0"}, "notify_url": {strings.TrimRight(g.PublicURL, "/") + "/api/v1/webhooks/payments/alipay"}, "biz_content": {string(biz)}}
	values.Set("sign", signRSA2(canonical(values), privateKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.GatewayURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	// The fields need individual tags; use an auxiliary map to tolerate optional error fields.
	var raw struct {
		Response json.RawMessage `json:"alipay_trade_precreate_response"`
	}
	if err = json.Unmarshal(body, &raw); err != nil {
		return "", errors.New("invalid alipay response")
	}
	var detail struct {
		Code    string `json:"code"`
		Msg     string `json:"msg"`
		SubCode string `json:"sub_code"`
		SubMsg  string `json:"sub_msg"`
		QRCode  string `json:"qr_code"`
	}
	if err = json.Unmarshal(raw.Response, &detail); err != nil {
		return "", errors.New("invalid alipay response")
	}
	if detail.Code != "10000" || detail.QRCode == "" {
		return "", fmt.Errorf("alipay precreate failed: %s %s", detail.SubCode, defaultString(detail.SubMsg, detail.Msg))
	}
	return detail.QRCode, nil
}

func VerifyAlipay(values url.Values, publicKeyText string) bool {
	publicKey, err := parsePublicKey(publicKeyText)
	if err != nil {
		return false
	}
	signature, err := base64.StdEncoding.DecodeString(values.Get("sign"))
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(canonical(values)))
	return rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature) == nil
}

func canonical(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key != "sign" && key != "sign_type" && values.Get(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values.Get(key))
	}
	return strings.Join(parts, "&")
}

func signRSA2(content string, key *rsa.PrivateKey) string {
	digest := sha256.Sum256([]byte(content))
	signature, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	return base64.StdEncoding.EncodeToString(signature)
}

func parsePrivateKey(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(normalizePEM(value, "PRIVATE KEY")))
	if block == nil {
		return nil, errors.New("invalid Alipay private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("invalid Alipay private key")
}

func parsePublicKey(value string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(normalizePEM(value, "PUBLIC KEY")))
	if block == nil {
		return nil, errors.New("invalid Alipay public key")
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("invalid Alipay public key")
}

func normalizePEM(value, kind string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "-----BEGIN") {
		return value
	}
	return "-----BEGIN " + kind + "-----\n" + value + "\n-----END " + kind + "-----"
}
