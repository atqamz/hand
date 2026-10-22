package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/proc"
)

const (
	phoneKeep   = 30 * time.Second
	phoneOutMax = 8 << 20
)

var phoneProbe = 2 * time.Second

var tailscaleApps = map[string]func(func(string) string) string{
	"darwin": func(func(string) string) string { return "/Applications/Tailscale.app/Contents/MacOS" },
	"windows": func(getenv func(string) string) string {
		if dir := getenv("ProgramFiles"); dir != "" {
			return filepath.Join(dir, "Tailscale")
		}
		return ""
	},
}

type phoneCache struct {
	mu   sync.Mutex
	at   time.Time
	addr string
	link board.PhoneLink
}

func (r *runner) phoneLink(ctx context.Context, addr string) board.PhoneLink {
	c := &r.phone
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && c.addr == addr && r.env.Now().Sub(c.at) < phoneKeep {
		return c.link
	}
	link := r.detectPhone(context.WithoutCancel(ctx), addr)
	c.at, c.addr, c.link = r.env.Now(), addr, link
	return link
}

func (r *runner) detectPhone(ctx context.Context, addr string) board.PhoneLink {
	_, port, _ := net.SplitHostPort(addr)
	link := board.PhoneLink{Port: port}
	path := r.env.Getenv("PATH")
	if app := tailscaleApps[runtime.GOOS]; app != nil {
		if dir := app(r.env.Getenv); dir != "" {
			path += string(filepath.ListSeparator) + dir
		}
	}
	bin, err := harness.LookPath("tailscale", path)
	if err != nil {
		return link
	}
	var serve, status []byte
	var wg sync.WaitGroup
	wg.Go(func() { serve = r.tailscaleOut(ctx, bin, "serve", "status", "--json") })
	wg.Go(func() { status = r.tailscaleOut(ctx, bin, "status", "--json") })
	wg.Wait()
	if !json.Valid(serve) || !bytes.HasPrefix(serve, []byte("{")) {
		return link
	}
	link.Tailscale = true
	link.URL, link.Funnel, link.Problem = board.ServeLink(serve, addr)
	link.HTTPS, link.Phones = board.TailnetStatus(status)
	return link
}

func (r *runner) tailscaleOut(ctx context.Context, bin string, args ...string) []byte {
	ctx, cancel := context.WithTimeout(ctx, phoneProbe)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env, cmd.WaitDelay = r.env.Environ(), time.Second
	proc.KillGroupOnCancel(cmd)
	proc.NoWindow(cmd)
	out, err := cmd.Output()
	if err != nil || len(out) > phoneOutMax {
		return nil
	}
	return bytes.TrimSpace(out)
}
