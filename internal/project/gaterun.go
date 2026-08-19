package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/atqamz/hand/internal/ghutil"
)

// Bounds how far back `no-mistakes runs` is asked to look. Large enough to cover any project's
// real history (the busiest project here sits under 30 runs total) without asking for
// literally unbounded output on every check.
const gateRunLimit = "10000"

// GateRunObservation answers whether a completed no-mistakes gate run recorded a pull request, in
// the same found/absent/unknown vocabulary ghutil.PRObservation uses (atqamz/hand#241). Absent is a
// positive finding; any reason the run list could not be read is unknown, never absent.
type GateRunObservation struct {
	State ghutil.ObservationState
	Probe ghutil.Probe
}

func (o GateRunObservation) Found() bool   { return o.State == ghutil.ObservationFound }
func (o GateRunObservation) Absent() bool  { return o.State == ghutil.ObservationAbsent }
func (o GateRunObservation) Unknown() bool { return !o.Found() && !o.Absent() }

// Reason is the sentence a caller reports when it will not act on this observation.
func (o GateRunObservation) Reason() string { return o.Probe.Explain() }

// ClassifyGateRun turns one clone's completed-run PR set - or the failure that kept GateRunPRs from
// reading it - into one observation about pr. Keyed on the PR URL a completed run's own `pr` step
// recorded, never a commit or branch, so a squash merge's unreachable pre-squash head never touches it.
func ClassifyGateRun(prs map[string]bool, err error, pr string) GateRunObservation {
	probe := ghutil.Probe{Command: fmt.Sprintf("no-mistakes runs --limit %s", gateRunLimit)}
	if err != nil {
		probe.Reason = err.Error()
		return GateRunObservation{State: ghutil.ObservationUnknown, Probe: probe}
	}
	if prs[pr] {
		return GateRunObservation{State: ghutil.ObservationFound, Probe: probe}
	}
	probe.Reason = "no completed no-mistakes run recorded this pull request"
	return GateRunObservation{State: ghutil.ObservationAbsent, Probe: probe}
}

// GateRunPRs returns the PR URLs recorded by completed no-mistakes runs in clonePath, scraped
// from `no-mistakes runs` text the way GateStatus does, never out of `~/.no-mistakes` directly.
// Per clone rather than per PR, so many tasks on one project pay one no-mistakes process.
func GateRunPRs(ctx context.Context, clonePath string) (map[string]bool, error) {
	// Every way of failing to ask no-mistakes at all is an error, never an empty set, so a
	// caller can keep "the gate recorded no such run" separate from "the question could not be
	// asked".
	if _, err := os.Stat(clonePath); err != nil {
		return nil, fmt.Errorf("no-mistakes clone path: %w", err)
	}
	cmd := exec.CommandContext(ctx, "no-mistakes", "runs", "--limit", gateRunLimit)
	cmd.Dir = clonePath
	out, err := cmd.CombinedOutput()
	// Checked ahead of the output text: a subprocess ctx killed mid-write can still leave text
	// that happens to contain a marker, and blaming the binary for a deadline hand itself set
	// would name a remedy that would not help either.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("no-mistakes runs did not complete: %w", ctx.Err())
	}
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
