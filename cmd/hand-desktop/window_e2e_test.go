//go:build e2e

package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fleetName = "Window Check"

var denied = regexp.MustCompile(`(?i)^(HAND_|SECONDHAND_|MYGO_|LUVUS_|CLAUDE|ANTHROPIC|OPENAI|CODEX|GH_|GITHUB_|ACTIONS_|RUNNER_|NODE_OPTIONS=|XDG_RUNTIME_DIR=|DBUS_SESSION_BUS_ADDRESS=)`)

type node struct {
	ID     string `json:"id"`
	Role   string `json:"role"`
	Label  string `json:"label"`
	Value  string `json:"value"`
	Parent string `json:"parent"`
}

type rpc struct {
	conn net.Conn
	r    *bufio.Reader
	next int
	wait time.Duration
}

func (c *rpc) call(method string, params any, into any) error {
	c.next++
	line, _ := json.Marshal(map[string]any{"id": c.next, "method": method, "params": params})
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return err
	}
	wait := c.wait
	if wait <= 0 {
		wait = 60 * time.Second
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(wait))
	answer, err := c.r.ReadBytes('\n')
	if err != nil {
		return err
	}
	var res struct {
		Result json.RawMessage
		Error  *struct{ Code, Message string }
	}
	if err := json.Unmarshal(answer, &res); err != nil {
		return err
	}
	if res.Error != nil {
		return fmt.Errorf("%s: %s: %s", method, res.Error.Code, res.Error.Message)
	}
	if into == nil {
		return nil
	}
	return json.Unmarshal(res.Result, into)
}

func goBuild(t *testing.T, dir, out, tags string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"build", "-trimpath", "-tags", tags, "-o", out}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", out, err, b)
	}
}

