package sidecar

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
)

func fakeClient(args []string) int {
	p := fakebin.Params()
	log := p["log"]
	fakebin.Append(log, "start "+strings.Join(args, " "))
	var keys []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		keys = append(keys, k)
	}
	fakebin.Append(log, "env "+strings.Join(keys, ","))
	listening := "listening addr=127.0.0.1:4242 id=" + idB
	drain := func() int {
		_, _ = io.Copy(io.Discard, os.Stdin)
		fakebin.Append(log, "stdin closed")
		return 0
	}
	switch p["mode"] {
	case "die":
		fmt.Fprintln(os.Stderr, "hand-iroh: the key is not 32 bytes")
		return 3
	case "silent":
		return drain()
	case "deaf":
		fmt.Println(listening)
		time.Sleep(time.Minute)
		return 0
	case "exit0":
		fmt.Println(listening)
		return 0
	case "noisy":
		fmt.Println("state connected")
		fmt.Println("listening addr=10.0.0.1:4242 id=" + idB)
		fmt.Println("listening addr=127.0.0.1:4242 id=" + strings.ToUpper(idB))
		fmt.Println(listening)
		fmt.Println("state weird")
		fmt.Println(" state lost")
		fmt.Println("state busy")
		fmt.Println("state board_down")
		fmt.Println("state unreachable")
		fmt.Println("state lost")
		return drain()
	}
	fmt.Println(listening)
	fmt.Println("state connecting")
	fmt.Println("state connected")
	return drain()
}

type clientRecorder struct {
	mu  sync.Mutex
	got []ClientStatus
}

func (r *clientRecorder) add(st ClientStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, st)
}

func (r *clientRecorder) all() []ClientStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

func (r *clientRecorder) trace() string {
	var out []string
	for _, st := range r.all() {
		out = append(out, strings.TrimSuffix(st.State+"/"+st.Link, "/"))
	}
	return strings.Join(out, " ")
}

type clientFixture struct {
	*fixture
	rec  *clientRecorder
	opts ClientOptions
}

func newClientFixture(t *testing.T, params map[string]string) *clientFixture {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	p := map[string]string{"log": log}
	for k, v := range params {
		p[k] = v
	}
	bin := fakebin.Install(t, dir, "hand-iroh", "client", p)
	rec := &clientRecorder{}
	return &clientFixture{
		fixture: &fixture{dir: dir, log: log, bin: bin},
		rec:     rec,
		opts: ClientOptions{
			Resolve:  func(context.Context) (string, error) { return bin, nil },
			Host:     idA,
			KeyDir:   filepath.Join(dir, "keys"),
			Dir:      dir,
			OnChange: rec.add,
		},
	}
}

func (f *clientFixture) start(t *testing.T) (*Client, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	c, err := NewClient(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return c, cancel, done
}

func TestNewClientRefusesWhatCouldReachArgvAsAnotherFlagOrAnotherHost(t *testing.T) {
	f := newClientFixture(t, nil)
	for name, edit := range map[string]func(*ClientOptions){
		"no binary":           func(o *ClientOptions) { o.Resolve = nil },
		"no key folder":       func(o *ClientOptions) { o.KeyDir = "" },
		"empty host":          func(o *ClientOptions) { o.Host = "" },
		"short host":          func(o *ClientOptions) { o.Host = idA[:63] },
		"upper-case host":     func(o *ClientOptions) { o.Host = strings.ToUpper(idA) },
		"flag as host":        func(o *ClientOptions) { o.Host = "--listen" },
		"host and a code":     func(o *ClientOptions) { o.Host = idA + " --code K7QM3XRD2P" },
		"unknown relay":       func(o *ClientOptions) { o.Relay = "ftp://relay.test/" },
		"relay with a space":  func(o *ClientOptions) { o.Relay = "https://a b/" },
		"relay with newline":  func(o *ClientOptions) { o.Relay = "https://a/\n--new-key" },
		"relay over 200":      func(o *ClientOptions) { o.Relay = "https://" + strings.Repeat("a", 201) },
		"listen on any":       func(o *ClientOptions) { o.Listen = "0.0.0.0:0" },
		"listen on a name":    func(o *ClientOptions) { o.Listen = "localhost:8080" },
		"listen on ipv6":      func(o *ClientOptions) { o.Listen = "[::1]:8080" },
		"listen on a network": func(o *ClientOptions) { o.Listen = "192.168.1.5:8080" },
		"listen without port": func(o *ClientOptions) { o.Listen = "127.0.0.1" },
		"direct elsewhere":    func(o *ClientOptions) { o.Direct = "10.0.0.2:7000" },
	} {
		o := f.opts
		edit(&o)
		if _, err := NewClient(o); err == nil {
			t.Errorf("%s: NewClient accepted %+v", name, o)
		}
	}
}

func TestTheClientsArgvIsExactlyTheFlagsItNeedsAndNeverACodeOrALink(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*ClientOptions)
		want string
	}{
		"pairing": {func(o *ClientOptions) { o.NewKey, o.Relay = true, "https://relay.test/" }, "client --host %s --key-dir %s --new-key --relay https://relay.test/"},
		"later":   {func(o *ClientOptions) {}, "client --host %s --key-dir %s"},
		"trial": {func(o *ClientOptions) {
			o.Relay, o.Direct, o.Listen = "none", "127.0.0.1:7000", "127.0.0.1:0"
		}, "client --host %s --key-dir %s --listen 127.0.0.1:0 --relay none --direct 127.0.0.1:7000"},
	} {
		f := newClientFixture(t, nil)
		tc.edit(&f.opts)
		_, cancel, done := f.start(t)
		eventually(t, name+": the client to start", func() bool { return len(f.logged("start ")) == 1 })
		cancel()
		finished(t, done)
		want := "start " + fmt.Sprintf(tc.want, idA, f.opts.KeyDir)
		if got := f.logged("start ")[0]; got != want {
			t.Errorf("%s: argv = %q, want %q", name, got, want)
		}
	}
}

