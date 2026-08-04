package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/harness"
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
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "model"), []byte("config-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "effort"), []byte("config-effort\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "model"), []byte("config-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "effort"), []byte("config-effort\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "model"), []byte("config-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "effort"), []byte("config-effort\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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

// TestResolveTierWarnsWhenHarnessCannotApplyModel covers all three model-less harnesses because
// their launch builders are near-copies: a test that only proves codex warns lets grok or pi
// regress in silence.
func TestResolveTierWarnsWhenHarnessCannotApplyModel(t *testing.T) {
	for _, harnessName := range []string{harness.Codex, harness.Grok, harness.Pi} {
		t.Run(harnessName, func(t *testing.T) {
			home := t.TempDir()
			briefAbs := writeTierBrief(t, home, declaredBrief)

			cmd, stderr := newTierTestCmd()
			model, _, _, err := resolveTier(cmd, home, briefAbs, harnessName, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if model != "brief-model" {
				t.Fatalf("got model=%q, want it still resolved even though %s cannot apply it", model, harnessName)
			}
			want := "warning: harness " + strconv.Quote(harnessName) + " has no model flag, ignoring model \"brief-model\"\n"
			if !strings.Contains(stderr.String(), want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), want)
			}
			if !strings.Contains(stderr.String(), "no effort flag") {
				t.Fatalf("stderr = %q, want the effort warning alongside the model one", stderr.String())
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
	if _, _, _, err := resolveTier(cmd, home, briefAbs, harness.Codex, "", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), "model") {
		t.Fatalf("stderr = %q, want no model warning when no model was resolved at all", stderr.String())
	}
	if !strings.Contains(stderr.String(), "no effort flag") {
		t.Fatalf("stderr = %q, want the effort warning still reported", stderr.String())
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
