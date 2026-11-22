package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/sidecar"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{"client": fakeClient})
	os.Exit(m.Run())
}

func fakeClient(args []string) int {
	p := fakebin.Params()
	log := p["log"]
	fakebin.Append(log, "start "+strings.Join(args, " "))
	b, _ := os.ReadFile(log)
	starts := strings.Count(string(b), "start ")
	failures, _ := strconv.Atoi(p["failures"])
	if i := slices.Index(args, "--new-key"); i >= 0 {
		dir := args[slices.Index(args, "--key-dir")+1]
		if _, err := os.Stat(filepath.Join(dir, keyFile)); err != nil {
			_ = os.WriteFile(filepath.Join(dir, keyFile), []byte("k"), 0o600)
		}
	}
	switch p["mode"] {
	case "die":
		code, _ := strconv.Atoi(p["code"])
		fmt.Fprintln(os.Stderr, "hand-iroh: it died")
		return code
	case "flaky":
		if starts <= failures {
			fmt.Fprintln(os.Stderr, "hand-iroh: it died")
			return 1
		}
	}
	fmt.Println("listening addr=127.0.0.1:4242 id=" + hostB)
	fmt.Println("state connecting")
	fmt.Println("state connected")
	_, _ = io.Copy(io.Discard, os.Stdin)
	fakebin.Append(log, "stdin closed")
	return 0
}

type rig struct {
	t     *testing.T
	store *Store
	sup   *Supervisor
	log   string

	mu     sync.Mutex
	states []string
	delays []time.Duration
}

func newRig(t *testing.T, params map[string]string, mod func(*SupervisorOptions)) *rig {
	t.Helper()
	dir := t.TempDir()
	r := &rig{t: t, store: OpenStore(filepath.Join(dir, "remote")), log: filepath.Join(dir, "log")}
	p := map[string]string{"log": r.log}
	for k, v := range params {
		p[k] = v
	}
	bin := fakebin.Install(t, dir, "hand-iroh", "client", p)
	o := SupervisorOptions{
		Store:   r.store,
		Resolve: func(context.Context) (string, error) { return bin, nil },
		OnChange: func(host string, st sidecar.ClientStatus) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.states = append(r.states, host[:4]+" "+st.State)
		},
		Min: time.Second, Max: 5 * time.Second,
	}
	if mod != nil {
		mod(&o)
	}
	sup, err := NewSupervisor(o)
	if err != nil {
		t.Fatal(err)
	}
	sup.wait = func(ctx context.Context, d time.Duration) bool {
		r.mu.Lock()
		r.delays = append(r.delays, d)
		n := len(r.delays)
		r.mu.Unlock()
		return n < 6 && ctx.Err() == nil
	}
	r.sup = sup
	t.Cleanup(sup.Close)
	return r
}

func (r *rig) until(what string, ok func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			r.t.Fatalf("timed out waiting for %s; log:\n%s", what, r.logText())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *rig) logText() string {
	b, _ := os.ReadFile(r.log)
	return string(b)
}

func (r *rig) lines(prefix string) []string {
	var out []string
	for _, l := range strings.Split(r.logText(), "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func (r *rig) ready(host string) {
	r.t.Helper()
	r.until("a ready client", func() bool { return r.sup.Base(host) != "" })
}

func (r *rig) finished(host string) {
	r.t.Helper()
	r.sup.mu.Lock()
	done := r.sup.runs[host].done
	r.sup.mu.Unlock()
	r.until("the loop to end", func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
}

func TestTheClientRunsWithOnlyTheHostTheKeyFolderAndTheRelay(t *testing.T) {
	link, err := Parse(fragment("v=1", "e="+hostA, "f="+flt, "c="+code, "r=https%3A%2F%2Frelay.example.test%2F"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRig(t, nil, nil)
	if err := r.sup.Start(Host{ID: link.Host, Relay: link.Relay}, false); err != nil {
		t.Fatal(err)
	}
	r.ready(hostA)
	keys := filepath.Join(r.store.dir, hostA)
	want := "start client --host " + hostA + " --key-dir " + keys + " --relay https://relay.example.test/"
	if got := r.lines("start"); len(got) != 1 || got[0] != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	if got := r.sup.Base(hostA); got != "http://127.0.0.1:4242" {
		t.Errorf("Base = %q", got)
	}
	if st := r.sup.Status(hostA); st.State != sidecar.Ready || st.ID != hostB {
		t.Errorf("status = %+v", st)
	}
	if got := r.sup.Base(hostB); got != "" {
		t.Errorf("another host's Base = %q", got)
	}
	r.mu.Lock()
	states := strings.Join(r.states, ",")
	r.mu.Unlock()
	if !strings.HasPrefix(states, "4d92 starting,4d92 ready") {
		t.Errorf("states = %s", states)
	}
	for _, secret := range []string{code, flt} {
		if strings.Contains(r.logText(), secret) || strings.Contains(states, secret) {
			t.Errorf("%q reached the client or a state", secret)
		}
	}
}

func TestNoRelayMeansNoRelayFlag(t *testing.T) {
	r := newRig(t, nil, nil)
	if err := r.sup.Start(Host{ID: hostA}, false); err != nil {
		t.Fatal(err)
	}
	r.ready(hostA)
	if got := r.lines("start")[0]; strings.Contains(got, "--relay") || strings.Contains(got, "--new-key") {
		t.Fatalf("argv = %q", got)
	}
}

func TestNewKeyIsPassedOnlyWhenAskedAndOnlyUntilTheKeyExists(t *testing.T) {
	r := newRig(t, map[string]string{"mode": "flaky", "failures": "1"}, nil)
	if err := r.sup.Start(Host{ID: hostA}, true); err != nil {
		t.Fatal(err)
	}
	r.ready(hostA)
	got := r.lines("start")
	if len(got) != 2 || !strings.Contains(got[0], " --new-key") || strings.Contains(got[1], "--new-key") {
		t.Fatalf("argv lines = %q", got)
	}
}

func TestAClientThatFailsForAKeyOrUsageProblemIsNotRestartedUntilStartedAgain(t *testing.T) {
	for _, code := range []int{2, 3} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			r := newRig(t, map[string]string{"mode": "die", "code": strconv.Itoa(code)}, nil)
			if err := r.sup.Start(Host{ID: hostA}, false); err != nil {
				t.Fatal(err)
			}
			r.finished(hostA)
			st := r.sup.Status(hostA)
			if st.State != sidecar.Failed || st.Code != code || !strings.Contains(st.Reason, "hand-iroh: it died") {
				t.Fatalf("status = %+v", st)
			}
			if len(r.lines("start")) != 1 || len(r.delays) != 0 || r.sup.Base(hostA) != "" {
				t.Fatalf("starts %d, delays %v, base %q", len(r.lines("start")), r.delays, r.sup.Base(hostA))
			}
			if err := r.sup.Start(Host{ID: hostA}, false); err != nil {
				t.Fatal(err)
			}
			r.until("a second start", func() bool { return len(r.lines("start")) == 2 })
			r.sup.Stop(hostA)
			if st := r.sup.Status(hostA); st.State != sidecar.Stopped {
				t.Fatalf("after Stop: %+v", st)
			}
		})
	}
}

func TestAClientThatCrashesIsRestartedWithADoublingDelayThatStopsAtTheMaximum(t *testing.T) {
	r := newRig(t, map[string]string{"mode": "die", "code": "1"}, func(o *SupervisorOptions) { o.Min, o.Max = time.Second, 5*time.Second })
	r.sup.stable = time.Hour
	if err := r.sup.Start(Host{ID: hostA}, false); err != nil {
		t.Fatal(err)
	}
	r.finished(hostA)
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second, 5 * time.Second}
	if !slices.Equal(r.delays, want) {
		t.Fatalf("delays = %v, want %v", r.delays, want)
	}
	if n := len(r.lines("start")); n != 6 {
		t.Fatalf("%d starts, want 6", n)
	}
}

