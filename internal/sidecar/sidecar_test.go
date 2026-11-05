package sidecar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
)

const (
	idA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	idB = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{"sidecar": fakeSidecar})
	os.Exit(m.Run())
}

func fakeSidecar(args []string) int {
	p := fakebin.Params()
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println(p["version"])
		return 0
	}
	log := p["log"]
	fakebin.Append(log, "start "+strings.Join(args, " "))
	var keys []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		keys = append(keys, k)
	}
	fakebin.Append(log, "env "+strings.Join(keys, ","))
	b, _ := os.ReadFile(log)
	starts := strings.Count(string(b), "start ")
	ready := "ready id=" + idA + " relay=none"
	drain := func() int {
		_, _ = io.Copy(io.Discard, os.Stdin)
		fakebin.Append(log, "stdin closed")
		return 0
	}
	switch p["mode"] {
	case "die":
		fmt.Fprintln(os.Stderr, p["stderr"])
		return 1
	case "flaky":
		if failures, _ := strconv.Atoi(p["failures"]); starts <= failures {
			fmt.Fprintln(os.Stderr, "hand-iroh: cannot open the endpoint: boom")
			return 1
		}
		fmt.Println(ready)
		return drain()
	case "silent":
		return drain()
	case "deaf":
		fmt.Println(ready)
		time.Sleep(time.Minute)
		return 0
	case "short":
		fmt.Println(ready)
		time.Sleep(60 * time.Millisecond)
		return 3
	case "flood":
		fmt.Print(strings.Repeat("x", 1<<20) + "\n")
		fmt.Println(ready)
		return drain()
	case "twice":
		fmt.Println(ready)
		fmt.Println("ready id=" + idB + " relay=none")
		return drain()
	}
	fmt.Println(ready)
	return drain()
}

type fixture struct {
	dir, log, bin string
	opts          Options
}

func newFixture(t *testing.T, params map[string]string) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{dir: dir, log: filepath.Join(dir, "log")}
	p := map[string]string{"log": f.log, "id": idA, "version": "hand-iroh " + Version + " iroh 1.4.1"}
	for k, v := range params {
		p[k] = v
	}
	f.bin = fakebin.Install(t, dir, "hand-iroh", "sidecar", p)
	f.opts = Options{
		Resolve:    func(context.Context) (string, error) { return f.bin, nil },
		Board:      "127.0.0.1:7778",
		SecretFile: filepath.Join(dir, "board.peer"),
		KeyDir:     filepath.Join(dir, "iroh"),
		Dir:        dir,
		Backoff:    func(int) time.Duration { return time.Millisecond },
	}
	return f
}

func (f *fixture) logged(prefix string) []string {
	b, _ := os.ReadFile(f.log)
	var out []string
	for line := range strings.Lines(string(b)) {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, prefix) {
			out = append(out, line)
		}
	}
	return out
}

type recorder struct {
	mu  sync.Mutex
	got []Status
}

func (r *recorder) add(st Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, st)
}

func (r *recorder) all() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

