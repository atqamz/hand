package runtime

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/routing"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/store"
)

func TestSpawnClassifiedRoutingFailuresPrecedeLifecycleSideEffects(t *testing.T) {
	planned := strings.Repeat("a", 40)
	for _, test := range []struct {
		name      string
		brief     string
		configure func(t *testing.T, home string)
		request   SpawnRequest
		base      string
		want      string
	}{
		{
			name:  "missing route",
			brief: "---\nexecution_class: standard\n---\nbrief\n",
			configure: func(t *testing.T, home string) {
				t.Helper()
				if err := routing.RemoveRoute(home, routing.TaskKindShip, routing.ExecutionClassStandard); err != nil {
					t.Fatal(err)
				}
			},
			want: "route ship.standard is not configured",
		},
		{
			name:  "dangling route",
			brief: "---\nexecution_class: standard\n---\nbrief\n",
			configure: func(t *testing.T, home string) {
				t.Helper()
				writeRoutingFile(t, home, "routes/ship.standard", "missing")
			},
			want: "profile \"missing\" is not configured",
		},
		{
			name:  "harness absent from PATH",
			brief: "---\nexecution_class: standard\n---\nbrief\n",
			configure: func(t *testing.T, home string) {
				t.Helper()
				configureRoute(t, home, state.KindShip, routing.ExecutionClassStandard, routing.Profile{Name: "daily", Harness: "claude"})
				t.Setenv("PATH", t.TempDir())
			},
			want: "harness \"claude\" is not installed on PATH",
		},
		{
			name:  "model incompatible with selected harness",
			brief: "---\nexecution_class: standard\n---\nbrief\n",
			configure: func(t *testing.T, home string) {
				t.Helper()
				configureRoute(t, home, state.KindShip, routing.ExecutionClassStandard, routing.Profile{Name: "daily", Harness: "grok"})
				addHarnessToPath(t, "grok")
			},
			request: SpawnRequest{Model: "opaque", ModelFromFlag: true},
			want:    "harness \"grok\" takes no model",
		},
		{
			name:  "mechanical prompt incompatible",
			brief: "---\nexecution_class: mechanical\nplanned_against: " + planned + "\n---\nbrief\n",
			configure: func(t *testing.T, home string) {
				t.Helper()
				configureRoute(t, home, state.KindShip, routing.ExecutionClassMechanical, routing.Profile{Name: "daily", Harness: "grok"})
				addHarnessToPath(t, "grok")
			},
			want: "cannot carry the required mechanical worker guidance",
		},
		{
			name:  "stale mechanical plan",
			brief: "---\nexecution_class: mechanical\nplanned_against: " + planned + "\n---\nbrief\n",
			configure: func(t *testing.T, home string) {
				t.Helper()
				configureRoute(t, home, state.KindShip, routing.ExecutionClassMechanical, routing.Profile{Name: "daily", Harness: "claude"})
				addHarnessToPath(t, "claude")
			},
			base: strings.Repeat("b", 40),
			want: "mechanical plan is stale",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := executionPlanHome(t, test.brief)
			if test.configure != nil {
				test.configure(t, home)
			}
			calls := &executionPlanCalls{}
			base := test.base
			if base == "" {
				base = planned
			}
			r := executionPlanRuntime(t, calls, func(string) (string, error) { return base, nil })

			req := test.request
			req.Home = home
			req.ID = "task-1"
			req.Project = "demo"
			req.Kind = state.KindShip
			_, err := r.Spawn(context.Background(), req)
			assertPreconditionError(t, err)
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Spawn() error = %q, want %q", err, test.want)
			}
			assertNoProvisioningSideEffects(t, home, calls)
			if _, err := state.ReadHistory(home, "task-1"); !errors.Is(err, state.ErrTaskNotFound) {
				t.Fatalf("history after refused Spawn = %v, want no Task/Attempt", err)
			}
		})
	}
}

