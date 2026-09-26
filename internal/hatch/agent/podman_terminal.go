package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Terminal creates a TTY exec and starts it attached. libpod then hijacks
// the HTTP connection into a raw bidirectional byte stream.
func (p *Podman) Terminal(ctx context.Context, name string, cols, rows int) (TerminalSession, error) {
	var created struct {
		ID string `json:"Id"`
	}
	if err := p.request(ctx, http.MethodPost, containerPath(name)+"/exec", map[string]any{
		"Cmd": shellCommand, "Env": []string{"TERM=xterm-256color", "HOME=/root"},
		"AttachStdin": true, "AttachStdout": true, "AttachStderr": true, "Tty": true,
	}, &created); err != nil {
		return nil, err
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", p.config.Socket)
	if err != nil {
		return nil, err
	}
	body := `{"Detach":false,"Tty":true}`
	request := fmt.Sprintf("POST %s/exec/%s/start HTTP/1.1\r\nHost: local\r\nContent-Type: application/json\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: %d\r\n\r\n%s",
		libpod, created.ID, len(body), body)
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(conn, request); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("start exec: %w", err)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("start exec: HTTP %d", response.StatusCode)
	}
	_ = conn.SetDeadline(time.Time{})
	terminal := &podmanTerminal{podman: p, exec: created.ID, conn: conn, reader: reader}
	_ = terminal.Resize(cols, rows)
	return terminal, nil
}

type podmanTerminal struct {
	podman *Podman
	exec   string
	conn   net.Conn
	reader *bufio.Reader
}

func (t *podmanTerminal) Read(p []byte) (int, error)  { return t.reader.Read(p) }
func (t *podmanTerminal) Write(p []byte) (int, error) { return t.conn.Write(p) }
func (t *podmanTerminal) Close() error                { return t.conn.Close() }

func (t *podmanTerminal) Resize(cols, rows int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	query := "h=" + strconv.Itoa(rows) + "&w=" + strconv.Itoa(cols)
	err := t.podman.request(ctx, http.MethodPost, "/exec/"+t.exec+"/resize?"+query, nil, nil)
	if err != nil && strings.Contains(err.Error(), "not running") {
		return nil
	}
	return err
}
