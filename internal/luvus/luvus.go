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
)

const Session = "hand"

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

type Client struct{ Socket string }

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

func StartServer(bin, session, dir string, environ []string) error {
	logPath := filepath.Join(dir, "server-start.log")
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(bin, "--session", session, "server", "start")
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, Scrub(environ), log, log
	if err := cmd.Run(); err != nil {
		out, _ := os.ReadFile(logPath)
		return fmt.Errorf("luvus server start: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func Scrub(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		agent := name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") || strings.HasPrefix(name, "CODEX_")
		pane := strings.HasPrefix(name, "LUVUS_") && name != "LUVUS_HOME"
		if !agent && !pane {
			out = append(out, kv)
		}
	}
	return out
}
