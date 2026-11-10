package board_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/transcript"
)

const scopeExport = `{"info":{},"messages":[` +
	`{"info":{"role":"assistant","finish":"stop","time":{"created":1790570001000}},"tokens":{"input":508139,"cache":{"read":0,"write":0}},"parts":[{"type":"text","text":"Done."}]}]}`

func TestTheTaskPanelDoesNotReadTheTranscript(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{
		"args":   "session export --standalone ses_1",
		"count":  count,
		"export": scopeExport,
	})
	fx := newFixture(t)
	seed(t, fx.st)
	fx.options.Transcript = &transcript.Reader{Paths: transcript.Paths{Opencode: bin, OpencodeData: t.TempDir()}}
	ctx := context.Background()
	sup, err := fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "opencode", Model: "m", Effort: "low", Argv: []string{"/bin/opencode"}, Session: "ses_1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	get(t, h, "/task/t1")
	if _, err := os.Stat(count); err == nil {
		b, _ := os.ReadFile(count)
		if strings.Count(string(b), "run") != 0 {
			t.Fatalf("the task panel exported the transcript %d times", strings.Count(string(b), "run"))
		}
	}
	fleet := region(get(t, h, "/"), "status")
	contains(t, "fleet", fleet, "508K")
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "run") != 1 {
		t.Fatalf("the fleet page exports = %q, %v; want 1", b, err)
	}
}

func TestTheConversationPageRendersTheGaugeOfItsConversation(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{
		"args":   "session export --standalone ses_1",
		"count":  count,
		"export": scopeExport,
	})
	fx := newFixture(t)
	seed(t, fx.st)
	fx.options.Transcript = &transcript.Reader{Paths: transcript.Paths{Opencode: bin, OpencodeData: t.TempDir()}}
	ctx := context.Background()
	sup, err := fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "opencode", Model: "m", Effort: "low", Argv: []string{"/bin/opencode"}, Session: "ses_1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	h := fx.handler()
	page := get(t, h, "/supervisor/log")
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "run") != 1 {
		t.Fatalf("the conversation page exports = %q, %v; want 1", b, err)
	}
	contains(t, "conversation", page, `class="ctx live"`, "508K")
	get(t, h, "/task/t1")
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "run") != 1 {
		t.Fatalf("the task panel exports = %q, %v; want 1 (the conversation's)", b, err)
	}
}

func TestTheTimelineExportsAnEndedSessionOnce(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	data := t.TempDir()
	wal := filepath.Join(data, "opencode.db-wal")
	if err := os.WriteFile(wal, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", map[string]string{"count": count, "export": scopeExport})
	fx := newFixture(t)
	seed(t, fx.st)
	fx.options.Transcript = &transcript.Reader{Paths: transcript.Paths{Opencode: bin, OpencodeData: data}}
	ctx := context.Background()
	for _, session := range []string{"ses_old", "ses_live"} {
		sup, err := fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "opencode", Model: "m", Effort: "low", Argv: []string{"/bin/opencode"}, Session: session})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
			t.Fatal(err)
		}
		if session == "ses_old" {
			if _, err := fx.st.EndSupervisor(ctx, sup.ID, state.AttemptInterrupted, "test"); err != nil {
				t.Fatal(err)
			}
		}
	}
	h := fx.handler()
	for i := range 3 {
		get(t, h, "/")
		later := time.Now().Add(time.Duration(i+1) * time.Minute)
		if err := os.Chtimes(wal, later, later); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "run") != 4 {
		t.Fatalf("exports = %q, %v; want 4: the ended session once, the live one on every database change", b, err)
	}
}

func opencodeFleet(t *testing.T, params map[string]string) (*fixture, string) {
	t.Helper()
	count := filepath.Join(t.TempDir(), "count")
	p := map[string]string{"count": count, "export": scopeExport}
	for k, v := range params {
		p[k] = v
	}
	bin := fakebin.Install(t, t.TempDir(), "opencode", "opencode", p)
	fx := newFixture(t)
	seed(t, fx.st)
	fx.options.Transcript = &transcript.Reader{Paths: transcript.Paths{Opencode: bin, OpencodeData: t.TempDir()}}
	return fx, count
}

func addOpencodeSupervisor(t *testing.T, fx *fixture, session, end string) {
	t.Helper()
	ctx := context.Background()
	sup, err := fx.st.AddSupervisor(ctx, state.SupervisorSpec{Harness: "opencode", Model: "m", Effort: "low", Argv: []string{"/bin/opencode"}, Session: session})
	if err != nil {
		t.Fatal(err)
	}
	if end == state.AttemptFailed {
		if _, err := fx.st.EndSupervisor(ctx, sup.ID, end, "create failed"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := fx.st.SupervisorRunning(ctx, sup.ID, state.Terminal{ServerGeneration: "gen-1", TerminalID: "t1", PaneID: "2", PID: 1, StartMarker: "m"}); err != nil {
		t.Fatal(err)
	}
	if end != "" {
		if _, err := fx.st.EndSupervisor(ctx, sup.ID, end, "test"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheTimelineSkipsASupervisorThatNeverStarted(t *testing.T) {
	fx, count := opencodeFleet(t, nil)
	addOpencodeSupervisor(t, fx, "ses_never", state.AttemptFailed)
	addOpencodeSupervisor(t, fx, "ses_live", "")
	h := fx.handler()
	for range 3 {
		get(t, h, "/")
	}
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "run") != 1 {
		t.Fatalf("exports = %q, %v; want 1: the live session only", b, err)
	}
}

func TestTheTimelineExportsEndedSessionsInParallel(t *testing.T) {
	fx, count := opencodeFleet(t, map[string]string{"sleep": "600ms"})
	for _, session := range []string{"ses_a", "ses_b", "ses_c", "ses_d"} {
		addOpencodeSupervisor(t, fx, session, state.AttemptInterrupted)
	}
	addOpencodeSupervisor(t, fx, "ses_live", "")
	h := fx.handler()
	start := time.Now()
	get(t, h, "/")
	if d := time.Since(start); d > 1800*time.Millisecond {
		t.Fatalf("cold read took %v, want about one export (600ms), not five in a row", d)
	}
	if b, err := os.ReadFile(count); err != nil || strings.Count(string(b), "run") != 5 {
		t.Fatalf("exports = %q, %v; want 5", b, err)
	}
}