func TestTheClientRunsWithAnEmptyEnvironmentAndStopsWhenItsInputCloses(t *testing.T) {
	f := newClientFixture(t, nil)
	c, cancel, done := f.start(t)
	eventually(t, "the client to connect", func() bool { return c.Status().Link == Connected })
	if st := c.Status(); st.State != Ready || st.Addr != "127.0.0.1:4242" || st.ID != idB {
		t.Fatalf("Status = %+v", st)
	}
	cancel()
	finished(t, done)
	seen := f.logged("env")
	if len(seen) != 1 {
		t.Fatalf("env lines = %q", seen)
	}
	for _, key := range strings.Split(strings.TrimSpace(strings.TrimPrefix(seen[0], "env")), ",") {
		if key != "" && !strings.EqualFold(key, "SystemRoot") {
			t.Errorf("the client inherited %q", key)
		}
	}
	if got := f.logged("stdin closed"); len(got) != 1 {
		t.Fatalf("the client was not stopped by closing its input: %q", got)
	}
	if st := c.Status(); st.State != Stopped || st.Code != 0 {
		t.Fatalf("final = %+v", st)
	}
	if got, want := f.rec.trace(), "starting ready ready/connecting ready/connected stopped"; got != want {
		t.Fatalf("states = %q, want %q", got, want)
	}
}

func TestTheClientIgnoresMalformedLinesAndStatesBeforeItListens(t *testing.T) {
	f := newClientFixture(t, map[string]string{"mode": "noisy"})
	c, cancel, done := f.start(t)
	eventually(t, "the last state", func() bool { return c.Status().Link == Lost })
	cancel()
	finished(t, done)
	if got, want := f.rec.trace(), "starting ready ready/busy ready/board_down ready/unreachable ready/lost stopped"; got != want {
		t.Fatalf("states = %q, want %q", got, want)
	}
	if st := f.rec.all()[1]; st.Addr != "127.0.0.1:4242" || st.ID != idB {
		t.Fatalf("listening = %+v", st)
	}
}

func TestAClientThatExitsOnItsOwnFailsWithItsCodeAndLastErrorLine(t *testing.T) {
	f := newClientFixture(t, map[string]string{"mode": "die"})
	c, _, done := f.start(t)
	finished(t, done)
	st := c.Status()
	if st.State != Failed || st.Code != 3 || !strings.HasSuffix(st.Reason, "hand-iroh: the key is not 32 bytes") || !strings.Contains(st.Reason, "exit status 3") {
		t.Fatalf("Status = %+v", st)
	}
	f = newClientFixture(t, map[string]string{"mode": "exit0"})
	c, _, done = f.start(t)
	finished(t, done)
	if st := c.Status(); st.State != Failed || st.Code != 0 || st.Reason != "exit status 0" {
		t.Fatalf("an exit with no stop = %+v", st)
	}
}

func TestAClientThatNeverListensIsKilledAfterTheReadyWait(t *testing.T) {
	f := newClientFixture(t, map[string]string{"mode": "silent"})
	f.opts.ReadyWait = 100 * time.Millisecond
	c, _, done := f.start(t)
	finished(t, done)
	if st := c.Status(); st.State != Failed || st.Reason != "no listening line within 100ms" {
		t.Fatalf("Status = %+v", st)
	}
}

func TestAClientThatIgnoresItsClosedInputIsKilledAfterTheStopWait(t *testing.T) {
	f := newClientFixture(t, map[string]string{"mode": "deaf"})
	f.opts.StopWait = 200 * time.Millisecond
	c, cancel, done := f.start(t)
	eventually(t, "the client to listen", func() bool { return c.Status().State == Ready })
	began := time.Now()
	cancel()
	finished(t, done)
	if took := time.Since(began); took > 10*time.Second {
		t.Fatalf("stopping took %v", took)
	}
	if st := c.Status(); st.State != Stopped || st.Code == 0 {
		t.Fatalf("Status = %+v: a killed client has no clean exit", st)
	}
}

func TestAClientWhoseBinaryCannotBeResolvedFailsWithTheReason(t *testing.T) {
	f := newClientFixture(t, nil)
	f.opts.Resolve = func(context.Context) (string, error) { return "", fmt.Errorf("%w: it is not on PATH", ErrNotFound) }
	c, _, done := f.start(t)
	finished(t, done)
	if st := c.Status(); st.State != Failed || !strings.Contains(st.Reason, "hand-iroh not found") {
		t.Fatalf("Status = %+v", st)
	}
}
