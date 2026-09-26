package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// consoleTickets signs short-lived console tickets for bridged terminals.
// Tickets are stateless so any API instance can verify them; the WebSocket
// request is additionally authenticated by the customer session cookie.
type consoleTickets struct {
	key []byte
	now func() time.Time
}

const consoleTicketTTL = 60 * time.Second

func newConsoleTickets(secret string) consoleTickets {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("vpsbill console ticket v1"))
	return consoleTickets{key: mac.Sum(nil), now: time.Now}
}

func (t consoleTickets) issue(serviceID, kind string) string {
	payload := serviceID + "|" + kind + "|" + strconv.FormatInt(t.now().Add(consoleTicketTTL).Unix(), 10)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return encoded + "." + t.sign(encoded)
}

func (t consoleTickets) verify(ticket, serviceID, kind string) error {
	encoded, signature, ok := strings.Cut(ticket, ".")
	if !ok || !hmac.Equal([]byte(signature), []byte(t.sign(encoded))) {
		return errors.New("invalid console ticket")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return errors.New("invalid console ticket")
	}
	parts := strings.Split(string(payload), "|")
	if len(parts) != 3 || parts[0] != serviceID || parts[1] != kind {
		return errors.New("console ticket does not match this service")
	}
	expires, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || t.now().Unix() > expires {
		return errors.New("console ticket expired")
	}
	return nil
}

func (t consoleTickets) sign(encoded string) string {
	mac := hmac.New(sha256.New, t.key)
	mac.Write([]byte(encoded))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
