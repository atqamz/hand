package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/spf13/cobra"
)

func writeTierBrief(t *testing.T, home, content string) string {
	t.Helper()
	path := filepath.Join(home, "brief.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Worker defaults are keyed by the harness they were chosen for, so a fixture that writes the bare
// config/model a pre-0.2.0 home carried would be read by nothing.
func writeTierConfig(t *testing.T, home, harnessName string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"model": "config-model", "effort": "config-effort"} {
		path := filepath.Join(home, "config", key+"."+harnessName)
		if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func newTierTestCmd() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	stderr := &bytes.Buffer{}
	cmd.SetErr(stderr)
	return cmd, stderr
}

const declaredBrief = "---\nmodel: brief-model\neffort: brief-effort\n---\n# Title\n"

func TestResolveTierFlagOverridesEverything(t *testing.T) {
	home := t.TempDir()
	briefAbs := writeTierBrief(t, home, declaredBrief)
	writeTierConfig(t, home, harness.Claude)

	cmd, _ := newTierTestCmd()
	model, effort, frontMatter, err := resolveTier(cmd, home, briefAbs, harness.Claude, "flag-model", "flag-effort")
	if err != nil {
		t.Fatal(err)
	}
	if model != "flag-model" || effort != "flag-effort" {
		t.Fatalf("got model=%q effort=%q, want the flags to win", model, effort)
	}
	if !frontMatter {
		t.Fatal("frontMatter = false, want true since the brief declares one")
	}
}

func TestResolveTierBriefOverridesConfig(t *testing.T) {
	home := t.TempDir()
	briefAbs := writeTierBrief(t, home, declaredBrief)
	writeTierConfig(t, home, harness.Claude)

	cmd, _ := newTierTestCmd()
	model, effort, frontMatter, err := resolveTier(cmd, home, briefAbs, harness.Claude, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if model != "brief-model" || effort != "brief-effort" {
		t.Fatalf("got model=%q effort=%q, want the brief to win over config", model, effort)
	}
	if !frontMatter {
		t.Fatal("frontMatter = false, want true")
	}
}

func TestResolveTierConfigAlone(t *testing.T) {
	home := t.TempDir()
	briefAbs := writeTierBrief(t, home, "# Title\n\nno declaration here\n")
	writeTierConfig(t, home, harness.Claude)

	cmd, _ := newTierTestCmd()
	model, effort, frontMatter, err := resolveTier(cmd, home, briefAbs, harness.Claude, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if model != "config-model" || effort != "config-effort" {
		t.Fatalf("got model=%q effort=%q, want config values", model, effort)
	}
	if frontMatter {
		t.Fatal("frontMatter = true, want false for a brief with no declaration")
	}
}

func TestResolveTierAllAbsent(t *testing.T) {
	home := t.TempDir()
	briefAbs := writeTierBrief(t, home, "# Title\n\nno declaration here\n")

	cmd, _ := newTierTestCmd()
	model, effort, frontMatter, err := resolveTier(cmd, home, briefAbs, harness.Claude, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if model != "" || effort != "" || frontMatter {
		t.Fatalf("got model=%q effort=%q frontMatter=%v, want everything absent", model, effort, frontMatter)
	}
}

func TestResolveTierUnknownBriefKeyIsNotAnError(t *testing.T) {
	home := t.TempDir()
	briefAbs := writeTierBrief(t, home, "---\nmodel: brief-model\nreviewer: someone\n---\n# Title\n")

	cmd, _ := newTierTestCmd()
	model, _, _, err := resolveTier(cmd, home, briefAbs, harness.Claude, "", "")
	if err != nil {
		t.Fatalf("unknown brief key produced an error: %v", err)
	}
	if model != "brief-model" {
		t.Fatalf("got model=%q", model)
	}
}

func TestResolveTierWarnsWhenHarnessCannotApplyEffort(t *testing.T) {
	home := t.TempDir()
	briefAbs := writeTierBrief(t, home, declaredBrief)

	cmd, stderr := newTierTestCmd()
	_, effort, _, err := resolveTier(cmd, home, briefAbs, harness.OpenCode, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if effort != "brief-effort" {
		t.Fatalf("got effort=%q, want it still resolved even though opencode cannot apply it", effort)
	}
	if !strings.Contains(stderr.String(), "opencode") || !strings.Contains(stderr.String(), "effort") {
		t.Fatalf("stderr = %q, want a warning naming the harness and effort", stderr.String())
	}
}

// Both file-based harnesses are near-copies, so proving only one warns lets the other regress.
// Exact-match rather than Contains because the single line is the point. Neither now drops the
// operator-decision rule or front-matter disclaimer (atqamz/hand#418), only model and effort.
func TestResolveTierWarnsOnceForEverythingAHarnessCannotCarry(t *testing.T) {
	for _, harnessName := range []string{harness.Grok, harness.Pi} {
		t.Run(harnessName, func(t *testing.T) {
			home := t.TempDir()
			briefAbs := writeTierBrief(t, home, declaredBrief)

			cmd, stderr := newTierTestCmd()
			model, effort, _, err := resolveTier(cmd, home, briefAbs, harnessName, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if model != "brief-model" || effort != "brief-effort" {
				t.Fatalf("got model=%q effort=%q, want both resolved even though %s cannot carry them", model, effort, harnessName)
			}
			want := "warning: harness " + strconv.Quote(harnessName) + ` cannot carry model "brief-model", effort "brief-effort"; launching anyway` + "\n"
			if stderr.String() != want {
				t.Fatalf("stderr = %q, want exactly %q", stderr.String(), want)
			}
		})
	}
}

// atqamz/hand#418: grok and pi now receive the operator-decision rule by appending it to the
// brief file, so a launch with nothing else declared produces no capability warning at all.
func TestResolveTierNoWarningWhenFileBasedHarnessCarriesPrompt(t *testing.T) {
	for _, harnessName := range []string{harness.Grok, harness.Pi} {
		t.Run(harnessName, func(t *testing.T) {
			home := t.TempDir()
			briefAbs := writeTierBrief(t, home, "# Title\n\nno declaration here\n")

			cmd, stderr := newTierTestCmd()
			if _, _, _, err := resolveTier(cmd, home, briefAbs, harnessName, "", ""); err != nil {
				t.Fatal(err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want no warning", stderr.String())
			}
		})
	}
}

func TestResolveTierNoModelWarningUnderOpenCode(t *testing.T) {
	home := t.TempDir()
	briefAbs := writeTierBrief(t, home, declaredBrief)

	cmd, stderr := newTierTestCmd()
	if _, _, _, err := resolveTier(cmd, home, briefAbs, harness.OpenCode, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "model") {
		t.Fatalf("stderr = %q, want no model warning under a harness that takes --model", stderr.String())
	}
}

func TestResolveTierNoWarningWhenNoModelResolved(t *testing.T) {
	home := t.TempDir()
	briefAbs := writeTierBrief(t, home, "---\neffort: brief-effort\n---\n# Title\n")

	cmd, stderr := newTierTestCmd()
	if _, _, _, err := resolveTier(cmd, home, briefAbs, harness.Grok, "", ""); err != nil {
		t.Fatal(err)
	}
	want := `warning: harness "grok" cannot carry effort "brief-effort"; launching anyway` + "\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q, want exactly %q (no model named, none was resolved)", stderr.String(), want)
	}
}

func TestResolveTierNoWarningWhenNoEffortResolved(t *testing.T) {
	home := t.TempDir()
	briefAbs := writeTierBrief(t, home, "# Title\n\nno declaration here\n")

	cmd, stderr := newTierTestCmd()
	if _, _, _, err := resolveTier(cmd, home, briefAbs, harness.OpenCode, "", ""); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no warning when no effort was resolved at all", stderr.String())
	}
}

func TestResolveTierNoWarningUnderClaude(t *testing.T) {
	home := t.TempDir()
	briefAbs := writeTierBrief(t, home, declaredBrief)

	cmd, stderr := newTierTestCmd()
	if _, _, _, err := resolveTier(cmd, home, briefAbs, harness.Claude, "", ""); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no warning under a harness that supports effort", stderr.String())
	}
}
