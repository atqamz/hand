package ghutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// ObservationState is the vocabulary every observation in hand answers in, GitHub or not - reused by
// project.GateRunObservation and mirrored by worktree.LeaseObservationState. Absent is a positive
// finding from a completed query; anything else is unknown, and nothing may be concluded from it.
type ObservationState string

const (
	ObservationFound   ObservationState = "found"
	ObservationAbsent  ObservationState = "absent"
	ObservationUnknown ObservationState = "unknown"
)

// Probe records how an observation was attempted, so an unknown carries the command that was run
// and what it answered instead. Same role as worktree.LeaseProbe: evidence travels with the state.
type Probe struct {
	Command string
	Reason  string
}

// Explain names the command a probe ran alongside its reason, when there is one. Shared by every
// observation type that carries a Probe, so the wording never drifts between them.
func (p Probe) Explain() string {
	if p.Command == "" {
		return p.Reason
	}
	return fmt.Sprintf("%s; observed by running %q", p.Reason, p.Command)
}

// PRObservation is one answer about one pull request. Merged, URL and Head are meaningful only
// where the state is found; Ambiguous is set only where a completed query returned candidates that
// do not resolve to a single winner.
type PRObservation struct {
	State     ObservationState
	URL       string
	Merged    bool
	Head      string
	Ambiguous *AmbiguousPRError
	Probe     Probe
}

func (o PRObservation) Found() bool { return o.State == ObservationFound }

// Absent reports that the query completed and there is genuinely no such pull request. It is never
// the residue of a failed query, so a caller may act on it.
func (o PRObservation) Absent() bool { return o.State == ObservationAbsent }

// Unknown is every state that is not one of the two positive findings, which is what keeps a zero
// value, an unset state, and any state added later from reading as an absence.
func (o PRObservation) Unknown() bool { return !o.Found() && !o.Absent() }

// Reason is the sentence a caller reports when it will not act on this observation, naming
// what answered nothing and the command that asked.
func (o PRObservation) Reason() string {
	return o.Probe.Explain()
}

// UnknownPRObservation reports an observation that could not be attempted at all, so a caller whose
// local prerequisite failed says unknown rather than passing an absence off as one GitHub answered.
func UnknownPRObservation(command, reason string) PRObservation {
	return PRObservation{State: ObservationUnknown, Probe: Probe{Command: command, Reason: reason}}
}

// The one diagnostic GitHub answers with when a pull request genuinely does not exist. An absent
// repository reports "Could not resolve to a Repository" instead, which is indistinguishable from
// one the token may not read, so only this shape proves absence (tests/contract pins both).
const prAbsentDiagnostic = "Could not resolve to a PullRequest"

func absentPR(probe Probe) PRObservation {
	probe.Reason = "gh reports no such pull request"
	return PRObservation{State: ObservationAbsent, Probe: probe}
}

func unknownPR(probe Probe, format string, args ...any) PRObservation {
	probe.Reason = fmt.Sprintf(format, args...)
	return PRObservation{State: ObservationUnknown, Probe: probe}
}

// Runs one gh query and decodes its JSON payload into payload. A non-nil result is the terminal
// observation: absent only for the diagnostic that proves a pull request does not exist, unknown
// for every other failure, including an unreadable payload at exit 0.
func decodeGHPayload(ctx context.Context, probe Probe, payload any, args ...string) *PRObservation {
	cmd := exec.CommandContext(ctx, "gh", args...)
	// gh writes warnings to stderr ahead of the JSON, so the payload is read from stdout alone;
	// CombinedOutput here corrupts the parse (atqamz/hand#21).
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		diagnostic := strings.TrimSpace(stderr.String())
		if strings.Contains(diagnostic, prAbsentDiagnostic) {
			observation := absentPR(probe)
			return &observation
		}
		observation := unknownPR(probe, "gh failed: %v: %s", err, diagnostic)
		return &observation
	}
	if err := json.Unmarshal(out, payload); err != nil {
		observation := unknownPR(probe, "gh exited zero and its output could not be parsed: %v: %s", err, strings.TrimSpace(string(out)))
		return &observation
	}
	return nil
}
