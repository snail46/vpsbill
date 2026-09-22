package payment

import (
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type EpayGateway struct{ APIURL, PartnerID, MerchantKey, PaymentType, PublicURL, SiteName string }

func (g EpayGateway) CheckoutURL(request CheckoutRequest) (string, error) {
	endpoint, err := url.Parse(strings.TrimSpace(g.APIURL))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return "", errors.New("invalid epay API URL")
	}
	if endpoint.Path == "" || strings.HasSuffix(endpoint.Path, "/") {
		endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/submit.php"
	}
	values := endpoint.Query()
	values.Set("pid", g.PartnerID)
	values.Set("type", defaultString(g.PaymentType, "alipay"))
	values.Set("out_trade_no", request.MerchantReference)
	values.Set("notify_url", strings.TrimRight(g.PublicURL, "/")+"/api/v1/webhooks/payments/epay")
	values.Set("return_url", strings.TrimRight(g.PublicURL, "/")+"/portal?payment="+url.QueryEscape(request.MerchantReference))
	values.Set("name", "账单 "+request.InvoiceNumber)
	values.Set("money", minorAmount(request.AmountMinor))
	values.Set("sitename", defaultString(g.SiteName, "VPS Billing"))
	values.Set("sign", EpaySign(values, g.MerchantKey))
	values.Set("sign_type", "MD5")
	endpoint.RawQuery = values.Encode()
	return endpoint.String(), nil
}

func EpaySign(values url.Values, key string) string {
	keys := make([]string, 0, len(values))
	for name, items := range values {
		if name != "sign" && name != "sign_type" && len(items) > 0 && items[0] != "" {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, name := range keys {
		parts = append(parts, name+"="+values.Get(name))
	}
	sum := md5.Sum([]byte(strings.Join(parts, "&") + key))
	return hex.EncodeToString(sum[:])
}

func VerifyEpay(values url.Values, key string) bool {
	expected, supplied := EpaySign(values, key), strings.ToLower(strings.TrimSpace(values.Get("sign")))
	return supplied != "" && subtle.ConstantTimeCompare([]byte(expected), []byte(supplied)) == 1
}

func minorAmount(value int64) string { return fmt.Sprintf("%d.%02d", value/100, value%100) }

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
