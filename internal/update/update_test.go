package update

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
)

type fixture struct {
	root, target, calls, sysdir  string
	alpha, alphaID, goneID, link string
	srv                          *server
	pin                          luvus.Pin
	o                            Options
}

var oldHand = handScript("0.8.0", "source", "unknown", "7", "0.14.3")

func lookup(env []string) func(string) string {
	return func(k string) string {
		v := ""
		for _, kv := range env {
			if s, ok := strings.CutPrefix(kv, k+"="); ok {
				v = s
			}
		}
		return v
	}
}

func keepLuvus(t *testing.T, root, version string) luvus.Pin {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "luvus")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'luvus "+version+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pin, err := luvus.Keep(context.Background(), root, bin, os.Environ(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return pin
}

func newRun(t *testing.T, fail map[string]bool) *fixture {
	t.Helper()
	f := &fixture{root: t.TempDir()}
	f.alphaID, f.alpha = newFleet(t, f.root, "alpha")
	var gone string
	f.goneID, gone = newFleet(t, f.root, "gone")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	f.target = binary(t, oldHand)
	files := fakeHand(t, "0.9.0", "edge", "0123456789ab", "7", "0.14.4")
	maps.Copy(files, fakeLuvus(t, "0.14.4"))
	f.srv = release(t, files)
	show := unitBlock("secondhand-board.service", "active", f.target, f.target+" board --addr 127.0.0.1:7777", "") + "\n" +
		unitBlock("secondhand-watch-alpha.service", "active", f.target, f.target+" watch", "HAND_HOME="+f.alpha)
	f.sysdir, f.calls = fakeSystemctl(t, listing("secondhand-board.service", "secondhand-watch-alpha.service"), show, fail)
	f.pin = keepLuvus(t, f.root, "0.14.4")
	env := append(pathEnv(f.sysdir), "HAND_CALLS="+f.calls)
	f.o = Options{
		Target:    f.target,
		From:      Build{Version: "0.8.0", Channel: "source", Commit: "unknown", Schema: 7, Luvus: "0.14.3"},
		Channel:   "edge",
		Root:      f.root,
		HandBase:  f.srv.URL,
		LuvusBase: f.srv.URL,
		Arch:      "amd64",
		Env:       env,
		Getenv:    lookup(env),
		Now:       func() time.Time { return stamp },
		Server: func(context.Context, string) (luvus.Server, bool) {
			return luvus.Server{Exe: f.pin.Path, SHA256: f.pin.SHA256}, true
		},
	}
	return f
}

