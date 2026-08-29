package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/agentsmd"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/routing"
	"github.com/atqamz/hand/internal/selfupdate"
	"github.com/atqamz/hand/internal/skill"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/supervision"
	"github.com/atqamz/hand/internal/toolchain"
)

// The doctor half of hand#440: a bundle that is otherwise intact but cannot clone https:// is
// reported with the ssh treatment, and never joins blocking or flips runtime_ready, so an
// ssh-only fleet home never reads as unready over a gap it has no way to close itself.
func TestRuntimeHTTPSFindingsNamesTheSSHTreatmentWithoutJoiningBlocking(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       toolchain.Status
		wantFindings int
	}{
		{name: "ready and https-ready reports nothing", status: toolchain.Status{Ready: true, GitHTTPSReady: true}, wantFindings: 0},
		{name: "ready but https-unready warns with the treatment", status: toolchain.Status{Ready: true, GitHTTPSReady: false}, wantFindings: 1},
		{name: "not ready at all adds nothing on top of the runtime blocking entry", status: toolchain.Status{Ready: false, GitHTTPSReady: false}, wantFindings: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings := runtimeHTTPSFindings(tc.status)
			if len(findings) != tc.wantFindings {
				t.Fatalf("runtimeHTTPSFindings(%+v) = %+v, want %d finding(s)", tc.status, findings, tc.wantFindings)
			}
			if tc.wantFindings == 0 {
				return
			}
			finding := findings[0]
			if finding.Severity != doctorWarning {
				t.Fatalf("finding severity = %q, want %q so it never blocks fleet-wide readiness", finding.Severity, doctorWarning)
			}
			for _, want := range []string{"git-remote-https", "ssh remote form", "git@host:owner/repo.git"} {
				if !strings.Contains(finding.Text, want) {
					t.Fatalf("finding text = %q, want it to mention %q", finding.Text, want)
				}
			}
		})
	}
}

func TestDoctorFindingsCoverFleetHealth(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, home string)
		want  []doctorFinding
	}{
		{
			name: "clean fleet",
			setup: func(t *testing.T, home string) {
				t.Helper()
				if _, err := agentsmd.Refresh(home); err != nil {
					t.Fatal(err)
				}
				if _, err := skill.Refresh(home); err != nil {
					t.Fatal(err)
				}
				mustConfigSet(t, settingHarness, harness.Claude)
			},
			want: []doctorFinding{{Severity: doctorInfo, Text: `routing resolves through explicit legacy defaults: harness "claude"`}},
		},
		{
			name: "unreachable gate",
			setup: func(t *testing.T, home string) {
				t.Helper()
				if _, err := agentsmd.Refresh(home); err != nil {
					t.Fatal(err)
				}
				if _, err := skill.Refresh(home); err != nil {
					t.Fatal(err)
				}
				if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", t.TempDir())
			},
			want: []doctorFinding{{Severity: doctorError, Text: `project "gated" no-mistakes gate is unreachable`}},
		},
		{
			name: "uninitialized gate",
			setup: func(t *testing.T, home string) {
				t.Helper()
				if _, err := agentsmd.Refresh(home); err != nil {
					t.Fatal(err)
				}
				if _, err := skill.Refresh(home); err != nil {
					t.Fatal(err)
				}
				if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", fakeNoMistakesPath(t, "repo not initialized"))
			},
			want: []doctorFinding{{Severity: doctorError, Text: `project "gated" no-mistakes gate is not initialized`}},
		},
		{
			name: "explicit legacy intent",
			setup: func(t *testing.T, home string) {
				t.Helper()
				if _, err := agentsmd.Refresh(home); err != nil {
					t.Fatal(err)
				}
				if _, err := skill.Refresh(home); err != nil {
					t.Fatal(err)
				}
				mustConfigSet(t, settingHarness, harness.Claude)
			},
			want: []doctorFinding{{Severity: doctorInfo, Text: `routing resolves through explicit legacy defaults: harness "claude"`}},
		},
		{
			name: "unstated legacy fallback",
			setup: func(t *testing.T, home string) {
				t.Helper()
				if _, err := agentsmd.Refresh(home); err != nil {
					t.Fatal(err)
				}
				if _, err := skill.Refresh(home); err != nil {
					t.Fatal(err)
				}
			},
			want: []doctorFinding{{Severity: doctorWarning, Text: `routing falls back to legacy defaults without explicit intent: harness "claude"`}},
		},
		{
			name: "partial routing config",
			setup: func(t *testing.T, home string) {
				t.Helper()
				if _, err := agentsmd.Refresh(home); err != nil {
					t.Fatal(err)
				}
				if _, err := skill.Refresh(home); err != nil {
					t.Fatal(err)
				}
				if err := routing.WriteProfile(home, routing.Profile{Name: "daily", Harness: harness.Claude}); err != nil {
					t.Fatal(err)
				}
				if err := routing.WriteRoute(home, routing.Route{Kind: routing.TaskKindScout, ExecutionClass: routing.ExecutionClassStandard, Profile: "daily"}); err != nil {
					t.Fatal(err)
				}
			},
			want: []doctorFinding{{Severity: doctorWarning, Text: "routing drift: route scout.mechanical is not configured"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Chdir(home)
			mkFleetDirs(t, home)
			t.Setenv("HAND_HARNESS", harness.Claude)
			tt.setup(t, home)

			findings, err := doctorFindings(home, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !hasDoctorFinding(findings, want) {
					t.Fatalf("findings = %#v, want %#v", findings, want)
				}
			}
		})
	}
}

