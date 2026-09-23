//go:build e2e

package e2e

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/store"
)

// Fresh canonical commands must compose without legacy migration or invented
// policy/execution history; repeating init must retain the exact Fleet identity.
func TestCanonicalInitProjectTaskCLI(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "fresh")
	created := runHand(t, parent, "init", "--canonical", home)
	if created.code != 0 {
		t.Fatalf("canonical init: %+v", created)
	}
	fleetID := canonicalOutputField(t, created, "fleet_id")
	before := snapshotTree(t, home)
	again := runHand(t, parent, "init", "--canonical", home)
	if again.code != 0 || canonicalOutputField(t, again, "fleet_id") != fleetID {
		t.Fatalf("repeated init: %+v", again)
	}
	assertTreeUnchanged(t, home, before)
	initGitRepo(t, filepath.Join(home, "projects", "sample"))
	registered := runHand(t, home, "project", "register", "sample")
	if registered.code != 0 {
		t.Fatalf("canonical register: %+v", registered)
	}
	projectID := canonicalOutputField(t, registered, "project_id")
	goal := "Preserve exact goal meaning"
	created = runHand(t, home, "task", "create", "t_goal", "--project-id", projectID, "--goal", goal)
	if created.code != 0 {
		t.Fatalf("canonical Task: %+v", created)
	}
	taskID := canonicalOutputField(t, created, "task_id")
	db := canonicalTestDB(t, home)
	var storedGoal, digest, storedProject, lifecycle string
	if err := db.QueryRow(`SELECT goal,goal_digest,project_id,lifecycle FROM task WHERE id=?`, taskID).Scan(&storedGoal, &digest, &storedProject, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if storedGoal != goal || digest != fmt.Sprintf("%x", sha256.Sum256([]byte(goal))) || storedProject != projectID || lifecycle != "active" {
		t.Fatalf("Task lost immutable meaning: %q %q %q %q", storedGoal, digest, storedProject, lifecycle)
	}
	var invented int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM policy_revision)+(SELECT COUNT(*) FROM plan)+
		(SELECT COUNT(*) FROM attempt)+(SELECT COUNT(*) FROM external_operation)+(SELECT COUNT(*) FROM legacy_import)`).Scan(&invented); err != nil || invented != 0 {
		t.Fatalf("invented policy/execution/import history = %d, %v", invented, err)
	}
	_ = db.Close()
	before = snapshotTree(t, home)
	replayed := runHand(t, home, "project", "register", "sample")
	if replayed.code != 0 || replayed.stdout != registered.stdout {
		t.Fatalf("exact registration replay: %+v", replayed)
	}
	assertTreeUnchanged(t, home, before)
	for _, args := range [][]string{
		{"task", "create", "t_goal", "--project-id", projectID, "--goal", goal},
		{"task", "create", "t_missing", "--project-id", "missing", "--goal", goal},
		{"project", "list"},
		{"init", home},
	} {
		got := runHand(t, home, args...)
		if got.code == 0 {
			t.Fatalf("unsafe command succeeded: %v %+v", args, got)
		}
		assertTreeUnchanged(t, home, before)
	}
	got := runHandEnv(t, home, []string{"HAND_ROLE=worker"}, "task", "create", "t_worker", "--project-id", projectID, "--goal", goal)
	if got.code != 3 {
		t.Fatalf("worker Task creation: %+v", got)
	}
	assertTreeUnchanged(t, home, before)
}

func TestCanonicalInitRefusesExistingTargetsWithoutMutation(t *testing.T) {
	for _, kind := range []string{"empty", "legacy", "partial"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			if kind == "legacy" {
				createCutoverLegacyFixture(t, home)
			}
			if kind == "partial" {
				if err := os.Mkdir(filepath.Join(home, "state"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(home, "state", ".canonical-init.db"), []byte("preserved interrupted bootstrap"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			seedPrivateRuntime(t, home)
			before := snapshotTree(t, home)
			got := runHand(t, home, "init", "--canonical", home)
			if got.code == 0 {
				t.Fatalf("adopted %s target: %+v", kind, got)
			}
			assertTreeUnchanged(t, home, before)
		})
	}
}

// Independent creators must never replace the active DB or split Fleet identity.
func TestCanonicalInitCompetingProcesses(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "race")
	processes := make([]*exec.Cmd, 4)
	for i := range processes {
		processes[i] = exec.Command(handBin, "init", "--canonical", home)
		processes[i].Dir = parent
		processes[i].Env = handProcessEnv()
		if err := processes[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	successes := 0
	for _, process := range processes {
		if process.Wait() == nil {
			successes++
		}
	}
	if successes == 0 {
		t.Fatal("no creator succeeded")
	}
	first, err := store.FleetIDReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, home)
	got := runHand(t, parent, "init", "--canonical", home)
	if got.code != 0 || canonicalOutputField(t, got, "fleet_id") != first {
		t.Fatalf("identity did not converge: %+v", got)
	}
	assertTreeUnchanged(t, home, before)
}

func TestCanonicalProjectRefusesRedirectedAndInvalidRepositories(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "fresh")
	if got := runHand(t, parent, "init", "--canonical", home); got.code != 0 {
		t.Fatalf("init: %+v", got)
	}
	initGitRepo(t, filepath.Join(home, "projects", "sample"))
	seedPrivateRuntime(t, home)
	for _, name := range []string{"../sample", `..\sample`, "missing", "."} {
		before := snapshotTree(t, home)
		if got := runHand(t, home, "project", "register", name); got.code == 0 {
			t.Fatalf("accepted invalid repository %q: %+v", name, got)
		}
		assertTreeUnchanged(t, home, before)
	}
	t.Run("redirected-projects", func(t *testing.T) {
		projects := filepath.Join(home, "projects")
		moved := filepath.Join(parent, "moved-projects")
		if err := os.Rename(projects, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, projects); err != nil {
			t.Skipf("native symlink creation unavailable: %v", err)
		}
		before := snapshotTree(t, home)
		if got := runHand(t, home, "project", "register", "sample"); got.code == 0 {
			t.Fatalf("accepted redirected projects: %+v", got)
		}
		assertTreeUnchanged(t, home, before)
	})
}

func canonicalOutputField(t *testing.T, result invocation, field string) string {
	t.Helper()
	for _, line := range strings.Split(result.stdout, "\n") {
		if value, found := strings.CutPrefix(line, field+": "); found {
			return value
		}
	}
	t.Fatalf("missing %s: %+v", field, result)
	return ""
}

func canonicalTestDB(t *testing.T, home string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: store.Path(home)}).EscapedPath()+"?mode=ro&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
