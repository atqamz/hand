package luvus_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/luvus/fakeuhp"
)

type scanFake struct {
	srv      *fakeuhp.Server
	mu       sync.Mutex
	order    []string
	unseen   int
	scanFail *fakeuhp.Fail
	known    bool
}

func newScanFake(t *testing.T, unseen int) *scanFake {
	t.Helper()
	old, oldTries := *luvus.ScanGap, *luvus.ScanTries
	*luvus.ScanGap, *luvus.ScanTries = time.Millisecond, 4
	t.Cleanup(func() { *luvus.ScanGap, *luvus.ScanTries = old, oldTries })
	f := &scanFake{srv: fakeuhp.Start(t, sock(t)), unseen: unseen}
	f.srv.Handle("terminal.backend.inventory", func(json.RawMessage) (any, error) {
		f.note("inventory")
		return map[string]any{"server_generation": "g", "terminals": []any{
			map[string]any{"terminal_id": "other", "pane_id": "4", "root_process": map[string]any{"pid": 1, "start_marker": "1"}},
			map[string]any{"terminal_id": "tid", "pane_id": "5", "root_process": map[string]any{"pid": 7, "start_marker": "88"}},
		}}, nil
	})
	f.srv.Handle("terminal.backend.processes", func(params json.RawMessage) (any, error) {
		f.note("processes " + string(params))
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.scanFail != nil {
			return nil, *f.scanFail
		}
		if f.unseen > 0 {
			f.unseen--
			return map[string]any{"scan": "unavailable", "executables": []string{}}, nil
		}
		f.known = true
		return map[string]any{"scan": "observed", "executables": []string{"codex"}}, nil
	})
	return f
}

func (f *scanFake) note(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.order = append(f.order, s)
}

func (f *scanFake) handle(method string, ready func() error) {
	f.handleAs(method, true, ready)
}

func (f *scanFake) handleAs(method string, needScan bool, ready func() error) {
	f.srv.Handle(method, func(json.RawMessage) (any, error) {
		f.mu.Lock()
		known := f.known
		f.mu.Unlock()
		f.note(method)
		if needScan && !known {
			return nil, fakeuhp.Fail{Code: "agent_not_ready", Message: "target pane is not a running agent"}
		}
		return map[string]any{"type": "ok"}, ready()
	})
}

func (f *scanFake) calls() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var kinds []string
	for _, o := range f.order {
		kinds = append(kinds, strings.Fields(o)[0])
	}
	return strings.Join(kinds, " ")
}

