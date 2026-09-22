package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type CheckoutRequest struct {
	MerchantReference string
	InvoiceNumber     string
	AmountMinor       int64
	Currency          string
	ExpiresAt         time.Time
}

type GenericGateway struct{ BaseURL, PublicURL, Secret string }

func (g GenericGateway) Available() bool { return strings.TrimSpace(g.BaseURL) != "" }

func (g GenericGateway) CheckoutURL(request CheckoutRequest) (string, error) {
	base, err := url.Parse(strings.TrimSpace(g.BaseURL))
	if err != nil || base.Scheme == "" || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return "", errors.New("invalid payment checkout URL")
	}
	public := strings.TrimRight(g.PublicURL, "/")
	values := base.Query()
	values.Set("merchant_reference", request.MerchantReference)
	values.Set("invoice_number", request.InvoiceNumber)
	values.Set("amount_minor", strconv.FormatInt(request.AmountMinor, 10))
	values.Set("currency", strings.ToUpper(request.Currency))
	values.Set("expires_at", strconv.FormatInt(request.ExpiresAt.Unix(), 10))
	values.Set("notify_url", public+"/api/v1/webhooks/payments/generic")
	values.Set("return_url", public+"/portal?payment="+url.QueryEscape(request.MerchantReference))
	canonical := values.Encode()
	mac := hmac.New(sha256.New, []byte(g.Secret))
	_, _ = mac.Write([]byte(canonical))
	values.Set("signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	base.RawQuery = values.Encode()
	return base.String(), nil
}
