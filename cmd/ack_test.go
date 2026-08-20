package cmd

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestAckRecordsTheActDurably(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip}); err != nil {
		t.Fatal(err)
	}
	report := "done: PR up\n"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newAckCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "--reason", "reviewed the diff and merge is mine to do"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "result: acknowledged\n") {
		t.Fatalf("out = %q, want an acknowledged confirmation", out.String())
	}
	for _, want := range []string{
		"id: task-1\n",
		"reason: reviewed the diff and merge is mine to do\n",
		"acknowledged_at: ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("out = %q, want field %q", out.String(), want)
		}
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AcknowledgedReason != "reviewed the diff and merge is mine to do" {
		t.Fatalf("AcknowledgedReason = %q", got.AcknowledgedReason)
	}
	if got.AcknowledgedAt == "" {
		t.Fatal("AcknowledgedAt not stamped")
	}
	if got.AcknowledgedOffset != int64(len(report)) {
		t.Fatalf("AcknowledgedOffset = %d, want %d", got.AcknowledgedOffset, len(report))
	}
}

// hand ack takes no --reason at all: atqamz/hand#267 leaves it optional, unlike hand deliver's.
func TestAckReasonIsOptional(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj"}); err != nil {
		t.Fatal(err)
	}

	cmd := newAckCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AcknowledgedAt == "" {
		t.Fatal("AcknowledgedAt not stamped without a --reason")
	}
}

func TestAckRefusesWhenTaskMissing(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newAckCmd()
	cmd.SetArgs([]string{"missing-task"})
	assertExitCode3(t, cmd.Execute())
}

// Re-running is a correction, not a conflict, the same convention hand deliver established: whatever
// the channel carries right now replaces what an earlier ack claimed to cover.
func TestAckIsIdempotentAndAdvancesTheCursor(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj"}); err != nil {
		t.Fatal(err)
	}
	first := "working: on it\n"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newAckCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetArgs([]string{"task-1", "--reason", "first pass"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	firstAck, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if firstAck.AcknowledgedOffset != int64(len(first)) {
		t.Fatalf("AcknowledgedOffset = %d, want %d", firstAck.AcknowledgedOffset, len(first))
	}

	second := first + "done: PR up\n"
	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	again := newAckCmd()
	again.SetOut(&strings.Builder{})
	again.SetArgs([]string{"task-1", "--reason", "second pass"})
	if err := again.Execute(); err != nil {
		t.Fatal(err)
	}
	secondAck, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if secondAck.AcknowledgedReason != "second pass" {
		t.Fatalf("AcknowledgedReason = %q, want the corrected reason", secondAck.AcknowledgedReason)
	}
	if secondAck.AcknowledgedOffset != int64(len(second)) {
		t.Fatalf("AcknowledgedOffset = %d, want %d covering everything reported so far", secondAck.AcknowledgedOffset, len(second))
	}
}

// The whole point of atqamz/hand#267: hand status renders no completion as acknowledged until this
// command has actually run against it.
func TestAckClearsTheUnacknowledgedFlagStatusRenders(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	writeFakeHerdrPaneStatus(t, "idle")
	if err := writeTaskAttempt(t, home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip,
		CreatedAt: "2026-07-24T10:00:00Z"}, state.Attempt{Lifecycle: state.AttemptRunning, Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}
	writeDoneReport(t, home, "task-1", "PR up")

	before := newStatusCmd()
	var beforeOut strings.Builder
	before.SetOut(&beforeOut)
	before.SetArgs(nil)
	if err := before.Execute(); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fleetFlags(t, beforeOut.String(), "task-1"), "unacknowledged") {
		t.Fatalf("flags = %v, want unacknowledged before hand ack runs", fleetFlags(t, beforeOut.String(), "task-1"))
	}

	ack := newAckCmd()
	ack.SetOut(&strings.Builder{})
	ack.SetArgs([]string{"task-1"})
	if err := ack.Execute(); err != nil {
		t.Fatal(err)
	}

	after := newStatusCmd()
	var afterOut strings.Builder
	after.SetOut(&afterOut)
	after.SetArgs(nil)
	if err := after.Execute(); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fleetFlags(t, afterOut.String(), "task-1"), "unacknowledged") {
		t.Fatalf("flags = %v, want unacknowledged cleared once hand ack has run", fleetFlags(t, afterOut.String(), "task-1"))
	}
}
