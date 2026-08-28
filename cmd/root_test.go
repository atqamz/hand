package cmd

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/selfupdate"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/steering"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

func devBuild(version string) selfupdate.BuildInfo {
	return selfupdate.BuildInfo{Version: version, Channel: selfupdate.ChannelDev}
}

func stableBuild(version string) selfupdate.BuildInfo {
	return selfupdate.BuildInfo{Version: version, Channel: selfupdate.ChannelStable}
}

func exitCodeFor(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return 1
}

func TestUsageArgsTagsMismatchAsExitCode2(t *testing.T) {
	validate := usageArgs(cobra.ExactArgs(2))
	err := validate(&cobra.Command{}, []string{"onlyone"})
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestUsageArgsPassesThroughValidArgs(t *testing.T) {
	validate := usageArgs(cobra.ExactArgs(2))
	if err := validate(&cobra.Command{}, []string{"a", "b"}); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

func TestRootRejectsUnknownCommand(t *testing.T) {
	root := newRootCmd(devBuild("test"))
	root.SetArgs([]string{"bogus-command"})
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	found, err := root.ExecuteC()
	if err == nil {
		t.Fatal("want error for unknown command")
	}
	if found != root {
		t.Fatalf("found = %v, want root itself", found.Name())
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("err = %v, want unknown command", err)
	}
}

func TestGroupRejectsUnknownSubcommand(t *testing.T) {
	for _, group := range []string{"project", "completion"} {
		t.Run(group, func(t *testing.T) {
			root := newRootCmd(devBuild("test"))
			root.SetArgs([]string{group, "bogus-subcommand"})
			root.SetOut(new(strings.Builder))
			root.SetErr(new(strings.Builder))
			_, err := root.ExecuteC()
			if code := exitCodeFor(t, err); code != 2 {
				t.Fatalf("code = %d, want 2 (err = %v)", code, err)
			}
			if !strings.Contains(err.Error(), "unknown command") {
				t.Fatalf("err = %v, want unknown command", err)
			}
		})
	}
}

func TestGroupBareInvocationShowsHelpWithoutError(t *testing.T) {
	root := newRootCmd(devBuild("test"))
	root.SetArgs([]string{"project"})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(new(strings.Builder))
	if _, err := root.ExecuteC(); err != nil {
		t.Fatalf("got %v, want nil (bare group shows help)", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("out = %q, want usage text", out.String())
	}
}

func TestHelpFormsIgnoreBrokenFleetRegistries(t *testing.T) {
	fixtures := []struct {
		name  string
		setup func(t *testing.T, home, path string)
	}{
		{name: "missing", setup: removeHelpRegistry},
		{name: "corrupt", setup: corruptHelpRegistry},
		{name: "unrelated ambiguity", setup: unrelatedAmbiguousHelpRegistry},
		{name: "selected Fleet ambiguity", setup: selectedAmbiguousHelpRegistry},
	}
	forms := []struct {
		name string
		args []string
	}{
		{name: "root long flag", args: []string{"--help"}},
		{name: "root short flag", args: []string{"-h"}},
		{name: "help command", args: []string{"help"}},
		{name: "help command target", args: []string{"help", "project"}},
		{name: "command long flag", args: []string{"project", "--help"}},
		{name: "command short flag", args: []string{"project", "-h"}},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			home := setupSessionHome(t)
			t.Setenv("SECONDHAND_HOME", filepath.Join(t.TempDir(), "Secondhand help test"))
			path, err := registry.Path()
			if err != nil {
				t.Fatal(err)
			}
			fixture.setup(t, home, path)
			before, beforeErr := os.ReadFile(path)

			for _, form := range forms {
				t.Run(form.name, func(t *testing.T) {
					out, errOut, err := executeRootForTest(t, devBuild("test"), nil, form.args...)
					if err != nil {
						t.Fatalf("help returned %v, stdout=%q stderr=%q", err, out, errOut)
					}
					if !strings.Contains(out, "Usage:") {
						t.Fatalf("stdout = %q, want Cobra usage output", out)
					}
					if errOut != "" {
						t.Fatalf("stderr = %q, want no registry preflight output", errOut)
					}
				})
			}

			after, afterErr := os.ReadFile(path)
			if !sameRegistrySnapshot(before, beforeErr, after, afterErr) {
				t.Fatalf("registry changed during help: before=(%q, %v), after=(%q, %v)", before, beforeErr, after, afterErr)
			}
		})
	}
}

func TestFleetCommandKeepsRegistryPreflight(t *testing.T) {
	fixtures := []struct {
		name        string
		setup       func(t *testing.T, home, path string)
		wantFailure bool
	}{
		{name: "missing", setup: removeHelpRegistry},
		{name: "corrupt", setup: corruptHelpRegistry, wantFailure: true},
		{name: "unrelated ambiguity", setup: unrelatedAmbiguousHelpRegistry},
		{name: "selected Fleet ambiguity", setup: selectedAmbiguousHelpRegistry, wantFailure: true},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			home := setupSessionHome(t)
			t.Setenv("SECONDHAND_HOME", filepath.Join(t.TempDir(), "Secondhand command test"))
			path, err := registry.Path()
			if err != nil {
				t.Fatal(err)
			}
			fixture.setup(t, home, path)

			_, _, err = executeRootForTest(t, devBuild("test"), nil, "config", "profile", "list")
			if fixture.wantFailure {
				if err == nil {
					t.Fatal("got nil error, want the existing Fleet preflight refusal")
				}
				return
			}
			if err != nil {
				t.Fatalf("got %v, want existing preflight behavior for this fixture", err)
			}
		})
	}
}

