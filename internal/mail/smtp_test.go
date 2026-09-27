package mail

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP accepts one plain SMTP session and returns the DATA section.
func fakeSMTP(t *testing.T) (int, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	data := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		reply := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
		reply("220 fake ESMTP")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			command := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
				reply("250 fake")
			case strings.HasPrefix(command, "MAIL"), strings.HasPrefix(command, "RCPT"):
				reply("250 ok")
			case command == "DATA":
				reply("354 go ahead")
				var body strings.Builder
				for {
					part, err := reader.ReadString('\n')
					if err != nil || part == ".\r\n" {
						break
					}
					body.WriteString(part)
				}
				data <- body.String()
				reply("250 queued")
			case command == "QUIT":
				reply("221 bye")
				return
			default:
				reply("250 ok")
			}
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, data
}

func TestSendDeliversUTF8Message(t *testing.T) {
	port, data := fakeSMTP(t)
	config := Config{Host: "127.0.0.1", Port: port, From: "VPSBill <noreply@example.com>", Security: "none"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Send(ctx, config, "alice@example.com", "密码重置", "打开链接：https://billing.example.com/reset"); err != nil {
		t.Fatal(err)
	}
	message := <-data
	if !strings.Contains(message, "To: <alice@example.com>") || !strings.Contains(message, "Subject: =?UTF-8?b?") {
		t.Fatalf("unexpected headers:\n%s", message)
	}
	encoded := strings.ReplaceAll(message[strings.Index(message, "\r\n\r\n")+4:], "\r\n", "")
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !strings.Contains(string(body), "https://billing.example.com/reset") {
		t.Fatalf("body %q err %v", body, err)
	}
}

func TestValidateRejectsPasswordWithoutTLS(t *testing.T) {
	config := Config{Host: "smtp.example.com", Port: 25, From: "noreply@example.com", Username: "user", Security: "none"}
	if config.Validate() == nil {
		t.Fatal("credentials over plain SMTP must be rejected")
	}
	config.Security, config.Port = "starttls", 587
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if (Config{Host: "smtp.example.com", Port: 587}).Configured() {
		t.Fatal("a server without a sender is not configured")
	}
}
