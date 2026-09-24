package project

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/ghutil"
	"github.com/atqamz/hand/internal/integration"
)

const gateRunLimit = "0"
const gateVerdictTimeout = 5 * time.Second

type GateRunIDs map[string][]string

type GateRunObservation struct {
	State ghutil.ObservationState
	Probe ghutil.Probe
}

func (o GateRunObservation) Found() bool    { return o.State == ghutil.ObservationFound }
func (o GateRunObservation) Absent() bool   { return o.State == ghutil.ObservationAbsent }
func (o GateRunObservation) Unknown() bool  { return !o.Found() && !o.Absent() }
func (o GateRunObservation) Reason() string { return o.Probe.Explain() }

func ClassifyGateRun(runs GateRunIDs, err error, pr string) GateRunObservation {
	probe := ghutil.Probe{Command: fmt.Sprintf("no-mistakes runs --limit %s", gateRunLimit)}
	switch {
	case err != nil:
		probe.Reason = err.Error()
	case len(runs[pr]) == 0:
		probe.Reason = "no durable no-mistakes run ID recorded this pull request"
	case len(runs[pr]) > 1:
		probe.Reason = "multiple no-mistakes run IDs recorded this pull request"
	default:
		probe.Reason = "current checks-passed readiness for run " + runs[pr][0] + " is not yet supported by the qualified provider"
	}
	return GateRunObservation{State: ghutil.ObservationUnknown, Probe: probe}
}

func ObserveGateRun(ctx context.Context, home string, p Project, registered bool, pr string, runPRs func(clonePath string) (GateRunIDs, error)) ghutil.ObservationState {
	if !registered || p.Mode != ModeNoMistakes {
		return ""
	}
	clonePath := filepath.Join(home, "projects", p.Name)
	runs, err := runPRs(clonePath)
	if err != nil || len(runs[pr]) != 1 {
		return ClassifyGateRun(runs, err, pr).State
	}
	return gateRunCIVerdict(ctx, clonePath, runs[pr][0], pr)
}

func gateRunCIVerdict(ctx context.Context, clonePath, runID, pr string) ghutil.ObservationState {
	probeCtx, cancel := context.WithTimeout(ctx, gateVerdictTimeout)
	defer cancel()
	stdout, _, err := integration.Run(probeCtx, "delivery/no-mistakes", clonePath, "axi", "ci-verdict", "--run", runID)
	if err != nil || probeCtx.Err() != nil {
		return ghutil.ObservationUnknown
	}
	fields, err := parseGateCIVerdict(string(stdout))
	if err != nil || fields[0] != runID || fields[1] != "running" || fields[2] != pr || !validFullGateSHA(fields[3]) ||
		fields[4] != "checks-passed" || fields[5] != "checks" && fields[5] != "declared-no-ci" || fields[6] != "" {
		return ghutil.ObservationUnknown
	}
	return ghutil.ObservationFound
}

func parseGateCIVerdict(output string) ([7]string, error) {
	var fields [7]string
	if !strings.HasSuffix(output, "\n") {
		return fields, fmt.Errorf("no-mistakes CI verdict is incomplete")
	}
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != len(fields) {
		return fields, fmt.Errorf("no-mistakes CI verdict has %d fields", len(lines))
	}
	for index, key := range [...]string{"run_id", "lifecycle", "pr", "head_sha", "verdict", "basis", "reason"} {
		value, ok := strings.CutPrefix(lines[index], key+": ")
		if !ok || value == "" {
			return fields, fmt.Errorf("no-mistakes CI verdict has malformed %s", key)
		}
		if strings.HasPrefix(value, "\"") {
			decoded, err := strconv.Unquote(value)
			if err != nil {
				return fields, fmt.Errorf("no-mistakes CI verdict has malformed %s: %w", key, err)
			}
			value = decoded
		} else if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\t") {
			return fields, fmt.Errorf("no-mistakes CI verdict has malformed %s", key)
		}
		fields[index] = value
	}
	return fields, nil
}

func validFullGateSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func GateRunPRs(ctx context.Context, clonePath string) (GateRunIDs, error) {
	if _, err := os.Stat(clonePath); err != nil {
		return nil, fmt.Errorf("no-mistakes clone path: %w", err)
	}
	stdout, stderr, err := integration.Run(ctx, "delivery/no-mistakes", clonePath, "runs", "--limit", gateRunLimit)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("no-mistakes runs did not complete: %w", ctx.Err())
	}
	text := string(append(stdout, stderr...))
	if strings.Contains(text, gateNotInitializedMarker) {
		return nil, fmt.Errorf("no-mistakes gate not initialized: %s", GateInitCommand(clonePath))
	}
	if strings.Contains(text, notGitRepoMarker) {
		return nil, fmt.Errorf("no-mistakes clone path is not a git repository: %s", clonePath)
	}
	if err != nil {
		return nil, fmt.Errorf("no-mistakes binary not found or not runnable: %w", err)
	}
	return parseGateRunIDs(string(stdout))
}

func parseGateRunIDs(output string) (GateRunIDs, error) {
	if !strings.HasSuffix(output, "\n") {
		return nil, fmt.Errorf("no-mistakes runs returned incomplete output")
	}
	if output == "\n" {
		return nil, fmt.Errorf("no-mistakes runs returned empty output")
	}
	if output == "  no runs yet. Push through the gate to start a pipeline:\n  git push no-mistakes <branch>\n" {
		return GateRunIDs{}, nil
	}
	runs := GateRunIDs{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 6 && len(fields) != 7 {
			return nil, fmt.Errorf("malformed no-mistakes runs row %q", line)
		}
		if !validGateRunStatus(fields[0]) || !validShortSHA(fields[2]) {
			return nil, fmt.Errorf("malformed no-mistakes runs row %q", line)
		}
		if _, err := time.Parse("2006-01-02 15:04", fields[3]+" "+fields[4]); err != nil {
			return nil, fmt.Errorf("malformed no-mistakes runs timestamp: %w", err)
		}
		id, ok := strings.CutPrefix(fields[5], "id:")
		if !ok || !validGateRunID(id) || seen[id] {
			return nil, fmt.Errorf("malformed or duplicate no-mistakes run ID in %q", line)
		}
		seen[id] = true
		if len(fields) == 7 {
			if !strings.HasPrefix(fields[6], "https://") {
				return nil, fmt.Errorf("malformed no-mistakes PR URL in %q", line)
			}
			runs[fields[6]] = append(runs[fields[6]], id)
		}
	}
	return runs, nil
}

func validGateRunStatus(status string) bool {
	switch status {
	case "pending", "running", "completed", "failed", "cancelled", "ci_monitor_interrupted":
		return true
	default:
		return false
	}
}

func validShortSHA(sha string) bool {
	if len(sha) != 8 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func validGateRunID(id string) bool {
	if len(id) != 26 {
		return false
	}
	for _, c := range id {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", c) {
			return false
		}
	}
	return true
}
