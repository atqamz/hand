package cmd

import (
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestDeliverRecordsTheReasonOnTheTask(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip}); err != nil {
		t.Fatal(err)
	}

	cmd := newDeliverCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1", "--reason", "PR 597 offered to kunchenguid/no-mistakes"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "result: delivered\n") {
		t.Fatalf("out = %q, want a delivered confirmation", out.String())
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeliveredReason != "PR 597 offered to kunchenguid/no-mistakes" {
		t.Fatalf("DeliveredReason = %q", got.DeliveredReason)
	}
	if got.DeliveredAt == "" {
		t.Fatal("DeliveredAt not stamped")
	}

	// A second run is a correction, not a conflict: the last word on what was
	// delivered is the one teardown should record.
	again := newDeliverCmd()
	again.SetOut(&strings.Builder{})
	again.SetArgs([]string{"task-1", "--reason", "report handed over instead"})
	if err := again.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err = state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeliveredReason != "report handed over instead" {
		t.Fatalf("DeliveredReason = %q, want the corrected reason", got.DeliveredReason)
	}
}

func TestDeliverRequiresAReason(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj"}); err != nil {
		t.Fatal(err)
	}

	cmd := newDeliverCmd()
	cmd.SetArgs([]string{"task-1"})
	assertExitCode2(t, cmd.Execute())

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeliveredAt != "" {
		t.Fatalf("DeliveredAt = %q, want nothing recorded without a reason", got.DeliveredAt)
	}
}

func TestDeliverRefusesWhenTaskMissing(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)

	cmd := newDeliverCmd()
	cmd.SetArgs([]string{"missing-task", "--reason", "whatever"})
	assertExitCode3(t, cmd.Execute())
}
