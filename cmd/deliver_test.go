package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/ghutil"
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
	for _, want := range []string{
		"id: task-1\n",
		"reason: PR 597 offered to kunchenguid/no-mistakes\n",
		"delivered: ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("out = %q, want field %q", out.String(), want)
		}
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

func TestDeliverReassertsPRMetadataWhenAPRIsRecorded(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	url := "https://github.com/owner/secondhand/pull/1"
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip, PR: url}); err != nil {
		t.Fatal(err)
	}
	faketool.GH{PRs: []faketool.GHPR{{URL: url, State: "OPEN", Body: "operator wrote this"}}}.Install(t, faketool.Bin(t))

	cmd := newDeliverCmd()
	cmd.SetOut(&strings.Builder{})
	cmd.SetArgs([]string{"task-1", "--reason", "PR ready for review"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	meta, observation := ghutil.FetchPRMetadata(context.Background(), url)
	if !observation.Found() {
		t.Fatalf("observation = %+v, want the recorded PR found", observation)
	}
	operatorBody, _, ok := ghutil.SplitBody(meta.Body)
	if !ok {
		t.Fatalf("body = %q, want hand deliver to have established a pipeline region", meta.Body)
	}
	if operatorBody != "operator wrote this" {
		t.Fatalf("operator body = %q, want the original content preserved", operatorBody)
	}
}

func TestDeliverPropagatesAReassertMetadataFailure(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	url := "https://github.com/owner/secondhand/pull/1"
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip, PR: url}); err != nil {
		t.Fatal(err)
	}
	// No gh at all, fake or real, so reasserting the recorded PR's metadata fails deterministically.
	faketool.NoTools(t)

	cmd := newDeliverCmd()
	cmd.SetArgs([]string{"task-1", "--reason", "PR ready for review"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "reassert operator-owned PR metadata") {
		t.Fatalf("got %v, want the reassert failure to propagate", err)
	}

	got, readErr := state.Read(home, "task-1")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got.DeliveredReason != "PR ready for review" {
		t.Fatalf("DeliveredReason = %q, want the delivery recorded even though reasserting its PR metadata failed", got.DeliveredReason)
	}
}
