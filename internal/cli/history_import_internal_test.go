package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func TestImportSessionReadsTheSupervisorRowsAgainForAnOpencodeSessionResumedAfterTheSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake opencode is a shell script")
	}
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 4, 40, 0, 0, time.UTC)
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte("#!/bin/sh\ntouch \"$(dirname \"$0\")/called\"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"HOME": t.TempDir(), "PATH": bin + ":/usr/bin:/bin"}
	r := &runner{env: Env{Getenv: func(k string) string { return vars[k] }, Stdout: io.Discard, Stderr: io.Discard, Now: func() time.Time { return now }}, home: t.TempDir()}
	spec := state.SupervisorSpec{Harness: "opencode", Argv: []string{"opencode"}, Session: "ses_1"}
	first, err := st.AddSupervisor(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	term := state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "1", PID: 1, StartMarker: "m"}
	if _, err := st.SupervisorRunning(ctx, first.ID, term); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EndSupervisor(ctx, first.ID, state.AttemptStopped, "stopped"); err != nil {
		t.Fatal(err)
	}
	sups, err := st.Supervisors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := importGroups(sups, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.AddSupervisor(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SupervisorRunning(ctx, second.ID, term); err != nil {
		t.Fatal(err)
	}
	row, err := importSession(ctx, r, st, groups[0], false)
	if err != nil {
		t.Fatal(err)
	}
	if row.status != "still live" {
		t.Fatalf("status = %q", row.status)
	}
	if _, err := os.Stat(filepath.Join(bin, "called")); err == nil {
		t.Fatalf("the opencode binary ran for a session resumed after the snapshot")
	}
}