func TestSpawnClassifiedRoutePersistsImmutableExecutionSnapshot(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: deep\n---\nbrief\n")
	configureRoute(t, home, state.KindShip, routing.ExecutionClassDeep, routing.Profile{Name: "brain", Harness: "claude", Model: "profile-model", Effort: "high"})
	calls := &executionPlanCalls{}
	r := executionPlanRuntime(t, calls, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	result, err := r.Spawn(context.Background(), SpawnRequest{Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip})
	if err != nil {
		t.Fatal(err)
	}
	if result.Harness != "claude" {
		t.Fatalf("result harness = %q, want claude", result.Harness)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got := *history.ActiveAttempt
	if got.Harness != "claude" || got.Model != "profile-model" || got.Effort != "high" || got.ExecutionClass != "deep" || got.RequestedProfile != "brain" || got.RoutingSource != string(routing.RoutingSourceRoute) {
		t.Fatalf("attempt = %+v, want routed execution snapshot", got)
	}

	configureRoute(t, home, state.KindShip, routing.ExecutionClassDeep, routing.Profile{Name: "daily", Harness: "codex", Model: "new-model", Effort: "low"})
	if err := routing.WriteRoute(home, routing.Route{Kind: routing.TaskKindShip, ExecutionClass: routing.ExecutionClassDeep, Profile: "daily"}); err != nil {
		t.Fatal(err)
	}
	history, err = state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	got = *history.ActiveAttempt
	if got.Harness != "claude" || got.Model != "profile-model" || got.Effort != "high" || got.RequestedProfile != "brain" {
		t.Fatalf("attempt after config change = %+v, want original snapshot", got)
	}
}

func TestClassifiedRouteDoesNotRequireSupervisorHarnessDetection(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: standard\n---\nbrief\n")
	t.Setenv("HAND_HARNESS", "stale-unsupported-name")
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	result, err := r.Spawn(context.Background(), SpawnRequest{Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip})
	if err != nil {
		t.Fatalf("Spawn() = %v, want the routed Profile to resolve without supervisor detection", err)
	}
	if result.Harness != harness.Claude || result.ExecutionClass != string(routing.ExecutionClassStandard) {
		t.Fatalf("result = %+v, want the routed Profile execution", result)
	}
}

func TestSpawnProfiledBriefOverridesEmitCompatibilityWarning(t *testing.T) {
	home := executionPlanHome(t, "---\nmodel: brief-model\neffort: low\nexecution_class: standard\n---\nbrief\n")
	configureRoute(t, home, state.KindShip, routing.ExecutionClassStandard, routing.Profile{Name: "daily", Harness: "claude", Model: "profile-model", Effort: "high"})
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	result, err := r.Spawn(context.Background(), SpawnRequest{Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "brief model and effort override selected profile") {
		t.Fatalf("warnings = %v, want one profile compatibility warning", result.Warnings)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := *history.ActiveAttempt; got.Model != "brief-model" || got.Effort != "low" {
		t.Fatalf("attempt = %+v, want brief overrides", got)
	}
}

func TestSpawnExplicitModelAndEffortOverrideProfile(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: standard\n---\nbrief\n")
	configureRoute(t, home, state.KindShip, routing.ExecutionClassStandard, routing.Profile{Name: "daily", Harness: "claude", Model: "profile-model", Effort: "high"})
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	_, err := r.Spawn(context.Background(), SpawnRequest{
		Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip,
		Model: "command-model", ModelFromFlag: true, Effort: "low", EffortFromFlag: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := *history.ActiveAttempt; got.Model != "command-model" || got.Effort != "low" {
		t.Fatalf("attempt = %+v, want explicit overrides", got)
	}
}

func TestSpawnModelAndEffortWithoutProvenanceDoNotOverrideProfile(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: standard\n---\nbrief\n")
	configureRoute(t, home, state.KindShip, routing.ExecutionClassStandard, routing.Profile{Name: "daily", Harness: "claude", Model: "profile-model", Effort: "high"})
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	_, err := r.Spawn(context.Background(), SpawnRequest{
		Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip, Model: "non-explicit", Effort: "low",
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := *history.ActiveAttempt; got.Model != "profile-model" || got.Effort != "high" {
		t.Fatalf("attempt = %+v, want Profile values without explicit provenance", got)
	}
}

func TestSpawnUnclassifiedBriefRetainsLegacyWarnAndLaunch(t *testing.T) {
	home := executionPlanHome(t, "---\nmodel: opaque\neffort: high\n---\nbrief\n")
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	result, err := r.Spawn(context.Background(), SpawnRequest{
		Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip,
		Harness: "grok", HarnessFromFlag: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "launching anyway") {
		t.Fatalf("warnings = %v, want legacy launch warning", result.Warnings)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := *history.ActiveAttempt; got.Harness != "grok" || got.Model != "opaque" || got.Effort != "high" || got.ExecutionClass != "" || got.RequestedProfile != "" || got.RoutingSource != string(routing.RoutingSourceLegacy) {
		t.Fatalf("attempt = %+v, want legacy snapshot", got)
	}
}

func TestSpawnUnclassifiedBriefDoesNotLoadProfileRoutes(t *testing.T) {
	home := executionPlanHome(t, "brief\n")
	t.Setenv("HAND_HARNESS", harness.Claude)
	profiles := filepath.Join(home, "config", "profiles")
	if err := os.RemoveAll(profiles); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profiles, []byte("broken profile directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	result, err := r.Spawn(context.Background(), SpawnRequest{Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip, Harness: "claude"})
	if err != nil {
		t.Fatalf("Spawn() = %v, want legacy launch without reading Profile routes", err)
	}
	if result.Harness != "claude" {
		t.Fatalf("result harness = %q, want claude", result.Harness)
	}
}

func TestSpawnUnclassifiedExplicitProfilePersistsSnapshotWithoutRoute(t *testing.T) {
	home := executionPlanHome(t, "brief\n")
	for _, kind := range routing.TaskKinds() {
		for _, class := range routing.ExecutionClasses() {
			if err := routing.RemoveRoute(home, kind, class); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := routing.WriteProfile(home, routing.Profile{Name: "direct", Harness: "claude", Model: "direct-model", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	_, err := r.Spawn(context.Background(), SpawnRequest{
		Home: home, ID: "task-1", Project: "demo", Kind: state.KindShip, Profile: "direct", ProfileFromFlag: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := *history.ActiveAttempt; got.Harness != "claude" || got.Model != "direct-model" || got.Effort != "high" || got.ExecutionClass != "" || got.RequestedProfile != "direct" || got.RoutingSource != string(routing.RoutingSourceExplicitProfile) {
		t.Fatalf("attempt = %+v, want explicit Profile snapshot", got)
	}
}

func TestSpawnUnclassifiedExplicitProfileRejectsBeforeLifecycleSideEffects(t *testing.T) {
	for _, test := range []struct {
		name    string
		profile routing.Profile
		request SpawnRequest
		setup   func(t *testing.T)
		want    string
	}{
		{
			name:    "harness absent from PATH",
			profile: routing.Profile{Name: "direct", Harness: "grok"},
			want:    "harness \"grok\" is not installed on PATH",
		},
		{
			name:    "model unsupported by harness",
			profile: routing.Profile{Name: "direct", Harness: "grok"},
			request: SpawnRequest{Model: "opaque", ModelFromFlag: true},
			setup: func(t *testing.T) {
				addHarnessToPath(t, "grok")
			},
			want: "harness \"grok\" takes no model",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := executionPlanHome(t, "brief\n")
			if test.setup != nil {
				test.setup(t)
			}
			if err := routing.WriteProfile(home, test.profile); err != nil {
				t.Fatal(err)
			}
			calls := &executionPlanCalls{}
			r := executionPlanRuntime(t, calls, func(string) (string, error) { return strings.Repeat("a", 40), nil })

			req := test.request
			req.Home = home
			req.ID = "task-1"
			req.Project = "demo"
			req.Kind = state.KindShip
			req.Profile = "direct"
			req.ProfileFromFlag = true
			_, err := r.Spawn(context.Background(), req)
			assertPreconditionError(t, err)
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Spawn() error = %q, want %q", err, test.want)
			}
			assertNoProvisioningSideEffects(t, home, calls)
			if _, err := state.ReadHistory(home, "task-1"); !errors.Is(err, state.ErrTaskNotFound) {
				t.Fatalf("history after refused Spawn = %v, want no Task/Attempt", err)
			}
		})
	}
}

func TestReopenResolvesCurrentProfileIntoNewAttempt(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: deep\n---\nbrief\n")
	createTerminalExecutionTask(t, home)
	configureRoute(t, home, state.KindShip, routing.ExecutionClassDeep, routing.Profile{Name: "daily", Harness: "claude", Model: "new-model", Effort: "high"})
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	_, err := r.Reopen(context.Background(), ReopenRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(history.Attempts))
	}
	if got := history.Attempts[1]; got.Harness != "claude" || got.Model != "new-model" || got.Effort != "high" || got.ExecutionClass != "deep" || got.RequestedProfile != "daily" || got.RoutingSource != string(routing.RoutingSourceRoute) {
		t.Fatalf("reopened attempt = %+v, want fresh routed snapshot", got)
	}
}

func TestReopenSchemaV10MigratesOnlyAfterValidRouting(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: deep\n---\nbrief\n")
	createTerminalExecutionTask(t, home)
	configureRoute(t, home, state.KindShip, routing.ExecutionClassDeep, routing.Profile{Name: "daily", Harness: "claude", Model: "new-model", Effort: "high"})
	downgradeRuntimeStoreToV10(t, home)
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	if _, err := r.Reopen(context.Background(), ReopenRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Reopen() = %v, want schema v10 upgrade after route validation", err)
	}
	if got, want := runtimeStoreSchemaVersion(t, home), latestRuntimeStoreSchemaVersion(t); got != want {
		t.Fatalf("schema version after valid Reopen = %d, want %d", got, want)
	}
}

func TestPromoteSchemaV10MigratesOnlyAfterValidRouting(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: standard\n---\nbrief\n")
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scout, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, Lifecycle: state.TaskOpen}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/old/worktree",
	})
	if err != nil {
		t.Fatal(err)
	}
	markExecutionAttemptRunning(t, home, scout.ID)
	configureRoute(t, home, state.KindShip, routing.ExecutionClassStandard, routing.Profile{Name: "deliver", Harness: "claude"})
	downgradeRuntimeStoreToV10(t, home)
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	if _, err := r.Promote(context.Background(), PromoteRequest{Home: home, ID: "task-1"}); err != nil {
		t.Fatalf("Promote() = %v, want schema v10 upgrade after route validation", err)
	}
	if got, want := runtimeStoreSchemaVersion(t, home), latestRuntimeStoreSchemaVersion(t); got != want {
		t.Fatalf("schema version after valid Promote = %d, want %d", got, want)
	}
}

func TestSchemaV10InvalidRouteDoesNotMigrateBeforeRefusal(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: deep\n---\nbrief\n")
	createTerminalExecutionTask(t, home)
	if err := routing.RemoveRoute(home, routing.TaskKindShip, routing.ExecutionClassDeep); err != nil {
		t.Fatal(err)
	}
	downgradeRuntimeStoreToV10(t, home)
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	_, err := r.Reopen(context.Background(), ReopenRequest{Home: home, ID: "task-1"})
	assertPreconditionError(t, err)
	if !strings.Contains(err.Error(), "route ship.deep is not configured") {
		t.Fatalf("Reopen() error = %v, want missing route", err)
	}
	if got := runtimeStoreSchemaVersion(t, home); got != 10 {
		t.Fatalf("schema version after invalid Reopen = %d, want unchanged v10", got)
	}
}

func TestPromoteResolvesShipRouteBeforeScoutMutation(t *testing.T) {
	home := executionPlanHome(t, "---\nexecution_class: standard\n---\nbrief\n")
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scout, err := state.CreateTaskWithAttempt(home, state.Task{ID: "task-1", Project: "demo", Kind: state.KindScout, Lifecycle: state.TaskOpen}, state.Attempt{
		TaskID: "task-1", Lifecycle: state.AttemptProvisioning, Harness: "claude", Worktree: "/old/worktree",
		Herdr: state.Herdr{WorkspaceID: "old-ws", TabID: "old-tab", PaneID: "old-pane"},
	})
	if err != nil {
		t.Fatal(err)
	}
	markExecutionAttemptRunning(t, home, scout.ID)
	configureRoute(t, home, state.KindScout, routing.ExecutionClassStandard, routing.Profile{Name: "investigate", Harness: "claude"})
	configureRoute(t, home, state.KindShip, routing.ExecutionClassStandard, routing.Profile{Name: "deliver", Harness: "codex", Model: "ship-model"})
	addHarnessToPath(t, "codex")
	r := executionPlanRuntime(t, &executionPlanCalls{}, func(string) (string, error) { return strings.Repeat("a", 40), nil })

	result, err := r.Promote(context.Background(), PromoteRequest{Home: home, ID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Harness != "codex" {
		t.Fatalf("result harness = %q, want codex ship route", result.Harness)
	}
	history, err := state.ReadHistory(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if history.Task.Kind != state.KindShip || len(history.Attempts) != 2 {
		t.Fatalf("history = %+v, want promoted ship attempt", history)
	}
	if got := history.Attempts[1]; got.Harness != "codex" || got.Model != "ship-model" || got.RequestedProfile != "deliver" || got.RoutingSource != string(routing.RoutingSourceRoute) {
		t.Fatalf("ship attempt = %+v, want ship route snapshot", got)
	}
}

func TestProvisionBuildsFromPersistedAttemptSnapshot(t *testing.T) {
	home, attempt := provisioningFixture(t)
	attempt.Harness = "codex"
	attempt.Model = "snapshot-model"
	attempt.Effort = "high"
	attempt.ExecutionClass = "deep"
	var gotHarness string
	var gotOptions harness.Options
	r := testProvisionRuntime(&provisionHerdr{}, func(lifecyclePhase) error { return nil })
	r.deps.buildHarness = func(name string, options harness.Options) (launchSpec, error) {
		gotHarness = name
		gotOptions = options
		return launchSpec{Executable: "launch"}, nil
	}

	if _, err := r.provision(context.Background(), provisioningRequest{
		home: home, projectName: "demo", clonePath: filepath.Join(home, "projects", "demo"), briefPath: filepath.Join(home, "data", "task-1", "brief.md"), attempt: attempt,
	}); err != nil {
		t.Fatal(err)
	}
	reportPath, err := filepath.Abs(state.ReportPath(home, attempt.TaskID))
	if err != nil {
		t.Fatal(err)
	}
	if gotHarness != "codex" || gotOptions.Model != "snapshot-model" || gotOptions.Effort != "high" || gotOptions.ExecutionClass != "deep" || gotOptions.ReportPath != reportPath {
		t.Fatalf("harness build = %q %+v, want persisted attempt snapshot", gotHarness, gotOptions)
	}
}

func configureRoute(t *testing.T, home, kind string, class routing.ExecutionClass, profile routing.Profile) {
	t.Helper()
	if err := routing.WriteProfile(home, profile); err != nil {
		t.Fatal(err)
	}
	if err := routing.WriteRoute(home, routing.Route{Kind: routing.TaskKind(kind), ExecutionClass: class, Profile: profile.Name}); err != nil {
		t.Fatal(err)
	}
}

func downgradeRuntimeStoreToV10(t *testing.T, home string) {
	t.Helper()
	db, err := sql.Open("sqlite", store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, column := range []string{"execution_class", "planned_against", "requested_profile", "routing_source"} {
		if _, err := db.Exec("ALTER TABLE attempt DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	for _, column := range []string{"repair_code", "repair_reason", "repair_attempt_id", "repair_observed_at"} {
		if _, err := db.Exec("ALTER TABLE task DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = 10"); err != nil {
		t.Fatal(err)
	}
}

// A fresh store always stamps itself at the newest schema version, so reading one back is how a
// test asserts "fully migrated" without hardcoding a version number that drifts with every
// unrelated migration added elsewhere in the package.
func latestRuntimeStoreSchemaVersion(t *testing.T) int {
	t.Helper()
	home := t.TempDir()
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return runtimeStoreSchemaVersion(t, home)
}

func runtimeStoreSchemaVersion(t *testing.T, home string) int {
	t.Helper()
	db, err := sql.Open("sqlite", store.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
}

func writeRoutingFile(t *testing.T, home, name, value string) {
	t.Helper()
	path := filepath.Join(home, "config", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func addHarnessToPath(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
