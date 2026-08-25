package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testFleetHerdr(observe func(context.Context) SessionObservation, start func(context.Context) error, attach func(context.Context) error) *FleetHerdr {
	return &FleetHerdr{
		fleetID:      "f_test",
		session:      SessionName("f_test"),
		observeFn:    observe,
		startFn:      start,
		attachFn:     attach,
		startTimeout: 50 * time.Millisecond,
		pollInterval: time.Millisecond,
	}
}

func setFleetHerdrHome(t *testing.T) {
	t.Helper()
	t.Setenv("SECONDHAND_HOME", t.TempDir())
}

func TestFleetHerdrEnsureReadyDoesNotStart(t *testing.T) {
	var starts atomic.Int32
	h := testFleetHerdr(func(context.Context) SessionObservation {
		return SessionObservation{Name: "hand-f_test", State: SessionRunningCompatible}
	}, func(context.Context) error {
		starts.Add(1)
		return nil
	}, nil)

	if err := h.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := starts.Load(); got != 0 {
		t.Fatalf("start calls = %d, want zero", got)
	}
}

func TestFleetHerdrEnsureStartsOnlyAfterStoppedReobserve(t *testing.T) {
	setFleetHerdrHome(t)
	var observations atomic.Int32
	var starts atomic.Int32
	h := testFleetHerdr(func(context.Context) SessionObservation {
		if observations.Add(1) <= 2 {
			return SessionObservation{Name: "hand-f_test", State: SessionStopped}
		}
		return SessionObservation{Name: "hand-f_test", State: SessionRunningCompatible}
	}, func(context.Context) error {
		starts.Add(1)
		return nil
	}, nil)

	if err := h.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("start calls = %d, want one", got)
	}
	if got := observations.Load(); got != 3 {
		t.Fatalf("observations = %d, want initial, post-lock, and post-start readiness", got)
	}
}

func TestFleetHerdrEnsureFailsClosedForUnknownAndIncompatible(t *testing.T) {
	tests := []struct {
		name  string
		state SessionState
		want  error
	}{
		{name: "unknown", state: SessionUnknown, want: ErrSessionUnknown},
		{name: "incompatible", state: SessionIncompatible, want: ErrSessionIncompatible},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var starts atomic.Int32
			h := testFleetHerdr(func(context.Context) SessionObservation {
				return SessionObservation{Name: "hand-f_test", State: tt.state, Reason: tt.name}
			}, func(context.Context) error {
				starts.Add(1)
				return nil
			}, nil)

			err := h.Ensure(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Ensure() = %v, want %v", err, tt.want)
			}
			if got := starts.Load(); got != 0 {
				t.Fatalf("start calls = %d, want zero", got)
			}
		})
	}
}

func TestFleetHerdrEnsureConvergesConcurrentCallers(t *testing.T) {
	t.Setenv("SECONDHAND_HOME", filepath.Join(t.TempDir(), "Secondhand 測試"))
	var running atomic.Bool
	var starts atomic.Int32
	observe := func(context.Context) SessionObservation {
		if running.Load() {
			return SessionObservation{Name: "hand-f_test", State: SessionRunningCompatible}
		}
		return SessionObservation{Name: "hand-f_test", State: SessionStopped}
	}
	start := func(context.Context) error {
		starts.Add(1)
		time.Sleep(10 * time.Millisecond)
		running.Store(true)
		return nil
	}

	const callers = 12
	results := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- testFleetHerdr(observe, start, nil).Ensure(context.Background())
		}()
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("start calls = %d, want one", got)
	}
}

func TestFleetHerdrEnsureRetriesAfterDetachedStartReceiptLoss(t *testing.T) {
	setFleetHerdrHome(t)
	var running atomic.Bool
	var starts atomic.Int32
	receiptErr := errors.New("detached start receipt lost")
	h := testFleetHerdr(func(context.Context) SessionObservation {
		if running.Load() {
			return SessionObservation{Name: "hand-f_test", State: SessionRunningCompatible}
		}
		return SessionObservation{Name: "hand-f_test", State: SessionStopped}
	}, func(context.Context) error {
		starts.Add(1)
		running.Store(true)
		return receiptErr
	}, nil)

	if err := h.Ensure(context.Background()); !errors.Is(err, receiptErr) {
		t.Fatalf("first Ensure() = %v, want receipt error", err)
	}
	if err := h.Ensure(context.Background()); err != nil {
		t.Fatalf("retry Ensure() = %v, want exact ready observation", err)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("start calls = %d, want one", got)
	}
}