func (r *recorder) states() []string {
	var out []string
	for _, st := range r.all() {
		out = append(out, st.State)
	}
	return out
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func run(t *testing.T, o Options) (*Sidecar, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	s := New(o)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return s, cancel, done
}

func finished(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestReadyLineIsParsedStrictly(t *testing.T) {
	for line, want := range map[string]Status{
		"ready id=" + idA + " relay=none":                                {State: Ready, ID: idA, Relay: "none"},
		"ready id=" + idB + " relay=https://euw1-1.relay.test./":         {State: Ready, ID: idB, Relay: "https://euw1-1.relay.test./"},
		"ready id=" + idA + " relay=http://127.0.0.1:3340/":              {State: Ready, ID: idA, Relay: "http://127.0.0.1:3340/"},
		"ready id=" + strings.ToUpper(idA) + " relay=none":               {},
		"ready id=" + idA[:63] + " relay=none":                           {},
		"ready id=" + idA + "0 relay=none":                               {},
		"ready id=" + idA + " relay=":                                    {},
		"ready id=" + idA + " relay=ftp://x":                             {},
		"ready id=" + idA + " relay=https://a b":                         {},
		"ready id=" + idA + " relay=none extra":                          {},
		" ready id=" + idA + " relay=none":                               {},
		"ready id=" + idA:                                                {},
		"":                                                               {},
		"ready id=" + idA + " relay=https://" + strings.Repeat("a", 201): {},
	} {
		got, ok := parseReady(line)
		if got != want || ok != (want.State != "") {
			t.Errorf("parseReady(%.90q) = %+v, %v; want %+v", line, got, ok, want)
		}
	}
}

func TestLinesKeepsMemoryBoundedAndReadsAReadyLineAfterJunk(t *testing.T) {
	l := &lines{ready: make(chan Status, 1)}
	junk := []byte(strings.Repeat("y", 1<<20))
	if n, err := l.Write(junk); n != len(junk) || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if len(l.buf) > lineMax {
		t.Fatalf("buffered %d bytes of a line without a newline", len(l.buf))
	}
	_, _ = l.Write([]byte("\nready id=" + idA + " relay=none\r\nready id=" + idB + " relay=none\n"))
	if st := <-l.ready; st.ID != idA {
		t.Fatalf("first ready line = %+v", st)
	}
	select {
	case st := <-l.ready:
		t.Fatalf("a second ready line was queued as %+v", st)
	default:
	}
}

func TestPinnedVersionIsTheCrateVersion(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "iroh", "Cargo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version = "(.+)"$`).FindSubmatch(b)
	if m == nil || string(m[1]) != Version {
		t.Fatalf("iroh/Cargo.toml version = %q, sidecar.Version = %q", m, Version)
	}
}

func TestLocateTakesTheBinaryNextToHandBeforePath(t *testing.T) {
	root := t.TempDir()
	beside, onPath := filepath.Join(root, "beside"), filepath.Join(root, "path")
	for _, d := range []string{beside, onPath} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	self := filepath.Join(beside, "hand")
	if runtime.GOOS == "windows" {
		self += ".exe"
	}
	want := fakebin.Install(t, beside, Name, "sidecar", nil)
	other := fakebin.Install(t, onPath, Name, "sidecar", nil)
	got, err := Locate(self, onPath)
	if err != nil || got != want {
		t.Fatalf("Locate = %q, %v; want the one next to hand, %q", got, err, want)
	}
	if err := os.Remove(want); err != nil {
		t.Fatal(err)
	}
	if got, err = Locate(self, onPath); err != nil || got != other {
		t.Fatalf("Locate without a neighbour = %q, %v; want %q", got, err, other)
	}
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	if _, err = Locate(self, onPath); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), beside) {
		t.Fatalf("Locate with none = %v; want ErrNotFound naming %s", err, beside)
	}
}

func TestLocateSkipsAFolderAndAFileThatIsNotExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no executable bit")
	}
	dir := t.TempDir()
	self := filepath.Join(dir, "hand")
	if err := os.Mkdir(filepath.Join(dir, Name), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Locate(self, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a folder named %s was taken: %v", Name, err)
	}
	if err := os.Remove(filepath.Join(dir, Name)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, Name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Locate(self, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a file with no execute bit was taken: %v", err)
	}
}

func TestVerifyRefusesAnotherVersionAndAnythingThatIsNotTheSidecar(t *testing.T) {
	for name, tc := range map[string]struct {
		version string
		want    error
		text    string
	}{
		"the pinned version": {"hand-iroh " + Version + " iroh 1.4.1", nil, ""},
		"a newer one":        {"hand-iroh 0.2.0 iroh 1.4.1", ErrVersion, "is 0.2.0, but this hand needs " + Version},
		"an older one":       {"hand-iroh 0.0.9 iroh 1.4.1", ErrVersion, "is 0.0.9"},
		"another program":    {"luvus 0.14.3", ErrNotSidecar, `printed "luvus 0.14.3"`},
		"no iroh version":    {"hand-iroh " + Version, ErrNotSidecar, "not `hand-iroh X.Y.Z iroh X.Y.Z`"},
		"nothing":            {"", ErrNotSidecar, `printed ""`},
	} {
		f := newFixture(t, map[string]string{"version": tc.version})
		got, err := Verify(context.Background(), f.bin)
		switch {
		case tc.want == nil && (err != nil || got != Version):
			t.Errorf("%s: Verify = %q, %v", name, got, err)
		case tc.want != nil && (!errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.text)):
			t.Errorf("%s: Verify = %q, %v; want %v with %q", name, got, err, tc.want, tc.text)
		}
	}
	if _, err := Verify(context.Background(), filepath.Join(t.TempDir(), "absent")); !errors.Is(err, ErrNotSidecar) {
		t.Fatalf("Verify of a missing file = %v", err)
	}
}

