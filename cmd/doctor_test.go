package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/agentsmd"
	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/routing"
)

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

			findings, err := doctorFindings(home)
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

func TestDoctorIncludesProjectListGateFinding(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)
	if err := project.Add(home, project.Project{Name: "gated", URL: "https://example.com/gated.git", Mode: project.ModeNoMistakes}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	findings, err := doctorFindings(home)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDoctorFinding(findings, doctorFinding{Severity: doctorError, Text: `project "gated" no-mistakes gate is unreachable`}) {
		t.Fatalf("findings = %#v, want project list gate finding", findings)
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
	mustConfigSet(t, settingHarness, harness.Claude)

	var out bytes.Buffer
	cmd := newDoctorCmd()
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("got error %v, want nil for a clean AGENTS.md", err)
	}
	want := "file: " + axi.Value(filepath.Join(home, "AGENTS.md")) + "\n" +
		"count: 1\n" +
		"violations: 0\n" +
		"findings[1]{line,severity,finding}:\n" +
		"  none,info," + axi.Value(`routing resolves through explicit legacy defaults: harness "claude"`) + "\n" +
		"help[1]:\n" +
		"  - No error findings, so this run passed; inspect warnings and info before the next dispatch\n"
	if out.String() != want {
		t.Fatalf("stdout = %q, want %q", out.String(), want)
	}
}

func TestDoctorReportsConfiguredRoutingDecision(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if err := routing.WriteProfile(home, routing.Profile{Name: "daily", Harness: harness.Claude, Model: "opus", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := routing.WriteRoute(home, routing.Route{Kind: routing.TaskKindScout, ExecutionClass: routing.ExecutionClassMechanical, Profile: "daily"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HARNESS", harness.Claude)

	findings, err := doctorFindings(home)
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
	if err := os.MkdirAll(filepath.Join(home, "config", "routes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "routes", "ship.deep"), []byte("missing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAND_HARNESS", harness.Claude)

	findings, err := doctorFindings(home)
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
	profileDir := filepath.Join(home, "config", "profiles", "broken")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "current"), []byte("missing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustConfigSet(t, settingHarness, harness.Claude)

	findings, err := doctorFindings(home)
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
	cmd := newDoctorCmd()
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

func TestDoctorTreatsMissingManagedMarkersAsViolation(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	// Refresh first so CLAUDE.md (a plain file on Windows, checked separately from
	// AGENTS.md's markers) is already correct: the overwrite below isolates the one
	// violation under test rather than also tripping the Windows-only CLAUDE.md check.
	if _, err := agentsmd.Refresh(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"),
		[]byte("# Hand-authored, no generated markers\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newDoctorCmd()
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
	if err := os.WriteFile(path, []byte("# Hand-authored, no generated markers\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newDoctorCmd()
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("got nil error, want removed managed markers to fail doctor")
	}
	if !strings.Contains(out.String(), "no hand:generated markers") {
		t.Fatalf("stdout = %q, want the missing-markers violation reported", out.String())
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
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := newDoctorCmd()
	cmd.SetOut(&out)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err == nil {
		t.Fatal("got nil error, want a deleted AGENTS.md to fail doctor")
	}
	if !strings.Contains(out.String(), "violations: 1\n") || !strings.Contains(out.String(), "AGENTS.md is missing") {
		t.Fatalf("stdout = %q, want one missing-file violation", out.String())
	}
}

func TestDoctorReportsMalformedMarkersWithLineNumbers(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"unpaired", "# Rules\n<!-- hand:generated:start -->\n", "  2,error,\"unpaired hand:generated start marker\""},
		{"duplicate", "<!-- hand:generated:start -->\n<!-- hand:generated:start -->\n<!-- hand:generated:end -->\n", "  2,error,\"duplicate hand:generated start marker\""},
		{"reversed", "<!-- hand:generated:end -->\n<!-- hand:generated:start -->\n", "  1,error,\"hand:generated end marker appears before start marker\""},
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
			cmd := newDoctorCmd()
			cmd.SetOut(&out)
			cmd.SetArgs(nil)
			if err := cmd.Execute(); err == nil {
				t.Fatal("got nil error, want malformed markers to fail doctor")
			}
			if !strings.Contains(out.String(), tt.want) {
				t.Fatalf("stdout = %q, want %q", out.String(), tt.want)
			}
		})
	}
}

func TestDoctorOutsideFleetHomeIsPrecondition(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HAND_HOME", "")

	cmd := newDoctorCmd()
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
