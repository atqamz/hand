// npm-publish-target.sh's post-publish retry loop (atqamz/hand#506) reacts to registry
// outcomes real npm cannot be made to produce on demand: absent-then-verified, or never
// catching up. These tests run the real script against a stub npm-registry-check.sh
// that plays back a scripted sequence; the checker's own classification is covered
// separately, by TestRegistryCheckDistinguishesEveryEnumeratedOutcome.
package npmpublish

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const stubRegistryCheckScript = `#!/usr/bin/env bash
# Pops the next "outcome:exit" line from $STUB_OUTCOMES on each call, clamping to the
# last line once exhausted, so a test can script an exact sequence across
# npm-publish-target.sh's repeated invocations of its checker. Reads the file into an
# array with a while/read loop rather than mapfile, which macOS's system bash (3.2)
# does not have - the CI runners this stub must also work under.
set -euo pipefail
idx_file="$STUB_STATE/idx"
idx=0
[[ -f "$idx_file" ]] && idx=$(<"$idx_file")
lines=()
while IFS= read -r outcome_line || [[ -n "$outcome_line" ]]; do
  lines+=("$outcome_line")
done < "$STUB_OUTCOMES"
line_no=$idx
if (( line_no >= ${#lines[@]} )); then
  line_no=$((${#lines[@]} - 1))
fi
echo $((idx + 1)) > "$idx_file"
line="${lines[$line_no]}"
outcome="${line%%:*}"
exit_code="${line#*:}"
printf 'call %s outcome=%s auth=%s\n' "$idx" "$outcome" "${NODE_AUTH_TOKEN:+set}" >> "$STUB_STATE/calls.log"
printf '%s\n' "$outcome"
exit "$exit_code"
`

// Copies the real npm-publish-target.sh next to the scripted stub above; script_dir is
// derived from BASH_SOURCE at run time, so the stub must live beside it to be picked up.
func newStubCheckerScriptDir(t *testing.T, outcomes []string) string {
	t.Helper()
	dir := t.TempDir()
	real, err := os.ReadFile(scriptPath(t, "npm-publish-target.sh"))
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "npm-publish-target.sh"), string(real))
	mustWriteFile(t, filepath.Join(dir, "npm-registry-check.sh"), stubRegistryCheckScript)
	if err := os.MkdirAll(filepath.Join(dir, "stub-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(dir, "outcomes.txt"), strings.Join(outcomes, "\n")+"\n")
	return dir
}

func runStubbedPublishTarget(t *testing.T, outcomes []string, extraEnv ...string) (out string, elapsed time.Duration, calls []string, err error) {
	t.Helper()
	scriptDir := newStubCheckerScriptDir(t, outcomes)
	stubState := filepath.Join(scriptDir, "stub-state")
	tarball := seedTarball(t, "@atqamz/hand-linux-x64", "0.7.0", "sha512-fresh", repoURL)

	cmd := exec.Command("bash", filepath.Join(scriptDir, "npm-publish-target.sh"),
		"@atqamz/hand-linux-x64", "0.7.0", tarball, "sha512-fresh", repoURL)
	cmd.Env = append(fakeNpmEnv(t, t.TempDir()),
		"STUB_OUTCOMES="+filepath.Join(scriptDir, "outcomes.txt"),
		"STUB_STATE="+stubState,
	)
	cmd.Env = append(cmd.Env, extraEnv...)

	start := time.Now()
	raw, runErr := cmd.CombinedOutput()
	return string(raw), time.Since(start), readCallsLog(t, stubState), runErr
}

func TestPublishTargetRetriesUntilTheRegistryCatchesUpThenVerifies(t *testing.T) {
	out, elapsed, calls, err := runStubbedPublishTarget(t,
		[]string{"absent-new-version:0", "absent-new-version:0", "absent-new-version:0", "verified-published:0"},
		"NPM_PUBLISH_VERIFY_INTERVAL_SECONDS=1", "NPM_PUBLISH_VERIFY_BUDGET_SECONDS=60")
	if err != nil {
		t.Fatalf("npm-publish-target.sh: %v: %s", err, out)
	}
	if !strings.Contains(out, "verified published") {
		t.Fatalf("output = %s, want it to report verified published", out)
	}
	if len(calls) != 4 {
		t.Fatalf("calls = %v, want exactly 4 (1 pre-publish + 3 post-publish retries)", calls)
	}
	if elapsed < 2*time.Second {
		t.Fatalf("elapsed = %s, want at least 2s: two absent outcomes must each cost one sleep", elapsed)
	}
}

func TestPublishTargetFailsImmediatelyOnANonzeroPostPublishOutcome(t *testing.T) {
	out, elapsed, calls, err := runStubbedPublishTarget(t,
		[]string{"absent-new-version:0", "integrity-mismatch:1"},
		"NPM_PUBLISH_VERIFY_INTERVAL_SECONDS=5", "NPM_PUBLISH_VERIFY_BUDGET_SECONDS=60")
	if err == nil {
		t.Fatalf("npm-publish-target.sh succeeded despite a post-publish integrity-mismatch; output = %s", out)
	}
	if !strings.Contains(out, "integrity-mismatch") {
		t.Fatalf("output = %s, want it to name the integrity-mismatch outcome", out)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %v, want exactly 2 (1 pre-publish + 1 post-publish, no retry on a hard-stop outcome)", calls)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("elapsed = %s, want under one interval: a hard-stop outcome must not sleep at all", elapsed)
	}
}

func TestPublishTargetWarnsAndSucceedsWhenTheVerifyBudgetRunsOut(t *testing.T) {
	out, elapsed, calls, err := runStubbedPublishTarget(t,
		[]string{"absent-new-version:0", "absent-new-version:0"},
		"NPM_PUBLISH_VERIFY_INTERVAL_SECONDS=1", "NPM_PUBLISH_VERIFY_BUDGET_SECONDS=2")
	if err != nil {
		t.Fatalf("npm-publish-target.sh: %v: %s", err, out)
	}
	if !strings.Contains(out, "::warning::") {
		t.Fatalf("output = %s, want a GitHub Actions ::warning:: annotation", out)
	}
	if !strings.Contains(out, "@atqamz/hand-linux-x64@0.7.0") {
		t.Fatalf("output = %s, want the warning to name the package and version", out)
	}
	if len(calls) != 4 {
		t.Fatalf("calls = %v, want exactly 4 (1 pre-publish + 3 post-publish before the 2s budget elapses)", calls)
	}
	if elapsed < 2*time.Second {
		t.Fatalf("elapsed = %s, want at least the 2s budget to have been spent retrying before giving up", elapsed)
	}
}