func removeHelpRegistry(t *testing.T, _, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func corruptHelpRegistry(t *testing.T, _, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func unrelatedAmbiguousHelpRegistry(t *testing.T, _, path string) {
	t.Helper()
	firstHome := filepath.Join(t.TempDir(), "first")
	secondHome := filepath.Join(t.TempDir(), "second")
	firstID := createHelpFleet(t, firstHome)
	secondID := createHelpFleet(t, secondHome)
	claimedHome := filepath.Join(t.TempDir(), "unrelated")
	insertHelpRegistryClaim(t, path, claimedHome, firstID)
	insertHelpRegistryClaim(t, path, claimedHome, secondID)
}

func selectedAmbiguousHelpRegistry(t *testing.T, home, path string) {
	t.Helper()
	currentID, err := state.FleetID(home)
	if err != nil {
		t.Fatal(err)
	}
	otherID := createHelpFleet(t, filepath.Join(t.TempDir(), "other"))
	registryDB, err := registry.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(home, currentID, time.Now().UTC()); err != nil {
		_ = registryDB.Close()
		t.Fatal(err)
	}
	if err := registryDB.Close(); err != nil {
		t.Fatal(err)
	}
	insertHelpRegistryClaim(t, path, home, otherID)
}

func insertHelpRegistryClaim(t *testing.T, path, home, fleetID string) {
	t.Helper()
	registryDB, err := registry.OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: filepath.ToSlash(path)}).EscapedPath()+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
INSERT INTO fleet_registry (fleet_id, last_known_home, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?);`, fleetID, home, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO fleet_locator (fleet_id, home, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?);`, fleetID, home, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func createHelpFleet(t *testing.T, home string) string {
	t.Helper()
	db, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.FleetID()
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func sameRegistrySnapshot(before []byte, beforeErr error, after []byte, afterErr error) bool {
	if os.IsNotExist(beforeErr) || os.IsNotExist(afterErr) {
		return os.IsNotExist(beforeErr) && os.IsNotExist(afterErr)
	}
	return beforeErr == nil && afterErr == nil && bytes.Equal(before, after)
}

func TestRootRejectsBadArgCount(t *testing.T) {
	root := newRootCmd(devBuild("test"))
	root.SetArgs([]string{"spawn", "onlyonearg"})
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	_, err := root.ExecuteC()
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func TestRootRejectsUnknownFlag(t *testing.T) {
	root := newRootCmd(devBuild("test"))
	root.SetArgs([]string{"spawn", "--bogus", "a", "b"})
	root.SetOut(new(strings.Builder))
	root.SetErr(new(strings.Builder))
	_, err := root.ExecuteC()
	if code := exitCodeFor(t, err); code != 2 {
		t.Fatalf("code = %d, want 2 (err = %v)", code, err)
	}
}

func runBareRoot(t *testing.T) string {
	t.Helper()
	root := newRootCmd(devBuild("test"))
	root.SetArgs([]string{})
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(new(strings.Builder))
	if _, err := root.ExecuteC(); err != nil {
		t.Fatalf("got %v, want nil (the bare command reports, it does not refuse)", err)
	}
	return out.String()
}

func TestBareInvocationLeadsWithTheFleetItManages(t *testing.T) {
	home := setupSessionHome(t)
	t.Setenv("HAND_HARNESS", harness.Claude)
	if err := state.Write(home, state.Task{ID: "task-1", Project: "myproj", Kind: state.KindShip}); err != nil {
		t.Fatal(err)
	}

	out := runBareRoot(t)
	for _, want := range []string{
		"session_bootstrap: complete\n",
		"tool: hand\n",
		"version: test\n",
		"count: 1\n",
		"held: 0\n",
		"  task-1,",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("out = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "Usage:") {
		t.Fatalf("out = %q, want the fleet rather than a help dump", out)
	}
}

// The bare command is the session hook, so it is the only place a supervising agent learns that the
// fleet is not configured yet, and the only place the question can be put in front of the operator.
func TestBareInvocationReportsConfigurationStateAndAsksTheOperator(t *testing.T) {
	setupSessionHome(t)
	t.Setenv("HAND_HARNESS", "unknown")

	out := runBareRoot(t)
	for _, want := range []string{
		"config_missing: 1\n",
		"config[3]{key,state,value}:\n",
		"harness,missing,none",
		"Ask the operator which harness",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("out = %q, want it to contain %q", out, want)
		}
	}

	if _, err := runConfigSet(t, settingHarness, "grok"); err != nil {
		t.Fatal(err)
	}
	out = runBareRoot(t)
	for _, want := range []string{
		"config_missing: 0\n",
		"harness,configured,grok",
		"model,unsupported,none",
		"effort,unsupported,none",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("out = %q, want it to contain %q", out, want)
		}
	}
	if strings.Contains(out, "Ask the operator") {
		t.Fatalf("out = %q, want no configuration question once nothing applicable is missing", out)
	}
}

// Since the #353 split, bare hand keeps the ordinary fleet overview while
// `hand session start` is one-time runtime bootstrap: the two documents are
// deliberately different answers to different questions.
func TestBareInvocationInHomeRendersTheFleetOverviewNotBootstrap(t *testing.T) {
	setupSessionHome(t)
	out := runBareRoot(t)
	for _, want := range []string{
		"orientation_schema: hand.supervisor.v1\n",
		"tasks[0]{id,state,reported,age,flags}:\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("out = %q, want the fleet overview to contain %q", out, want)
		}
	}
	if strings.Contains(out, "next_command: hand orient\n") {
		t.Fatalf("out = %q, want the overview not to answer the bootstrap question", out)
	}

	started := runSessionStartForTest(t)
	if !strings.Contains(started, "session_bootstrap: complete\n") || !strings.Contains(started, "next_command: hand orient\n") {
		t.Fatalf("session start = %q, want the bootstrap contract", started)
	}
	if started == out {
		t.Fatal("bare hand rendered the same document as hand session start")
	}
}

func TestBareInvocationRefusesWorkerRoleBeforeReadingContext(t *testing.T) {
	home := setupSessionHome(t)
	if err := os.Remove(filepath.Join(home, "data", "operator.md")); err != nil {
		t.Fatal(err)
	}
	t.Setenv(harness.RoleEnv, harness.WorkerRole)

	_, _, err := executeRootForTest(t, devBuild("test"), nil)
	assertExitCode(t, err, 3)
	if want := "supervisor session bootstrap is unavailable when HAND_ROLE=worker"; !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

func TestBareInvocationOutsideAFleetHomeSaysSoAndNamesTheWayIn(t *testing.T) {
	t.Setenv("HAND_HARNESS", harness.Claude)
	t.Setenv(harness.RoleEnv, "")
	t.Chdir(t.TempDir())
	t.Setenv("HAND_HOME", "")

	out := runBareRoot(t)
	if !strings.Contains(out, "home: none\n") {
		t.Fatalf("out = %q, want it to state that there is no fleet home", out)
	}
	if !strings.Contains(out, "`hand init`") {
		t.Fatalf("out = %q, want it to name hand init", out)
	}
	if strings.Contains(out, "count:") {
		t.Fatalf("out = %q, want no fleet blocks with no fleet to report", out)
	}
}

func TestErrorDocumentNamesTheKindBehindEveryExitCode(t *testing.T) {
	for _, tc := range []struct {
		code int
		kind string
	}{
		{1, "general"},
		{2, "usage"},
		{3, "precondition"},
		{4, "no-event"},
		{5, "arm-failed"},
		{6, "send-not-submitted"},
		{7, "send-uncertain"},
		{8, "watch-interrupted"},
		{9, "watch-replaced"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			var out strings.Builder
			if err := renderError(&out, errors.New("something went wrong"), tc.code, "hand spawn"); err != nil {
				t.Fatal(err)
			}
			want := "error: something went wrong\nkind: " + tc.kind + "\nexit: " + strconv.Itoa(tc.code) + "\n"
			if !strings.HasPrefix(out.String(), want) {
				t.Fatalf("error document = %q, want it to start with %q", out.String(), want)
			}
		})
	}
}

// atqamz/hand#460: the refusal's help line names both ways forward rather than the generic
// precondition filler, per docs/adr/every-diagnosis-names-a-reachable-treatment.md.
func TestErrorDocumentNamesBothWaysForwardForAnAmbiguousHome(t *testing.T) {
	var out strings.Builder
	err := fmt.Errorf(
		"%w: HAND_HOME is %q, the working directory is inside %q; unset HAND_HOME to act on the working directory's home, or run from outside %q to act on HAND_HOME's",
		home.ErrAmbiguousHome, "/live", "/scratch", "/scratch")
	if renderErr := renderError(&out, err, 3, "hand status"); renderErr != nil {
		t.Fatal(renderErr)
	}
	for _, want := range []string{"kind: precondition\n", "exit: 3\n", "Unset HAND_HOME", "run this command from outside"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("error document = %q, want %q", out.String(), want)
		}
	}
	if strings.Contains(out.String(), "Nothing changed: this refuses until the state it names is fixed") {
		t.Fatalf("error document = %q, want the specific two-way help, not the generic precondition filler", out.String())
	}
}