func TestResolveLocatesThenVerifies(t *testing.T) {
	f := newFixture(t, map[string]string{"version": "hand-iroh 9.9.9 iroh 1.4.1"})
	self := filepath.Join(f.dir, "hand")
	bin, err := Resolve(self, "")(context.Background())
	if !errors.Is(err, ErrVersion) || bin != f.bin {
		t.Fatalf("Resolve = %q, %v", bin, err)
	}
	if _, err := Resolve(filepath.Join(t.TempDir(), "hand"), "")(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve with no binary = %v", err)
	}
}

func TestStartPassesPathsNeverTheSecretAndAnEmptyEnvironment(t *testing.T) {
	f := newFixture(t, nil)
	secret := "feedfacefeedfacefeedfacefeedfacefeedfacefeedface"
	if err := os.WriteFile(f.opts.SecretFile, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_TEST_CANARY", "leaked")
	t.Setenv("HTTPS_PROXY", "http://user:pass@proxy.test")
	s, _, _ := run(t, f.opts)
	eventually(t, "ready", func() bool { return s.Status().State == Ready })
	if st := s.Status(); st.ID != idA || st.Relay != "none" || st.Reason != "" {
		t.Fatalf("Status = %+v", st)
	}
	want := fmt.Sprintf("start --board 127.0.0.1:7778 --secret-file %s --key-dir %s", f.opts.SecretFile, f.opts.KeyDir)
	if got := f.logged("start "); len(got) != 1 || got[0] != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if b, _ := os.ReadFile(f.log); strings.Contains(string(b), secret) {
		t.Fatalf("the secret reached the sidecar's argv or environment:\n%s", b)
	}
	seen := f.logged("env")
	if len(seen) != 1 {
		t.Fatalf("env lines = %q", seen)
	}
	for _, key := range strings.Split(strings.TrimSpace(strings.TrimPrefix(seen[0], "env")), ",") {
		if key != "" && !strings.EqualFold(key, "SystemRoot") {
			t.Errorf("the sidecar inherited %q", key)
		}
	}
}

func TestShutdownClosesTheSidecarsStdinAndWaitsForItsExit(t *testing.T) {
	f := newFixture(t, nil)
	rec := &recorder{}
	f.opts.OnChange = rec.add
	s, cancel, done := run(t, f.opts)
	eventually(t, "ready", func() bool { return s.Status().State == Ready })
	if got := f.logged("stdin closed"); len(got) != 0 {
		t.Fatalf("stdin closed while the board runs: %q", got)
	}
	cancel()
	finished(t, done)
	if got := f.logged("stdin closed"); len(got) != 1 {
		t.Fatalf("stdin closes seen after shutdown = %q, want 1", got)
	}
	if got := rec.states(); !slices.Equal(got, []string{Starting, Ready, Stopped}) {
		t.Fatalf("states = %q", got)
	}
	if st := s.Status(); st.State != Stopped || st.ID != "" {
		t.Fatalf("Status after shutdown = %+v", st)
	}
}

func TestAnExitedSidecarRestartsWithGrowingBackoffAndShowsWhy(t *testing.T) {
	f := newFixture(t, map[string]string{"mode": "flaky", "failures": "3"})
	var mu sync.Mutex
	var waits []int
	f.opts.Backoff = func(n int) time.Duration {
		mu.Lock()
		defer mu.Unlock()
		waits = append(waits, n)
		return time.Millisecond
	}
	rec := &recorder{}
	f.opts.OnChange = rec.add
	f.opts.Attempts = 5
	s, _, _ := run(t, f.opts)
	eventually(t, "ready", func() bool { return s.Status().State == Ready })
	mu.Lock()
	got := slices.Clone(waits)
	mu.Unlock()
	if !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("backoff asked for failures %v, want [1 2 3]", got)
	}
	if n := len(f.logged("start ")); n != 4 {
		t.Fatalf("starts = %d, want 4", n)
	}
	var reasons []string
	for _, st := range rec.all() {
		if st.State == Starting && st.Reason != "" {
			reasons = append(reasons, st.Reason)
		}
	}
	if len(reasons) != 3 || reasons[0] != "exit status 1: hand-iroh: cannot open the endpoint: boom" {
		t.Fatalf("restart reasons = %q", reasons)
	}
}

