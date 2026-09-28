package luvus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	ErrUnreachable  = errors.New("luvus server unreachable")
	ErrIncompatible = errors.New("luvus is incompatible")
)

var Required = []string{
	"uhp.capabilities",
	"terminal.backend.create",
	"terminal.backend.inventory",
	"terminal.backend.validate",
	"terminal.backend.close",
	"agent.explain",
	"agent.read",
	"agent.prompt",
	"agent.keys",
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return "luvus " + e.Code + ": " + e.Message }

func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

const DefaultTimeout = 30 * time.Second

type Client struct {
	Socket  string
	Timeout time.Duration
}

func SocketPath(getenv func(string) string, session string) string {
	if p := getenv("HAND_LUVUS_SOCKET"); p != "" {
		return p
	}
	root := getenv("LUVUS_HOME")
	if root == "" {
		root = filepath.Join(getenv("HOME"), ".luvus")
	}
	p := filepath.Join(root, "sessions", session, "luvus.sock")
	if len(p) < 100 {
		return p
	}
	h := fnv.New64a()
	h.Write([]byte(p))
	return fmt.Sprintf("/tmp/luvus-%d/%016x-api.sock", os.Geteuid(), h.Sum64())
}

var requests atomic.Uint64

func (c Client) Call(ctx context.Context, method string, params, out any) error {
	if params == nil {
		params = struct{}{}
	}
	req, err := json.Marshal(struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{"hand-" + strconv.FormatUint(requests.Add(1), 10), method, params})
	if err != nil {
		return err
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return fmt.Errorf("%w at %s: %w", ErrUnreachable, c.Socket, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(bytes.TrimSpace(line)) == 0 {
		return fmt.Errorf("luvus %s: no reply: %w", method, err)
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("luvus %s: bad reply: %w", method, err)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}

type Capabilities struct {
	Protocol struct {
		Name  string `json:"name"`
		Major int    `json:"major"`
		Minor int    `json:"minor"`
	} `json:"protocol"`
	Methods          []string `json:"methods"`
	ServerGeneration string   `json:"server_generation"`
}

func (c Client) Check(ctx context.Context) (Capabilities, error) {
	var caps Capabilities
	if err := c.Call(ctx, "uhp.capabilities", nil, &caps); err != nil {
		return caps, err
	}
	if caps.Protocol.Name != "luvus-uhp" || caps.Protocol.Major != 1 {
		return caps, fmt.Errorf("%w: server speaks %s %d.%d, hand needs luvus-uhp 1.x", ErrIncompatible, caps.Protocol.Name, caps.Protocol.Major, caps.Protocol.Minor)
	}
	for _, m := range Required {
		if !slices.Contains(caps.Methods, m) {
			return caps, fmt.Errorf("%w: server lacks method %s", ErrIncompatible, m)
		}
	}
	return caps, nil
}

func Ensure(ctx context.Context, c Client, start func() error) (Capabilities, error) {
	caps, err := c.Check(ctx)
	if !errors.Is(err, ErrUnreachable) {
		return caps, err
	}
	if err := start(); err != nil {
		return caps, err
	}
	return c.Check(ctx)
}

func StartServer(ctx context.Context, bin, session, unit, dir string, environ []string) error {
	env := Scrub(environ)
	if run, ok := userManager(env); ok && unit != "" {
		if err := startUnit(ctx, run, bin, session, unit, dir, env); !errors.Is(err, errNoManager) {
			return err
		}
	}
	logPath := filepath.Join(dir, "server-start.log")
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := command(ctx, env, bin, "--session", session, "server", "start")
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, log, log
	if err := cmd.Run(); err != nil {
		out, _ := os.ReadFile(logPath)
		return fmt.Errorf("luvus server start: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

var errNoManager = errors.New("no systemd user manager")

var systemdOwned = []string{"INVOCATION_ID", "JOURNAL_STREAM", "SYSTEMD_EXEC_PID", "MANAGERPID", "NOTIFY_SOCKET", "LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES", "WATCHDOG_PID", "WATCHDOG_USEC"}

func userManager(env []string) (string, bool) {
	var runtime, path string
	for _, kv := range env {
		switch name, value, _ := strings.Cut(kv, "="); name {
		case "XDG_RUNTIME_DIR":
			runtime = value
		case "PATH":
			path = value
		}
	}
	if runtime == "" {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(runtime, "systemd", "private")); err != nil {
		return "", false
	}
	for _, dir := range filepath.SplitList(path) {
		p := filepath.Join(dir, "systemd-run")
		if fi, err := os.Stat(p); filepath.IsAbs(dir) && err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

func command(ctx context.Context, env []string, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env, cmd.WaitDelay = env, time.Second
	return cmd
}

func startUnit(ctx context.Context, run, bin, session, unit, dir string, env []string) error {
	systemctl := func(args ...string) error {
		return command(ctx, env, filepath.Join(filepath.Dir(run), "systemctl"), append([]string{"--user"}, args...)...).Run()
	}
	_ = systemctl("reset-failed", unit+".service")
	args := []string{"--user", "--unit=" + unit, "--description=Luvus server for " + session, "--working-directory=" + dir,
		"-p", "Type=forking", "-p", "Restart=on-failure", "-p", "RestartSec=5"}
	for _, kv := range env {
		if name, _, _ := strings.Cut(kv, "="); !slices.Contains(systemdOwned, name) {
			args = append(args, "-E", name)
		}
	}
	out, err := command(ctx, env, run, append(args, bin, "--session", session, "server", "start")...).CombinedOutput()
	switch {
	case err == nil:
		return nil
	case bytes.Contains(out, []byte("Failed to connect to")):
		return errNoManager
	case bytes.Contains(out, []byte("already loaded")) && systemctl("start", unit+".service") == nil:
		return nil
	}
	return fmt.Errorf("systemd-run --user --unit=%s: %w: %s", unit, err, bytes.TrimSpace(out))
}

func Scrub(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		agent := name == "CLAUDECODE" || name == "HAND_HOME" || strings.HasPrefix(name, "CLAUDE_CODE_") || strings.HasPrefix(name, "CODEX_") && name != "CODEX_HOME"
		pane := strings.HasPrefix(name, "LUVUS_") && name != "LUVUS_HOME"
		if !agent && !pane {
			out = append(out, kv)
		}
	}
	return out
}
