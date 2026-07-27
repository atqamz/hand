package brief

import (
	"os"
	"path/filepath"
	"testing"
)

// realBriefFixture is a verbatim copy of data/secondhand-th-floor/brief.md from the fleet
// this repo runs, chosen because it opens with plain prose ("You are a crewmate...") rather
// than a "#" heading - the shape most likely to trip up a header parser that scans for
// "key: value" lines before the first heading instead of requiring an explicit "---" fence.
const realBriefFixture = `You are a crewmate: an autonomous worker agent managed by the first mate. Work on your own; do not wait for a human.

# Task

Record a minimum supported treehouse version in the README. This is a one-line documentation change. Do not expand it.

Issue: https://github.com/atqamz/secondhand/issues/41. Read it, including the most recent comment, which narrows the issue to exactly this.

## Background (verified by the first mate)

treehouse v2.0.0 dropped ` + "`--json`" + ` from ` + "`get --lease`" + `, which broke every ` + "`hand spawn`" + `, because ` + "`internal/worktree/worktree.go`" + ` ` + "`Get()`" + ` passes ` + "`--json`" + ` and unmarshals ` + "`{\"path\": ...}`" + `. treehouse v2.1.0 reintroduced ` + "`--json`" + ` as the surface of an unrelated feature, stable lease identities. Verified on the first mate's host: treehouse v2.1.0, ` + "`--json`" + ` present in ` + "`treehouse get --help`" + `, and a real ` + "`hand spawn`" + ` succeeding end to end with the Go code unmodified.

So there is no code bug left. What is left is that nothing in this repo records the version floor, so a fresh install on v2.0.0 or v2.0.1 fails with ` + "`unknown flag: --json`" + ` and no hint as to why.

## What to do

In ` + "`README.md`" + `, the Requirements list already pins Go ("Go 1.26.5 or newer") but pins neither herdr nor treehouse. Change only the treehouse line:

` + "```" + `
- [treehouse](https://github.com/kunchenguid/treehouse) - git worktree pool manager
` + "```" + `

to name the floor, matching the phrasing the Go line already uses:

` + "```" + `
- [treehouse](https://github.com/kunchenguid/treehouse) v2.1.0 or newer - git worktree pool manager
` + "```" + `

## Constraints / acceptance

- That single line is the whole change. Do not add a version floor to herdr, gh, or anything else: there is no evidence for those, and inventing one is worse than leaving it unpinned.
- Do not touch ` + "`internal/worktree/worktree.go`" + ` or any Go file. There is no code change in this task.
- Do not add a changelog entry by hand. ` + "`CHANGELOG.md`" + ` is generated.
- No em dash anywhere. Plain hyphen.
`

func writeBrief(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseRealBriefWithoutDeclarationUnchanged(t *testing.T) {
	path := writeBrief(t, realBriefFixture)

	d, present, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if present {
		t.Fatalf("present = true, want false: %+v", d)
	}
	if d != (Declaration{}) {
		t.Fatalf("got %+v, want zero Declaration", d)
	}
}

func TestParseHeadingFirstBriefUnchanged(t *testing.T) {
	path := writeBrief(t, "# fix-login - repair the broken login flow\n\nRepo: `example/app`.\n")

	d, present, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if present || d != (Declaration{}) {
		t.Fatalf("got present=%v d=%+v, want no declaration", present, d)
	}
}

func TestParseDeclaresModelAndEffort(t *testing.T) {
	path := writeBrief(t, "---\nmodel: claude-opus-5\neffort: high\n---\n# Title\n\nBody.\n")

	d, present, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Fatal("present = false, want true")
	}
	if d.Model != "claude-opus-5" || d.Effort != "high" {
		t.Fatalf("got %+v", d)
	}
}

func TestParseIgnoresUnknownKeys(t *testing.T) {
	path := writeBrief(t, "---\nmodel: claude-opus-5\nnote: whatever this is\nreviewer: someone\n---\n# Title\n")

	d, present, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Fatal("present = false, want true")
	}
	if d.Model != "claude-opus-5" || d.Effort != "" {
		t.Fatalf("got %+v, want unknown keys ignored", d)
	}
}

func TestParseOnlyModelDeclared(t *testing.T) {
	path := writeBrief(t, "---\nmodel: claude-haiku-4-5\n---\n# Title\n")

	d, present, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if !present || d.Model != "claude-haiku-4-5" || d.Effort != "" {
		t.Fatalf("got present=%v d=%+v", present, d)
	}
}

func TestParseUnclosedFrontMatterTreatedAsProse(t *testing.T) {
	path := writeBrief(t, "---\nmodel: claude-opus-5\n\n# Title\n\nno closing fence above\n")

	d, present, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if present || d != (Declaration{}) {
		t.Fatalf("got present=%v d=%+v, want unclosed front matter treated as prose", present, d)
	}
}

func TestParseEmptyBrief(t *testing.T) {
	path := writeBrief(t, "")

	d, present, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if present || d != (Declaration{}) {
		t.Fatalf("got present=%v d=%+v, want no declaration", present, d)
	}
}

func TestParseMissingBrief(t *testing.T) {
	_, _, err := Parse(filepath.Join(t.TempDir(), "missing.md"))
	if err == nil {
		t.Fatal("expected error for missing brief")
	}
}