func TestFleetHerdrEnsureTimeoutDoesNotKillOrRestart(t *testing.T) {
	setFleetHerdrHome(t)
	var starts atomic.Int32
	h := testFleetHerdr(func(context.Context) SessionObservation {
		return SessionObservation{Name: "hand-f_test", State: SessionStopped}
	}, func(context.Context) error {
		starts.Add(1)
		return nil
	}, nil)
	h.startTimeout = 10 * time.Millisecond

	err := h.Ensure(context.Background())
	if !errors.Is(err, ErrEnsureTimeout) {
		t.Fatalf("Ensure() = %v, want timeout", err)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("start calls = %d, want one without blind restart", got)
	}
}

func TestFleetHerdrOpenEnsuresBeforeAttach(t *testing.T) {
	setFleetHerdrHome(t)
	var order []string
	var mu sync.Mutex
	appendOrder := func(value string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, value)
	}
	var observations atomic.Int32
	h := testFleetHerdr(func(context.Context) SessionObservation {
		appendOrder("observe")
		if observations.Add(1) <= 2 {
			return SessionObservation{Name: "hand-f_test", State: SessionStopped}
		}
		return SessionObservation{Name: "hand-f_test", State: SessionRunningCompatible}
	}, func(context.Context) error {
		appendOrder("start")
		return nil
	}, func(context.Context) error {
		appendOrder("attach")
		return nil
	})

	if err := h.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(order, ","), "observe,observe,start,observe,attach"; got != want {
		t.Fatalf("operation order = %q, want %q", got, want)
	}
}

func TestDaemonEnvironmentStripsSemanticIdentityAndPreservesCredentials(t *testing.T) {
	parent := []string{
		"HAND_HOME=/stale/home",
		"HAND_ROLE=worker",
		"HAND_TASK_ID=task-1",
		"HAND_ATTEMPT_ID=7",
		"HAND_REPORT_PATH=/stale/report",
		"HERDR_ENV=worker",
		"HERDR_SESSION=default",
		"HERDR_SOCKET_PATH=/stale/socket",
		"HERDR_WORKSPACE_ID=w1",
		"HERDR_TAB_ID=t1",
		"HERDR_PANE_ID=p1",
		"HAND_BRIDGE_ID=bridge-1",
		"HAND_ROUTING_MARKER=route-1",
		"HAND_RUNTIME_ID=runtime-1",
		"HAND_SUPERVISOR_ID=supervisor-1",
		"CLAUDECODE=1",
		"CLAUDE_CODE_CHILD_SESSION=child",
		"CLAUDE_CODE_SESSION_ID=session",
		"CODEX_THREAD_ID=thread",
		"PI_CODING_AGENT=true",
		"GROK_AGENT=true",
		"OPENCODE_SESSION_ID=session",
		"NO_MISTAKES_RUN_ID=run-1",
		"HERDR_TEST_CREDENTIAL=keep",
		"PATH=/usr/bin",
	}

	values := environmentMap(daemonEnvironment(parent))
	for _, key := range []string{
		"HAND_HOME", "HAND_ROLE", "HAND_TASK_ID", "HAND_ATTEMPT_ID", "HAND_REPORT_PATH",
		"HERDR_ENV", "HERDR_SESSION", "HERDR_SOCKET_PATH", "HERDR_WORKSPACE_ID", "HERDR_TAB_ID", "HERDR_PANE_ID",
		"HAND_BRIDGE_ID", "HAND_ROUTING_MARKER", "HAND_RUNTIME_ID", "HAND_SUPERVISOR_ID",
		"CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION", "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "PI_CODING_AGENT", "GROK_AGENT",
		"OPENCODE_SESSION_ID", "NO_MISTAKES_RUN_ID",
	} {
		if _, found := values[key]; found {
			t.Fatalf("daemon environment retained %s", key)
		}
	}
	if got := values["HERDR_TEST_CREDENTIAL"]; got != "keep" {
		t.Fatalf("credential = %q, want keep", got)
	}
	if got := values["PATH"]; got != "/usr/bin" {
		t.Fatalf("PATH = %q, want /usr/bin", got)
	}
}

