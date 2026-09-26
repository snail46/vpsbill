package wsterm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"vpsbill/internal/hatch/agent/agenttest"
)

func TestPumpUsesBrowserFraming(t *testing.T) {
	terminal := agenttest.NewRuntime("lxc")
	terminal.Instances["svc"] = &agenttest.Instance{Status: "running"}
	session, err := terminal.Terminal(context.Background(), "svc", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	echo := session.(*agenttest.EchoTerminal)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		Pump(r.Context(), conn, session)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	browser, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.CloseNow()
	read := func() (websocket.MessageType, string) {
		kind, data, err := browser.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return kind, string(data)
	}
	if kind, prompt := read(); kind != websocket.MessageBinary || prompt != "hatch$ " {
		t.Fatalf("output must be binary frames: %v %q", kind, prompt)
	}
	if err := browser.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":132,"rows":43}`)); err != nil {
		t.Fatal(err)
	}
	if err := browser.Write(ctx, websocket.MessageBinary, []byte("id\r")); err != nil {
		t.Fatal(err)
	}
	if _, echoed := read(); echoed != "echo:id\r" {
		t.Fatalf("keystrokes not forwarded: %q", echoed)
	}
	if echo.Cols != 132 || echo.Rows != 43 {
		t.Fatalf("resize not applied: %dx%d", echo.Cols, echo.Rows)
	}
	echo.Close()
	if _, _, err := browser.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("closing the terminal must close the socket normally, got %v", err)
	}
}

func TestClientRoundTrip(t *testing.T) {
	terminal := agenttest.NewRuntime("lxc")
	terminal.Instances["svc"] = &agenttest.Instance{Status: "running"}
	session, _ := terminal.Terminal(context.Background(), "svc", 80, 24)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		Pump(r.Context(), conn, session)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(conn)
	defer client.Close()
	buffer := make([]byte, 64)
	if n, _ := client.Read(buffer); string(buffer[:n]) != "hatch$ " {
		t.Fatalf("unexpected prompt %q", buffer[:n])
	}
	if err := client.Resize(90, 20); err != nil {
		t.Fatal(err)
	}
	_, _ = client.Write([]byte("w\r"))
	if n, _ := client.Read(buffer); string(buffer[:n]) != "echo:w\r" {
		t.Fatalf("unexpected echo %q", buffer[:n])
	}
}
