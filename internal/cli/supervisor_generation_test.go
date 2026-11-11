package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

func writeGeneration(t *testing.T, h *harness, generation string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.vars["SECONDHAND_HOME"], "board.generation"), []byte(generation+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func liveGeneration(t *testing.T, pid int) string {
	t.Helper()
	marker, err := luvus.ProcStartMarker(pid)
	if err != nil {
		t.Fatalf("start marker of %d: %v", pid, err)
	}
	return fmt.Sprintf("%d %s", pid, marker)
}

func holdProcess(t *testing.T) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "HAND_TEST_HOLD=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func watchGeneration(home string) string {
	b, _ := os.ReadFile(filepath.Join(home, "watch.generation"))
	return strings.TrimSpace(string(b))
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
	generation := liveGeneration(t, os.Getpid())
	writeGeneration(t, h, generation)
	h.vars["HAND_GENERATION"] = generation
	out := h.ok("supervisor", "stop")
	has(t, "stop", out, "supervisor: s1", "status: stopped")
	if !rt.isClosed(rt.terms[0].id) {
		t.Fatal("the typed stop did not close the supervisor pane")
	}
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.stopped"}, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("stopped events = %+v, %v", events, err)
	}
	for _, want := range []string{"requested by hand supervisor stop", "start " + strings.Fields(generation)[1], "luvus hand-"} {
		if !strings.Contains(events[0].Detail, want) {
			t.Fatalf("stop detail = %q, missing %q", events[0].Detail, want)
		}
	}
}

func TestADeadGenerationRefusesToCloseTheSupervisorPane(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	term := rt.terms[0]
	generation := "2147483646 stale-marker"
	writeGeneration(t, h, generation)
	h.vars["HAND_GENERATION"] = generation
	out, errOut, code := h.run("supervisor", "stop")
	if code == 0 || !strings.Contains(errOut, "generation") {
		t.Fatalf("dead generation stop = %d %q %q", code, out, errOut)
	}
	if rt.isClosed(term.id) {
		t.Fatal("the dead generation closed the supervisor pane")
	}
	events, err := openStore(t, h).EventsAfter(context.Background(), 0, []string{"supervisor.refused"}, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("refusals = %+v, %v", events, err)
	}
	for _, want := range []string{generation, "no longer alive", "hand supervisor stop"} {
		if !strings.Contains(events[0].Detail, want) {
			t.Fatalf("refusal detail = %q, missing %q", events[0].Detail, want)
		}
	}
}

func TestAWatcherReseedsWhenTheBoardGenerationChanges(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	term := rt.terms[0]
	enableWatcher(t, h.home)
	h.vars["HAND_TEST_REAL_WATCH"] = "1"
	t.Cleanup(func() { h.run("watch", "stop") })
	root := h.vars["SECONDHAND_HOME"]
	first := liveGeneration(t, os.Getpid())
	second := liveGeneration(t, holdProcess(t))
	writeGeneration(t, h, first)
	env := h.env(io.Discard, io.Discard)
	if _, err := cli.EnsureWatcher(context.Background(), env, h.home, root, first); err != nil {
		t.Fatal(err)
	}
	waitLive(t, "the watcher of the first generation", func() bool { return watchGeneration(h.home) == first })
	oldPID, err := os.ReadFile(filepath.Join(h.home, "watch.pid"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneration(t, h, second)
	if _, err := cli.EnsureWatcher(context.Background(), env, h.home, root, second); err != nil {
		t.Fatal(err)
	}
	waitLive(t, "the watcher of the second generation", func() bool {
		pid, err := os.ReadFile(filepath.Join(h.home, "watch.pid"))
		return err == nil && !bytes.Equal(pid, oldPID) && watchGeneration(h.home) == second
	})
	settle()
	if err := term.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	st := openStore(t, h)
	var events []state.Event
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		rt.srv.Publish("terminal.exited", map[string]any{"pane_id": term.pane, "terminal_id": term.id, "server_generation": rt.srv.Generation()})
		if events, _ = st.EventsAfter(context.Background(), 0, []string{"supervisor.exited"}, 10); len(events) == 1 {
			break
		}
	}
	if len(events) != 1 {
		log, _ := os.ReadFile(filepath.Join(h.home, "watch.log"))
		t.Fatalf("the reseeded watcher recorded no exit; watch.log:\n%s", log)
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

func TestAWatcherExitNamesWhoRecordedIt(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	term := rt.terms[0]
	generation := liveGeneration(t, os.Getpid())
	writeGeneration(t, h, generation)
	h.vars["HAND_GENERATION"] = generation
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
	for _, want := range []string{"recorded by hand watch", "pid "} {
		if !strings.Contains(events[0].Detail, want) {
			t.Fatalf("exit detail = %q, missing %q", events[0].Detail, want)
		}
	}
}
