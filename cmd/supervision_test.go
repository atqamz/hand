package cmd

import (
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
