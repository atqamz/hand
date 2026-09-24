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
	"strconv"
	"strings"
	"sync"
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
	if canonicalOutputField(t, created, "registry") != "registered" {
		t.Fatalf("fresh canonical Fleet not registered: %+v", created)
	}
	listed := runHand(t, parent, "fleet")
	if listed.code != 0 || !strings.Contains(listed.stdout, fleetID+",") || !strings.Contains(listed.stdout, ",ready,") {
		t.Fatalf("fresh canonical Fleet not discoverable: %+v", listed)
	}
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
	secondhandHome := filepath.Join(parent, ".secondhand")
	processes := make([]*exec.Cmd, 4)
	for i := range processes {
		processes[i] = exec.Command(handBin, "init", "--canonical", home)
		processes[i].Dir = parent
		processes[i].Env = handProcessEnv("SECONDHAND_HOME=" + secondhandHome)
	}
	if err := startCompetingProcesses(processes); err != nil {
		t.Fatal(err)
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
	if _, err := os.Stat(filepath.Join(secondhandHome, "registry.db")); err != nil {
		t.Fatalf("competing creators did not register in their isolated registry: %v", err)
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

func TestCanonicalInitCompetingProcessStartFailureJoinsEarlierProcess(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "race")
	first := exec.Command(handBin, "init", "--canonical", home)
	first.Dir = parent
	first.Env = handProcessEnv("SECONDHAND_HOME=" + filepath.Join(parent, ".secondhand"))
	t.Cleanup(func() {
		if first.Process != nil && first.ProcessState == nil {
			_ = first.Process.Kill()
			_ = first.Wait()
		}
	})
	missing := exec.Command(filepath.Join(parent, "missing-hand"))
	if err := startCompetingProcesses([]*exec.Cmd{first, missing}); err == nil {
		t.Fatal("missing second process started")
	}
	if first.ProcessState == nil {
		_ = first.Process.Kill()
		_ = first.Wait()
		t.Fatal("earlier process was not joined after later start failed")
	}
}

func startCompetingProcesses(processes []*exec.Cmd) error {
	for i, process := range processes {
		if err := process.Start(); err != nil {
			for _, started := range processes[:i] {
				_ = started.Process.Kill()
				_ = started.Wait()
			}
			return err
		}
	}
	return nil
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
			if strings.HasPrefix(value, `"`) {
				decoded, err := strconv.Unquote(value)
				if err != nil {
					t.Fatal(err)
				}
				return decoded
			}
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

// Policy compare-and-set and Plan creation must retain historical meaning across
// config changes, stale callers, rollback, process restart and competing writers.
func TestCanonicalPolicyPlanCLI(t *testing.T) {
	parent := t.TempDir()
	fleet := filepath.Join(parent, "fleet")
	requireOK := func(args ...string) invocation {
		t.Helper()
		got := runHand(t, fleet, args...)
		if got.code != 0 {
			t.Fatalf("%v: %+v", args, got)
		}
		return got
	}
	if got := runHand(t, parent, "init", "--canonical", fleet); got.code != 0 {
		t.Fatal(got)
	}
	initGitRepo(t, filepath.Join(fleet, "projects", "sample"))
	registered := requireOK("project", "register", "sample")
	project := canonicalOutputField(t, registered, "project_id")
	workspace := canonicalOutputField(t, registered, "workspace_binding_id")
	policyArgs := func(id, previous, profile string) []string {
		return []string{"project", "policy", id, "--project-id", project, "--supersedes", previous,
			"--worker-profile-ref", profile, "--qualification-policy-ref", "review-v1", "--integration-policy-ref", "",
			"--production-policy-ref", "", "--publication-policy-ref", ""}
	}
	planArgs := func(id, task, intent, judgment, policy string) []string {
		return []string{"plan", "create", id, "--task-id", task, "--workspace-binding-id", workspace,
			"--policy-revision-id", policy, "--intent", intent, "--judgment", judgment,
			"--basis", "exact registered repository", "--brief", "Preserve this Plan's meaning"}
	}
	requireOK(policyArgs("policy_1", "", "worker-v1")...)
	for _, intent := range []string{"explore", "execute"} {
		for _, judgment := range []string{"mechanical", "bounded", "substantial"} {
			id := intent + "_" + judgment
			requireOK("task", "create", "task_"+id, "--project-id", project, "--goal", "Goal "+id)
			requireOK(planArgs("plan_"+id, "task_"+id, intent, judgment, "policy_1")...)
		}
	}
	requireOK(policyArgs("policy_2", "policy_1", "worker-v2")...)
	db := canonicalTestDB(t, fleet)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan WHERE policy_revision_id='policy_1' AND lifecycle='active'`).Scan(&count); err != nil || count != 6 {
		t.Fatalf("policy edit rewrote existing Plans: %d %v", count, err)
	}
	var digest, profile, superseded string
	if err := db.QueryRow(`SELECT policy_digest,worker_profile_ref,superseded_at FROM policy_revision WHERE id='policy_1'`).Scan(&digest, &profile, &superseded); err != nil || len(digest) != 64 || profile != "worker-v1" || superseded == "" {
		t.Fatalf("policy history lost: %q %q %q %v", digest, profile, superseded, err)
	}
	_ = db.Close()
	before := snapshotTree(t, fleet)
	for _, args := range [][]string{
		policyArgs("policy_3", "policy_1", "worker-v3"), // stale predecessor
		policyArgs("policy_2", "policy_2", "worker-v3"), // insert failure must roll back supersession
		policyArgs("policy_3", "policy_2", "invalid\nreference"),
		{"project", "policy", "policy_3", "--project-id", project}, // missing explicit declarations
		planArgs("plan_stale", "task_explore_mechanical", "explore", "mechanical", "policy_1"),
		planArgs("plan_alias", "task_explore_mechanical", "scout", "mechanical", "policy_2"),
		planArgs("plan_duplicate", "task_explore_mechanical", "explore", "mechanical", "policy_2"),
	} {
		if got := runHand(t, fleet, args...); got.code == 0 {
			t.Fatalf("unsafe command accepted: %v %+v", args, got)
		}
		assertTreeUnchanged(t, fleet, before)
	}
	for _, args := range [][]string{policyArgs("worker_policy", "policy_2", "worker-v2"), planArgs("worker_plan", "task_explore_mechanical", "explore", "mechanical", "policy_2")} {
		if got := runHandEnv(t, fleet, []string{"HAND_ROLE=worker"}, args...); got.code != 3 {
			t.Fatalf("worker mutation was not refused: %+v", got)
		}
		assertTreeUnchanged(t, fleet, before)
	}
	args := planArgs("plan_successor", "task_explore_mechanical", "execute", "bounded", "policy_2")
	args[1] = "replan"
	args = append(args, "--predecessor", "plan_explore_mechanical")
	requireOK(args...)
	before = snapshotTree(t, fleet)
	args[2] = "plan_late"
	if got := runHand(t, fleet, args...); got.code == 0 {
		t.Fatalf("stale replan retargeted successor: %+v", got)
	}
	assertTreeUnchanged(t, fleet, before)

	// Two actual CLI processes race for the same predecessor; only one may win.
	results := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			cmd := exec.Command(handBin, policyArgs(fmt.Sprintf("policy_race_%d", i), "policy_2", "worker-race")...)
			cmd.Dir, cmd.Env = fleet, handProcessEnv("SECONDHAND_HOME="+filepath.Join(fleet, ".secondhand"))
			results[i] = cmd.Run()
		})
	}
	wg.Wait()
	if (results[0] == nil) == (results[1] == nil) {
		t.Fatalf("policy competitors did not have exactly one winner: %v", results)
	}
	db = canonicalTestDB(t, fleet)
	if err := db.QueryRow(`SELECT COUNT(*) FROM policy_revision`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("policy race history: %d %v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan WHERE id='plan_explore_mechanical' AND lifecycle='superseded'
		AND intent='explore' AND judgment='mechanical' AND policy_revision_id='policy_1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("replan lost predecessor meaning: %d %v", count, err)
	}
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM attempt)+(SELECT COUNT(*) FROM external_operation)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("policy/Plan commands invented execution: %d %v", count, err)
	}
	_ = db.Close()
	currentPolicy := "policy_race_0"
	if results[1] == nil {
		currentPolicy = "policy_race_1"
	}
	// Same path and commit in a different physical repository is not the binding.
	requireOK("task", "create", "task_replaced", "--project-id", project, "--goal", "Reject physical alias")
	repo := filepath.Join(fleet, "projects", "sample")
	for i, replaced := range []string{filepath.Join(repo, ".git"), repo} {
		backup := filepath.Join(parent, fmt.Sprintf("original-%d", i))
		if err := os.Rename(replaced, backup); err != nil {
			t.Fatal(err)
		}
		if err := os.CopyFS(replaced, os.DirFS(backup)); err != nil {
			t.Fatal(err)
		}
		before = snapshotTree(t, fleet)
		if got := runHand(t, fleet, planArgs("plan_replaced", "task_replaced", "explore", "bounded", currentPolicy)...); got.code == 0 {
			t.Fatalf("Plan accepted a physical repository replacement: %+v", got)
		}
		assertTreeUnchanged(t, fleet, before)
	}
}
