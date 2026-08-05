package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/harness"
)

func setupConfigHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	mkFleetDirs(t, home)
	t.Chdir(home)
	return home
}

func runConfigSet(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newConfigSetCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func mustConfigSet(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runConfigSet(t, args...)
	if err != nil {
		t.Fatalf("hand config set %v: %v", args, err)
	}
	return out
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != want {
		t.Fatalf("got %v, want ExitError code %d", err, want)
	}
}

func settingState(t *testing.T, home, key string) string {
	t.Helper()
	for _, s := range readWorkerConfig(home).settings {
		if s.key == key {
			return s.state
		}
	}
	t.Fatalf("no %q setting in the config report", key)
	return ""
}

// The state an unconfigured home reports is the whole first-run flow in one document: one question to
// answer, and two that are not questions yet because applicability follows the harness.
func TestConfigOnAnEmptyHomeAsksForTheHarnessFirst(t *testing.T) {
	setupConfigHome(t)

	cmd := newConfigCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	for _, want := range []string{
		"config_missing: 1\n",
		"harness,missing,none",
		"model,pending-harness,none",
		"effort,pending-harness,none",
		"hand config set harness <name>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("config output = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "hand config set model") {
		t.Fatalf("config output = %q, want no model question before a harness is chosen", got)
	}
}

// The matrix is read off internal/harness rather than restated, so this is the assertion that the
// reported applicability is the same fact hand launches with.
func TestConfigApplicabilityFollowsTheHarnessContract(t *testing.T) {
	home := setupConfigHome(t)

	for _, tc := range []struct {
		harness string
		model   string
		effort  string
	}{
		{harness.Claude, stateMissing, stateMissing},
		{harness.OpenCode, stateMissing, stateUnsupported},
		{harness.Codex, stateMissing, stateMissing},
		{harness.Grok, stateUnsupported, stateUnsupported},
		{harness.Pi, stateUnsupported, stateUnsupported},
	} {
		mustConfigSet(t, settingHarness, tc.harness)
		if got := settingState(t, home, settingModel); got != tc.model {
			t.Fatalf("%s model state = %q, want %q", tc.harness, got, tc.model)
		}
		if got := settingState(t, home, settingEffort); got != tc.effort {
			t.Fatalf("%s effort state = %q, want %q", tc.harness, got, tc.effort)
		}
		if harness.SupportsModel(tc.harness) != (tc.model != stateUnsupported) {
			t.Fatalf("%s: the expectation above disagrees with harness.SupportsModel", tc.harness)
		}
		if harness.SupportsEffort(tc.harness) != (tc.effort != stateUnsupported) {
			t.Fatalf("%s: the expectation above disagrees with harness.SupportsEffort", tc.harness)
		}
	}
}

func TestConfigSetPersistsUnderTheConfiguredHarness(t *testing.T) {
	home := setupConfigHome(t)

	mustConfigSet(t, settingHarness, harness.Claude)
	out := mustConfigSet(t, settingModel, "claude-opus-5")

	got, err := os.ReadFile(filepath.Join(home, "config", "model.claude"))
	if err != nil {
		t.Fatalf("config/model.claude missing after set: %v", err)
	}
	if string(got) != "claude-opus-5\n" {
		t.Fatalf("config/model.claude = %q, want %q", got, "claude-opus-5\n")
	}
	if _, err := os.Stat(filepath.Join(home, "config", "model")); err == nil {
		t.Fatal("an unkeyed config/model was written, which the next harness would inherit")
	}
	// The answer's own document carries the recheck, so a supervisor never has to guess what is left.
	for _, want := range []string{"file: config/model.claude", "model,configured,claude-opus-5", "config_missing: 1\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("set output = %q, want it to contain %q", out, want)
		}
	}
}

// The launch path has to read what `hand config set` wrote, or the fleet is configured with a value
// nothing dispatches.
func TestConfigSetFeedsTheResolvedTier(t *testing.T) {
	home := setupConfigHome(t)
	briefAbs := writeTierBrief(t, home, "# Title\n")

	mustConfigSet(t, settingHarness, harness.Claude)
	mustConfigSet(t, settingModel, "claude-opus-5")
	mustConfigSet(t, settingEffort, "high")

	cmd, _ := newTierTestCmd()
	model, effort, _, err := resolveTier(cmd, home, briefAbs, harness.Claude, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if model != "claude-opus-5" || effort != "high" {
		t.Fatalf("got model=%q effort=%q, want the persisted defaults", model, effort)
	}
}

func TestConfigSetRefusesModelBeforeAHarnessIsChosen(t *testing.T) {
	home := setupConfigHome(t)

	_, err := runConfigSet(t, settingModel, "claude-opus-5")
	assertExitCode(t, err, 3)
	if _, err := os.Stat(filepath.Join(home, "config", "model")); err == nil {
		t.Fatal("config/model was written despite the refusal")
	}
}

func TestConfigSetRefusesWhatTheHarnessCannotCarry(t *testing.T) {
	home := setupConfigHome(t)
	mustConfigSet(t, settingHarness, harness.Grok)

	for _, key := range []string{settingModel, settingEffort} {
		_, err := runConfigSet(t, key, "whatever")
		assertExitCode(t, err, 2)
		entries, dirErr := os.ReadDir(filepath.Join(home, "config"))
		if dirErr != nil {
			t.Fatal(dirErr)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), key) {
				t.Fatalf("%s was written for grok, which cannot use it", e.Name())
			}
		}
	}
}