func TestDoctorWarnsForOpenBriefWithoutReportPath(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	missingPath := filepath.Join(home, "data", "missing", "brief.md")
	declaredPath := filepath.Join(home, "data", "declared", "brief.md")
	suffixPath := filepath.Join(home, "data", "suffix", "brief.md")
	directoryPath := filepath.Join(home, "data", "directory", "brief.md")
	for _, path := range []string{missingPath, declaredPath, suffixPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(directoryPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(missingPath, []byte("do the work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	declaredReportPath := state.ReportPath(home, "declared")
	if err := os.WriteFile(declaredPath, []byte("append reports to "+declaredReportPath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	suffixReportPath := state.ReportPath(home, "suffix")
	if err := os.WriteFile(suffixPath, []byte("append reports to "+suffixReportPath+".bak\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, task := range []state.Task{
		{ID: "missing", Brief: "data/missing/brief.md", Lifecycle: state.TaskOpen},
		{ID: "declared", Brief: "data/declared/brief.md", Lifecycle: state.TaskOpen},
		{ID: "suffix", Brief: "data/suffix/brief.md", Lifecycle: state.TaskOpen},
		{ID: "directory", Brief: "data/directory/brief.md", Lifecycle: state.TaskOpen},
		{ID: "unreadable", Brief: "data/unreadable\x00/brief.md", Lifecycle: state.TaskOpen},
	} {
		if err := state.CreateTask(home, task); err != nil {
			t.Fatal(err)
		}
	}

	histories, err := state.ListOpenHistoriesReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := doctorFindings(".", histories)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDoctorFinding(findings, doctorFinding{Severity: doctorWarning, Text: `task "missing" brief does not declare report path`}) {
		t.Fatalf("findings = %#v, want missing report path warning", findings)
	}
	if hasDoctorFinding(findings, doctorFinding{Severity: doctorWarning, Text: `task "declared" brief does not declare report path`}) {
		t.Fatalf("findings = %#v, declared report path should not warn", findings)
	}
	for _, id := range []string{"suffix", "directory"} {
		if !hasDoctorFinding(findings, doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf(`task %q brief does not declare report path`, id)}) {
			t.Fatalf("findings = %#v, want %s report path warning", findings, id)
		}
	}
	if !hasDoctorFindingPrefix(findings, doctorWarning, `task "unreadable" brief could not be read: `) {
		t.Fatalf("findings = %#v, want unreadable brief finding", findings)
	}
}

func TestDoctorWarnsForOpenShipTaskReportedDoneWithNoPR(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)

	for _, task := range []state.Task{
		{ID: "stuck", Project: "demo", Kind: state.KindShip, Brief: "data/stuck/brief.md", Lifecycle: state.TaskOpen},
		{ID: "delivered", Project: "demo", Kind: state.KindShip, Brief: "data/delivered/brief.md", Lifecycle: state.TaskOpen, PR: "https://github.com/atqamz/hand/pull/1"},
		{ID: "working", Project: "demo", Kind: state.KindShip, Brief: "data/working/brief.md", Lifecycle: state.TaskOpen},
		{ID: "scouting", Project: "demo", Kind: state.KindScout, Brief: "data/scouting/brief.md", Lifecycle: state.TaskOpen},
	} {
		if err := state.CreateTask(home, task); err != nil {
			t.Fatal(err)
		}
	}
	for id, report := range map[string]string{
		"stuck":     "done: nothing left to do\n",
		"delivered": "done: PR opened\n",
		"working":   "working: still going\n",
		"scouting":  "done: report written\n",
	} {
		if err := os.WriteFile(state.ReportPath(home, id), []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	histories, err := state.ListOpenHistoriesReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := doctorFindings(".", histories)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDoctorFinding(findings, doctorFinding{Severity: doctorWarning, Text: "task \"stuck\" is an open ship task that reported done with no pull request recorded; run `hand status stuck` to see what unblocks it"}) {
		t.Fatalf("findings = %#v, want stuck ship task warning", findings)
	}
	for _, id := range []string{"delivered", "working", "scouting"} {
		for _, finding := range findings {
			if finding.Severity == doctorWarning && strings.Contains(finding.Text, fmt.Sprintf("task %q is an open ship task", id)) {
				t.Fatalf("findings = %#v, task %q should not warn", findings, id)
			}
		}
	}
}

func hasDoctorFindingPrefix(findings []doctorFinding, severity doctorSeverity, prefix string) bool {
	for _, finding := range findings {
		if finding.Severity == severity && strings.HasPrefix(finding.Text, prefix) {
			return true
		}
	}
	return false
}

func hasDoctorFindingContaining(findings []doctorFinding, severity doctorSeverity, text string) bool {
	for _, finding := range findings {
		if finding.Severity == severity && strings.Contains(finding.Text, text) {
			return true
		}
	}
	return false
}

func TestDoctorFindsWorktreeUsingAnotherFleetClone(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://example.com/demo.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(home, "projects", "demo")
	foreign := filepath.Join(t.TempDir(), "demo")
	initGitRepo(t, clone)
	initGitRepo(t, foreign)
	if err := state.CreateTask(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreateAttempt(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning, Worktree: foreign}); err != nil {
		t.Fatal(err)
	}
	histories, err := state.ListOpenHistoriesReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := doctorFindings(home, histories)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, finding := range findings {
		if finding.Severity == doctorError && strings.Contains(finding.Text, `task "task-1" worktree is rooted in another Git repository`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("findings = %#v, want a cross-fleet worktree finding", findings)
	}
}

func TestDoctorFindsWorktreeUsingTheFleetHomeCheckout(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://example.com/demo.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(home, "projects", "demo")
	operatorCheckout := filepath.Join(home, "operator-checkout")
	initGitRepo(t, clone)
	initGitRepo(t, operatorCheckout)
	if err := state.CreateTask(home, state.Task{ID: "task-1", Project: "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.CreateAttempt(home, state.Attempt{TaskID: "task-1", Lifecycle: state.AttemptRunning, Worktree: operatorCheckout}); err != nil {
		t.Fatal(err)
	}
	histories, err := state.ListOpenHistoriesReadOnly(home)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := doctorFindings(home, histories)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if finding.Severity == doctorError && strings.Contains(finding.Text, `task "task-1" worktree is rooted in another Git repository`) {
			return
		}
	}
	t.Fatalf("findings = %#v, want an operator-checkout worktree finding", findings)
}

func TestDoctorReportsAnAliasedPoolSlotAcrossPoolRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SECONDHAND_HOME", filepath.Join(home, "secondhand"))
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://example.com/demo.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clone)
	first := filepath.Join(clone, ".treehouse", "pool-a", "1", "demo")
	second := filepath.Join(home, ".treehouse", "pool-b", "1", "demo")
	runGitOutput(t, clone, "worktree", "add", "-q", "-b", "first", first)
	runGitOutput(t, clone, "worktree", "add", "-q", "-b", "second", second)
	metadata, err := os.ReadFile(filepath.Join(first, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, ".git"), metadata, 0o644); err != nil {
		t.Fatal(err)
	}

	metadataPath := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(metadata)), "gitdir:"))
	findings := doctorWorktreeFindings(home, nil, []project.Project{{Name: "demo"}})
	for _, want := range []string{metadataPath, first, second} {
		if !hasDoctorFindingContaining(findings, doctorError, want) {
			t.Fatalf("findings = %#v, want alias finding to name %q", findings, want)
		}
	}
	if !hasDoctorFindingContaining(findings, doctorError, "metadata target") {
		t.Fatalf("findings = %#v, want a metadata-target collision", findings)
	}
	if !hasDoctorFindingContaining(findings, doctorError, second+`" is unsound`) {
		t.Fatalf("findings = %#v, want the stale claimant reported as unsound", findings)
	}
}

func TestDoctorReportsAPoolSlotWithMissingMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SECONDHAND_HOME", filepath.Join(home, "secondhand"))
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := project.Add(home, project.Project{Name: "demo", URL: "https://example.com/demo.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clone)
	slot := filepath.Join(home, ".treehouse", "pool", "1", "demo")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	missingMetadata := filepath.Join(clone, ".git", "worktrees", "gone")
	if err := os.WriteFile(filepath.Join(slot, ".git"), []byte("gitdir: "+missingMetadata+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := doctorWorktreeFindings(home, nil, []project.Project{{Name: "demo"}})
	if !hasDoctorFindingContaining(findings, doctorError, slot) || !hasDoctorFindingContaining(findings, doctorError, "metadata directory does not exist") {
		t.Fatalf("findings = %#v, want the missing metadata target and slot", findings)
	}
}

func TestDoctorIncludesProjectListGateFinding(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	findings, err := doctorFindings(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDoctorFinding(findings, doctorFinding{Severity: doctorError, Text: `project "gated" no-mistakes gate is unreachable`}) {
		t.Fatalf("findings = %#v, want project list gate finding", findings)
	}
}

func TestDoctorKeepsProjectReadinessSeparateFromGateRunEvidence(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	if err := project.Add(home, project.Project{Name: "local", URL: "https://example.com/local.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "gated"), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "no-mistakes.log")
	faketool.NoMistakes{
		Status: "gate: ready",
		Runs:   "no completed runs",
		Log:    log,
	}.Install(t, faketool.Bin(t))

	findings, err := doctorFindings(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if strings.Contains(finding.Text, "no-mistakes gate") {
			t.Fatalf("findings = %#v, want no project setup finding for ready integration", findings)
		}
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(calls), "no-mistakes status\n"; got != want {
		t.Fatalf("no-mistakes calls = %q, want only the Project readiness check %q", got, want)
	}
}

func hasDoctorFinding(findings []doctorFinding, want doctorFinding) bool {
	for _, finding := range findings {
		if finding.Severity == want.Severity && finding.Text == want.Text {
			return true
		}
	}
	return false
}

// A clean fleet still reports its effective routing decision rather than making
// an operator infer it from a lack of findings.
func TestDoctorCleanFleetReportsEffectiveRouting(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	// Every required tool present, so this run's only finding is the routing decision under
	// test - onPath only checks PATH presence, never invokes any of these, so zero-valued
	// fakes are enough.
	bin := faketool.Bin(t)
	faketool.Treehouse{}.Install(t, bin)
	faketool.Herdr{}.Install(t, bin)
	faketool.GH{}.Install(t, bin)

	var out bytes.Buffer
	cmd := newDoctorCmd(stableBuild("v0.1.0"))
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got error %v, want nil for a clean AGENTS.md", err)
	}
	want := "file: " + axi.Value(filepath.Join(home, "AGENTS.md")) + "\n" +
		"version: v0.1.0\n" +
		"channel: stable\n" +
		"commit: unknown\n" +
		"distribution: \"\"\n" +
		"count: 1\n" +
		"violations: 0\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("stdout = %q, want it to contain %q", out.String(), want)
	}
	wantFindings := "findings[1]{line,severity,finding}:\n" +
		"  none,info," + axi.Value(`routing resolves through explicit legacy defaults: harness "claude"`) + "\n" +
		"help[1]:\n" +
		"  - No error findings, so this run passed; inspect warnings and info before the next dispatch\n"
	if !strings.Contains(out.String(), wantFindings) {
		t.Fatalf("stdout = %q, want it to contain %q", out.String(), wantFindings)
	}
}

func TestDoctorReportsConfiguredRoutingDecision(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if err := routing.WriteProfile(home, routing.Profile{Name: "daily", Harness: harness.Claude, Model: "opus", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := routing.WriteRoute(home, routing.Route{Kind: routing.TaskKindScout, ExecutionClass: routing.ExecutionClassMechanical, Profile: "daily"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HARNESS", harness.Claude)

	findings, err := doctorFindings(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := doctorFinding{Severity: doctorInfo, Text: `routing decision: scout.mechanical -> profile "daily" -> harness "claude", model "opus", effort "high"`}
	if !hasDoctorFinding(findings, want) {
		t.Fatalf("findings = %#v, want %q", findings, want.Text)
	}
}

func TestDoctorReportsUnresolvedConfiguredRoutingDecision(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "config", "routes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "routes", "ship.deep"), []byte("missing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HARNESS", harness.Claude)

	findings, err := doctorFindings(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := doctorFinding{Severity: doctorWarning, Text: `routing decision: ship.deep -> unavailable (profile "missing" does not exist or is invalid)`}
	if !hasDoctorFinding(findings, want) {
		t.Fatalf("findings = %#v, want %q", findings, want.Text)
	}
}

func TestDoctorReportsMalformedRoutingBeforeEffectiveFallback(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	profileDir := filepath.Join(home, "config", "profiles", "broken")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "current"), []byte("missing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)

	findings, err := doctorFindings(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	profileIndex := -1
	fallbackIndex := -1
	for i, finding := range findings {
		if strings.HasPrefix(finding.Text, "routing drift: profile") {
			profileIndex = i
		}
		if strings.HasPrefix(finding.Text, "routing effective fallback after configuration problems: harness") {
			fallbackIndex = i
		}
	}
	if profileIndex < 0 || fallbackIndex < 0 || profileIndex >= fallbackIndex {
		t.Fatalf("findings = %#v, want malformed profile before fallback", findings)
	}
}

func TestDoctorReportsViolationsAndExitsNonZero(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "AGENTS.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nFixed on 2026-07-29.\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newDoctorCmd(stableBuild("v0.1.0"))
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	err = cmd.Execute()
	if err == nil {
		t.Fatal("got nil error, want a non-nil error for a perishable-content hit")
	}
	want := "file: " + axi.Value(filepath.Join(home, "AGENTS.md")) + "\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("stdout = %q, want the findings anchored at the resolved home's absolute path %q", out.String(), want)
	}
	if !strings.Contains(out.String(), "violations: 1\n") || !strings.Contains(out.String(), ",error,") {
		t.Fatalf("stdout = %q, want one finding counted and marked at error severity", out.String())
	}
}

// INV-OUT-4's stdout half: `hand doctor` renders its full report to stdout
// and still returns an error on a violation, and renderError's separate
// document (simulated here against its own buffer) never touches it.
func TestDoctorViolationsKeepStdoutReportAndRenderASeparateErrorDocument(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "AGENTS.md")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nFixed on 2026-07-29.\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newDoctorCmd(stableBuild("v0.1.0"))
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	runErr := cmd.Execute()
	if runErr == nil {
		t.Fatal("got nil error, want a non-nil error for a perishable-content hit")
	}
	report := out.String()
	if !strings.Contains(report, "violations: 1\n") {
		t.Fatalf("stdout = %q, want the doctor report with a violation counted", report)
	}

	var errDoc strings.Builder
	if renderErr := renderError(&errDoc, runErr, 1, "hand doctor"); renderErr != nil {
		t.Fatal(renderErr)
	}
	if out.String() != report {
		t.Fatalf("stdout changed after rendering the error document: got %q, want unchanged %q", out.String(), report)
	}
	for _, want := range []string{"error: ", "kind: general\n", "exit: 1\n"} {
		if !strings.Contains(errDoc.String(), want) {
			t.Fatalf("error document = %q, want %q", errDoc.String(), want)
		}
	}
	if strings.Contains(errDoc.String(), "violations:") {
		t.Fatalf("error document = %q, want the doctor report's fields to stay out of the error document", errDoc.String())
	}
}

func TestDoctorTreatsMissingManagedMarkersAsViolation(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	// Refresh first so CLAUDE.md (a plain file on Windows, checked separately from
	// AGENTS.md's canonical content) is already correct: the overwrite below isolates the one
	// violation under test rather than also tripping the Windows-only CLAUDE.md check.
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"),
		[]byte("# Hand-authored, no generated markers\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newDoctorCmd(stableBuild("v0.1.0"))
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("got nil error, want missing managed markers to fail doctor")
	}
	if !strings.Contains(out.String(), "violations: 1\n") {
		t.Fatalf("stdout = %q, want the missing markers counted as a violation", out.String())
	}
	if !strings.Contains(out.String(), "  none,error,") {
		t.Fatalf("stdout = %q, want a whole-file finding to carry no line number", out.String())
	}
}

func TestDoctorFailsWhenManagedMarkersAreRemovedAfterInitialization(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	path := filepath.Join(home, "AGENTS.md")
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Hand-authored, no generated markers\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newDoctorCmd(stableBuild("v0.1.0"))
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("got nil error, want removed managed markers to fail doctor")
	}
	if !strings.Contains(out.String(), "AGENTS.md has drifted from the canonical Hand-owned content") {
		t.Fatalf("stdout = %q, want the drift violation reported", out.String())
	}
}

func TestDoctorFailsWhenAgentsFileIsDeletedAfterInitialization(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	path := filepath.Join(home, "AGENTS.md")
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newDoctorCmd(stableBuild("v0.1.0"))
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("got nil error, want a deleted AGENTS.md to fail doctor")
	}
	if !strings.Contains(out.String(), "violations: 1\n") || !strings.Contains(out.String(), "AGENTS.md is missing") {
		t.Fatalf("stdout = %q, want one missing-file violation", out.String())
	}
}

// Every drift shape now collapses to one whole-file finding: the canonical AGENTS.md is
// compared byte-for-byte, so doctor reports no partial-drift detail.
func TestDoctorReportsDriftForMalformedOrForeignContentWithNoLineNumber(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"unpaired", "# Rules\n<!-- hand:generated:start -->\n"},
		{"duplicate", "<!-- hand:generated:start -->\n<!-- hand:generated:start -->\n<!-- hand:generated:end -->\n"},
		{"reversed", "<!-- hand:generated:end -->\n<!-- hand:generated:start -->\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Chdir(home)
			mkFleetDirs(t, home)
			if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			cmd := newDoctorCmd(stableBuild("v0.1.0"))
			cmd.SetOut(&out)
			cmd.SetArgs(nil)
			if err := cmd.Execute(); err == nil {
				t.Fatal("got nil error, want drifted content to fail doctor")
			}
			if !strings.Contains(out.String(), "  none,error,\"AGENTS.md has drifted from the canonical Hand-owned content") {
				t.Fatalf("stdout = %q, want one whole-file drift finding with no line number", out.String())
			}
		})
	}
}

func TestDoctorOutsideFleetHomeIsPrecondition(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HAND_HOME", "")

	cmd := newDoctorCmd(stableBuild("v0.1.0"))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs(nil)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("got nil error, want a precondition failure outside a fleet home")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want an ExitError with code 3", err)
	}
}

func TestDoctorReportsBinaryVersionChannelCommitAndDistribution(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)

	var out bytes.Buffer
	info := selfupdate.BuildInfo{Version: "v0.6.0", Channel: selfupdate.ChannelEdge, Commit: "abcdef1234567890", Distribution: selfupdate.DistributionBrew}
	cmd := newDoctorCmd(info)
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"version: v0.6.0\n",
		"channel: edge\n",
		"commit: " + selfupdate.DisplayCommit("abcdef1234567890") + "\n",
		"distribution: " + selfupdate.DistributionBrew + "\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want it to contain %q", got, want)
		}
	}
}

func TestDoctorFlagsEveryMissingBundledSkillDestination(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	// No skill.Refresh: every destination is missing.

	findings, err := doctorFindings(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range findings {
		if strings.Contains(f.Text, "bundled skill is missing") {
			if f.Severity != doctorError {
				t.Fatalf("got severity %v for a missing-skill finding, want error", f.Severity)
			}
			n++
		}
	}
	if n != len(skill.DestinationDirs(home)) {
		t.Fatalf("got %d missing-skill findings, want one per destination (%d): %#v", n, len(skill.DestinationDirs(home)), findings)
	}
}

func TestDoctorFlagsADriftedBundledSkillFile(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	dir := skill.DestinationDirs(home)[0]
	path := filepath.Join(dir, "SKILL.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, []byte("\nstray\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, err := doctorFindings(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDoctorFinding(findings, doctorFinding{Severity: doctorError, Text: fmt.Sprintf("bundled skill at %s has drifted from the canonical content: run hand init '%s' to refresh it", dir, home)}) {
		t.Fatalf("findings = %#v, want a drift finding naming %s", findings, dir)
	}
}

func TestDoctorFlagsAForeignFileAtASkillDestinationAsAConflict(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	dir := skill.DestinationDirs(home)[0]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, err := doctorFindings(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range findings {
		if strings.Contains(f.Text, "foreign, unmanaged file") {
			found = true
			if f.Severity != doctorError {
				t.Fatalf("got severity %v for a skill conflict finding, want error", f.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("findings = %#v, want a foreign-file conflict finding for %s", findings, dir)
	}
}

func TestDoctorWarnsOnEachMissingRequiredTool(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	faketool.NoTools(t)

	findings, err := doctorFindings(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range foundationalTools {
		want := doctorFinding{Severity: doctorWarning, Text: fmt.Sprintf("required tool %q is not on PATH", tool)}
		if !hasDoctorFinding(findings, want) {
			t.Fatalf("findings = %#v, want a missing-tool warning for %q", findings, tool)
		}
	}
}

func TestGHRequiredOnlyWhenARegisteredProjectDeliversThroughGitHub(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{mode: project.ModeLocalOnly, want: false},
		{mode: project.ModeDirectPR, want: true},
		{mode: project.ModeNoMistakes, want: true},
	}
	for _, tt := range tests {
		got := ghRequired([]project.Project{{Name: "p", Mode: tt.mode}})
		if got != tt.want {
			t.Errorf("ghRequired(mode=%q) = %v, want %v", tt.mode, got, tt.want)
		}
	}
	if ghRequired(nil) {
		t.Error("ghRequired(nil) = true, want false for a fleet with no registered projects")
	}
}

func TestDoctorToolsReportsFoundationalAndContextualRequirement(t *testing.T) {
	faketool.NoTools(t)
	bin := faketool.Bin(t)
	faketool.Command{Name: "git"}.Install(t, bin)
	faketool.Treehouse{}.Install(t, bin)

	got := doctorTools([]project.Project{{Name: "p", Mode: project.ModeDirectPR}})
	want := []toolReadiness{
		{Tool: "git", Installed: true, Required: true},
		{Tool: "treehouse", Installed: true, Required: true},
		{Tool: "herdr", Installed: false, Required: true},
		{Tool: "gh", Installed: false, Required: true},
	}
	if len(got) != len(want) {
		t.Fatalf("doctorTools() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("doctorTools()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestDoctorHarnessesReportsEverySupervisorHarness(t *testing.T) {
	faketool.NoTools(t)
	bin := faketool.Bin(t)
	faketool.Command{Name: harness.Codex}.Install(t, bin)

	got := doctorHarnesses()
	if len(got) != len(supervision.SupervisorHosts()) {
		t.Fatalf("doctorHarnesses() = %#v, want one entry per %v", got, supervision.SupervisorHosts())
	}
	for _, h := range got {
		want := h.Name == harness.Codex
		if h.Installed != want {
			t.Fatalf("doctorHarnesses() entry %q installed = %v, want %v", h.Name, h.Installed, want)
		}
	}
}

func TestDoctorBlockingAndNextStayInStepAndReadyFollowsBlocking(t *testing.T) {
	tools := []toolReadiness{
		{Tool: "git", Installed: true, Required: true},
		{Tool: "treehouse", Installed: false, Required: true},
		{Tool: "herdr", Installed: true, Required: true},
		{Tool: "gh", Installed: false, Required: false},
	}
	harnesses := []harnessReadiness{{Name: harness.Claude, Installed: false}}

	blocking := doctorBlocking(1, tools, harnesses)
	want := []string{"fleet-health", "treehouse", "harness"}
	if len(blocking) != len(want) {
		t.Fatalf("doctorBlocking() = %v, want %v", blocking, want)
	}
	for i := range want {
		if blocking[i] != want[i] {
			t.Fatalf("doctorBlocking()[%d] = %q, want %q", i, blocking[i], want[i])
		}
	}

	next := doctorNext(blocking)
	if len(next) != len(blocking) {
		t.Fatalf("doctorNext() = %v, want one entry per blocking item %v", next, blocking)
	}
	if next[1] != "install treehouse" {
		t.Fatalf("doctorNext()[1] = %q, want %q", next[1], "install treehouse")
	}

	if doctorBlocking(0, []toolReadiness{{Tool: "git", Installed: true, Required: true}}, []harnessReadiness{{Name: harness.Claude, Installed: true}}) == nil {
		t.Fatal("doctorBlocking() returned a nil slice for a ready fleet, want an empty non-nil slice so the rendered list still states its count")
	}
}

func TestDoctorReportsReadyWhenEveryFoundationalToolAndOneHarnessArePresent(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	faketool.NoTools(t)
	bin := faketool.Bin(t)
	faketool.Command{Name: "git"}.Install(t, bin)
	faketool.Treehouse{}.Install(t, bin)
	faketool.Herdr{}.Install(t, bin)
	faketool.Command{Name: harness.Claude}.Install(t, bin)

	var out bytes.Buffer
	cmd := newDoctorCmd(stableBuild("v0.1.0"))
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got error %v, want nil for a ready fleet", err)
	}
	want := "tools[4]{tool,installed,required}:\n" +
		"  git,true,true\n" +
		"  treehouse,true,true\n" +
		"  herdr,true,true\n" +
		"  gh,false,false\n" +
		"supervisor_harnesses[5]{name,installed}:\n" +
		"  claude,true\n" +
		"  codex,false\n" +
		"  grok,false\n" +
		"  pi,false\n" +
		"  opencode,false\n" +
		"ready: true\n" +
		"blocking[0]:\n" +
		"next[0]:\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("stdout = %q, want it to contain %q", out.String(), want)
	}
}

func TestDoctorReportsNotReadyWithBlockingAndNextWhenTreehouseAndEveryHarnessAreMissing(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	faketool.NoTools(t)
	bin := faketool.Bin(t)
	faketool.Command{Name: "git"}.Install(t, bin)
	faketool.Herdr{}.Install(t, bin)

	var out bytes.Buffer
	cmd := newDoctorCmd(stableBuild("v0.1.0"))
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got error %v, want nil: a missing foundational tool or harness is a warning, not an error", err)
	}
	want := "ready: false\n" +
		"blocking[2]:\n" +
		"  - treehouse\n" +
		"  - harness\n" +
		"next[2]:\n" +
		"  - install treehouse\n" +
		"  - install and authenticate at least one supported Supervisor Harness (see `supervisor_harnesses` above), then run hand doctor\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("stdout = %q, want it to contain %q", out.String(), want)
	}
}

func TestDoctorGHNotRequiredForALocalOnlyProjectDoesNotBlockReadiness(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if _, err := skill.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "local", URL: "https://example.com/local.git", Mode: project.ModeLocalOnly}); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	faketool.NoTools(t)
	bin := faketool.Bin(t)
	faketool.Command{Name: "git"}.Install(t, bin)
	faketool.Treehouse{}.Install(t, bin)
	faketool.Herdr{}.Install(t, bin)
	faketool.Command{Name: harness.Claude}.Install(t, bin)

	var out bytes.Buffer
	cmd := newDoctorCmd(stableBuild("v0.1.0"))
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	if !strings.Contains(out.String(), "gh,false,false\n") {
		t.Fatalf("stdout = %q, want gh reported installed=false, required=false for a local-only project", out.String())
	}
	if !strings.Contains(out.String(), "ready: true\n") {
		t.Fatalf("stdout = %q, want ready: true since gh is not required", out.String())
	}
}

func TestDoctorDoesNotTreatWorkerOnlyAntigravityAsSupervisorReady(t *testing.T) {
	faketool.NoTools(t)
	bin := faketool.Bin(t)
	faketool.Command{Name: harness.Executable(harness.Antigravity)}.Install(t, bin)

	got := doctorHarnesses()
	for _, item := range got {
		if item.Name == harness.Antigravity {
			t.Fatalf("doctor Supervisor readiness includes worker-only Antigravity: %#v", got)
		}
		if item.Installed {
			t.Fatalf("doctor Supervisor readiness = %#v, want no installed Supervisor Harness", got)
		}
	}
	if anyHarnessInstalled(got) {
		t.Fatalf("worker-only agy satisfied Supervisor readiness: %#v", got)
	}
}

// Lays down a project clone whose Treehouse pool has one slot leased to holder. faketool.Bin
// also isolates SECONDHAND_HOME, so the caller's own registry.Open calls land in a fresh registry.
func leaseHolderFixture(t *testing.T, holder string) (home string, projects []project.Project) {
	t.Helper()
	home = t.TempDir()
	clone := filepath.Join(home, "projects", "demo")
	initGitRepo(t, clone)
	bin := faketool.Bin(t)
	slot := filepath.Join(home, "pool", "5")
	faketool.Treehouse{
		Held:         []string{slot},
		LeaseHolders: map[string]string{slot: holder},
	}.Install(t, bin)
	return home, []project.Project{{Name: "demo"}}
}

func TestDoctorReportsLeaseHolderAbsentFromRegistry(t *testing.T) {
	home, projects := leaseHolderFixture(t, "hand:f_167a403f6e12d103a5d310cc10fecedc:340-scout")
	// The registry exists - atqamz/hand#432's measured case - but never heard of this id, which is
	// a different condition from the registry itself being missing.
	registryDB, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Close(); err != nil {
		t.Fatal(err)
	}

	findings := doctorLeaseHolderFindings(home, projects)
	found := false
	for _, finding := range findings {
		if finding.Severity == doctorError && strings.Contains(finding.Text, `is absent from the Fleet registry`) &&
			strings.Contains(finding.Text, "f_167a403f6e12d103a5d310cc10fecedc") && strings.Contains(finding.Text, `"340-scout"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("findings = %#v, want an absent-Fleet finding naming the slot and the id", findings)
	}
}

func TestDoctorReportsAMissingRegistryAsOneFindingRatherThanPerHolderAbsence(t *testing.T) {
	home, projects := leaseHolderFixture(t, "hand:f_167a403f6e12d103a5d310cc10fecedc:340-scout")
	registryPath, err := registry.Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("registry.db unexpectedly exists at %s before the check runs", registryPath)
	}

	findings := doctorLeaseHolderFindings(home, projects)
	if len(findings) != 1 {
		t.Fatalf("findings = %#v, want exactly one finding for a missing registry, not one per leased slot", findings)
	}
	if !strings.Contains(findings[0].Text, registryPath) || !strings.Contains(findings[0].Text, "hand init") {
		t.Fatalf("findings[0] = %#v, want it to name the registry path and a way to re-register it", findings[0])
	}
	if strings.Contains(findings[0].Text, "absent from the Fleet registry") {
		t.Fatalf("findings[0] = %#v, a missing registry must not be reported as a per-holder absence", findings[0])
	}
}

func TestDoctorReportsLeaseHolderRegisteredButNotReady(t *testing.T) {
	notReadyHome := t.TempDir()
	fleetID, err := state.FleetID(notReadyHome)
	if err != nil {
		t.Fatal(err)
	}
	home, projects := leaseHolderFixture(t, "hand:"+fleetID+":401-repair")
	registryDB, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(notReadyHome, fleetID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Close(); err != nil {
		t.Fatal(err)
	}
	// The home vanishes after registering, so classification observes it missing rather than
	// ready - "registered but not ready" wants a different repair than "absent".
	if err := os.RemoveAll(notReadyHome); err != nil {
		t.Fatal(err)
	}

	findings := doctorLeaseHolderFindings(home, projects)
	found := false
	for _, finding := range findings {
		if finding.Severity == doctorWarning && strings.Contains(finding.Text, "registered but not ready") &&
			strings.Contains(finding.Text, fleetID) && strings.Contains(finding.Text, `"401-repair"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("findings = %#v, want a registered-but-not-ready finding naming the slot and the id", findings)
	}
}

func TestDoctorReportsUnparseableLeaseHolder(t *testing.T) {
	// A real non-Hand lease observed in atqamz/hand#412's repair table - not "hand:<fleet>:<task>"
	// shaped, and must not collapse into "absent". No registry is set up at all: parsing a holder
	// never needs one, so a missing registry must not surface here either.
	home, projects := leaseHolderFixture(t, "codex-196")

	findings := doctorLeaseHolderFindings(home, projects)
	found := false
	for _, finding := range findings {
		if finding.Severity == doctorWarning && strings.Contains(finding.Text, "does not parse as a Hand lease holder") &&
			strings.Contains(finding.Text, `"codex-196"`) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("findings = %#v, want an unparseable-holder finding naming the raw holder string", findings)
	}
	for _, finding := range findings {
		if strings.Contains(finding.Text, "absent from the Fleet registry") {
			t.Fatalf("findings = %#v, an unparseable holder must not be reported as absent", findings)
		}
		if strings.Contains(finding.Text, "Fleet registry") && strings.Contains(finding.Text, "does not exist") {
			t.Fatalf("findings = %#v, parsing a holder never needs the registry, so a missing registry must not surface here", findings)
		}
	}
}

func TestDoctorSaysNothingForLeaseHolderRegisteredAndReady(t *testing.T) {
	readyHome := t.TempDir()
	fleetID, err := state.FleetID(readyHome)
	if err != nil {
		t.Fatal(err)
	}
	home, projects := leaseHolderFixture(t, "hand:"+fleetID+":123-ready")
	registryDB, err := registry.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Register(readyHome, fleetID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := registryDB.Close(); err != nil {
		t.Fatal(err)
	}

	findings := doctorLeaseHolderFindings(home, projects)
	if len(findings) != 0 {
		t.Fatalf("findings = %#v, want silence for a lease held by a registered, ready Fleet", findings)
	}
}

func TestDoctorReportsRegistryUnreadableRatherThanTreatingHoldersAsAbsent(t *testing.T) {
	home, projects := leaseHolderFixture(t, "hand:f_167a403f6e12d103a5d310cc10fecedc:340-scout")
	registryPath, err := registry.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registryPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	findings := doctorLeaseHolderFindings(home, projects)
	if len(findings) != 1 {
		t.Fatalf("findings = %#v, want exactly one finding about the unreadable registry", findings)
	}
	if !strings.Contains(findings[0].Text, "Fleet registry") || !strings.Contains(findings[0].Text, "could not be read") {
		t.Fatalf("findings[0] = %#v, want a diagnosis about the registry itself", findings[0])
	}
	if strings.Contains(findings[0].Text, "absent from the Fleet registry") {
		t.Fatalf("findings[0] = %#v, an unreadable registry must not be reported as an absent holder", findings[0])
	}
}
