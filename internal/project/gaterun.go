package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/integration"
)

// Bounds how far back `no-mistakes runs` is asked to look. Large enough to cover any project's
// real history (the busiest project here sits under 30 runs total) without asking for
// literally unbounded output on every check.
const gateRunLimit = "10000"

// GateRunObservation answers whether the newest no-mistakes gate run for a pull request completed, in
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

// ClassifyGateRun turns one clone's run PR set - or the failure that kept GateRunPRs from reading it -
// into one observation about pr. Keyed on the PR URL a run's own `pr` step recorded, never a commit
// or branch, so a squash merge's unreachable pre-squash head never touches it.
func ClassifyGateRun(prs map[string]bool, err error, pr string) GateRunObservation {
	probe := ghutil.Probe{Command: fmt.Sprintf("no-mistakes runs --limit %s", gateRunLimit)}
	if err != nil {
		probe.Reason = err.Error()
		return GateRunObservation{State: ghutil.ObservationUnknown, Probe: probe}
	}
	completed, recorded := prs[pr]
	if completed {
		return GateRunObservation{State: ghutil.ObservationFound, Probe: probe}
	}
	if recorded {
		probe.Reason = "the newest no-mistakes run that recorded this pull request has not completed"
		return GateRunObservation{State: ghutil.ObservationUnknown, Probe: probe}
	}
	probe.Reason = "no no-mistakes run recorded this pull request, or its newest run failed"
	return GateRunObservation{State: ghutil.ObservationAbsent, Probe: probe}
}

// ObserveGateRun is the applicability check hand status and hand watch share: empty when p is not
// registered or not run through no-mistakes, so a caller cannot answer the question differently for
// the same project. runPRs is the caller's own no-mistakes invocation, cached or not.
func ObserveGateRun(home string, p Project, registered bool, pr string, runPRs func(clonePath string) (map[string]bool, error)) ghutil.ObservationState {
	if !registered || p.Mode != ModeNoMistakes {
		return ""
	}
	prs, err := runPRs(filepath.Join(home, "projects", p.Name))
	return ClassifyGateRun(prs, err, pr).State
}

// GateRunPRs maps each PR URL whose newest no-mistakes run in clonePath did not fail to whether that
// run completed, scraped from `no-mistakes runs` text the way GateStatus does, never out
// of `~/.no-mistakes` directly. Per clone rather than per PR, so many tasks on one project pay one process.
func GateRunPRs(ctx context.Context, clonePath string) (map[string]bool, error) {
	// Every way of failing to ask no-mistakes at all is an error, never an empty set, so a
	// caller can keep "the gate recorded no such run" separate from "the question could not be
	// asked".
	if _, err := os.Stat(clonePath); err != nil {
		return nil, fmt.Errorf("no-mistakes clone path: %w", err)
	}
	stdout, stderr, err := integration.Run(ctx, "delivery/no-mistakes", clonePath, "runs", "--limit", gateRunLimit)
	// Checked ahead of the output text: a subprocess ctx killed mid-write can still leave text
	// that happens to contain a marker, and blaming the binary for a deadline hand itself set
	// would name a remedy that would not help either.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("no-mistakes runs did not complete: %w", ctx.Err())
	}
	text := string(append(stdout, stderr...))
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
	// A true entry establishes only that the `pr` step opened that exact PR from a newest run that
	// reached completed, not a per-commit answer: no-mistakes keys its state on working_path
	// and hand records no head commit to compare against.
	prs := make(map[string]bool)
	seen := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// So a push amending the PR outside the gate after its matched run reads as gated exactly as
		// it did before that push, and a PR opened outside the `pr` step reads as absent even behind a
		// run that did complete. Both gaps are real, and documented rather than papered over.
		pr := fields[len(fields)-1]
		if seen[pr] {
			continue
		}
		seen[pr] = true
		if fields[0] != "failed" {
			prs[pr] = fields[0] == "completed"
		}
	}
	return prs, nil
}
