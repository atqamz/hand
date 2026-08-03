package project

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// gateRunLimit bounds how far back `no-mistakes runs` is asked to look. Large enough to cover any
// project's real history (the busiest project here sits under 30 runs total) without asking for
// literally unbounded output on every check.
const gateRunLimit = "10000"

// GateRunPRs returns the set of PR URLs recorded by completed no-mistakes runs in clonePath, read
// from `no-mistakes runs` text - the same read-only, text-scraping approach GateStatus already uses,
// never `~/.no-mistakes/state.sqlite` directly. It answers per clone rather than per PR so a caller
// checking many tasks on one project pays one no-mistakes process, not one per task.
//
// Membership establishes only that the no-mistakes `pr` step opened that exact PR from a run that
// reached `completed`. It is deliberately not a per-commit answer: no-mistakes' own state is keyed on
// working_path, not per-PR (see SPECS.md's "Gate preflight"), and hand records no head commit for a
// task's PR to compare against. A push to the same branch after the matched run completed - amending
// the PR without a new run - reads as gated here exactly as it did before that push; that gap is real
// and is documented rather than papered over with a false confidence this data cannot support. A PR
// opened by hand outside the no-mistakes `pr` step, even behind a run that did complete, also reads
// as absent, for the same reason: nothing ties that URL to that run's own bookkeeping.
//
// Every way of failing to ask no-mistakes at all is an error, never an empty set, so a caller can
// keep "the gate recorded no such run" separate from "the question could not be asked": a missing
// clone path, an unrunnable binary, and - read from the output text rather than the exit code, the
// same way GateStatus reads them, since no-mistakes can print either while exiting 0 - an
// uninitialized gate or a clone path that is not a git repository at all.
func GateRunPRs(clonePath string) (map[string]bool, error) {
	if _, err := os.Stat(clonePath); err != nil {
		return nil, fmt.Errorf("no-mistakes clone path: %w", err)
	}
	cmd := exec.Command("no-mistakes", "runs", "--limit", gateRunLimit)
	cmd.Dir = clonePath
	out, err := cmd.CombinedOutput()
	text := string(out)
	if strings.Contains(text, gateNotInitializedMarker) {
		return nil, fmt.Errorf("no-mistakes gate not initialized: %s", GateInitCommand(clonePath))
	}
	if strings.Contains(text, notGitRepoMarker) {
		return nil, fmt.Errorf("no-mistakes clone path is not a git repository: %s", clonePath)
	}
	if err != nil {
		return nil, fmt.Errorf("no-mistakes binary not found or not runnable: %w", err)
	}
	prs := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "completed" {
			continue
		}
		prs[fields[len(fields)-1]] = true
	}
	return prs, nil
}
