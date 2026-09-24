//go:build e2e

package e2e

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Actual CLI processes must preserve authority across restart and replan without
// inventing delivery, acknowledging evidence, or selecting a successor Decision.
func TestCanonicalDecisionCLI(t *testing.T) {
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
	registered := ok("project", "register", "sample")
	project := canonicalOutputField(t, registered, "project_id")
	workspace := canonicalOutputField(t, registered, "workspace_binding_id")
	ok("task", "create", "t_decision", "--project-id", project, "--goal", "Keep exact operator authority")
	ok("project", "policy", "policy_decision", "--project-id", project,
		"--worker-profile-ref", "", "--qualification-policy-ref", "", "--integration-policy-ref", "",
		"--production-policy-ref", "", "--publication-policy-ref", "")
	plan := []string{"plan", "create", "p_decision", "--task-id", "t_decision", "--workspace-binding-id", workspace,
		"--policy-revision-id", "policy_decision", "--intent", "explore", "--judgment", "bounded",
		"--basis", "registered repository", "--brief", "Inspect explicit operator choices"}
	ok(plan...)
	const timestamp = "2026-09-23T12:00:00Z"
	question := func(id string) []string {
		return []string{"decision", "create", id, "--task-id", "t_decision", "--scope", "plan",
			"--plan-id", "p_decision", "--question", "Which reversible approach?", "--created-at", timestamp}
	}
	answer := func(decision, id, text string) []string {
		return []string{"decision", "answer", decision, "--answer-id", id, "--answer", text,
			"--operator-ref", "operator:fixture", "--answered-at", timestamp, "--operator-answer"}
	}
	closure := func(id, reason string) []string {
		return []string{"decision", "close", id, "--reason", reason, "--closed-at", timestamp,
			"--evidence-digest", fmt.Sprintf("%x", sha256.Sum256([]byte("fixture closure evidence: "+id+" "+reason)))}
	}
	for _, id := range []string{"d_answer", "d_stale", "d_race", "d_cancel", "d_closure_race"} {
		ok(question(id)...)
	}
	before := snapshotTree(t, fleet)
	ok(question("d_answer")...)
	shown := ok("decision", "show", "d_answer")
	if canonicalOutputField(t, shown, "state") != "open" || canonicalOutputField(t, shown, "owner_current") != "true" {
		t.Fatalf("question not inspectable: %+v", shown)
	}
	assertTreeUnchanged(t, fleet, before)
	for _, args := range [][]string{
		answer("d_answer", "a_missing_authority", "option A")[:11],
		append(question("d_unrelated"), "--report-id", "unknown_report"),
		{"decision", "show", "missing"},
		closure("d_stale", "stale"), // positively current ownership cannot be called stale
		append(closure("d_cancel", "cancelled"), "--evidence-digest", "unproven"),
		append(closure("d_cancel", "cancelled"), "--closed-at", "tomorrow"),
	} {
		if got := runHand(t, fleet, args...); got.code == 0 {
			t.Fatalf("unsafe authority/read accepted: %v %+v", args, got)
		}
		assertTreeUnchanged(t, fleet, before)
	}
	for _, args := range [][]string{question("d_worker"), answer("d_answer", "a_worker", "option A"), closure("d_cancel", "cancelled")} {
		if got := runHandEnv(t, fleet, []string{"HAND_ROLE=worker"}, args...); got.code != 3 {
			t.Fatalf("worker authority was not refused: %+v", got)
		}
		assertTreeUnchanged(t, fleet, before)
	}
	const exactAnswer = "Use option A.\nKeep option B as a fallback."
	ok(answer("d_answer", "a_exact", exactAnswer)...)
	before = snapshotTree(t, fleet)
	ok(answer("d_answer", "a_exact", exactAnswer)...)
	shown = ok("decision", "show", "d_answer")
	if !strings.Contains(shown.stdout, fmt.Sprintf("%x", sha256.Sum256([]byte(exactAnswer)))) ||
		canonicalOutputField(t, shown, "state") != "answered" {
		t.Fatalf("Answer lost exact bytes/authority after process restart: %+v", shown)
	}
	if got := runHand(t, fleet, answer("d_answer", "a_conflict", "option B")...); got.code == 0 {
		t.Fatalf("conflicting Answer accepted: %+v", got)
	}
	assertTreeUnchanged(t, fleet, before)
	ok(closure("d_cancel", "cancelled")...)
	before = snapshotTree(t, fleet)
	ok(closure("d_cancel", "cancelled")...)
	ok(question("d_cancel")...)
	if got := runHand(t, fleet, answer("d_cancel", "a_cancelled", "option A")...); got.code == 0 {
		t.Fatalf("closed Decision reopened for an Answer: %+v", got)
	}
	if got := runHand(t, fleet, append(closure("d_cancel", "cancelled"), "--closed-at", "2026-09-23T12:01:00Z")...); got.code == 0 {
		t.Fatalf("closure history was overwritten: %+v", got)
	}
	shown = ok("decision", "show", "d_cancel")
	if canonicalOutputField(t, shown, "state") != "closed" || canonicalOutputField(t, shown, "closure_reason") != "cancelled" {
		t.Fatalf("exact closure lost after process restart: %+v", shown)
	}
	assertTreeUnchanged(t, fleet, before)

	results := make([]invocation, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			results[i] = runHand(t, fleet, answer("d_race", fmt.Sprintf("a_race_%d", i), fmt.Sprintf("option %d", i))...)
		})
	}
	wg.Wait()
	if (results[0].code == 0) == (results[1].code == 0) {
		t.Fatalf("conflicting processes did not have exactly one winner: %+v", results)
	}
	raceActions := [][]string{answer("d_closure_race", "a_closure_race", "option A"), closure("d_closure_race", "cancelled")}
	for i := range results {
		wg.Go(func() { results[i] = runHand(t, fleet, raceActions[i]...) })
	}
	wg.Wait()
	if (results[0].code == 0) == (results[1].code == 0) {
		t.Fatalf("Answer/closure processes did not have exactly one winner: %+v", results)
	}
	expectedAnswers, expectedClosures := 2, 2 // explicit cancellation + stale closure below
	if results[0].code == 0 {
		expectedAnswers++
	} else {
		expectedClosures++
	}
	plan[1], plan[2] = "replan", "p_successor"
	ok(append(plan, "--predecessor", "p_decision")...)
	before = snapshotTree(t, fleet)
	if got := runHand(t, fleet, answer("d_stale", "a_late", "option A")...); got.code == 0 || !strings.Contains(got.stderr, "not current") {
		t.Fatalf("late Answer did not refuse exact old Plan: %+v", got)
	}
	shown = ok("decision", "show", "d_stale")
	if canonicalOutputField(t, shown, "state") != "open" || canonicalOutputField(t, shown, "owner_current") != "false" {
		t.Fatalf("read invented closure or hid stale ownership: %+v", shown)
	}
	ok(answer("d_answer", "a_exact", exactAnswer)...)
	ok(question("d_answer")...)
	assertTreeUnchanged(t, fleet, before)
	ok(closure("d_stale", "stale")...)
	before = snapshotTree(t, fleet)
	ok(closure("d_stale", "stale")...)
	shown = ok("decision", "show", "d_stale")
	if canonicalOutputField(t, shown, "state") != "closed" || canonicalOutputField(t, shown, "owner_current") != "false" ||
		canonicalOutputField(t, shown, "closure_reason") != "stale" {
		t.Fatalf("stale closure retargeted successor or lost history: %+v", shown)
	}
	if got := runHand(t, fleet, closure("d_answer", "stale")...); got.code == 0 {
		t.Fatalf("answered Decision changed to closed: %+v", got)
	}
	assertTreeUnchanged(t, fleet, before)
	db := canonicalTestDB(t, fleet)
	var answers, invented int
	if err := db.QueryRow(`SELECT COUNT(*) FROM decision_answer`).Scan(&answers); err != nil || answers != expectedAnswers {
		t.Fatalf("Answer history changed: %d %v", answers, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM decision_closure`).Scan(&answers); err != nil || answers != expectedClosures {
		t.Fatalf("closure history changed: %d %v", answers, err)
	}
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM worker_input)+(SELECT COUNT(*) FROM worker_wake_operation)+
		(SELECT COUNT(*) FROM worker_input_acknowledgement)+(SELECT COUNT(*) FROM worker_report_acknowledgement)+
		(SELECT COUNT(*) FROM task_hold)+(SELECT COUNT(*) FROM external_operation)`).Scan(&invented); err != nil || invented != 0 {
		t.Fatalf("Decision interaction invented delivery/ack/effects: %d %v", invented, err)
	}
}

func TestCanonicalDecisionRefusesLegacyAndMissingStores(t *testing.T) {
	for _, kind := range []string{"missing", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			fleet := t.TempDir()
			if kind == "legacy" {
				createCutoverLegacyFixture(t, fleet)
			}
			seedPrivateRuntime(t, fleet)
			before := snapshotTree(t, fleet)
			if got := runHand(t, fleet, "decision", "show", "d_missing"); got.code == 0 {
				t.Fatalf("accepted %s store: %+v", kind, got)
			}
			assertTreeUnchanged(t, fleet, before)
		})
	}
}
