package orientation

import (
	"context"
	"strings"
	"testing"
)

func TestProviderKeepsExactTargetTokenStableAndChangesActiveGeneration(t *testing.T) {
	provider := NewProvider(func(context.Context) (Evidence, error) {
		return Evidence{
			FleetID: "f_one",
			Targets: []TargetEvidence{{ID: "task-1", Kind: "task", Generation: []string{"attempt-1", "running"}}},
		}, nil
	})

	first, err := provider.Orientation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Orientation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Monitors[0].Currentness != second.Monitors[0].Currentness {
		t.Fatalf("unchanged token = %q and %q, want stable", first.Monitors[0].Currentness, second.Monitors[0].Currentness)
	}

	provider = NewProvider(func(context.Context) (Evidence, error) {
		return Evidence{
			FleetID: "f_one",
			Targets: []TargetEvidence{{ID: "task-1", Kind: "task", Generation: []string{"attempt-2", "running"}}},
		}, nil
	})
	changed, err := provider.Orientation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Monitors[0].Currentness == changed.Monitors[0].Currentness {
		t.Fatalf("replaced generation kept token %q", changed.Monitors[0].Currentness)
	}
}

func TestProviderScopesCollidingIDsByFleetAndTargetKind(t *testing.T) {
	makeOrientation := func(fleet string, kind string) SupervisorOrientation {
		provider := NewProvider(func(context.Context) (Evidence, error) {
			return Evidence{FleetID: fleet, Targets: []TargetEvidence{{ID: "same", Kind: kind, Generation: []string{"one"}}}}, nil
		})
		got, err := provider.Orientation(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	a := makeOrientation("f_one", "task")
	b := makeOrientation("f_two", "task")
	c := makeOrientation("f_one", "fleet")
	if a.Monitors[0].ID == b.Monitors[0].ID || a.Monitors[0].Currentness == b.Monitors[0].Currentness {
		t.Fatal("colliding task IDs across Fleets share monitor identity or currentness")
	}
	if a.Monitors[0].ID == c.Monitors[0].ID || a.Monitors[0].Currentness == c.Monitors[0].Currentness {
		t.Fatal("different target kinds share monitor identity or currentness")
	}
}

func TestProviderKeepsCurrentnessAcrossAHomeMoveWithTheSameFleetIdentity(t *testing.T) {
	makeOrientation := func(home string) SupervisorOrientation {
		provider := NewProvider(func(context.Context) (Evidence, error) {
			return Evidence{
				FleetID: home,
				Targets: []TargetEvidence{{ID: "task-1", Kind: "task", Generation: []string{"attempt-1", "running"}}},
			}, nil
		})
		got, err := provider.Orientation(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	first := makeOrientation("f_same")
	moved := makeOrientation("f_same")
	if first.Monitors[0] != moved.Monitors[0] {
		t.Fatalf("moved-home target = %#v, want unchanged Fleet-scoped target", moved.Monitors[0])
	}
}

func TestTaskTargetDoesNotIncludeAcknowledgementChannel(t *testing.T) {
	facts := TaskTargetFacts{
		ID: "task-1", Kind: "task", CreatedAt: "created", Lifecycle: "open", RuntimeIdentity: []string{"s", "w", "t", "p"},
		ReportState: "done", ReportOffset: 12, ReportDigest: "report", DoneVerified: true,
	}
	first := TaskTarget("f_one", facts)
	facts.ReportState = "done"
	second := TaskTarget("f_one", facts)
	if first != second {
		t.Fatalf("unchanged task target = %#v and %#v, want stable when acknowledgement is unchanged", first, second)
	}
}

func TestProviderSortsAndBoundsOrientationWithExplicitUncertainty(t *testing.T) {
	provider := NewProvider(func(context.Context) (Evidence, error) {
		return Evidence{
			FleetID: "f_one",
			Targets: []TargetEvidence{
				{ID: "z", Kind: "task", Generation: []string{"z"}},
				{ID: "a", Kind: "task", Generation: []string{"a"}},
			},
			Work: []WorkEvidence{
				{ID: "z", Kind: "task", State: "unknown"},
				{ID: "a", Kind: "task", State: "working"},
			},
			Errors:       []BoundedError{{Kind: "registry", Reason: "unavailable"}},
			MonitorState: MonitorStateUnknown,
		}, nil
	})

	got, err := provider.Orientation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != Schema || got.FleetID != "f_one" {
		t.Fatalf("header = %#v, want schema and Fleet ID", got)
	}
	if len(got.Monitors) != 2 || got.Monitors[0].ID >= got.Monitors[1].ID {
		t.Fatalf("monitors = %#v, want deterministic ordering", got.Monitors)
	}
	if got.MonitorState != MonitorStateUnknown || len(got.Errors) != 1 || got.Errors[0].Kind != "registry" {
		t.Fatalf("uncertainty = %#v, want explicit unknown registry state", got)
	}
}

func TestBuildNormalizesUnknownMonitorStateAndBoundsText(t *testing.T) {
	got := Build(Evidence{
		FleetID:      "f_one",
		MonitorState: "invalid",
		Actionable: []ActionableEvidence{{
			TargetID: "task-1", TargetKind: "task", Kind: "blocked", Reason: strings.Repeat("x", 500),
		}},
		Targets: []TargetEvidence{{ID: "task-1", Kind: "task", Generation: []string{"one"}}},
	})
	if got.MonitorState != MonitorStateUnknown {
		t.Fatalf("monitor state = %q, want unknown", got.MonitorState)
	}
	if !got.Truncated || len([]rune(got.Actionable[0].Reason)) != 256 {
		t.Fatalf("bounded actionable = %#v, want 256-rune truncated reason", got.Actionable[0])
	}
}

func TestBuildDeduplicatesTargetsAndSortsAllBoundedCollections(t *testing.T) {
	got := Build(Evidence{
		FleetID: "f_one",
		Targets: []TargetEvidence{
			{ID: "task-1", Kind: "task", Generation: []string{"one"}},
			{ID: "task-1", Kind: "task", Generation: []string{"two"}},
		},
		Work:        []WorkEvidence{{ID: "task-1", Kind: "task"}},
		NextActions: []NextAction{{Kind: "z", Reason: "later"}, {Kind: "a", Reason: "first"}},
		Errors:      []BoundedError{{Kind: "z", Reason: "later"}, {Kind: "a", Reason: "first"}},
	})
	if len(got.Monitors) != 1 || len(got.Errors) != 3 {
		t.Fatalf("orientation = %#v, want one monitor and duplicate/missing-target errors", got)
	}
	if got.NextActions[0].Kind != "a" || got.Errors[0].Kind != "a" {
		t.Fatalf("orientation ordering = %#v, want deterministic ordering", got)
	}
}