func (f *fixture) log(t *testing.T, keep ...string) []string {
	t.Helper()
	b, _ := os.ReadFile(f.calls)
	var out []string
	for line := range strings.Lines(string(b)) {
		line = strings.TrimSpace(line)
		for _, k := range keep {
			if strings.Contains(line, k) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}

func (f *fixture) unchanged(t *testing.T) {
	t.Helper()
	if b, err := os.ReadFile(f.target); err != nil || string(b) != oldHand {
		t.Fatalf("target changed: %v", err)
	}
	absent(t, filepath.Join(f.root, "backups"))
}

var changes = []string{" stop ", " start ", " restart ", "hand "}

func byName(fleets []FleetResult) []FleetResult {
	out := slices.Clone(fleets)
	slices.SortFunc(out, func(a, b FleetResult) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func TestRunUpdatesTheFleet(t *testing.T) {
	f := newRun(t, nil)
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != "updated" || rep.Failed || len(rep.Backups) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	if b, _ := os.ReadFile(f.target); string(b) != handScript("0.9.0", "edge", "0123456789ab", "7", "0.14.4") {
		t.Fatalf("target = %q", b)
	}
	for _, p := range rep.Backups {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"systemctl --user stop secondhand-watch-alpha.service", "systemctl --user start secondhand-watch-alpha.service", "systemctl --user restart secondhand-board.service", "hand init @ " + f.alpha}
	if got := f.log(t, changes...); !slices.Equal(got, want) {
		t.Fatalf("calls = %q", got)
	}
	if got := byName(rep.Fleets); !slices.Equal(got, []FleetResult{{"alpha", "ok", "kept"}, {f.goneID, "skipped: missing", "kept"}}) {
		t.Fatalf("fleets = %+v", got)
	}
}

func TestRunCheckChangesNothing(t *testing.T) {
	f := newRun(t, nil)
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "checked" || rep.To.Commit != "0123456789ab" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	f.unchanged(t)
	if got := f.log(t, changes...); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunUpToDateChangesNothing(t *testing.T) {
	f := newRun(t, nil)
	f.o.From = Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: 7, Luvus: "0.14.4"}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	f.unchanged(t)
	if got := f.log(t, changes...); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunRefusesAnOlderSchema(t *testing.T) {
	f := newRun(t, nil)
	for name, body := range fakeHand(t, "0.9.0", "edge", "0123456789ab", "6", "0.14.4") {
		f.srv.set(name, body)
	}
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "update: 0.9.0 reads state schema 6, older than this fleet's 7") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestRunChangesNothingOnABadChecksum(t *testing.T) {
	f := newRun(t, nil)
	f.srv.set("checksums.txt", []byte(strings.Repeat("0", 64)+"  hand-linux-amd64.tar.gz\n"))
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestRunReportsAFailedUnitAndCarriesOn(t *testing.T) {
	f := newRun(t, map[string]bool{"start": true})
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Failed || !slices.ContainsFunc(rep.Help, func(h string) bool {
		return strings.Contains(h, "systemctl --user start secondhand-watch-alpha.service")
	}) {
		t.Fatalf("report = %+v", rep)
	}
	got := f.log(t, " restart ", "hand init")
	if !slices.Equal(got, []string{"systemctl --user restart secondhand-board.service", "hand init @ " + f.alpha}) {
		t.Fatalf("calls = %q", got)
	}
}

func TestRunNamesTheTargetWhenTheSwapFails(t *testing.T) {
	f := newRun(t, nil)
	dir := filepath.Dir(f.target)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), f.target) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(f.target); string(b) != oldHand {
		t.Fatal("target changed")
	}
	if got := f.log(t, changes...); len(got) != 0 {
		t.Fatalf("calls = %q", got)
	}
}

func TestTargetResolvesASymlink(t *testing.T) {
	f := newRun(t, nil)
	link := filepath.Join(t.TempDir(), "hand-next")
	if err := os.Symlink(f.target, link); err != nil {
		t.Fatal(err)
	}
	got, err := Target(link)
	if err != nil || got != f.target {
		t.Fatalf("Target = %q, %v", got, err)
	}
	f.o.Target = got
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if dest, err := os.Readlink(link); err != nil || dest != f.target {
		t.Fatalf("link = %q, %v", dest, err)
	}
	if b, _ := os.ReadFile(link); string(b) != handScript("0.9.0", "edge", "0123456789ab", "7", "0.14.4") {
		t.Fatal("the link does not reach the new binary")
	}
}

func TestRenderPrintsTheReport(t *testing.T) {
	r := Report{
		Status:  "updated",
		From:    Build{Version: "0.8.0", Channel: "source", Commit: "unknown"},
		To:      Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab"},
		PinFrom: "0.14.3",
		PinTo:   "0.14.4",
		Backups: []string{"/r/backups/hand.20261002T010203"},
		Units:   []UnitResult{{"secondhand-board.service", "restart", "ok"}},
		Fleets:  []FleetResult{{"alpha", "ok", "switched"}},
		Help:    []string{"systemctl --user start secondhand-watch-alpha.service"},
	}
	out := Render(r).String()
	for _, want := range []string{"status: updated\n", "from: 0.8.0 source unknown\n", "to: 0.9.0 edge 0123456789ab\n", "luvus: 0.14.3 -> 0.14.4\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	order := []string{"backups[1]:", "units[1]{name,action,result}:\n  secondhand-board.service,restart,ok\n", "fleets[1]{name,init,luvus}:\n  alpha,ok,switched\n", "help[1]:\n  - systemctl --user start secondhand-watch-alpha.service\n"}
	at := 0
	for _, want := range order {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("missing %q after %d:\n%s", want, at, out)
		}
		at += i + len(want)
	}
	if at != len(out) {
		t.Fatalf("help is not last:\n%s", out)
	}
	r.PinFrom = "0.14.4"
	if out := Render(r).String(); !strings.Contains(out, "luvus: 0.14.4 kept\n") {
		t.Fatalf("kept:\n%s", out)
	}
}
