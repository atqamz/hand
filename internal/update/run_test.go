package update

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

type fixture struct {
	root, target, calls          string
	sessionDir, version          string
	old                          string
	alpha, alphaID, goneID, gone string
	srv                          *server
	pin                          luvus.Pin
	o                            Options
	mu                           sync.Mutex
	status                       string
	uhp                          *fakeuhp.Server
	terms                        []luvus.Terminal
	stopErr                      error
	revision, typed              int64
	slept                        []time.Duration
	startsOld                    bool
}

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
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := os.WriteFile(bin, fakebin.Embed(t, "luvus", map[string]string{"version": version}), 0o755); err != nil {
		t.Fatal(err)
	}
	pin, err := luvus.Keep(context.Background(), root, bin, os.Environ(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return pin
}

func newFetch(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{root: t.TempDir(), calls: filepath.Join(t.TempDir(), "calls")}
	f.old = handScript(t, "0.8.0", "source", "unknown", "9", "0.14.3")
	f.target = binary(t, f.old)
	files := fakeHand(t, "0.9.0", "edge", "0123456789ab", "9", "0.14.4")
	maps.Copy(files, fakeLuvus(t, "0.14.4"))
	f.srv = release(t, files)
	f.pin = keepLuvus(t, f.root, "0.14.4")
	env := []string{"HAND_CALLS=" + f.calls}
	goos, arch := hostTarget()
	f.o = Options{
		Target:    f.target,
		From:      Build{Version: "0.8.0", Channel: "source", Commit: "unknown", Schema: 9, Luvus: "0.14.3"},
		Channel:   "edge",
		Root:      f.root,
		HandBase:  f.srv.URL,
		LuvusBase: f.srv.URL,
		OS:        goos,
		Arch:      arch,
		Env:       env,
		Getenv:    lookup(env),
		Now:       func() time.Time { return stamp },
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
	if b, err := os.ReadFile(f.target); err != nil || string(b) != f.old {
		t.Fatalf("target changed: %v", err)
	}
	absent(t, filepath.Join(f.root, "backups"))
}

var changes = []string{" stop ", " start ", " restart ", "hand ", "kill", "watcher @ ", "board @ "}

func TestRunCheckChangesNothing(t *testing.T) {
	f := newFetch(t)
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
	f := newFetch(t)
	f.o.From = Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: 9, Luvus: "0.14.4"}
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
	f := newFetch(t)
	for name, body := range fakeHand(t, "0.9.0", "edge", "0123456789ab", "6", "0.14.4") {
		f.srv.set(name, body)
	}
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "update: 0.9.0 reads state schema 6, older than this fleet's 9") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestRunChangesNothingOnABadChecksum(t *testing.T) {
	f := newFetch(t)
	f.srv.set(handSums(), []byte(strings.Repeat("0", 64)+"  "+handAsset()+"\n"))
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestTargetResolvesASymlink(t *testing.T) {
	f := newFetch(t)
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
	if b, _ := os.ReadFile(link); string(b) != handScript(t, "0.9.0", "edge", "0123456789ab", "9", "0.14.4") {
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

func TestRunPinsTheTestedLuvus(t *testing.T) {
	f := newFetch(t)
	f.pin = keepLuvus(t, f.root, "0.14.3")
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if pin, ok, err := luvus.LoadPin(f.root); err != nil || !ok || pin.Version != "0.14.4" {
		t.Fatalf("pin = %+v, %v, %v", pin, ok, err)
	}
	if out := Render(rep).String(); !strings.Contains(out, "luvus: 0.14.3 -> 0.14.4\n") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestRunNeverLowersThePin(t *testing.T) {
	f := newFetch(t)
	f.pin = keepLuvus(t, f.root, "0.15.0")
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if pin, _, _ := luvus.LoadPin(f.root); pin.Version != "0.15.0" {
		t.Fatalf("pin = %+v", pin)
	}
	if slices.ContainsFunc(f.srv.asked(), func(p string) bool { return strings.Contains(p, "luvus-") }) {
		t.Fatalf("asked %q", f.srv.asked())
	}
}

func TestRunRefusesABuildFromAnotherChannel(t *testing.T) {
	f := newFetch(t)
	for name, body := range fakeHand(t, "0.9.0", "stable", "0123456789ab", "9", "0.14.4") {
		f.srv.set(name, body)
	}
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "update: the download is a stable build, not edge") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestRunDoesNotHoldWhileDownloading(t *testing.T) {
	f := newFetch(t)
	f.pin = keepLuvus(t, f.root, "0.14.3")
	f.srv.set(luvusAsset("0.14.4"), nil)
	held := 0
	f.o.Hold = func() { held++ }
	if _, err := Run(context.Background(), f.o); err == nil || held != 0 {
		t.Fatalf("held %d times, %v", held, err)
	}
	f.unchanged(t)
}

func TestRecordReportsAJournalItCannotWrite(t *testing.T) {
	var r Report
	if err := r.record(Options{Root: filepath.Join(t.TempDir(), "missing")}, func(j *journal) { j.Watcher = []string{"w"} }); err == nil {
		t.Fatal("record wrote into a missing folder")
	}
	if len(r.journal.Watcher) != 0 {
		t.Fatalf("a failed record kept %q in memory", r.journal.Watcher)
	}
}
