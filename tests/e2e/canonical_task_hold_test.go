//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Multiple deferrals and operator authority must remain separate across CLI
// restart, failed typed-child insertion and competing exact resolutions.
func TestCanonicalTaskHoldCLI(t *testing.T) {
	parent := t.TempDir()
	fleet := filepath.Join(parent, "fleet")
	if got := runHand(t, parent, "init", "--canonical", fleet); got.code != 0 {
		t.Fatal(got)
	}
	initGitRepo(t, filepath.Join(fleet, "projects", "sample"))
	ok := func(args ...string) invocation {
		t.Helper()
		got := runHand(t, fleet, args...)
		if got.code != 0 {
			t.Fatalf("%v: %+v", args, got)
		}
		return got
	}
	projectID := canonicalOutputField(t, ok("project", "register", "sample"), "project_id")
	for _, id := range []string{"t_held", "t_dependency"} {
		ok("task", "create", id, "--project-id", projectID, "--goal", "Goal "+id)
	}
	const timestamp = "2026-09-23T12:00:00Z"
	digest := strings.Repeat("a", 64)
	create := func(id, kind string) []string {
		return []string{"task", "hold", "create", id, "--task-id", "t_held", "--kind", kind,
			"--reason", "Fixture deferral", "--evidence-digest", digest, "--created-at", timestamp}
	}
	resolve := func(id, resolution string) []string {
		return []string{"task", "hold", "resolve", id, "--resolution", resolution,
			"--evidence-digest", digest, "--resolved-at", timestamp}
	}
	ok(create("h_plain", "operator")...)
	ok(append(create("h_blocked", "blocked"), "--blocked-on-task-id", "t_dependency", "--recheck-not-before", "2026-09-24T12:00:00Z")...)
	ok("decision", "create", "d_hold", "--task-id", "t_held", "--scope", "task", "--question", "Continue after the checkpoint?", "--created-at", timestamp)
	ok(append(create("h_decision", "operator"), "--decision-id", "d_hold")...)
	before := snapshotTree(t, fleet)
	shown := ok("task", "hold", "show", "h_blocked")
	for key, want := range map[string]string{
		"ordinal": "2", "blocked_on_task_id": "t_dependency", "decision_id": "none",
		"recheck_not_before": "2026-09-24T12:00:00Z", "unresolved": "true", "owner_current": "true",
	} {
		if canonicalOutputField(t, shown, key) != want {
			t.Fatalf("Hold lost %s: %+v", key, shown)
		}
	}
	for _, args := range [][]string{
		create("h_plain", "operator"), // caller must inspect a lost response, not duplicate history
		append(create("h_bad", "blocked"), "--decision-id", "unknown"),
		append(create("h_bad", "operator"), "--blocked-on-task-id", "t_dependency"),
		append(create("h_bad", "blocked"), "--recheck-not-before", "tomorrow"),
		{"task", "hold", "show", "h_bad"},
	} {
		if got := runHand(t, fleet, args...); got.code == 0 {
			t.Fatalf("invalid Hold operation succeeded: %v %+v", args, got)
		}
		assertTreeUnchanged(t, fleet, before)
	}
	for _, args := range [][]string{create("h_worker", "operator"), resolve("h_plain", "released")} {
		if got := runHandEnv(t, fleet, []string{"HAND_ROLE=worker"}, args...); got.code != 3 {
			t.Fatalf("worker mutated TaskHold: %+v", got)
		}
		assertTreeUnchanged(t, fleet, before)
	}
	ok("decision", "answer", "d_hold", "--answer-id", "a_hold", "--answer", "Proceed after checking dependencies",
		"--operator-answer", "--operator-ref", "operator:fixture", "--answered-at", timestamp)
	shown = ok("task", "hold", "show", "h_decision")
	if canonicalOutputField(t, shown, "unresolved") != "true" || canonicalOutputField(t, shown, "decision_id") != "d_hold" {
		t.Fatalf("Answer implicitly resolved or retargeted Hold: %+v", shown)
	}
	ok(resolve("h_decision", "released")...)
	before = snapshotTree(t, fleet)
	shown = ok("task", "hold", "show", "h_decision")
	if canonicalOutputField(t, shown, "unresolved") != "false" || canonicalOutputField(t, shown, "resolution") != "released" ||
		canonicalOutputField(t, shown, "resolution_evidence_digest") != digest {
		t.Fatalf("exact resolution lost on process restart: %+v", shown)
	}
	if got := runHand(t, fleet, resolve("h_decision", "released")...); got.code == 0 {
		t.Fatalf("resolution replay overwrote immutable history: %+v", got)
	}
	for _, id := range []string{"h_plain", "h_blocked"} {
		if shown := ok("task", "hold", "show", id); canonicalOutputField(t, shown, "unresolved") != "true" {
			t.Fatalf("another Hold resolved implicitly: %+v", shown)
		}
	}
	assertTreeUnchanged(t, fleet, before)
	results := make([]invocation, 2)
	var wg sync.WaitGroup
	for i, resolution := range []string{"released", "cancelled"} {
		wg.Go(func() { results[i] = runHand(t, fleet, resolve("h_plain", resolution)...) })
	}
	wg.Wait()
	if (results[0].code == 0) == (results[1].code == 0) {
		t.Fatalf("competing Hold resolutions did not have exactly one winner: %+v", results)
	}
	db := canonicalTestDB(t, fleet)
	var holds, resolutions, questions, answers, invented int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM task_hold),(SELECT COUNT(*) FROM task_hold_resolution),
		(SELECT COUNT(*) FROM decision),(SELECT COUNT(*) FROM decision_answer),
		(SELECT COUNT(*) FROM attempt)+(SELECT COUNT(*) FROM external_operation)+(SELECT COUNT(*) FROM worker_input)+
		(SELECT COUNT(*) FROM worker_input_acknowledgement)+(SELECT COUNT(*) FROM worker_report_acknowledgement)+
		(SELECT COUNT(*) FROM task WHERE lifecycle<>'active')`).Scan(&holds, &resolutions, &questions, &answers, &invented); err != nil || holds != 3 || resolutions != 2 || questions != 1 || answers != 1 || invented != 0 {
		t.Fatalf("Hold/Decision independence: holds=%d resolutions=%d questions=%d answers=%d invented=%d: %v", holds, resolutions, questions, answers, invented, err)
	}
}

func TestCanonicalTaskHoldReadRefusesMissingAndLegacyStores(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		fleet := t.TempDir()
		if legacy {
			createCutoverLegacyFixture(t, fleet)
		}
		seedPrivateRuntime(t, fleet)
		before := snapshotTree(t, fleet)
		if got := runHand(t, fleet, "task", "hold", "show", "unknown"); got.code == 0 {
			t.Fatalf("read adopted missing/legacy store: %+v", got)
		}
		assertTreeUnchanged(t, fleet, before)
	}
}
