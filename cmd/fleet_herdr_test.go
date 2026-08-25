package cmd

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
)

type fakeFleetHerdrCommand struct {
	session      string
	observation  herdr.SessionObservation
	observeCalls int
	ensureCalls  int
	openCalls    int
}

func (f *fakeFleetHerdrCommand) Session() string {
	return f.session
}

func (f *fakeFleetHerdrCommand) Observe(context.Context) herdr.SessionObservation {
	f.observeCalls++
	return f.observation
}

func (f *fakeFleetHerdrCommand) Ensure(context.Context) error {
	f.ensureCalls++
	return nil
}

func (f *fakeFleetHerdrCommand) Open(context.Context) error {
	f.openCalls++
	return nil
}

func TestFleetHerdrStatusObservesOnlyCurrentFleetNamespace(t *testing.T) {
	home := setupSessionHome(t)
	fleetID, err := state.FleetIDReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeFleetHerdrCommand{
		session: herdr.SessionName(fleetID),
		observation: herdr.SessionObservation{
			Name:   herdr.SessionName(fleetID),
			State:  herdr.SessionRunningCompatible,
			Reason: "server is compatible",
		},
	}
	original := newFleetHerdrCommand
	newFleetHerdrCommand = func(gotFleetID string) fleetHerdrCommand {
		if gotFleetID != fleetID {
			t.Fatalf("fleet ID = %q, want %q", gotFleetID, fleetID)
		}
		return fake
	}
	t.Cleanup(func() { newFleetHerdrCommand = original })

	var out bytes.Buffer
	root := newRootCmd(devBuild("test"))
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"fleet", "herdr", "status"})
	before := snapshotFleetTree(t, home)
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	if after := snapshotFleetTree(t, home); !slices.Equal(after, before) {
		t.Fatalf("status mutated Fleet home:\nbefore: %v\nafter: %v", before, after)
	}

	for _, want := range []string{
		"fleet_id: " + fleetID + "\n",
		"herdr_session: " + herdr.SessionName(fleetID) + "\n",
		"state: running-compatible\n",
		"reason: server is compatible\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status output = %q, want %q", out.String(), want)
		}
	}
	if fake.observeCalls != 1 || fake.ensureCalls != 0 || fake.openCalls != 0 {
		t.Fatalf("calls = observe %d, ensure %d, open %d; want observe only", fake.observeCalls, fake.ensureCalls, fake.openCalls)
	}
}

func TestFleetHerdrCommandOpensCurrentFleetNamespace(t *testing.T) {
	home := setupSessionHome(t)
	fleetID, err := state.FleetIDReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeFleetHerdrCommand{session: herdr.SessionName(fleetID)}
	original := newFleetHerdrCommand
	newFleetHerdrCommand = func(gotFleetID string) fleetHerdrCommand {
		if gotFleetID != fleetID {
			t.Fatalf("fleet ID = %q, want %q", gotFleetID, fleetID)
		}
		return fake
	}
	t.Cleanup(func() { newFleetHerdrCommand = original })

	var out bytes.Buffer
	root := newRootCmd(devBuild("test"))
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"fleet", "herdr"})
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}

	if fake.openCalls != 1 || fake.ensureCalls != 0 || fake.observeCalls != 0 {
		t.Fatalf("calls = observe %d, ensure %d, open %d; want Open only", fake.observeCalls, fake.ensureCalls, fake.openCalls)
	}
	for _, want := range []string{
		"fleet_id: " + fleetID + "\n",
		"herdr_session: " + herdr.SessionName(fleetID) + "\n",
		"opened: true\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("open output = %q, want %q", out.String(), want)
		}
	}
}
