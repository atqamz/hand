package cmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/harness"
)

func setupSupervisionHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	mkFleetDirs(t, home)
	if err := initMarker(home); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HOME", home)
	t.Setenv("HAND_HARNESS", harness.Codex)
	t.Setenv(harness.RoleEnv, "")
	return home
}

func executeSupervision(t *testing.T, args ...string) (string, error) {
	t.Helper()
	_, _, err := executeRootForTest(t, devBuild("test"), nil, append([]string{"supervision"}, args...)...)
	return "", err
}

func TestSupervisorCommandsRejectWorkerRole(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"wait", []string{"wait", "--host", "codex"}, "supervision wait is unavailable when HAND_ROLE=worker"},
		{"claude-stop", []string{"claude-stop"}, "supervision claude-stop is unavailable when HAND_ROLE=worker"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setupSupervisionHome(t)
			t.Setenv(harness.RoleEnv, harness.WorkerRole)

			_, _, err := executeRootForTest(t, devBuild("test"), nil, append([]string{"supervision"}, test.args...)...)
			assertExitCode(t, err, 3)
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %q, want %q", err, test.want)
			}
		})
	}
}

func TestOrientRejectsWorkerRoleBeforeReadingTheFleet(t *testing.T) {
	setupSupervisionHome(t)
	t.Setenv(harness.RoleEnv, harness.WorkerRole)

	_, _, err := executeRootForTest(t, devBuild("test"), nil, "orient")
	assertExitCode(t, err, 3)
	if want := "hand orient is unavailable when HAND_ROLE=worker"; !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

func TestSupervisionWaitValidatesHostAndTimeout(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"unknown host", []string{"wait", "--host", "bogus"}, 2, `invalid --host "bogus"`},
		{"worker-capable name outside the supervisor registry", []string{"wait", "--host", "antigravity"}, 2, `invalid --host "antigravity"`},
		{"missing host", []string{"wait"}, 2, `invalid --host ""`},
		{"non-positive timeout", []string{"wait", "--host", "codex", "--timeout", "0s"}, 2, "must be positive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setupSupervisionHome(t)

			_, err := executeSupervision(t, test.args...)
			assertExitCode(t, err, test.code)
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %q, want %q", err, test.want)
			}
		})
	}
}

func TestSupervisionWaitIsHiddenFromTheCommandReference(t *testing.T) {
	root := newRootCmd(devBuild("test"))
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(new(strings.Builder))
	if err := root.Help(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "supervision") {
		t.Fatalf("help = %q, want the internal-facing group hidden", out.String())
	}
}

// hand init owns every static integration byte; session start is read-only
// qualification. A home with foreign content at managed paths reports the
// conflict and degradation without touching a single byte.
func TestSessionStartNeverMutatesStaticIntegration(t *testing.T) {
	for _, test := range []struct {
		name       string
		harness    string
		wantStatus string
	}{
		{"codex without integration", harness.Codex, "bootstrap_integration_status: absent\n"},
		{"opencode with foreign plugin", harness.OpenCode, "bootstrap_integration_status: conflict\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := setupSupervisionHome(t)
			t.Setenv("HAND_HARNESS", test.harness)
			foreign := "// an operator's own plugin\n"
			writeFile(t, filepath.Join(home, ".opencode", "plugins", "hand-supervisor-wake.js"), foreign)
			writeFile(t, filepath.Join(home, ".pi", "extensions", "hand-supervisor-wake.ts"), "// operator extension\n")
			writeFile(t, filepath.Join(home, ".claude", "settings.json"), "{custom: true}\n")
			writeFile(t, filepath.Join(home, ".codex", "hooks.json"), "{\"hooks\":{}}\n")
			before := snapshotDir(t, home)

			out, err := executeSessionStart(t, nil)
			if err != nil {
				t.Fatalf("session start = %v, want a completed read-only bootstrap", err)
			}
			if !strings.Contains(out, "session_bootstrap: complete\n") || !strings.Contains(out, test.wantStatus) {
				t.Fatalf("out = %q, want honest non-mutating qualification", out)
			}
			if after := snapshotDir(t, home); !slices.Equal(after, before) {
				t.Fatalf("session start mutated static integration:\nbefore: %v\nafter:  %v", before, after)
			}
			if !strings.Contains(out, "hand init") {
				t.Fatalf("out = %q, want the init recovery action", out)
			}
		})
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func snapshotDir(t *testing.T, root string) []string {
	t.Helper()
	var snapshot []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		entry := rel
		if info.Mode().IsRegular() {
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			entry += " " + fmt.Sprintf("%x", sha256.Sum256(body))
		}
		snapshot = append(snapshot, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// hand init is the exclusive repair path: a Hand-owned stale asset is
// replaced, while a foreign file at the exact managed path fails init with a
// precondition and stays byte-for-byte untouched.
func TestInitExclusivelyOwnsStaticIntegrationRepair(t *testing.T) {
	home := t.TempDir()
	mkFleetDirs(t, home)
	if err := initMarker(home); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HOME", home)
	t.Setenv("HAND_HARNESS", harness.Codex)
	t.Setenv(harness.RoleEnv, "")

	stale := "// Hand-owned supervisor wake integration (stale)\noutdated body\n"
	writeFile(t, filepath.Join(home, ".pi", "extensions", "hand-supervisor-wake.ts"), stale)

	root := newRootCmd(devBuild("test"))
	root.SetArgs([]string{"init", home})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(new(bytes.Buffer))
	if _, err := root.ExecuteC(); err != nil {
		t.Fatalf("init over owned-stale asset: %v", err)
	}
	repaired, err := os.ReadFile(filepath.Join(home, ".pi", "extensions", "hand-supervisor-wake.ts"))
	if err != nil || !bytes.Contains(repaired, []byte("hand.supervision.wake.v1")) {
		t.Fatalf("stale asset not replaced with the canonical extension: %q, %v", repaired, err)
	}

	foreign := "// an operator's own extension; do not touch\n"
	writeFile(t, filepath.Join(home, ".opencode", "plugins", "hand-supervisor-wake.js"), foreign)

	root = newRootCmd(devBuild("test"))
	root.SetArgs([]string{"init", home})
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	_, initErr := root.ExecuteC()
	assertExitCode(t, initErr, 3)
	kept, readErr := os.ReadFile(filepath.Join(home, ".opencode", "plugins", "hand-supervisor-wake.js"))
	if readErr != nil || string(kept) != foreign {
		t.Fatalf("foreign file changed by refused init: %q, %v", kept, readErr)
	}
}

// Hand integration is Fleet-local by construction: initializing a fleet home
// must leave no hook, plugin, or extension in any global harness directory.
func TestInitWritesNoGlobalHarnessConfiguration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME isolation asserted on unix runners")
	}
	globalHome := t.TempDir()
	fleet := t.TempDir()
	t.Setenv("HOME", globalHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(globalHome, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(globalHome, ".local", "state"))
	t.Setenv("HAND_HARNESS", harness.Codex)
	t.Setenv(harness.RoleEnv, "")
	t.Chdir(fleet)

	root := newRootCmd(devBuild("test"))
	root.SetArgs([]string{"init"})
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	if _, err := root.ExecuteC(); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, banned := range []string{".claude/settings.json", ".opencode", ".pi", ".codex/hooks.json"} {
		if _, err := os.Stat(filepath.Join(globalHome, filepath.Dir(banned), filepath.Base(banned))); !os.IsNotExist(err) {
			t.Fatalf("global surface %s exists under the isolated HOME (%v); integration leaked out of the fleet home", banned, err)
		}
	}
}
