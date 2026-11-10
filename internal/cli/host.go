package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/boot"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

var (
	hostBeatEvery = 15 * time.Second
	hostIdentity  = func() (string, string) {
		name, _ := os.Hostname()
		return name, boot.ID()
	}
	errHostLost = fmt.Errorf("%w: another host took this fleet over", state.ErrConflict)
)

func (r *runner) selfHost() fleet.HostMarker {
	name, id := hostIdentity()
	return fleet.HostMarker{Host: name, Boot: id, PID: os.Getpid(), At: r.env.Now().UTC()}
}

func hostLabel(m fleet.HostMarker) string {
	return fmt.Sprintf("%s (pid %d, last beat %s)", m.Host, m.PID, m.At.UTC().Format(time.RFC3339))
}

func (r *runner) judgeHost() (*fleet.HostMarker, error) {
	m, found, err := fleet.ReadHost(r.home)
	if err != nil {
		fmt.Fprintf(r.env.Stderr, "warning: cannot read %s: %v; treating it as absent; `hand fleet unlock` removes it\n", filepath.Join(r.home, fleet.HostFile), err)
		return nil, nil
	}
	if !found {
		return nil, nil
	}
	self := r.selfHost()
	switch fleet.JudgeHost(m, found, self, self.At, r.watchHeld) {
	case fleet.HostRefuse:
		return nil, fmt.Errorf("%w: this fleet is open on host %s: its watcher last beat %s ago. Close it there; or, if that host is gone, run `hand fleet unlock` here", state.ErrConflict, hostLabel(m), self.At.Sub(m.At).Round(time.Second))
	case fleet.HostTakeOver:
		if !r.noAdopt {
			return &m, nil
		}
	}
	return nil, nil
}

func (r *runner) takeOver(st *state.Store, from, self fleet.HostMarker) error {
	cur, found, err := fleet.ReadHost(r.home)
	if err == nil && !found {
		return nil
	}
	if err != nil || !sameMarker(cur, from) {
		again, err := r.judgeHost()
		if err != nil || again == nil {
			return err
		}
		from = *again
	}
	ctx := r.ctx()
	live, err := st.LiveAttempts(ctx)
	if err != nil {
		return err
	}
	var gone []int64
	for _, a := range live {
		if !rootAlive(a.PID, a.StartMarker) {
			gone = append(gone, a.ID)
		}
	}
	detail := fmt.Sprintf("host %s took over from %s boot %s after %s without a beat; %d live attempts marked interrupted", self.Host, hostLabel(from), from.Boot, self.At.Sub(from.At).Round(time.Second), len(gone))
	reason := "the fleet moved to host " + self.Host + "; this attempt ran on " + from.Host
	if err := st.HostHandover(ctx, "fleet.took_over", detail, reason, gone); err != nil {
		return err
	}
	if cur, found, err := fleet.ReadHost(r.home); err == nil && found && sameMarker(cur, from) {
		if err := fleet.RemoveHost(r.home); err != nil {
			return err
		}
	}
	fmt.Fprintf(r.env.Stderr, "hand: took over from host %s: %d live attempts marked interrupted\n", from.Host, len(gone))
	return nil
}

func (r *runner) beatHost() error {
	self := r.selfHost()
	m, found, err := fleet.ReadHost(r.home)
	if err != nil {
		return err
	}
	if found && !strings.EqualFold(m.Host, self.Host) && (m.Boot == "" || !m.SameBoot(self)) {
		return fmt.Errorf("%w: host %s holds it now", errHostLost, hostLabel(m))
	}
	return fleet.WriteHost(r.home, self)
}

func (r *runner) releaseHost() {
	m, found, err := fleet.ReadHost(r.home)
	if err == nil && found && m.PID == os.Getpid() && m.SameMachine(r.selfHost()) {
		_ = fleet.RemoveHost(r.home)
	}
}