func TestErrorDocumentIncludesSendStateDetails(t *testing.T) {
	var out strings.Builder
	err := &steering.Error{
		Cause:     errors.New("text outcome is ambiguous"),
		Send:      &state.SendAttempt{ID: 7},
		AttemptID: 42,
		State:     state.SendUncertain,
		Reason:    "text-outcome-ambiguous",
	}
	if renderErr := renderError(&out, err, 7, "hand send"); renderErr != nil {
		t.Fatal(renderErr)
	}
	for _, want := range []string{
		"send_id: 7\n",
		"attempt: 42\n",
		"send_state: uncertain\n",
		"reason: text-outcome-ambiguous\n",
		"do not blindly retry",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("error document = %q, want %q", out.String(), want)
		}
	}
}

func TestUsageErrorHelpNamesTheCommandThatRefused(t *testing.T) {
	var out strings.Builder
	if err := renderError(&out, errors.New("accepts 2 arg(s), received 1"), 2, "hand hold set"); err != nil {
		t.Fatal(err)
	}
	want := "help[1]:\n  - Run `hand hold set --help` for the arguments and flags this command accepts\n"
	if !strings.HasSuffix(out.String(), want) {
		t.Fatalf("error document = %q, want it to end with %q", out.String(), want)
	}
}

func TestLifecycleHelpDescribesInterruptionAndReplacementFacts(t *testing.T) {
	interrupted := strings.Join(errorHelp(8, "hand watch"), " ")
	for _, want := range []string{"generic interruption", "no fleet event", "releases ownership", "re-armed"} {
		if !strings.Contains(interrupted, want) {
			t.Fatalf("exit 8 help = %q, want %q", interrupted, want)
		}
	}
	if strings.Contains(interrupted, "nothing was taken over") || strings.Contains(interrupted, "still holds") {
		t.Fatalf("exit 8 help = %q, contains an obsolete ownership claim", interrupted)
	}

	replaced := strings.Join(errorHelp(9, "hand watch"), " ")
	for _, want := range []string{"explicitly displaced", "no fleet event", "takeover successor", "acquires ownership"} {
		if !strings.Contains(replaced, want) {
			t.Fatalf("exit 9 help = %q, want %q", replaced, want)
		}
	}
	if strings.Contains(replaced, "launch another") {
		t.Fatalf("exit 9 help = %q, tells the displaced operator to launch another successor", replaced)
	}
}

// A general error is the one code with no recovery a caller can be told in
// advance, so it gets no help block rather than a line saying nothing.
func TestGeneralErrorCarriesNoHelpBlock(t *testing.T) {
	var out strings.Builder
	if err := renderError(&out, errors.New("write config/harness: disk full"), 1, "hand init"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "help[") {
		t.Fatalf("error document = %q, want no help block", out.String())
	}
}

func TestMultiLineErrorStaysOneField(t *testing.T) {
	var out strings.Builder
	joined := errors.Join(errors.New("write data/backlog.md: read-only"), errors.New("write data/operator.md: read-only"))
	if err := renderError(&out, joined, 1, "hand init"); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(out.String(), "\n"); lines != 3 {
		t.Fatalf("error document = %q, want its three fields on three lines", out.String())
	}
	if !strings.Contains(out.String(), `\n`) {
		t.Fatalf("error document = %q, want the embedded newline escaped", out.String())
	}
}
