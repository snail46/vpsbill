// Package mail sends plain text mail through the SMTP server configured in
// the admin site settings.
package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Config describes the outgoing SMTP server. Security is "starttls" (usually
// port 587), "tls" (implicit TLS, usually 465) or "none".
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	Security string
}

// Configured reports whether enough is set to attempt delivery.
func (c Config) Configured() bool {
	return strings.TrimSpace(c.Host) != "" && c.Port > 0 && strings.TrimSpace(c.From) != ""
}

// Validate checks the settings without connecting.
func (c Config) Validate() error {
	if !c.Configured() {
		return nil
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("SMTP 端口无效")
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return errors.New("发件人地址无效")
	}
	switch c.Security {
	case "starttls", "tls", "none":
	default:
		return errors.New("加密方式只支持 starttls、tls 或 none")
	}
	if c.Security == "none" && c.Username != "" {
		return errors.New("使用账号密码登录时必须启用 STARTTLS 或 TLS")
	}
	return nil
}

// Send delivers one plain text message.
func Send(ctx context.Context, c Config, to, subject, body string) error {
	if !c.Configured() {
		return errors.New("SMTP is not configured")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	from, err := mail.ParseAddress(c.From)
	if err != nil {
		return err
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return err
	}
	address := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	dialer := &net.Dialer{Deadline: deadline}
	tlsConfig := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if c.Security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(deadline)
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()
	if c.Security == "starttls" {
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if c.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message(from, recipient, subject, body)); err != nil {
		writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func message(from, to *mail.Address, subject, body string) []byte {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]
	var b strings.Builder
	b.WriteString("From: " + from.String() + "\r\n")
	b.WriteString("To: " + to.String() + "\r\n")
	b.WriteString("Subject: " + mime.BEncoding.Encode("UTF-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: <" + hex.EncodeToString(id) + "@" + domain + ">\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")
	return []byte(b.String())
}
