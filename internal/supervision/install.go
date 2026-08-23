package supervision

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/sessionhook"
)

// OwnershipMarker is the first line every managed host asset starts with. A
// destination file without it is foreign content at a Hand-managed path, which
// is a conflict to surface, never permission to overwrite.
const OwnershipMarker = "// Hand-owned supervisor wake integration"

// ClaudeStopHookArgs is the argv after the executable that the Hand-owned
// Claude Stop hook runs.
const ClaudeStopHookArgs = "supervision claude-stop"

// InstallResult is one managed surface's outcome.
type InstallResult struct {
	Host   string
	Path   string
	State  string // installed, unchanged, replaced, conflict, absent
	Detail string
}

// InstallClaudeStopHook merges the Hand-owned Stop hook into the fleet home's
// .claude/settings.json through the conflict-safe sessionhook posture, then
// reports the resulting state.
func InstallClaudeStopHook(home, exe string) (InstallResult, error) {
	changed, err := sessionhook.Ensure(home, exe, "Stop", ClaudeStopHookArgs)
	if err != nil {
		return InstallResult{Host: "claude", Path: filepath.Join(home, ".claude", "settings.json"), State: "conflict", Detail: err.Error()}, err
	}
	state, err := sessionhook.State(home, exe, "Stop", ClaudeStopHookArgs)
	if err != nil {
		return InstallResult{Host: "claude", Path: filepath.Join(home, ".claude", "settings.json")}, err
	}
	if changed {
		return InstallResult{Host: "claude", Path: filepath.Join(home, ".claude", "settings.json"), State: "installed"}, nil
	}
	return InstallResult{Host: "claude", Path: filepath.Join(home, ".claude", "settings.json"), State: "unchanged", Detail: state}, nil
}

// CheckClaudeStopHook reports the Stop hook state without writing anything.
func CheckClaudeStopHook(home, exe string) (InstallResult, error) {
	state, err := sessionhook.State(home, exe, "Stop", ClaudeStopHookArgs)
	if err != nil {
		return InstallResult{Host: "claude", State: "conflict", Detail: err.Error()}, err
	}
	return InstallResult{Host: "claude", Path: filepath.Join(home, ".claude", "settings.json"), State: state}, nil
}

// Substitutes install-time paths into an asset template. Both are JSON-quoted
// so the generated JS/TS stays syntactically valid for any path, including
// spaces and Unicode.
func renderAsset(a Asset, exe, home string) []byte {
	body := bytes.ReplaceAll(a.Body, []byte("__HAND_EXECUTABLE__"), []byte(quoteJS(exe)))
	return bytes.ReplaceAll(body, []byte("__HAND_HOME__"), []byte(quoteJS(home)))
}

func quoteJS(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`).Replace(value) + `"`
}

// CheckHostAssets reports managed asset states for one host without writing.
// exe and home are the install-time substitutions canonical bytes are judged
// against.
func CheckHostAssets(home, host, exe string) ([]InstallResult, error) {
	var results []InstallResult
	for _, a := range HostAssets(host) {
		path := filepath.Join(home, a.RelPath)
		existing, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			results = append(results, InstallResult{Host: host, Path: path, State: "absent"})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", a.RelPath, err)
		}
		results = append(results, InstallResult{Host: host, Path: path, State: assetState(existing, renderAsset(a, exe, home))})
	}
	return results, nil
}

// Compares existing bytes to the canonical rendered shape, with the ownership
// marker as the ownership proof: a marked file that no longer matches is a
// stale Hand version, an unmarked file is foreign content.
func assetState(existing, canonical []byte) string {
	if !bytes.HasPrefix(bytes.TrimSpace(existing), []byte(OwnershipMarker)) {
		return "conflict"
	}
	if bytes.Equal(existing, canonical) {
		return "unchanged"
	}
	return "stale"
}

// InstallHostAssets writes the managed assets for one host, replacing only
// Hand-owned stale versions and refusing foreign content at exact managed
// paths. Canonical files stay untouched, so repeated init changes no bytes.
func InstallHostAssets(home, host, exe string) ([]InstallResult, error) {
	var results []InstallResult
	for _, a := range HostAssets(host) {
		path := filepath.Join(home, a.RelPath)
		result := InstallResult{Host: host, Path: path}
		existing, err := os.ReadFile(path)
		switch {
		case os.IsNotExist(err):
			result.State = "installed"
		case err != nil:
			return nil, fmt.Errorf("read %s: %w", a.RelPath, err)
		default:
			switch assetState(existing, renderAsset(a, exe, home)) {
			case "conflict":
				result.State = "conflict"
				result.Detail = "a foreign file occupies the Hand-managed path; move it aside, then run hand init again"
				results = append(results, result)
				continue
			case "unchanged":
				result.State = "unchanged"
				results = append(results, result)
				continue
			default:
				result.State = "replaced"
			}
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", filepath.Dir(a.RelPath), err)
		}
		if err := atomicfile.Write(path, ".hand-supervisor-wake-", renderAsset(a, exe, home), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", a.RelPath, err)
		}
		results = append(results, result)
	}
	return results, nil
}
