package harness

import "testing"

func TestWorkerExecutionShapeIsIndependentFromSupervisorCapability(t *testing.T) {
	if !IsOneShot(Antigravity) {
		t.Fatal("Antigravity must remain a one-shot worker")
	}
	if SupportsSteering(Antigravity) {
		t.Fatal("one-shot Antigravity must not expose pane steering")
	}
	for _, name := range []string{Claude, Codex, Grok, Pi, OpenCode} {
		if IsOneShot(name) {
			t.Fatalf("%s unexpectedly classified one-shot", name)
		}
		if !SupportsSteering(name) {
			t.Fatalf("resident worker %s unexpectedly non-steerable", name)
		}
	}
}

func TestAntigravityLaunchEvidenceRequiresTypedInitForExactCwd(t *testing.T) {
	const cwd = "/tmp/worktree"
	plain := "agy response\n"
	if LaunchEvidenceInOutput(Antigravity, plain, cwd) {
		t.Fatal("plain response text is not launch evidence")
	}
	init := "shell noise\n{\"event\":\"init\",\"conversation_id\":\"c1\",\"init\":{\"cwd\":\"/tmp/worktree\"}}\n"
	if !LaunchEvidenceInOutput(Antigravity, init, cwd) {
		t.Fatal("typed init event for the exact CWD must prove Antigravity initialized")
	}
	if LaunchEvidenceInOutput(Antigravity, init, "/tmp/other-worktree") {
		t.Fatal("stale init from another CWD must not prove this launch")
	}
	missingCwd := "{\"event\":\"init\",\"conversation_id\":\"c1\",\"init\":{}}\n"
	if LaunchEvidenceInOutput(Antigravity, missingCwd, cwd) {
		t.Fatal("unscoped init must not prove launch")
	}
	resultOnly := "{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\"}}\n"
	if LaunchEvidenceInOutput(Antigravity, resultOnly, cwd) {
		t.Fatal("provider result status must not substitute for launch init evidence")
	}
	if LaunchEvidenceInOutput(Antigravity, init, "") {
		t.Fatal("launch evidence without an expected CWD must fail closed")
	}
}
