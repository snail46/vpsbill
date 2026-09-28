package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func NewTOTPSecret() (string, error) {
	value := make([]byte, 20)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(value), nil
}

func TOTPUri(issuer, email, secret string) string {
	label := strings.TrimSpace(issuer) + ":" + strings.TrimSpace(email)
	values := url.Values{}
	values.Set("secret", secret)
	values.Set("issuer", strings.TrimSpace(issuer))
	values.Set("algorithm", "SHA1")
	values.Set("digits", "6")
	values.Set("period", "30")
	return "otpauth://totp/" + url.PathEscape(label) + "?" + values.Encode()
}

func VerifyTOTP(secret, code string, now time.Time) bool {
	_, ok := MatchTOTP(secret, code, now)
	return ok
}

// MatchTOTP checks a code against the current 30-second step and its
// neighbours and returns the step it matched, so callers can refuse a code
// that was already used.
func MatchTOTP(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	if _, err := strconv.Atoi(code); err != nil {
		return 0, false
	}
	for offset := -1; offset <= 1; offset++ {
		at := now.Add(time.Duration(offset) * 30 * time.Second)
		if totpCode(secret, at) == code {
			return at.Unix() / 30, true
		}
	}
	return 0, false
}

func totpCode(secret string, now time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return ""
	}
	counter := uint64(now.Unix() / 30)
	message := make([]byte, 8)
	binary.BigEndian.PutUint64(message, counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(message)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 | (uint32(sum[offset+1])&0xff)<<16 | (uint32(sum[offset+2])&0xff)<<8 | (uint32(sum[offset+3]) & 0xff)
	return fmt.Sprintf("%06d", value%1000000)
}