func TestFleetHerdrServerParentHelper(t *testing.T) {
	if os.Getenv("HERDR_TEST_PARENT_HELPER") != "1" {
		return
	}
	client, err := NewClientAt(os.Getenv("HERDR_TEST_SERVER_EXECUTABLE"), []string{
		"HAND_HOME=/stale/home",
		"HAND_ROLE=worker",
		"HERDR_ENV=stale",
		"HERDR_SESSION=wrong",
		"HERDR_SOCKET_PATH=/stale/socket",
		"CODEX_THREAD_ID=stale-thread",
		"HERDR_TEST_CREDENTIAL=keep",
		"PATH=" + os.Getenv("PATH"),
		"HERDR_TEST_ARGS=" + os.Getenv("HERDR_TEST_ARGS"),
		"HERDR_TEST_ENV=" + os.Getenv("HERDR_TEST_ENV"),
		"HERDR_TEST_READY=" + os.Getenv("HERDR_TEST_READY"),
		"HERDR_TEST_SURVIVED=" + os.Getenv("HERDR_TEST_SURVIVED"),
	})
	if err != nil {
		t.Fatal(err)
	}
	client.session = "hand-f_test"
	if err := client.startServer(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFleetHerdrServerStartUsesStructuredArgvAndSurvivesParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the detached shell fixture is POSIX-only; Windows uses the native helper")
	}
	root := t.TempDir()
	server := filepath.Join(root, "managed herdr server")
	argsPath := filepath.Join(root, "args")
	envPath := filepath.Join(root, "env")
	readyPath := filepath.Join(root, "ready")
	survivedPath := filepath.Join(root, "survived")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nenv > %q\n: > %q\nsleep 0.25\n: > %q\n", argsPath, envPath, readyPath, survivedPath)
	if err := os.WriteFile(server, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(os.Args[0], "-test.run=^TestFleetHerdrServerParentHelper$")
	command.Env = append(os.Environ(),
		"HERDR_TEST_PARENT_HELPER=1",
		"HERDR_TEST_SERVER_EXECUTABLE="+server,
		"HERDR_TEST_ARGS="+argsPath,
		"HERDR_TEST_ENV="+envPath,
		"HERDR_TEST_READY="+readyPath,
		"HERDR_TEST_SURVIVED="+survivedPath,
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, readyPath)
	if err := command.Wait(); err != nil {
		t.Fatalf("parent helper: %v", err)
	}
	waitForTestFile(t, survivedPath)

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(string(args)), []string{"--session", "hand-f_test", "server"}; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("server argv = %q, want %q", got, want)
	}
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	values := environmentMap(strings.Split(strings.TrimSpace(string(env)), "\n"))
	for _, key := range []string{"HAND_HOME", "HAND_ROLE", "HERDR_ENV", "HERDR_SESSION", "HERDR_SOCKET_PATH", "CODEX_THREAD_ID"} {
		if _, found := values[key]; found {
			t.Fatalf("server environment retained %s: %q", key, env)
		}
	}
	if got := values["HERDR_TEST_CREDENTIAL"]; got != "keep" {
		t.Fatalf("server credential = %q, want keep", got)
	}
}

func TestFleetHerdrAttachUsesStructuredArgvAndScrubbedEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the shell fixture is POSIX-only")
	}
	root := t.TempDir()
	attach := filepath.Join(root, "managed herdr attach")
	argsPath := filepath.Join(root, "args")
	envPath := filepath.Join(root, "env")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nenv > %q\n", argsPath, envPath)
	if err := os.WriteFile(attach, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	client, err := NewClientAt(attach, []string{
		"HAND_HOME=/stale/home",
		"HAND_ROLE=worker",
		"HERDR_SESSION=wrong",
		"CODEX_THREAD_ID=stale-thread",
		"HERDR_TEST_CREDENTIAL=keep",
		"PATH=" + os.Getenv("PATH"),
	})
	if err != nil {
		t.Fatal(err)
	}
	client.session = "hand-f_test"
	if err := client.attach(context.Background()); err != nil {
		t.Fatal(err)
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Fields(string(args)), []string{"session", "attach", "hand-f_test"}; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("attach argv = %q, want %q", got, want)
	}
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	values := environmentMap(strings.Split(strings.TrimSpace(string(env)), "\n"))
	for _, key := range []string{"HAND_HOME", "HAND_ROLE", "HERDR_SESSION", "CODEX_THREAD_ID"} {
		if _, found := values[key]; found {
			t.Fatalf("attach environment retained %s: %q", key, env)
		}
	}
	if got := values["HERDR_TEST_CREDENTIAL"]; got != "keep" {
		t.Fatalf("attach credential = %q, want keep", got)
	}
}

func waitForTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func environmentMap(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