func TestARunThatLastedResetsTheDelay(t *testing.T) {
	r := newRig(t, map[string]string{"mode": "die", "code": "1"}, nil)
	r.sup.stable = 0
	if err := r.sup.Start(Host{ID: hostA}, false); err != nil {
		t.Fatal(err)
	}
	r.finished(hostA)
	for _, d := range r.delays {
		if d != time.Second {
			t.Fatalf("delays = %v", r.delays)
		}
	}
}

func TestAMissingBinaryIsRetriedAndItsReasonIsShown(t *testing.T) {
	r := newRig(t, nil, func(o *SupervisorOptions) {
		o.Resolve = func(context.Context) (string, error) { return "", errors.New("hand-iroh: needs 0.2.0") }
	})
	if err := r.sup.Start(Host{ID: hostA}, false); err != nil {
		t.Fatal(err)
	}
	r.finished(hostA)
	if st := r.sup.Status(hostA); st.State != sidecar.Failed || !strings.Contains(st.Reason, "needs 0.2.0") || len(r.delays) != 6 {
		t.Fatalf("status = %+v, delays %v", st, r.delays)
	}
}

func TestStopClosesTheClientsInputAndASecondStartIsNotNeededWhileItRuns(t *testing.T) {
	r := newRig(t, nil, nil)
	h := Host{ID: hostA}
	for range 2 {
		if err := r.sup.Start(h, false); err != nil {
			t.Fatal(err)
		}
	}
	r.ready(hostA)
	if n := len(r.lines("start")); n != 1 {
		t.Fatalf("%d starts for one host", n)
	}
	r.sup.Stop(hostA)
	if n := len(r.lines("stdin closed")); n != 1 {
		t.Fatalf("stdin closed %d times", n)
	}
	if st := r.sup.Status(hostA); st.State != sidecar.Stopped || r.sup.Base(hostA) != "" {
		t.Fatalf("status = %+v, base %q", st, r.sup.Base(hostA))
	}
	if err := r.sup.Start(h, false); err != nil {
		t.Fatal(err)
	}
	r.until("a second client", func() bool { return len(r.lines("start")) == 2 })
	r.sup.Stop("0000")
}

func TestCloseStopsEveryHostsClient(t *testing.T) {
	r := newRig(t, nil, nil)
	for _, id := range []string{hostA, hostB} {
		if err := r.sup.Start(Host{ID: id}, false); err != nil {
			t.Fatal(err)
		}
	}
	r.ready(hostA)
	r.ready(hostB)
	r.sup.Close()
	if n := len(r.lines("stdin closed")); n != 2 {
		t.Fatalf("stdin closed %d times, want 2", n)
	}
}

func TestStartRefusesWhatTheClientRefusesAndStartsNothing(t *testing.T) {
	r := newRig(t, nil, nil)
	if err := r.sup.Start(Host{ID: hostA, Relay: "ftp://x.test"}, false); !errors.Is(err, sidecar.ErrClientOptions) {
		t.Fatalf("a bad relay: %v", err)
	}
	if err := r.sup.Start(Host{ID: "../x"}, false); !errors.Is(err, ErrBad) {
		t.Fatalf("a bad host: %v", err)
	}
	if r.logText() != "" || len(r.sup.runs) != 0 {
		t.Fatalf("something started: %q", r.logText())
	}
	if _, err := NewSupervisor(SupervisorOptions{}); !errors.Is(err, ErrNoStore) {
		t.Fatalf("an empty supervisor: %v", err)
	}
}
