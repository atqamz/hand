package workerobs

import (
	"errors"
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

type inspectorStub struct {
	info  herdr.ProcessInfo
	err   error
	calls int
}

func (s *inspectorStub) PaneProcessInfo(string) (herdr.ProcessInfo, error) {
	s.calls++
	return s.info, s.err
}

func TestNormalizeResidentWorkerPassesThroughWithoutProcessProbe(t *testing.T) {
	in := herdr.Pane{PaneID: "p1", Agent: harness.Claude, AgentStatus: herdr.StatusBlocked}
	probe := &inspectorStub{err: errors.New("must not be called")}
	got, err := Normalize(state.Attempt{Harness: harness.Claude}, in, probe)
	if err != nil || got != in || probe.calls != 0 {
		t.Fatalf("Normalize() = %+v, %v, calls=%d; want unchanged resident pane", got, err, probe.calls)
	}
}

func TestNormalizeOneShotUsesProcessPresenceForWorking(t *testing.T) {
	probe := &inspectorStub{info: herdr.ProcessInfo{
		ForegroundProcesses: []herdr.Process{{Name: "agy"}},
	}}
	got, err := Normalize(
		state.Attempt{Harness: harness.Antigravity, LaunchConfirmedAt: "2026-08-24T00:00:00Z"},
		herdr.Pane{PaneID: "p1", AgentStatus: herdr.StatusUnknown},
		probe,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != harness.Antigravity || got.AgentStatus != herdr.StatusWorking {
		t.Fatalf("Normalize() = %+v, want Antigravity working", got)
	}
}

func TestNormalizeConfirmedExitedOneShotIsDoneLivenessOnly(t *testing.T) {
	got, err := Normalize(
		state.Attempt{Harness: harness.Antigravity, LaunchConfirmedAt: "2026-08-24T00:00:00Z"},
		herdr.Pane{PaneID: "p1", AgentStatus: herdr.StatusUnknown},
		&inspectorStub{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent != harness.Antigravity || got.AgentStatus != herdr.StatusDone {
		t.Fatalf("Normalize() = %+v, want confirmed exited Antigravity done-liveness", got)
	}
}

func TestNormalizeUnconfirmedExitedOneShotStaysUnknown(t *testing.T) {
	in := herdr.Pane{PaneID: "p1", AgentStatus: herdr.StatusUnknown}
	got, err := Normalize(state.Attempt{Harness: harness.Antigravity}, in, &inspectorStub{})
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Fatalf("Normalize() = %+v, want no fabricated identity before launch confirmation", got)
	}
}

func TestNormalizeDoesNotOverwriteDifferentLiveAgent(t *testing.T) {
	in := herdr.Pane{PaneID: "p1", Agent: harness.Claude, AgentStatus: herdr.StatusWorking}
	probe := &inspectorStub{info: herdr.ProcessInfo{ForegroundProcesses: []herdr.Process{{Name: "agy"}}}}
	got, err := Normalize(
		state.Attempt{Harness: harness.Antigravity, LaunchConfirmedAt: "2026-08-24T00:00:00Z"},
		in,
		probe,
	)
	if err != nil || got != in || probe.calls != 0 {
		t.Fatalf("Normalize() = %+v, %v, calls=%d; want conflicting live identity preserved", got, err, probe.calls)
	}
}

func TestNormalizeProcessObservationFailureDoesNotFabricateExit(t *testing.T) {
	in := herdr.Pane{PaneID: "p1", AgentStatus: herdr.StatusUnknown}
	got, err := Normalize(
		state.Attempt{Harness: harness.Antigravity, LaunchConfirmedAt: "2026-08-24T00:00:00Z"},
		in,
		&inspectorStub{err: errors.New("process info unavailable")},
	)
	if err == nil || got != in {
		t.Fatalf("Normalize() = %+v, %v; want unchanged pane plus observation error", got, err)
	}
}