func TestKeysAsksForAProcessScanBeforeTheFirstKeyOnly(t *testing.T) {
	f := newScanFake(t, 0)
	f.handle("agent.keys", func() error { return nil })
	c := luvus.Client{Socket: f.srv.Socket}
	ctx := context.Background()
	for range 2 {
		if err := c.Keys(ctx, "5", []string{"enter"}, 7, "tid"); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.calls(); got != "inventory processes agent.keys agent.keys" {
		t.Fatalf("calls = %s", got)
	}
	want := `{"server_generation":"g","terminal_id":"tid","pane_id":"5","expected_root":{"pid":7,"start_marker":"88"}}`
	if got := string(f.srv.Calls("terminal.backend.processes")[0]); got != want {
		t.Fatalf("processes params = %s, want %s", got, want)
	}
}

func TestKeysScansAgainAfterARefusalAndRetriesOnce(t *testing.T) {
	f := newScanFake(t, 2)
	f.handle("agent.keys", func() error { return nil })
	c := luvus.Client{Socket: f.srv.Socket}
	if err := c.Keys(context.Background(), "5", []string{"enter"}, 7, "tid"); err != nil {
		t.Fatal(err)
	}
	if got := f.calls(); got != "inventory processes agent.keys inventory processes processes agent.keys" {
		t.Fatalf("calls = %s", got)
	}

	f = newScanFake(t, 0)
	f.handle("agent.keys", func() error { return fakeuhp.Fail{Code: "agent_not_ready", Message: "input is fenced"} })
	err := luvus.Client{Socket: f.srv.Socket}.Keys(context.Background(), "5", []string{"enter"}, 7, "tid")
	if luvus.Code(err) != "agent_not_ready" || strings.Contains(err.Error(), "could not scan") {
		t.Fatalf("keys err = %v", err)
	}
	if got := f.calls(); got != "inventory processes agent.keys inventory processes agent.keys" {
		t.Fatalf("a refusal that stays: calls = %s", got)
	}
}

func TestKeysStopsPollingAfterTheScanTries(t *testing.T) {
	f := newScanFake(t, 100)
	f.handle("agent.keys", func() error { return nil })
	err := luvus.Client{Socket: f.srv.Socket}.Keys(context.Background(), "5", []string{"enter"}, 7, "tid")
	if luvus.Code(err) != "agent_not_ready" {
		t.Fatalf("keys err = %v", err)
	}
	if n := len(f.srv.Calls("terminal.backend.processes")); n != 1+4 {
		t.Fatalf("processes calls = %d, want the first scan and 4 polls", n)
	}
	if n := len(f.srv.Calls("agent.keys")); n != 2 {
		t.Fatalf("keys calls = %d, want 2", n)
	}
}

func TestAScanThatFailsNeverBlocksAKeyThatWorks(t *testing.T) {
	for name, fail := range map[string]fakeuhp.Fail{
		"an older Luvus":   {Code: "invalid_request", Message: "unknown terminal backend method"},
		"a stale terminal": {Code: "stale_terminal", Message: "terminal is gone"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newScanFake(t, 0)
			f.scanFail = &fail
			f.handleAs("agent.keys", false, func() error { return nil })
			c := luvus.Client{Socket: f.srv.Socket}
			if err := c.Keys(context.Background(), "5", []string{"enter"}, 7, "tid"); err != nil {
				t.Fatal(err)
			}
			if got := f.calls(); got != "inventory processes agent.keys" {
				t.Fatalf("calls = %s", got)
			}
		})
	}
}

func TestARefusalWithoutAScanSaysSoAndIsNotRetried(t *testing.T) {
	f := newScanFake(t, 0)
	f.scanFail = &fakeuhp.Fail{Code: "invalid_request", Message: "unknown terminal backend method"}
	f.handle("agent.keys", func() error { return nil })
	err := luvus.Client{Socket: f.srv.Socket}.Keys(context.Background(), "5", []string{"enter"}, 7, "tid")
	if luvus.Code(err) != "agent_not_ready" || !strings.Contains(err.Error(), "target pane is not a running agent; Luvus could not scan the pane's processes (luvus invalid_request: unknown terminal backend method)") {
		t.Fatalf("keys err = %v", err)
	}
	if n := len(f.srv.Calls("agent.keys")); n != 1 {
		t.Fatalf("keys calls = %d, want 1", n)
	}
}

func TestOtherRefusalsPassThroughWithoutAScan(t *testing.T) {
	f := newScanFake(t, 0)
	f.handleAs("agent.keys", false, func() error {
		return fakeuhp.Fail{Code: "content_revision_conflict", Message: "terminal changed"}
	})
	err := luvus.Client{Socket: f.srv.Socket}.Keys(context.Background(), "5", []string{"enter"}, 7, "tid")
	if luvus.Code(err) != "content_revision_conflict" {
		t.Fatalf("keys err = %v", err)
	}
	if got := f.calls(); got != "inventory processes agent.keys" {
		t.Fatalf("calls = %s", got)
	}
}

func TestPromptScansLikeKeys(t *testing.T) {
	f := newScanFake(t, 0)
	f.handle("agent.prompt", func() error { return nil })
	if err := (luvus.Client{Socket: f.srv.Socket}).Prompt(context.Background(), "5", "", "hello"); err != nil {
		t.Fatal(err)
	}
	if got := f.calls(); got != "inventory processes agent.prompt" {
		t.Fatalf("calls = %s", got)
	}
}
