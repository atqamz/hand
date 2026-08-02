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

// GateRanForPR reports whether a completed no-mistakes run's own recorded PR is exactly prURL, read
// from `no-mistakes runs` text in clonePath - the same read-only, text-scraping approach GateStatus
// already uses, never `~/.no-mistakes/state.sqlite` directly.
//
// This establishes only that the no-mistakes `pr` step opened this exact PR from a run that reached
// `completed`. It is deliberately not a per-commit answer: no-mistakes' own state is keyed on
// working_path, not per-PR (see SPECS.md's "Gate preflight"), and hand records no head commit for a
// task's PR to compare against. A push to the same branch after the matched run completed - amending
// the PR without a new run - reads as gated here exactly as it did before that push; that gap is real
// and is documented rather than papered over with a false confidence this data cannot support. A PR
// opened by hand outside the no-mistakes `pr` step, even behind a run that did complete, also reads
// as not found, for the same reason: nothing ties that URL to that run's own bookkeeping.
func GateRanForPR(clonePath, prURL string) (bool, error) {
	if prURL == "" {
		return false, nil
	}
	if _, err := os.Stat(clonePath); err != nil {
		return false, fmt.Errorf("no-mistakes clone path: %w", err)
	}
	cmd := exec.Command("no-mistakes", "runs", "--limit", gateRunLimit)
	cmd.Dir = clonePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("no-mistakes binary not found or not runnable: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "completed" {
			continue
		}
		if fields[len(fields)-1] == prURL {
			return true, nil
		}
	}
	return false, nil
}
