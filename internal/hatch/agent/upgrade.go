package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vpsbill/internal/hatch/protocol"
)

// Self-upgrade: every API image bundles the agent built from the same
// commit (/api/v1/agent/download/), and the server asks a connecting agent
// that runs another version to move to it. The new binary is staged in
// <state_dir>/bin, never written over the installed one (the systemd unit
// keeps /usr/local read-only); at start the installed binary hands over to
// the staged one. A staged build that does not stay connected for a minute
// in stagedTries starts is dropped, the installed binary runs again, and
// that version is refused from then on (see rejectedFile).

const (
	stagedTries = 3
	// maxAgentBytes bounds a downloaded binary.
	maxAgentBytes = 256 << 20
)

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)

func stagedBinary(stateDir string) string { return filepath.Join(stateDir, "bin", "hatch-agent") }
func stagedTriesFile(stateDir string) string {
	return filepath.Join(stateDir, "bin", "tries")
}

// stagedVersionFile names the staged build's version; rejectedFile the
// last version dropped for failing to start.
func stagedVersionFile(stateDir string) string { return filepath.Join(stateDir, "bin", "version") }
func rejectedFile(stateDir string) string      { return filepath.Join(stateDir, "bin", "rejected") }

// Handover replaces this process with the staged agent build when there is
// one and this process is not it. It returns when this binary should keep
// running.
func Handover(config Config, logger *slog.Logger) {
	staged := stagedBinary(config.StateDir)
	if _, err := os.Stat(staged); err != nil {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if self == staged {
		return
	}
	if !config.Upgrades() {
		logger.Info("auto_upgrade is off; ignoring the staged agent build", "path", staged)
		return
	}
	data, _ := os.ReadFile(stagedTriesFile(config.StateDir))
	tries, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if tries >= stagedTries {
		logger.Error("staged agent build failed to stay connected; running the installed one", "path", staged, "tries", tries)
		if version, err := os.ReadFile(stagedVersionFile(config.StateDir)); err == nil {
			_ = os.WriteFile(rejectedFile(config.StateDir), version, 0o600)
		}
		_ = os.Remove(staged)
		_ = os.Remove(stagedTriesFile(config.StateDir))
		_ = os.Remove(stagedVersionFile(config.StateDir))
		return
	}
	if err := os.WriteFile(stagedTriesFile(config.StateDir), []byte(strconv.Itoa(tries+1)), 0o600); err != nil {
		logger.Error("record staged agent start", "error", err)
		return
	}
	err = syscall.Exec(staged, os.Args, os.Environ())
	logger.Error("start staged agent build", "path", staged, "error", err)
}

// confirmUpgrade marks the running build as good once it stayed connected.
func confirmUpgrade(stateDir string) { _ = os.Remove(stagedTriesFile(stateDir)) }

// upgrade stages the build the server names and asks the process to restart
// once other requests are done.
func (c *Client) upgrade(ctx context.Context, raw json.RawMessage) error {
	var params protocol.UpgradeParams
	if err := json.Unmarshal(raw, &params); err != nil || !versionPattern.MatchString(params.Version) {
		return errorf(protocol.CodeInvalid, "invalid upgrade version")
	}
	if !c.config.Upgrades() {
		return errorf(protocol.CodeUnsupported, "auto_upgrade is off in the agent config")
	}
	if params.Version == c.version {
		return nil
	}
	if rejected, _ := os.ReadFile(rejectedFile(c.config.StateDir)); strings.TrimSpace(string(rejected)) == params.Version {
		return errorf(protocol.CodeConflict, "version %s failed to start on this host before; reinstall the agent to retry", params.Version)
	}
	if !c.upgrading.CompareAndSwap(false, true) {
		return errorf(protocol.CodeConflict, "an upgrade is already running")
	}
	staged, err := c.stage(ctx, params.Version)
	if err != nil {
		c.upgrading.Store(false)
		return err
	}
	c.logger.Info("agent upgrade staged; restarting when idle", "from", c.version, "to", params.Version, "path", staged)
	go c.restartWhenIdle()
	return nil
}

// stage downloads, checks and stages one build.
func (c *Client) stage(ctx context.Context, version string) (string, error) {
	base := strings.TrimRight(c.config.ServerURL, "/") + "/api/v1/agent/download/"
	name := "hatch-agent-linux-" + runtime.GOARCH
	sums, err := c.download(ctx, base+"SHA256SUMS", 64<<10)
	if err != nil {
		return "", err
	}
	want := ""
	scanner := bufio.NewScanner(bytes.NewReader(sums))
	for scanner.Scan() {
		if fields := strings.Fields(scanner.Text()); len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			want = strings.ToLower(fields[0])
		}
	}
	if want == "" {
		return "", fmt.Errorf("SHA256SUMS lists no %s", name)
	}
	binary, err := c.download(ctx, base+name, maxAgentBytes)
	if err != nil {
		return "", err
	}
	if sum := sha256.Sum256(binary); hex.EncodeToString(sum[:]) != want {
		return "", fmt.Errorf("%s does not match SHA256SUMS", name)
	}
	staged := stagedBinary(c.config.StateDir)
	if err := os.MkdirAll(filepath.Dir(staged), 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(staged), "hatch-agent-*.new")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(binary)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(file.Name(), 0o755)
	}
	if err != nil {
		return "", err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(checkCtx, file.Name(), "version").Output()
	if err != nil {
		return "", fmt.Errorf("run the downloaded agent: %w", err)
	}
	if got := strings.TrimSpace(string(output)); got != version {
		return "", fmt.Errorf("the downloaded agent is version %q, not %q", got, version)
	}
	if err := os.WriteFile(stagedVersionFile(c.config.StateDir), []byte(version), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(file.Name(), staged); err != nil {
		return "", err
	}
	confirmUpgrade(c.config.StateDir)
	return staged, nil
}

func (c *Client) download(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered HTTP %d", url, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return data, nil
}

// restartWhenIdle waits for running requests (instance creation takes
// minutes) and then ends the process, which systemd starts again on the
// staged build.
func (c *Client) restartWhenIdle() {
	deadline := time.Now().Add(requestTimeout)
	for c.busy.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Second)
	}
	c.logger.Info("restarting on the upgraded agent build")
	if c.exit != nil {
		c.exit()
	}
}
