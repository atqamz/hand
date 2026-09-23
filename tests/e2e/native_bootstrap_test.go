//go:build nativebootstrap && !e2e && !test

package e2e

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/store"
	"github.com/atqamz/hand/internal/toolchain"
)

// This opt-in native test builds an untagged CLI and downloads its exact locked
// runtime. It qualifies the canonical planning/Decision prefix, never worker execution.
func TestNativeCanonicalBootstrap(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "hand")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-tags=", "-o", binary, ".")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build untagged Hand: %v\n%s", err, out)
	}
	bytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("production binary SHA-256: %x", sha256.Sum256(bytes))
	if info, err := exec.Command("go", "version", "-m", binary).CombinedOutput(); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("production build identity:\n%s", info)
	}
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "SECONDHAND_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		path := filepath.Join(root, strings.ToLower(key))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	for _, key := range []string{"HAND_HOME", "HAND_ROLE", "HAND_HARNESS", "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_CONFIG_COUNT"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "absent-git-config"))
	run := func(dir, executable string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		t.Logf("%s %v\n%s", filepath.Base(executable), args, out)
		if err != nil {
			t.Fatalf("native command failed: %v", err)
		}
		return string(out)
	}
	fleet := filepath.Join(root, "fleet")
	initialized := run(root, binary, "init", "--canonical", fleet)
	if again := run(root, binary, "init", "--canonical", fleet); again != initialized {
		t.Fatal("canonical init changed Fleet identity after process restart")
	}
	if nativeField(t, initialized, "registry") != "registered" {
		t.Fatal("native canonical init omitted Fleet discovery")
	}
	listed := run(root, binary, "fleet")
	if !strings.Contains(listed, nativeField(t, initialized, "fleet_id")+",") || !strings.Contains(listed, ",ready,") {
		t.Fatal("native canonical Fleet is not discoverable after process restart")
	}
	managed := run(root, binary, "runtime", "ensure")
	gitPath := nativeField(t, managed, "git")
	managedRuntime, err := toolchain.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(fleet, "projects", "sample")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=Native fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-qm", "fixture"}} {
		spec, err := managedRuntime.Process(gitPath, toolchain.GitArgsWithTemplate(managedRuntime.GitTemplateDir, args)...)
		if err != nil {
			t.Fatal(err)
		}
		spec.Dir = repo
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		out, err := spec.Output(ctx)
		cancel()
		if err != nil {
			t.Fatalf("locked Git fixture setup: %v\n%s", err, out)
		}
	}
	registered := run(fleet, binary, "project", "register", "sample")
	if again := run(fleet, binary, "project", "register", "sample"); again != registered {
		t.Fatal("registration replay changed exact Project/WorkspaceBinding identity")
	}
	projectID := nativeField(t, registered, "project_id")
	run(fleet, binary, "task", "create", "t_native", "--project-id", projectID, "--goal", "native production goal")
	if id, err := store.FleetIDReadOnly(fleet); err != nil || id != nativeField(t, initialized, "fleet_id") {
		t.Fatalf("canonical schema/identity validation: %s, %v", id, err)
	}
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: store.Path(fleet)}).EscapedPath()+"?mode=ro&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var goal, owner string
	if err := db.QueryRow(`SELECT goal,project_id FROM task WHERE id='t_native' AND lifecycle='active'`).Scan(&goal, &owner); err != nil || goal != "native production goal" || owner != projectID {
		t.Fatalf("native Task did not retain exact meaning/owner: %q %q %v", goal, owner, err)
	}
	var effects int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM external_operation)+(SELECT COUNT(*) FROM attempt)+(SELECT COUNT(*) FROM policy_revision)`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("bootstrap invented policy/Attempt/effects: %d, %v", effects, err)
	}
	run(fleet, binary, "project", "policy", "policy_native", "--project-id", projectID,
		"--worker-profile-ref", "", "--qualification-policy-ref", "native-review-v1",
		"--integration-policy-ref", "", "--production-policy-ref", "", "--publication-policy-ref", "")
	planArgs := []string{"plan", "create", "plan_native", "--task-id", "t_native",
		"--workspace-binding-id", nativeField(t, registered, "workspace_binding_id"),
		"--policy-revision-id", "policy_native", "--intent", "explore", "--judgment", "bounded",
		"--basis", "registered native Git revision", "--brief", "native immutable brief"}
	run(fleet, binary, planArgs...)
	questionArgs := []string{"decision", "create", "decision_native", "--task-id", "t_native",
		"--scope", "plan", "--plan-id", "plan_native", "--question", "Choose the bounded approach?",
		"--created-at", "2026-09-23T12:00:00Z"}
	run(fleet, binary, questionArgs...)
	questionArgs[2] = "decision_late"
	run(fleet, binary, questionArgs...)
	holdDigest := fmt.Sprintf("%x", sha256.Sum256([]byte("native fixture deferral evidence")))
	for _, id := range []string{"hold_native", "hold_independent"} {
		args := []string{"task", "hold", "create", id, "--task-id", "t_native", "--kind", "operator",
			"--reason", "Native checkpoint deferral", "--evidence-digest", holdDigest, "--created-at", "2026-09-23T12:00:00Z"}
		if id == "hold_native" {
			args = append(args, "--decision-id", "decision_native")
		}
		run(fleet, binary, args...)
	}
	answerArgs := []string{"decision", "answer", "decision_native", "--answer-id", "answer_native",
		"--answer", "Use the bounded approach", "--operator-ref", "operator:native-fixture",
		"--answered-at", "2026-09-23T12:01:00Z", "--operator-answer"}
	answered := run(fleet, binary, answerArgs...)
	if again := run(fleet, binary, answerArgs...); again != answered {
		t.Fatal("native Answer replay did not converge after process restart")
	}
	shown := run(fleet, binary, "decision", "show", "decision_native")
	if nativeField(t, shown, "state") != "answered" || nativeField(t, shown, "owner_current") != "true" {
		t.Fatal("native Answer is not independently inspectable")
	}
	shown = run(fleet, binary, "task", "hold", "show", "hold_native")
	if nativeField(t, shown, "unresolved") != "true" || nativeField(t, shown, "decision_id") != "decision_native" {
		t.Fatal("native Answer implicitly resolved or retargeted TaskHold")
	}
	run(fleet, binary, "task", "hold", "resolve", "hold_native", "--resolution", "released", "--evidence-digest", holdDigest,
		"--resolved-at", "2026-09-23T12:01:00Z")
	shown = run(fleet, binary, "task", "hold", "show", "hold_native")
	if nativeField(t, shown, "unresolved") != "false" || nativeField(t, shown, "resolution") != "released" {
		t.Fatal("native exact TaskHold resolution not retained after process restart")
	}
	shown = run(fleet, binary, "task", "hold", "show", "hold_independent")
	if nativeField(t, shown, "unresolved") != "true" || nativeField(t, shown, "decision_id") != "none" {
		t.Fatal("native Hold resolution affected independent deferral")
	}
	planArgs[1], planArgs[2] = "replan", "plan_native_successor"
	planArgs = append(planArgs, "--predecessor", "plan_native")
	run(fleet, binary, planArgs...)
	var plans int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan WHERE task_id='t_native' AND policy_revision_id='policy_native'
		AND ((id='plan_native' AND lifecycle='superseded') OR
		(id='plan_native_successor' AND lifecycle='active' AND predecessor_plan_id='plan_native'))`).Scan(&plans); err != nil || plans != 2 {
		t.Fatalf("native Plan/replan lineage: %d %v", plans, err)
	}
	shown = run(fleet, binary, "decision", "show", "decision_native")
	if nativeField(t, shown, "state") != "answered" || nativeField(t, shown, "owner_current") != "false" {
		t.Fatal("native replan lost Answer history or retargeted it")
	}
	answerArgs[2], answerArgs[4] = "decision_late", "answer_late"
	lateCtx, lateCancel := context.WithTimeout(context.Background(), time.Minute)
	late := exec.CommandContext(lateCtx, binary, answerArgs...)
	late.Dir = fleet
	lateOut, lateErr := late.CombinedOutput()
	lateCancel()
	if lateErr == nil || !strings.Contains(string(lateOut), "not current") {
		t.Fatalf("native late Answer did not refuse exact old Plan: %v\n%s", lateErr, lateOut)
	}
	closure := []string{"decision", "close", "decision_late", "--reason", "stale", "--closed-at", "2026-09-23T12:02:00Z",
		"--evidence-digest", fmt.Sprintf("%x", sha256.Sum256([]byte("native replan replaced exact Plan plan_native")))}
	closed := run(fleet, binary, closure...)
	if again := run(fleet, binary, closure...); again != closed {
		t.Fatal("native stale closure replay did not converge")
	}
	shown = run(fleet, binary, "decision", "show", "decision_late")
	if nativeField(t, shown, "state") != "closed" || nativeField(t, shown, "closure_reason") != "stale" || nativeField(t, shown, "owner_current") != "false" {
		t.Fatal("native stale closure lost history or retargeted successor")
	}
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM worker_input)+(SELECT COUNT(*) FROM worker_wake_operation)+
		(SELECT COUNT(*) FROM worker_input_acknowledgement)`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("native Answer invented delivery/ack: %d %v", effects, err)
	}
	var holds, resolutions int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM task_hold),(SELECT COUNT(*) FROM task_hold_resolution)`).Scan(&holds, &resolutions); err != nil || holds != 2 || resolutions != 1 {
		t.Fatalf("native Hold evidence changed implicitly: %d %d %v", holds, resolutions, err)
	}
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM external_operation)+(SELECT COUNT(*) FROM attempt)`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("policy/Plan invented Attempt/effects: %d, %v", effects, err)
	}
	backup := filepath.Join(root, "original-repository")
	if err := os.Rename(repo, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(repo, os.DirFS(backup)); err != nil {
		t.Fatal(err)
	}
	planArgs[2], planArgs[len(planArgs)-1] = "plan_replaced", "plan_native_successor"
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	replaced := exec.CommandContext(ctx, binary, planArgs...)
	replaced.Dir = fleet
	out, err := replaced.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "physical identity") {
		t.Fatalf("native Plan accepted physical replacement: %v\n%s", err, out)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan WHERE lifecycle='active' AND id='plan_native_successor'`).Scan(&plans); err != nil || plans != 1 {
		t.Fatalf("physical replacement refusal changed successor: %d %v", plans, err)
	}
	t.Log("native physical replacement refused; original Plan lineage retained")

	// Both processes use one user registry. Copying a DB must not grant a second
	// writable Fleet even when the copied identity has not yet been registered.
	clone := filepath.Join(root, "copied-fleet")
	if err := os.CopyFS(clone, os.DirFS(fleet)); err != nil {
		t.Fatal(err)
	}
	originalDB, err := os.ReadFile(store.Path(fleet))
	if err != nil {
		t.Fatal(err)
	}
	refuseDuplicate := func(dir string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "task", "create", "task_duplicate", "--project-id", projectID, "--goal", "Must refuse duplicate Fleet")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || !strings.Contains(string(out), "also valid at") {
			t.Fatalf("native duplicate Fleet accepted mutation: %v\n%s", err, out)
		}
		bytes, err := os.ReadFile(store.Path(dir))
		if err != nil || sha256.Sum256(bytes) != sha256.Sum256(originalDB) {
			t.Fatalf("duplicate refusal changed canonical state: %v", err)
		}
	}
	refuseDuplicate(clone)
	if copied := run(root, binary, "init", "--canonical", clone); nativeField(t, copied, "registry") != "duplicate" ||
		nativeField(t, copied, "fleet_id") != nativeField(t, initialized, "fleet_id") {
		t.Fatal("copied Fleet registration concealed duplicate identity")
	}
	refuseDuplicate(fleet)
	refuseDuplicate(clone)
	if shown := run(clone, binary, "decision", "show", "decision_native"); nativeField(t, shown, "state") != "answered" {
		t.Fatal("duplicate registry projection hid immutable Answer history")
	}
	if shown := run(clone, binary, "task", "hold", "show", "hold_independent"); nativeField(t, shown, "unresolved") != "true" {
		t.Fatal("duplicate registry projection hid or resolved TaskHold history")
	}
	t.Log("native duplicate Fleet writes refused before/after registration; history remains readable")

	// Fail discovery after publication, then restart init against that same DB.
	// The registry is disposable infrastructure; the Fleet DB retains authority.
	registryPath := filepath.Join(os.Getenv("SECONDHAND_HOME"), "registry.db")
	registryBackup := filepath.Join(root, "preserved-registry.db")
	if err := os.Rename(registryPath, registryBackup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(registryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	repairFleet := filepath.Join(root, "repair-fleet")
	repairCtx, repairCancel := context.WithTimeout(context.Background(), time.Minute)
	failed := exec.CommandContext(repairCtx, binary, "init", "--canonical", repairFleet)
	failed.Dir = root
	failedOut, failedErr := failed.CombinedOutput()
	repairCancel()
	if failedErr == nil || nativeField(t, string(failedOut), "registry") != "failed" ||
		!strings.Contains(string(failedOut), "registry discovery update failed") {
		t.Fatalf("native init concealed discovery failure: %v\n%s", failedErr, failedOut)
	}
	repairID, err := store.FleetIDReadOnly(repairFleet)
	if err != nil || repairID != nativeField(t, string(failedOut), "fleet_id") {
		t.Fatalf("native failed discovery lost canonical identity: %s %v", repairID, err)
	}
	beforeRepair, err := os.ReadFile(store.Path(repairFleet))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(registryPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(registryBackup, registryPath); err != nil {
		t.Fatal(err)
	}
	repaired := run(root, binary, "init", "--canonical", repairFleet)
	afterRepair, err := os.ReadFile(store.Path(repairFleet))
	if err != nil || sha256.Sum256(beforeRepair) != sha256.Sum256(afterRepair) ||
		nativeField(t, repaired, "fleet_id") != repairID || nativeField(t, repaired, "registry") != "registered" {
		t.Fatalf("native discovery repair changed canonical state: %v", err)
	}
	listed = run(root, binary, "fleet")
	if !strings.Contains(listed, repairID+",") || !strings.Contains(listed, ",ready,") {
		t.Fatal("native repaired Fleet is not discoverable")
	}
	t.Log("native discovery repair preserved published canonical DB byte-for-byte")
}

func nativeField(t *testing.T, doc, key string) string {
	t.Helper()
	for _, line := range strings.Split(doc, "\n") {
		if value, found := strings.CutPrefix(line, key+": "); found {
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
	t.Fatalf("missing %s in %s", key, doc)
	return ""
}
