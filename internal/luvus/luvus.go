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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/atqamz/hand/internal/proc"
)

var (
	ErrUnreachable  = errors.New("luvus server unreachable")
	ErrIncompatible = errors.New("luvus is incompatible")
	ErrForeignOwner = errors.New("luvus server belongs to another user")
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
	Relist  func(ctx context.Context, stale string) (string, error)
}

func (c Client) dial(ctx context.Context) (net.Conn, error) {
	addr := c.Socket
	conn, err := dial(ctx, addr)
	if err != nil && c.Relist != nil && !errors.Is(err, ErrForeignOwner) {
		if fresh, listErr := c.Relist(ctx, addr); listErr != nil {
			err = errors.Join(err, listErr)
		} else {
			addr = fresh
			conn, err = dial(ctx, addr)
		}
	}
	switch {
	case err == nil:
		return conn, nil
	case errors.Is(err, ErrForeignOwner):
		return nil, fmt.Errorf("%w: refusing %s", err, addr)
	}
	return nil, fmt.Errorf("%w at %s: %w", ErrUnreachable, addr, err)
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
	return fmt.Sprintf("%s/luvus-%d/%016x-api.sock", tempRoot, os.Geteuid(), h.Sum64())
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
	conn, err := c.dial(ctx)
	if err != nil {
		return err
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

var (
	pingWait     = 5 * time.Second
	serverExitBy = 10 * time.Second
)

func (c Client) ping(ctx context.Context, out any) error {
	ctx, cancel := context.WithTimeout(ctx, pingWait)
	defer cancel()
	return c.Call(ctx, "ping", nil, out)
}

func (c Client) Version(ctx context.Context) (string, error) {
	var pong struct {
		Version string `json:"version"`
	}
	err := c.ping(ctx, &pong)
	return pong.Version, err
}

func (c Client) StopServer(ctx context.Context, sessionDir string, kill func(pid int, marker string) error) error {
	file := filepath.Join(sessionDir, "server.pid")
	var pid int
	var marker string
	b, _ := os.ReadFile(file)
	n, _ := fmt.Sscanf(string(b), "%d %s", &pid, &marker)
	trusted := n == 2 && pid > 0 && marker != ""
	alive := func() bool {
		if !trusted {
			return false
		}
		m, err := ProcStartMarker(pid)
		return err == nil && m == marker
	}
	answers := func(ctx context.Context) bool { return c.ping(ctx, nil) == nil }
	if !alive() {
		if answers(ctx) {
			return fmt.Errorf("luvus server answers, but %s names no live process; stop it by hand", file)
		}
		return nil
	}
	acked := c.Call(ctx, "server.stop", nil, nil) == nil
	waitCtx, cancel := context.WithTimeout(ctx, serverExitBy)
	defer cancel()
	for ; waitCtx.Err() == nil; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(file); !alive() && (err != nil || !answers(waitCtx)) {
			return nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !acked && answers(ctx) {
		return fmt.Errorf("luvus server %d refused server.stop and still answers; it was left running", pid)
	}
	if err := kill(pid, marker); err != nil {
		return err
	}
	if alive() {
		return fmt.Errorf("luvus server %d is still alive after the kill", pid)
	}
	return nil
}

func AttachArgv(bin, session, pane string) []string {
	if pane == "" {
		return []string{bin, "session", "attach", session}
	}
	return []string{bin, "--session", session, "attach", pane}
}

func StartServer(ctx context.Context, bin, session, dir string, environ []string) error {
	ctx, cancel := context.WithTimeout(ctx, startWait)
	defer cancel()
	env := longPaths(runtime.GOOS, Scrub(environ))
	logPath := filepath.Join(dir, "server-start.log")
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	start := func(name string, args ...string) error {
		cmd := command(ctx, env, name, args...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, log, log
		return cmd.Run()
	}
	serve := []string{bin, "--session", session, "server", "start"}
	if run, ok := UserManager(env); ok && start(run, append([]string{"--user", "--scope", "--collect", "--quiet", "--description=Luvus server for " + session, "--"}, serve...)...) == nil {
		return nil
	}
	if err := start(serve[0], serve[1:]...); err != nil {
		out, _ := os.ReadFile(logPath)
		return fmt.Errorf("luvus server start: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func longPaths(goos string, env []string) []string {
	if goos != "windows" || slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(strings.ToUpper(kv), "GIT_CONFIG_COUNT=") }) {
		return env
	}
	return append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.longpaths", "GIT_CONFIG_VALUE_0=true")
}

var startWait = time.Minute

func UserManager(env []string) (string, bool) {
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
	proc.NoWindow(cmd)
	return cmd
}

func Scrub(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); !scrubbed(name) {
			out = append(out, kv)
		}
	}
	return out
}

func scrubbed(name string) bool {
	agent := name == "CLAUDECODE" || name == "HAND_HOME" || strings.HasPrefix(name, "CLAUDE_CODE_") || strings.HasPrefix(name, "CODEX_") && name != "CODEX_HOME"
	pane := strings.HasPrefix(name, "LUVUS_") && name != "LUVUS_HOME"
	return agent || pane
}
