package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/store"
	"github.com/atqamz/hand/internal/worktree"
)

// #348 revision 4 "Drift gate": pass, retryable observation-unknown, and abort-only drift stay separate.
func TestEvaluateLegacyV18CutoverDrift(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(t *testing.T, home string, deps *legacyV18CutoverDriftDeps)
		want  legacyV18CutoverDriftVerdict
		codes []string
	}{
		{name: "unchanged", want: legacyV18CutoverDriftPass},
		{name: "changed HEAD", want: legacyV18CutoverDriftFound, codes: []string{"project-head-changed"}, edit: func(_ *testing.T, _ string, deps *legacyV18CutoverDriftDeps) {
			deps.headCommit = func(string) (string, error) { return strings.Repeat("b", 40), nil }
		}},
		{name: "HEAD unreadable", want: legacyV18CutoverDriftUnknown, codes: []string{"project-head-unobservable"}, edit: func(_ *testing.T, _ string, deps *legacyV18CutoverDriftDeps) {
			deps.headCommit = func(string) (string, error) { return "", errors.New("git busy") }
		}},
		{name: "different object at the same path", want: legacyV18CutoverDriftFound, codes: []string{"project-identity-changed"}, edit: func(_ *testing.T, _ string, deps *legacyV18CutoverDriftDeps) {
			deps.physicalIdentity = func(path string, _ os.FileInfo) (string, error) { return "reallocated:" + path, nil }
		}},
		{name: "birth time unavailable", want: legacyV18CutoverDriftUnknown, codes: []string{"project-identity-unobservable"}, edit: func(_ *testing.T, _ string, deps *legacyV18CutoverDriftDeps) {
			deps.physicalIdentity = func(string, os.FileInfo) (string, error) { return "", errors.New("birth time unavailable") }
		}},
		{name: "missing clone", want: legacyV18CutoverDriftFound, codes: []string{"project-missing"}, edit: func(t *testing.T, home string, _ *legacyV18CutoverDriftDeps) {
			if err := os.RemoveAll(filepath.Join(home, "projects", "demo")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "new managed path", want: legacyV18CutoverDriftFound, codes: []string{"project-orphan-path"}, edit: func(t *testing.T, home string, _ *legacyV18CutoverDriftDeps) {
			if err := os.MkdirAll(filepath.Join(home, "projects", "added-after-freeze"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "Treehouse unobservable", want: legacyV18CutoverDriftUnknown, codes: []string{"project-revision-unobservable"}, edit: func(_ *testing.T, _ string, deps *legacyV18CutoverDriftDeps) {
			deps.treehouse.headCommit = func(string) (string, error) { return "", errors.New("git busy") }
		}},
		{name: "drift outranks unknown", want: legacyV18CutoverDriftFound, codes: []string{"project-head-changed", "project-revision-unobservable"}, edit: func(_ *testing.T, _ string, deps *legacyV18CutoverDriftDeps) {
			deps.headCommit = func(string) (string, error) { return strings.Repeat("b", 40), nil }
			deps.treehouse.headCommit = func(string) (string, error) { return "", errors.New("git busy") }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, frozen, deps := legacyV18CutoverDriftFixture(t)
			if tc.edit != nil {
				tc.edit(t, home, &deps)
			}
			requireLegacyV18CutoverDrift(t, evaluateLegacyV18CutoverDrift(context.Background(), home, frozen, deps), tc.want, tc.codes...)
		})
	}
}

// #348 revision 4 "Drift gate": after the restart only session and label attribute a Herdr resource to this Fleet.
func TestEvaluateLegacyV18CutoverDriftAttributesHerdrAfterRestart(t *testing.T) {
	for _, tc := range []struct {
		name       string
		session    string
		label      string
		want       legacyV18CutoverDriftVerdict
		wantDetail string
	}{
		{"this Fleet's session", "hand-f_self", "anything", legacyV18CutoverDriftUnknown, "close this Fleet's workspace"},
		{"this Fleet's label", "default", "hand:f_self:demo", legacyV18CutoverDriftUnknown, "close this Fleet's workspace"},
		{"another Fleet's label", "default", "hand:f_other:demo", legacyV18CutoverDriftPass, ""},
		{"legacy label naming a manifest Project", "default", "hand:demo", legacyV18CutoverDriftUnknown, "do not close it"},
		{"legacy label naming another Project", "default", "hand:elsewhere", legacyV18CutoverDriftPass, ""},
		{"unrelated workspace", "default", "notes", legacyV18CutoverDriftPass, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, frozen, deps := legacyV18CutoverDriftFixture(t)
			deps.herdr.observeSession = func(_ context.Context, session string) herdr.SessionObservation {
				if session == tc.session {
					return runningLegacyV18CutoverHerdrSession(context.Background(), session)
				}
				return herdr.SessionObservation{Name: session, State: herdr.SessionStopped}
			}
			deps.herdr.inventoryFor = legacyV18CutoverHerdrInventories(map[string]*fakeLegacyV18CutoverHerdrInventory{
				tc.session: {workspaces: []herdr.Workspace{{WorkspaceID: "w1", Label: tc.label}}},
			})
			result := evaluateLegacyV18CutoverDrift(context.Background(), home, frozen, deps)
			if result.Verdict != tc.want || (tc.wantDetail != "" && !strings.Contains(result.Subjects[0].Detail, tc.wantDetail)) {
				t.Fatalf("Herdr drift = %#v, want %s with %q", result, tc.want, tc.wantDetail)
			}
		})
	}
	home, frozen, deps := legacyV18CutoverDriftFixture(t)
	deps.herdr.observeSession = func(_ context.Context, session string) herdr.SessionObservation {
		return herdr.SessionObservation{Name: session, State: herdr.SessionUnknown, Reason: "socket unreadable"}
	}
	requireLegacyV18CutoverDrift(t, evaluateLegacyV18CutoverDrift(context.Background(), home, frozen, deps), legacyV18CutoverDriftUnknown, "herdr-session-unobservable")
}

func legacyV18CutoverDriftFixture(t *testing.T) (string, store.LegacyV18CutoverFrozenEvidence, legacyV18CutoverDriftDeps) {
	t.Helper()
	home, clone, plan, treehouse := legacyV18CutoverProjectTreehouseFixture(t)
	_, herdrDeps := legacyV18CutoverHerdrFixture()
	frozen := store.LegacyV18CutoverFrozenEvidence{
		Plan: plan,
		Projects: []store.LegacyV18CutoverManifestProjectInput{{
			SourceProjectID:      "project-1",
			Locator:              "projects/demo",
			RepositoryPhysicalID: "id:" + clone,
			CommonDirPhysicalID:  "id:" + filepath.Join(clone, ".git"),
			Revision:             strings.Repeat("a", 40),
			LegacyName:           "demo",
		}},
	}
	deps := legacyV18CutoverDriftDeps{
		headCommit:       func(string) (string, error) { return strings.Repeat("a", 40), nil },
		physicalIdentity: func(path string, _ os.FileInfo) (string, error) { return "id:" + path, nil },
		treehouse:        treehouse,
		herdr:            herdrDeps,
	}
	return home, frozen, deps
}

func requireLegacyV18CutoverDrift(t *testing.T, result legacyV18CutoverDriftResult, want legacyV18CutoverDriftVerdict, codes ...string) {
	t.Helper()
	if result.Verdict != want {
		t.Fatalf("drift verdict = %s (%#v), want %s", result.Verdict, result.Subjects, want)
	}
	for _, code := range codes {
		found := false
		for _, subject := range result.Subjects {
			found = found || subject.Code == code
		}
		if !found {
			t.Fatalf("drift subjects %#v lack %q", result.Subjects, code)
		}
	}
}

// #348 revision 4 C4-13: only a Treehouse state proven to have changed for this Fleet is drift; the unclassifiable is retryable.
func TestEvaluateLegacyV18CutoverDriftClassifiesTreehouseBlockers(t *testing.T) {
	recordedLease := store.LegacyV18CutoverWorktreeObservation{AttemptID: 7, ProjectID: "project-1", LeaseID: "lease-7"}
	for _, tc := range []struct {
		name     string
		pool     []worktree.PoolEntry
		recorded bool
		lease    func(path, expected string) worktree.LeaseObservation
		slots    bool
		want     legacyV18CutoverDriftVerdict
		code     string
	}{
		{name: "this Fleet's lease", pool: []worktree.PoolEntry{{Path: "pool/slot", Status: "leased", LeaseID: "l1", LeaseHolder: "hand:f_self:t1"}}, want: legacyV18CutoverDriftFound, code: "treehouse-live-or-unknown-lease"},
		{name: "unparsable holder", pool: []worktree.PoolEntry{{Path: "pool/slot", Status: "leased", LeaseID: "l1", LeaseHolder: "someone"}}, want: legacyV18CutoverDriftUnknown, code: "treehouse-live-or-unknown-lease"},
		{name: "lease without identity", pool: []worktree.PoolEntry{{Path: "pool/slot", Status: "leased", LeaseHolder: "hand:f_self:t1"}}, want: legacyV18CutoverDriftUnknown, code: "treehouse-live-or-unknown-lease"},
		{name: "unknown pool status", pool: []worktree.PoolEntry{{Path: "pool/slot", Status: "rebuilding"}}, want: legacyV18CutoverDriftUnknown, code: "treehouse-pool-state-unknown"},
		{name: "recorded lease unprovable", recorded: true, lease: func(string, string) worktree.LeaseObservation {
			return worktree.LeaseObservation{State: worktree.LeaseUnprovable}
		}, want: legacyV18CutoverDriftUnknown, code: "treehouse-recorded-lease-unresolved"},
		{name: "recorded slot taken by this Fleet", recorded: true, pool: []worktree.PoolEntry{{Path: "pool/slot", Status: "leased", LeaseID: "l2", LeaseHolder: "hand:f_self:t2"}}, lease: func(string, string) worktree.LeaseObservation {
			return worktree.LeaseObservation{State: worktree.LeaseMismatch, LeaseID: "l2"}
		}, want: legacyV18CutoverDriftFound, code: "treehouse-recorded-lease-unresolved"},
		{name: "recorded lease live", recorded: true, lease: func(string, string) worktree.LeaseObservation {
			return worktree.LeaseObservation{State: worktree.LeaseExact}
		}, want: legacyV18CutoverDriftFound, code: "treehouse-recorded-lease-live"},
		{name: "orphan slot with unknown lease", slots: true, lease: func(string, string) worktree.LeaseObservation {
			return worktree.LeaseObservation{State: worktree.LeaseUnknown}
		}, want: legacyV18CutoverDriftUnknown, code: "treehouse-orphan-slot-unresolved"},
		{name: "orphan slot with a known lease", slots: true, lease: func(string, string) worktree.LeaseObservation {
			return worktree.LeaseObservation{State: worktree.LeaseExact, LeaseID: "l3"}
		}, want: legacyV18CutoverDriftFound, code: "treehouse-orphan-slot-unresolved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, frozen, deps := legacyV18CutoverDriftFixture(t)
			slot := filepath.Join(home, "pool", "slot")
			for i := range tc.pool {
				tc.pool[i].Path = slot
			}
			deps.treehouse.poolStatus = func(string) ([]worktree.PoolEntry, error) { return tc.pool, nil }
			if tc.recorded {
				recorded := recordedLease
				recorded.WorktreePath = slot
				frozen.Plan.Worktrees = []store.LegacyV18CutoverWorktreeObservation{recorded}
			}
			if tc.lease != nil {
				deps.treehouse.observeLease = func(_, path, expected string) worktree.LeaseObservation { return tc.lease(path, expected) }
			}
			if tc.slots {
				deps.treehouse.discoverPoolSlots = func(string, ...string) ([]worktree.PoolSlot, error) {
					return []worktree.PoolSlot{{Path: slot}}, nil
				}
			}
			requireLegacyV18CutoverDrift(t, evaluateLegacyV18CutoverDrift(context.Background(), home, frozen, deps), tc.want, tc.code)
		})
	}
}
