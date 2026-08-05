package project

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Bounds how far back `no-mistakes runs` is asked to look. Large enough to cover any project's
// real history (the busiest project here sits under 30 runs total) without asking for
// literally unbounded output on every check.
const gateRunLimit = "10000"

// GateRunPRs returns the PR URLs recorded by completed no-mistakes runs in clonePath, scraped
// from `no-mistakes runs` text the way GateStatus does, never out of `~/.no-mistakes` directly.
// Per clone rather than per PR, so many tasks on one project pay one no-mistakes process.
func GateRunPRs(clonePath string) (map[string]bool, error) {
	// Every way of failing to ask no-mistakes at all is an error, never an empty set, so a
	// caller can keep "the gate recorded no such run" separate from "the question could not be
	// asked".
	if _, err := os.Stat(clonePath); err != nil {
		return nil, fmt.Errorf("no-mistakes clone path: %w", err)
	}
	cmd := exec.Command("no-mistakes", "runs", "--limit", gateRunLimit)
	cmd.Dir = clonePath
	out, err := cmd.CombinedOutput()
	text := string(out)
	// Both read out of the output text rather than the exit code, the way GateStatus reads
	// them: `no-mistakes runs` exits 1 for an uninitialized gate and for a non-git clone path
	// alike, leaving the exit code with nothing to tell the two apart.
	if strings.Contains(text, gateNotInitializedMarker) {
		return nil, fmt.Errorf("no-mistakes gate not initialized: %s", GateInitCommand(clonePath))
	}
	if strings.Contains(text, notGitRepoMarker) {
		return nil, fmt.Errorf("no-mistakes clone path is not a git repository: %s", clonePath)
	}
	if err != nil {
		return nil, fmt.Errorf("no-mistakes binary not found or not runnable: %w", err)
	}
	// Membership establishes only that the `pr` step opened that exact PR from a run that
	// reached completed, not a per-commit answer: no-mistakes keys its state on working_path
	// and hand records no head commit to compare against.
	prs := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "completed" {
			continue
		}
		// So a push amending the PR after its matched run reads as gated exactly as it did
		// before that push, and a PR opened outside the `pr` step reads as absent even behind a
		// run that did complete. Both gaps are real, and documented rather than papered over.
		prs[fields[len(fields)-1]] = true
	}
	return prs, nil
}
