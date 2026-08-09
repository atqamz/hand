package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func setupHoldHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)
	mkFleetDirs(t, home)
	return home
}

func TestHoldSetOperator(t *testing.T) {
	home := setupHoldHome(t)

	cmd := newHoldSetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"fix-login", "--kind", "operator", "--reason", "needs a call"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "fix-login") {
		t.Fatalf("out = %q, want it to name the id", out.String())
	}

	got, found, err := state.ReadHold(home, "fix-login")
	if err != nil || !found {
		t.Fatalf("ReadHold = %v, %v", found, err)
	}
	if got.Kind != state.HoldKindOperator || got.Reason != "needs a call" {
		t.Fatalf("got %+v", got)
	}
}

func TestHoldSetBlocked(t *testing.T) {
	home := setupHoldHome(t)

	cmd := newHoldSetCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"fix-login", "--kind", "blocked", "--reason", "waiting on migration", "--blocked-on", "other-task"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, found, err := state.ReadHold(home, "fix-login")
	if err != nil || !found {
		t.Fatalf("ReadHold = %v, %v", found, err)
	}
	if got.Kind != state.HoldKindBlocked || got.BlockedOn != "other-task" {
		t.Fatalf("got %+v", got)
	}
}

func TestHoldSetUpsertsReason(t *testing.T) {
	home := setupHoldHome(t)

	run := func(reason string) {
		cmd := newHoldSetCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetArgs([]string{"fix-login", "--kind", "operator", "--reason", reason})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	run("first reason")
	run("narrowed down reason")

	got, _, err := state.ReadHold(home, "fix-login")
	if err != nil {
		t.Fatal(err)
	}
	if got.Reason != "narrowed down reason" {
		t.Fatalf("got %+v, want the second set to win", got)
	}

	holds, err := state.ListHolds(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 1 {
		t.Fatalf("ListHolds = %+v, want the upsert to leave a single row", holds)
	}
}

func TestHoldSetRejectsInvalidKind(t *testing.T) {
	setupHoldHome(t)

	cmd := newHoldSetCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"fix-login", "--kind", "waiting-room", "--reason", "needs a call"})
	assertExitCode2(t, cmd.Execute())
}

// The limit kind is hand watch's, and the message says so rather than claiming the kind
// does not exist: an operator who has seen one in hand status needs to know who sets it.
func TestHoldSetRefusesTheMachineSetLimitKind(t *testing.T) {
	home := setupHoldHome(t)

	cmd := newHoldSetCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"fix-login", "--kind", "limit", "--reason", "out of quota"})
	err := cmd.Execute()
	assertExitCode2(t, err)
	if !strings.Contains(err.Error(), "hand watch") {
		t.Fatalf("err = %v, want it to name hand watch as the owner of the kind", err)
	}
	if _, found, readErr := state.ReadHold(home, "fix-login"); found || readErr != nil {
		t.Fatalf("ReadHold = %v, %v, want no hold written", found, readErr)
	}
}

// Clearing one by hand is allowed and stays allowed: it is the operator's escape hatch
// out of a hold hand set on their behalf, and refusing it would make the machine-set
// kind the one thing an operator cannot undo.
func TestHoldClearAcceptsAMachineSetLimitHold(t *testing.T) {
	home := setupHoldHome(t)
	if err := state.SetHold(home, state.Hold{ID: "fix-login", Kind: state.HoldKindLimit, Reason: "out of quota"}); err != nil {
		t.Fatal(err)
	}

	cmd := newHoldClearCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"fix-login"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := state.ReadHold(home, "fix-login"); found || err != nil {
		t.Fatalf("ReadHold after clear = %v, %v, want gone", found, err)
	}
}

func TestHoldSetRequiresReason(t *testing.T) {
	setupHoldHome(t)

	cmd := newHoldSetCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"fix-login", "--kind", "operator"})
	assertExitCode2(t, cmd.Execute())
}

func TestHoldSetRequiresBlockedOnForBlockedKind(t *testing.T) {
	setupHoldHome(t)

	cmd := newHoldSetCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"fix-login", "--kind", "blocked", "--reason", "needs a call"})
	assertExitCode2(t, cmd.Execute())
}

func TestHoldSetRejectsBlockedOnForOperatorKind(t *testing.T) {
	setupHoldHome(t)

	cmd := newHoldSetCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"fix-login", "--kind", "operator", "--reason", "needs a call", "--blocked-on", "other-task"})
	assertExitCode2(t, cmd.Execute())
}

func TestHoldClear(t *testing.T) {
	home := setupHoldHome(t)
	if err := state.SetHold(home, state.Hold{ID: "fix-login", Kind: state.HoldKindOperator, Reason: "needs a call"}); err != nil {
		t.Fatal(err)
	}

	cmd := newHoldClearCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"fix-login"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "fix-login") {
		t.Fatalf("out = %q, want it to name the id", out.String())
	}

	_, found, err := state.ReadHold(home, "fix-login")
	if err != nil || found {
		t.Fatalf("ReadHold after clear = %v, %v, want gone", found, err)
	}
}

func TestHoldClearMissing(t *testing.T) {
	setupHoldHome(t)

	cmd := newHoldClearCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"nope"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !errors.Is(err, state.ErrHoldNotFound) {
		t.Fatalf("err = %v, want ErrHoldNotFound", err)
	}
}
