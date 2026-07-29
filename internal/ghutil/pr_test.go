package ghutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeGHPRView fakes `gh pr view --json state`, emitting a stderr line
// ahead of the JSON payload so a CombinedOutput regression at the call site
// fails the parse the same way real gh's progress output does.
func writeFakeGHPRView(t *testing.T, state string, exitCode int, stderrLine string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n"
	if stderrLine != "" {
		script += fmt.Sprintf("echo %q >&2\n", stderrLine)
	}
	if exitCode != 0 {
		script += fmt.Sprintf("exit %d\n", exitCode)
	} else {
		script += fmt.Sprintf("printf '{\"state\":\"%s\"}\\n'\n", state)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestPRIsMergedIgnoresStderrNoise(t *testing.T) {
	for _, c := range []struct {
		state string
		want  bool
	}{{"MERGED", true}, {"OPEN", false}} {
		writeFakeGHPRView(t, c.state, 0, "Warning: gh version 2.40.0 is out of date")
		got, err := PRIsMerged(context.Background(), "42")
		if err != nil {
			t.Fatalf("PRIsMerged with state %s: %v", c.state, err)
		}
		if got != c.want {
			t.Errorf("PRIsMerged with state %s = %v, want %v", c.state, got, c.want)
		}
	}
}

func TestPRIsMergedReportsExitStatusWithoutStderr(t *testing.T) {
	writeFakeGHPRView(t, "", 1, "")
	_, err := PRIsMerged(context.Background(), "42")
	if err == nil {
		t.Fatal("want error when gh exits non-zero")
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("got %q, want the exit status in the message", err)
	}
}

// writeFakeGHPRList fakes `gh pr list --json number,url,state`, emitting a stderr
// line ahead of the JSON array payload for the same reason writeFakeGHPRView
// does: a CombinedOutput regression at the call site must fail the parse.
func writeFakeGHPRList(t *testing.T, body string, exitCode int, stderrLine string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n"
	if stderrLine != "" {
		script += fmt.Sprintf("echo %q >&2\n", stderrLine)
	}
	if exitCode != 0 {
		script += fmt.Sprintf("exit %d\n", exitCode)
	} else {
		script += fmt.Sprintf("printf '%s'\n", body)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestFindPRByBranchReturnsMatch(t *testing.T) {
	writeFakeGHPRList(t, `[{"url":"https://github.com/owner/repo/pull/5","state":"MERGED"}]`, 0, "Warning: gh version 2.40.0 is out of date")
	url, merged, found, err := FindPRByBranch(context.Background(), "owner/repo", "task-1-branch")
	if err != nil {
		t.Fatal(err)
	}
	if !found || url != "https://github.com/owner/repo/pull/5" || !merged {
		t.Fatalf("got (%q, %v, %v), want the merged PR", url, merged, found)
	}
}

// TestFindPRByBranchPrefersMergedOverClosedUnmerged is the atqamz/secondhand#77
// regression: a branch carrying a merged PR alongside a closed-unmerged one
// (a duplicate opened by mistake, say) must resolve to the merged PR rather
// than an arbitrary pick.
func TestFindPRByBranchPrefersMergedOverClosedUnmerged(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"CLOSED"},`+
		`{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"}]`, 0, "")
	url, merged, found, err := FindPRByBranch(context.Background(), "owner/repo", "task-1-branch")
	if err != nil {
		t.Fatal(err)
	}
	if !found || !merged || url != "https://github.com/owner/repo/pull/5" {
		t.Fatalf("got (%q, %v, %v), want the merged PR", url, merged, found)
	}
}

// TestFindPRByBranchReturnsSoleClosedUnmergedPR proves a branch with only a
// closed-unmerged PR still resolves to it rather than treating the tier rule
// as requiring a merged candidate to exist.
func TestFindPRByBranchReturnsSoleClosedUnmergedPR(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"CLOSED"}]`, 0, "")
	url, merged, found, err := FindPRByBranch(context.Background(), "owner/repo", "task-1-branch")
	if err != nil {
		t.Fatal(err)
	}
	if !found || merged || url != "https://github.com/owner/repo/pull/9" {
		t.Fatalf("got (%q, %v, %v), want the sole closed-unmerged PR unmerged", url, merged, found)
	}
}

// TestFindPRByBranchRefusesTwoMergedPRs proves an ambiguous winning tier
// refuses rather than picking either candidate.
func TestFindPRByBranchRefusesTwoMergedPRs(t *testing.T) {
	writeFakeGHPRList(t, `[{"number":9,"url":"https://github.com/owner/repo/pull/9","state":"MERGED"},`+
		`{"number":5,"url":"https://github.com/owner/repo/pull/5","state":"MERGED"}]`, 0, "")
	_, _, _, err := FindPRByBranch(context.Background(), "owner/repo", "task-1-branch")
	var ambiguous *AmbiguousPRError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want an AmbiguousPRError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Fatalf("Candidates = %+v, want both PR 9 and PR 5 named", ambiguous.Candidates)
	}
	if !strings.Contains(err.Error(), "#9") || !strings.Contains(err.Error(), "#5") {
		t.Fatalf("got %q, want both PR numbers named", err.Error())
	}
}

func TestFindPRByBranchNoMatch(t *testing.T) {
	writeFakeGHPRList(t, `[]`, 0, "")
	_, _, found, err := FindPRByBranch(context.Background(), "owner/repo", "task-1-branch")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("want found=false for an empty result")
	}
}

func TestFindPRByBranchReportsExitStatusWithoutStderr(t *testing.T) {
	writeFakeGHPRList(t, "", 1, "")
	_, _, _, err := FindPRByBranch(context.Background(), "owner/repo", "task-1-branch")
	if err == nil {
		t.Fatal("want error when gh exits non-zero")
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("got %q, want the exit status in the message", err)
	}
}

func TestRepoSlugFromRemote(t *testing.T) {
	cases := []struct {
		remote string
		slug   string
		ok     bool
	}{
		{"https://github.com/atqamz/secondhand", "atqamz/secondhand", true},
		{"https://github.com/atqamz/secondhand.git", "atqamz/secondhand", true},
		{"git@github.com:atqamz/secondhand.git", "atqamz/secondhand", true},
		{"ssh://git@github.com/atqamz/secondhand.git", "atqamz/secondhand", true},
		{"local", "", false},
		{"https://gitlab.com/atqamz/secondhand", "", false},
		{"https://github.com/atqamz", "", false},
		{"https://github.com/atqamz/secondhand/extra", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		slug, ok := RepoSlugFromRemote(c.remote)
		if slug != c.slug || ok != c.ok {
			t.Errorf("RepoSlugFromRemote(%q) = (%q, %v), want (%q, %v)", c.remote, slug, ok, c.slug, c.ok)
		}
	}
}