func TestRealWindow(t *testing.T) {
	outDir := os.Getenv("HAND_WINDOW_OUT")
	if outDir == "" {
		t.Skip("set HAND_WINDOW_OUT to a folder to run a real window and keep its picture there")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "hw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dir := func(name string) string {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	bin, home, sh, fleet, tmp, state := dir("bin"), dir("home"), dir("sh"), dir("fleet"), dir("tmp"), dir("state")
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	handBin, deskBin := filepath.Join(bin, "hand"+exe), filepath.Join(bin, "hand-desktop"+exe)
	goBuild(t, "../..", handBin, "", ".")
	if runtime.GOOS == "windows" {
		goBuild(t, ".", deskBin, "e2e", "-ldflags=-H=windowsgui", ".")
	} else {
		goBuild(t, ".", deskBin, "e2e", ".")
	}

	sock := filepath.Join(dir("s"), "w.sock")
	env := []string{}
	for _, kv := range os.Environ() {
		if !denied.MatchString(kv) {
			env = append(env, kv)
		}
	}
	env = append(env,
		"HOME="+home, "SECONDHAND_HOME="+sh, "XDG_STATE_HOME="+state,
		"TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp,
		"HAND_LUVUS_BASE=http://127.0.0.1:1", "GIT_TERMINAL_PROMPT=0",
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+home, "LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"), "APPDATA="+filepath.Join(home, "AppData", "Roaming"))
	}
	run := func(cmd *exec.Cmd) *exec.Cmd {
		cmd.Env = env
		cmd.Dir = fleet
		return cmd
	}
	if b, err := run(exec.Command(handBin, "init", "--name", fleetName)).CombinedOutput(); err != nil {
		t.Fatalf("hand init: %v\n%s", err, b)
	}

	boardLog, err := os.Create(filepath.Join(outDir, "board.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer boardLog.Close()
	board := run(exec.Command(handBin, "board", "--addr", "127.0.0.1:0"))
	board.Stdout, board.Stderr = boardLog, boardLog
	if err := board.Start(); err != nil {
		t.Fatalf("hand board: %v", err)
	}
	boardDone := make(chan struct{})
	go func() { _ = board.Wait(); close(boardDone) }()
	t.Cleanup(func() {
		_ = run(exec.Command(handBin, "board", "stop")).Run()
		select {
		case <-boardDone:
		case <-time.After(15 * time.Second):
			_ = board.Process.Kill()
			<-boardDone
		}
	})
	for i := 0; ; i++ {
		if _, err := os.Stat(filepath.Join(sh, "board.addr")); err == nil {
			break
		}
		if i >= 200 {
			t.Fatal("the board wrote no board.addr in 20 s")
		}
		time.Sleep(100 * time.Millisecond)
	}

	if b, err := run(exec.Command(handBin, "open", "--print")).CombinedOutput(); err != nil {
		t.Fatalf("hand open --print: %v\n%s", err, b)
	}

	winLog, err := os.Create(filepath.Join(outDir, "window.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer winLog.Close()
	win := run(exec.Command(deskBin))
	win.Env = append(win.Env, "MYGO_AUTOMATION="+sock)
	win.Stdout, win.Stderr = winLog, winLog
	stdin, err := win.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := win.Start(); err != nil {
		t.Fatalf("hand-desktop: %v", err)
	}
	var winErr error
	winDone := make(chan struct{})
	go func() { winErr = win.Wait(); close(winDone) }()
	t.Cleanup(func() {
		_ = win.Process.Kill()
		<-winDone
	})

	var conn net.Conn
	for i := 0; conn == nil; i++ {
		select {
		case <-winDone:
			t.Fatalf("the window ended before it listened: %v", winErr)
		default:
		}
		if conn, err = net.Dial("unix", sock); err != nil {
			conn = nil
			if i >= 300 {
				t.Fatalf("no automation socket in 30 s: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	defer conn.Close()
	c := &rpc{conn: conn, r: bufio.NewReader(conn)}

	var windows struct{ Windows []struct{ ID int } }
	opened := time.Now().Add(30 * time.Second)
	for len(windows.Windows) == 0 {
		c.wait = time.Until(opened)
		if c.wait <= 0 {
			t.Fatal("no window opened in 30 s")
		}
		if err := c.call("windows", nil, &windows); err != nil {
			t.Fatalf("no window opened in 30 s: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.wait = 0
	id := windows.Windows[0].ID

	var nodes []node
	var tree json.RawMessage
	has := func(role, want string) bool {
		for _, n := range nodes {
			if n.Label == want && (role == "" || n.Role == role) {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		if err := c.call("idle", map[string]any{"window": id, "timeout": 5000}, nil); err != nil && !strings.Contains(err.Error(), "timeout") {
			t.Fatal(err)
		}
		var res struct{ Nodes []node }
		var raw json.RawMessage
		if err := c.call("tree", map[string]any{"window": id}, &raw); err != nil {
			t.Fatal(err)
		}
		tree = raw
		_ = json.Unmarshal(raw, &res)
		nodes = res.Nodes
		if has("text", fleetName) && (has("text", "Needs") || has("button", "Needs")) {
			break
		}
		if time.Now().After(deadline) {
			_ = os.WriteFile(filepath.Join(outDir, "tree.json"), tree, 0o644)
			t.Fatalf("the element tree holds no %q and a Needs head after 90 s (%d elements); see tree.json", fleetName, len(nodes))
		}
		time.Sleep(time.Second)
	}
	if err := os.WriteFile(filepath.Join(outDir, "tree.json"), tree, 0o644); err != nil {
		t.Fatal(err)
	}

	var shot struct {
		PNG           string
		Width, Height int
	}
	if err := c.call("capture", map[string]any{"window": id}, &shot); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(shot.PNG)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("the capture is not a PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() < 200 || b.Dy() < 200 {
		t.Fatalf("the capture is %dx%d", b.Dx(), b.Dy())
	}
	colours := map[[4]uint32]bool{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y && len(colours) < 8; y += 4 {
		for x := b.Min.X; x < b.Max.X && len(colours) < 8; x += 4 {
			r, g, bl, a := img.At(x, y).RGBA()
			colours[[4]uint32{r, g, bl, a}] = true
		}
	}
	if len(colours) < 8 {
		t.Fatalf("the capture is blank: %d colours", len(colours))
	}
	if err := os.WriteFile(filepath.Join(outDir, "window.png"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	_ = stdin.Close()
	select {
	case <-winDone:
		if winErr != nil {
			t.Fatalf("the window did not quit cleanly: %v", winErr)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the window did not end 30 s after it was told to quit")
	}
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the window left its socket behind: %v", err)
	}
}
