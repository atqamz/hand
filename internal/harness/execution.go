package harness

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
)

// IsOneShot reports whether a Worker Harness exits after one autonomous turn.
// Supervisor capability is owned independently by internal/supervision.
func IsOneShot(name string) bool {
	return name == Antigravity
}

// SupportsSteering reports whether hand send can safely target the resident Worker.
// A one-shot pane returns to its shell after exit and therefore must never receive steering text.
func SupportsSteering(name string) bool {
	return IsSupported(name) && !IsOneShot(name)
}

// LaunchEvidenceInOutput recognizes machine-readable evidence that the exact one-shot launch
// initialized in expectedCwd; unscoped scrollback init cannot prove the current Attempt.
// Provider terminal result/status never substitutes for Hand's report outcome channel.
func LaunchEvidenceInOutput(name, text, expectedCwd string) bool {
	if name != Antigravity || expectedCwd == "" {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event struct {
			Event string `json:"event"`
			Init  struct {
				Cwd string `json:"cwd"`
			} `json:"init"`
		}
		if json.Unmarshal([]byte(line), &event) == nil &&
			event.Event == "init" &&
			sameLaunchPath(event.Init.Cwd, expectedCwd) {
			return true
		}
	}
	return false
}

func sameLaunchPath(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	got, want = filepath.Clean(got), filepath.Clean(want)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(got, want)
	}
	return got == want
}
