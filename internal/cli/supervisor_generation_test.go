package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func writeGeneration(t *testing.T, h *harness, generation string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.vars["SECONDHAND_HOME"], "board.generation"), []byte(generation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAStaleGenerationRefusesToCloseTheSupervisorPane(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	term := rt.terms[0]

	writeGeneration(t, h, "dead 1")
	h.vars["HAND_GENERATION"] = "dead 1"
	writeGeneration(t, h, "live 2")
	out, errOut, code := h.run("supervisor", "stop")
	if code == 0 || !strings.Contains(errOut, "generation") {
		t.Fatalf("stale stop = %d %q %q", code, out, errOut)
	}
	if rt.isClosed(term.id) {
		t.Fatal("the stale process closed the supervisor pane")
	}
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.refused"}, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("refusals = %+v, %v", events, err)
	}
	for _, want := range []string{"s1", "dead 1", "live 2", "hand supervisor stop", "pid "} {
		if !strings.Contains(events[0].Detail, want) {
			t.Fatalf("refusal detail = %q, missing %q", events[0].Detail, want)
		}
	}
	sup, ok, err := openStore(t, h).LatestSupervisor(context.Background())
	if err != nil || !ok || sup.Status != state.AttemptRunning {
		t.Fatalf("supervisor after the refusal = %+v, %v, %v", sup, ok, err)
	}
}

func TestAShortLivedStopStillClosesTheSupervisorPane(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	writeGeneration(t, h, "live 2")
	out := h.ok("supervisor", "stop")
	has(t, "stop", out, "supervisor: s1", "status: stopped")
	if !rt.isClosed(rt.terms[0].id) {
		t.Fatal("the typed stop did not close the supervisor pane")
	}
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.stopped"}, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("stopped events = %+v, %v", events, err)
	}
	for _, want := range []string{"requested by hand supervisor stop", "pid ", "luvus hand-"} {
		if !strings.Contains(events[0].Detail, want) {
			t.Fatalf("stop detail = %q, missing %q", events[0].Detail, want)
		}
	}
}

func TestTheBoardRecordsTheGenerationItsWatchersHold(t *testing.T) {
	h := newHarness(t)
	h.ok("init")
	_, stop := startBoard(t, h, "127.0.0.1")
	root := h.vars["SECONDHAND_HOME"]
	generation, err := os.ReadFile(filepath.Join(root, "board.generation"))
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(generation))
	if len(fields) != 2 {
		t.Fatalf("board.generation = %q", generation)
	}
	pid, err := os.ReadFile(filepath.Join(root, "board.pid"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(pid), fields[0]+" ") {
		t.Fatalf("board.pid = %q, generation = %q", pid, generation)
	}
	stop()
	if _, err := os.Stat(filepath.Join(root, "board.generation")); !os.IsNotExist(err) {
		t.Fatalf("board.generation after stop: %v", err)
	}
}

func TestAWatcherExitNamesWhoAsked(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	term := rt.terms[0]
	writeGeneration(t, h, "live 2")
	h.vars["HAND_GENERATION"] = "live 2"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.runCtx(ctx, "watch", "--every", "50ms")
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitLive(t, "the watcher", func() bool {
		_, err := os.Stat(filepath.Join(h.home, "watch.pid"))
		return err == nil
	})
	if err := term.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	st := openStore(t, h)
	var events []state.Event
	var err error
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if events, err = st.EventsAfter(context.Background(), 0, []string{"supervisor.exited"}, 10); err == nil && len(events) == 1 {
			break
		}
	}
	if len(events) != 1 {
		t.Fatal("the watcher recorded no exit")
	}
	for _, want := range []string{"requested by hand watch", "pid "} {
		if !strings.Contains(events[0].Detail, want) {
			t.Fatalf("exit detail = %q, missing %q", events[0].Detail, want)
		}
	}
}