func TestTheDefaultBackoffDoublesFromOneSecondToACap(t *testing.T) {
	b := New(Options{}).o.Backoff
	var got []time.Duration
	for n := 1; n <= 7; n++ {
		got = append(got, b(n))
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	if !slices.Equal(got, want) {
		t.Fatalf("backoff = %v, want %v", got, want)
	}
	o := New(Options{}).o
	if o.Attempts != 5 || o.Stable != time.Minute || o.ReadyWait != 30*time.Second || o.StopWait != 5*time.Second {
		t.Fatalf("defaults = %+v", o)
	}
}

func TestItGivesUpAfterRepeatedFastFailuresAndSaysSo(t *testing.T) {
	f := newFixture(t, map[string]string{"mode": "die", "stderr": "hand-iroh: cannot use the key in /k: permission denied"})
	f.opts.Attempts = 3
	rec := &recorder{}
	f.opts.OnChange = rec.add
	s, _, done := run(t, f.opts)
	finished(t, done)
	want := Status{State: Failed, Reason: "gave up after 3 quick exits: exit status 1: hand-iroh: cannot use the key in /k: permission denied"}
	if st := s.Status(); st != want {
		t.Fatalf("Status = %+v, want %+v", st, want)
	}
	if n := len(f.logged("start ")); n != 3 {
		t.Fatalf("starts = %d, want 3", n)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(f.logged("start ")); n != 3 {
		t.Fatalf("it kept restarting after giving up: %d starts", n)
	}
}

func TestALongRunResetsTheCountOfFastFailures(t *testing.T) {
	f := newFixture(t, map[string]string{"mode": "short"})
	f.opts.Attempts = 2
	f.opts.Stable = 20 * time.Millisecond
	s, _, _ := run(t, f.opts)
	eventually(t, "five starts", func() bool { return len(f.logged("start ")) >= 5 })
	if st := s.Status(); st.State == Failed {
		t.Fatalf("gave up although every run outlived the stable time: %+v", st)
	}
}

func TestFailuresBelowTheStableTimeAreNotReset(t *testing.T) {
	f := newFixture(t, map[string]string{"mode": "short"})
	f.opts.Attempts = 2
	f.opts.Stable = time.Hour
	s, _, done := run(t, f.opts)
	finished(t, done)
	if st := s.Status(); st.State != Failed || !strings.HasPrefix(st.Reason, "gave up after 2 quick exits") {
		t.Fatalf("Status = %+v", st)
	}
}

func TestASidecarThatNeverSaysReadyIsKilledAndCounted(t *testing.T) {
	f := newFixture(t, map[string]string{"mode": "silent"})
	f.opts.ReadyWait = 100 * time.Millisecond
	f.opts.Attempts = 1
	s, _, done := run(t, f.opts)
	finished(t, done)
	if st := s.Status(); st.State != Failed || st.Reason != "gave up after 1 quick exits: no ready line within 100ms" {
		t.Fatalf("Status = %+v", st)
	}
}

func TestASidecarThatIgnoresItsStdinIsKilledAfterTheStopWait(t *testing.T) {
	f := newFixture(t, map[string]string{"mode": "deaf"})
	f.opts.StopWait = 100 * time.Millisecond
	s, cancel, done := run(t, f.opts)
	eventually(t, "ready", func() bool { return s.Status().State == Ready })
	began := time.Now()
	cancel()
	finished(t, done)
	if took := time.Since(began); took > 15*time.Second {
		t.Fatalf("shutdown took %v", took)
	}
	if st := s.Status(); st.State != Stopped {
		t.Fatalf("Status = %+v", st)
	}
}

func TestAMissingOrWrongBinaryFailsAtOnceWithoutRetries(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.Resolve = func(context.Context) (string, error) {
		return "", fmt.Errorf("%w: it is not next to hand (/x) or on PATH", ErrNotFound)
	}
	rec := &recorder{}
	f.opts.OnChange = rec.add
	_, _, done := run(t, f.opts)
	finished(t, done)
	got := rec.all()
	if len(got) != 2 || got[0].State != Starting || got[1].State != Failed || got[1].Reason != "hand-iroh not found: it is not next to hand (/x) or on PATH" {
		t.Fatalf("statuses = %+v", got)
	}
	if n := len(f.logged("start ")); n != 0 {
		t.Fatalf("a missing binary was started %d times", n)
	}
}

func TestResolveRunsAgainOnEveryRestart(t *testing.T) {
	f := newFixture(t, map[string]string{"mode": "flaky", "failures": "1"})
	var mu sync.Mutex
	calls := 0
	f.opts.Resolve = func(context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if calls++; calls == 2 {
			return "", fmt.Errorf("%w: hand-iroh is 9.9.9, but this hand needs %s", ErrVersion, Version)
		}
		return f.bin, nil
	}
	s, _, done := run(t, f.opts)
	finished(t, done)
	if st := s.Status(); st.State != Failed || !strings.Contains(st.Reason, "9.9.9") {
		t.Fatalf("Status = %+v", st)
	}
	if n := len(f.logged("start ")); n != 1 {
		t.Fatalf("starts = %d, want 1", n)
	}
}

func TestAFloodOnStdoutAndASecondReadyLineChangeNothing(t *testing.T) {
	for _, mode := range []string{"flood", "twice"} {
		f := newFixture(t, map[string]string{"mode": mode})
		s, _, _ := run(t, f.opts)
		eventually(t, mode+" ready", func() bool { return s.Status().State == Ready })
		time.Sleep(30 * time.Millisecond)
		if st := s.Status(); st.ID != idA || st.State != Ready {
			t.Fatalf("%s: Status = %+v", mode, st)
		}
	}
}

func TestAHostileStderrIsShownAsOneShortPrintableLine(t *testing.T) {
	hostile := "first line\nfinal \x1b[31mred\x00\r text " + strings.Repeat("é", 400)
	f := newFixture(t, map[string]string{"mode": "die", "stderr": hostile})
	f.opts.Attempts = 1
	s, _, done := run(t, f.opts)
	finished(t, done)
	reason := s.Status().Reason
	if strings.ContainsAny(reason, "\n\r\x1b\x00") || strings.Contains(reason, "first line") {
		t.Fatalf("reason = %q", reason)
	}
	body := strings.TrimPrefix(reason, "gave up after 1 quick exits: exit status 1: ")
	if n := len([]rune(body)); n > reasonMax+3 {
		t.Fatalf("reason holds %d characters", n)
	}
}

func TestStartFailureIsAReason(t *testing.T) {
	f := newFixture(t, nil)
	f.opts.Resolve = func(context.Context) (string, error) { return filepath.Join(f.dir, "absent"), nil }
	f.opts.Attempts = 1
	s, _, done := run(t, f.opts)
	finished(t, done)
	if st := s.Status(); st.State != Failed || !strings.Contains(st.Reason, "cannot start") {
		t.Fatalf("Status = %+v", st)
	}
}