// Keying is what makes a harness switch re-evaluate rather than inherit: an opencode worker must never be
// launched with the model identifier chosen for claude.
func TestConfigSwitchingHarnessNeverInheritsAnotherHarnessDefault(t *testing.T) {
	home := setupConfigHome(t)
	briefAbs := writeTierBrief(t, home, "# Title\n")

	mustConfigSet(t, settingHarness, harness.Claude)
	mustConfigSet(t, settingModel, "claude-opus-5")
	mustConfigSet(t, settingHarness, harness.OpenCode)

	if got := settingState(t, home, settingModel); got != stateMissing {
		t.Fatalf("opencode model state = %q, want %q", got, stateMissing)
	}
	cmd, _ := newTierTestCmd()
	model, _, _, err := resolveTier(cmd, home, briefAbs, harness.OpenCode, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if model != "" {
		t.Fatalf("opencode resolved model = %q, want claude's default not to reach it", model)
	}

	// Switching back finds the earlier answer again rather than having lost it.
	mustConfigSet(t, settingHarness, harness.Claude)
	if got := settingState(t, home, settingModel); got != stateConfigured {
		t.Fatalf("claude model state = %q, want %q", got, stateConfigured)
	}
}

// A configured value is not asked about again, and one the operator never answered stays visible without
// blocking anything else the session does.
func TestConfigRepeatSessionsOnlyAskForWhatIsStillMissing(t *testing.T) {
	home := setupConfigHome(t)
	mustConfigSet(t, settingHarness, harness.Claude)
	mustConfigSet(t, settingModel, "claude-opus-5")

	help := workerConfigHelp(readWorkerConfig(home))
	if len(help) != 1 || !strings.Contains(help[0], "hand config set effort") {
		t.Fatalf("help = %v, want only the declined effort question", help)
	}

	mustConfigSet(t, settingEffort, "high")
	if help := workerConfigHelp(readWorkerConfig(home)); len(help) != 0 {
		t.Fatalf("help = %v, want no questions once every applicable value is configured", help)
	}
}

func TestConfigSetRejectsUnknownKeysAndUnusableValues(t *testing.T) {
	setupConfigHome(t)

	for _, args := range [][]string{
		{"notify", "say hi"},
		{settingHarness, "sol"},
		{settingHarness, ""},
	} {
		_, err := runConfigSet(t, args...)
		assertExitCode(t, err, 2)
	}

	mustConfigSet(t, settingHarness, harness.Claude)
	for _, value := range []string{"", " ", "two words", " padded"} {
		_, err := runConfigSet(t, settingModel, value)
		assertExitCode(t, err, 2)
	}
}

// A pre-0.2.0 home carried config/model with no record of the harness it was chosen for. Read as-is it
// would become the default for whatever harness the home switches to next.
func TestMigrateWorkerSettingsKeysAnOlderHomesDefaults(t *testing.T) {
	home := setupConfigHome(t)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, value string) {
		if err := os.WriteFile(filepath.Join(home, "config", name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("harness", harness.Claude)
	write("model", "claude-sonnet-5")
	write("effort", "high")

	moved, err := migrateWorkerSettings(home)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(moved, ",") != "model,effort" {
		t.Fatalf("moved = %v, want model and effort", moved)
	}
	if got := settingState(t, home, settingModel); got != stateConfigured {
		t.Fatalf("model state = %q, want the migrated value to be read", got)
	}
	for _, name := range []string{"model", "effort"} {
		if _, err := os.Stat(filepath.Join(home, "config", name)); err == nil {
			t.Fatalf("config/%s is still unkeyed after the migration", name)
		}
	}
	if again, err := migrateWorkerSettings(home); err != nil || len(again) != 0 {
		t.Fatalf("second run moved %v (%v), want nothing left to move", again, err)
	}
}

// A home with no harness file still dispatches claude (hand spawn's fallback), so that is the harness its
// unkeyed value belonged to.
func TestMigrateWorkerSettingsKeysToClaudeWhenNoHarnessIsConfigured(t *testing.T) {
	home := setupConfigHome(t)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "model"), []byte("claude-sonnet-5\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := migrateWorkerSettings(home); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(home, "config", "model.claude"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "claude-sonnet-5" {
		t.Fatalf("config/model.claude = %q", got)
	}
}

func TestCommandStartupMigratesOlderWorkerDefaultsBeforeReportingConfig(t *testing.T) {
	home := setupConfigHome(t)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"harness": harness.Claude,
		"model":   "claude-sonnet-5",
		"effort":  "high",
	} {
		if err := os.WriteFile(filepath.Join(home, "config", name), []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	root := newRootCmd("test")
	root.SetArgs([]string{"config"})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	if _, err := root.ExecuteC(); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"model,configured,claude-sonnet-5",
		"effort,configured,high",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("config output = %q, want it to contain %q", out.String(), want)
		}
	}
	for _, name := range []string{"model", "effort"} {
		if _, err := os.Stat(filepath.Join(home, "config", name+".claude")); err != nil {
			t.Fatalf("migrated config/%s.claude: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(home, "config", name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("legacy config/%s still exists: %v", name, err)
		}
	}
}