func (r *runner) heartbeat(ctx context.Context, lost context.CancelCauseFunc) {
	tick := time.NewTicker(hostBeatEvery)
	defer tick.Stop()
	warned := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		err := r.beatHost()
		switch {
		case errors.Is(err, errHostLost):
			lost(err)
			return
		case err != nil && !warned:
			warned = true
			fmt.Fprintf(r.env.Stderr, "warning: cannot write %s: %v; another host may take this fleet over\n", filepath.Join(r.home, fleet.HostFile), err)
		case err == nil:
			warned = false
		}
	}
}

func cmdFleetUnlock(r *runner, args []string) error {
	if _, err := parse(flags("fleet unlock"), args, 0); err != nil {
		return err
	}
	home, err := resolveHome(r.env, r.home, r.getwd)
	if err != nil {
		return err
	}
	if home == "" {
		return fmt.Errorf("%w: not inside a fleet home; cd into one or pass --home DIR", state.ErrNotFound)
	}
	r.home = home
	if err := r.needHome(); err != nil {
		return err
	}
	var d toon.Doc
	d.Field("home", home)
	m, found, err := fleet.ReadHost(home)
	unreadable := err != nil
	if unreadable {
		fmt.Fprintf(r.env.Stderr, "warning: cannot read %s: %v; removing it\n", filepath.Join(home, fleet.HostFile), err)
		found = true
	}
	if !found {
		d.Field("unlock", "free")
		d.Help("No host holds this fleet open")
		return r.print(&d)
	}
	held, err := r.watchHeld()
	if err != nil {
		return err
	}
	if held && m.SameMachine(r.selfHost()) {
		return fmt.Errorf("%w: this host's watcher (pid %d) holds the fleet open; run `hand watch stop` first", state.ErrConflict, m.PID)
	}
	st, err := state.Open(r.dbPath(), r.env.Now)
	if err != nil {
		return err
	}
	defer st.Close()
	detail := fmt.Sprintf("unlocked a marker of host %s boot %s pid %d, last beat %s", m.Host, m.Boot, m.PID, m.At.UTC().Format(time.RFC3339))
	if unreadable {
		detail = "unlocked a marker that could not be read"
	}
	if err := st.HostHandover(r.ctx(), "fleet.unlocked", detail, "", nil); err != nil {
		return err
	}
	if err := fleet.RemoveHost(home); err != nil {
		return err
	}
	d.Field("unlock", "cleared")
	if !unreadable {
		d.Field("host", m.Host)
		d.Field("pid", strconv.Itoa(m.PID))
		d.Field("last_beat", m.At.UTC().Format(time.RFC3339))
	}
	d.Help("Attempts that ran on that host stay as they were; the watcher ends the ones whose terminal is gone")
	return r.print(&d)
}

func (r *runner) doctorHost(c *checks) {
	m, found, err := fleet.ReadHost(r.home)
	if err != nil {
		c.add("host marker", "warn", err.Error())
		return
	}
	if !found {
		return
	}
	self := r.selfHost()
	held, herr := r.watchHeld()
	if herr != nil {
		held = true
	}
	switch fleet.JudgeHost(m, found, self, self.At, func() (bool, error) { return held, nil }) {
	case fleet.HostRefuse:
		c.add("open on another host", "fail", fmt.Sprintf("host %s holds this fleet open: its watcher last beat %s ago; close it there, or run `hand fleet unlock` if that host is gone", hostLabel(m), self.At.Sub(m.At).Round(time.Second)))
	case fleet.HostTakeOver:
		c.add("unclean close", "warn", fmt.Sprintf("host %s closed this fleet without clearing its marker (%s); the next `hand` command that opens it takes over and marks the attempts that were live interrupted", hostLabel(m), self.At.Sub(m.At).Round(time.Second)))
	default:
		if !held {
			c.add("unclean close", "warn", fmt.Sprintf("this host's watcher (pid %d) ended without clearing %s; `hand watch` overwrites it", m.PID, fleet.HostFile))
		}
	}
}

func sameMarker(a, b fleet.HostMarker) bool {
	return a.Host == b.Host && a.Boot == b.Boot && a.PID == b.PID && a.At.Equal(b.At)
}
