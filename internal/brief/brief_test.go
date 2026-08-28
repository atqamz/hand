package brief

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Verbatim copy of data/secondhand-th-floor/brief.md from the fleet this repo runs, chosen
// because it opens with plain prose rather than a "#" heading - the shape most likely to
// trip a parser that scans for "key: value" lines instead of requiring a "---" fence.
const realBriefFixture = `You are a crewmate: an autonomous worker agent managed by the first mate. Work on your own; do not wait for a human.

# Task

Record a minimum supported treehouse version in the README. This is a one-line documentation change. Do not expand it.

Issue: https://github.com/atqamz/hand/issues/41. Read it, including the most recent comment, which narrows the issue to exactly this.

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

func TestParseDeclaresExecutionClass(t *testing.T) {
	for _, class := range []ExecutionClass{ExecutionClassMechanical, ExecutionClassStandard, ExecutionClassDeep} {
		t.Run(string(class), func(t *testing.T) {
			d, present, err := Parse(writeBrief(t, "---\nexecution_class: "+string(class)+"\n---\n# Title\n"))
			if err != nil {
				t.Fatal(err)
			}
			if !present || d.ExecutionClass != class {
				t.Fatalf("got present=%v declaration=%+v, want class %q", present, d, class)
			}
		})
	}
}

func TestParseDeclaresPlannedAgainstFullObjectID(t *testing.T) {
	for _, commit := range []string{strings.Repeat("a", 40), strings.Repeat("b", 64)} {
		d, present, err := Parse(writeBrief(t, "---\nplanned_against: "+commit+"\n---\n# Title\n"))
		if err != nil {
			t.Fatal(err)
		}
		if !present || d.PlannedAgainst != commit {
			t.Fatalf("got present=%v declaration=%+v, want planned_against %q", present, d, commit)
		}
	}
}

func TestParseRejectsInvalidExecutionClass(t *testing.T) {
	for _, value := range []string{"cheap", "Mechanical", `""`} {
		t.Run(value, func(t *testing.T) {
			_, _, err := Parse(writeBrief(t, "---\nexecution_class: "+value+"\n---\n# Title\n"))
			if err == nil || !strings.Contains(err.Error(), "execution_class") || !strings.Contains(err.Error(), value) {
				t.Fatalf("Parse() = %v, want execution_class error naming %q", err, value)
			}
		})
	}
}

func TestParseRecognizedMetadataErrorsAreTyped(t *testing.T) {
	_, _, err := Parse(writeBrief(t, "---\nexecution_class: cheap\n---\n# Title\n"))
	if err == nil {
		t.Fatal("Parse() = nil, want validation error")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("Parse() = %T %v, want ValidationError", err, err)
	}
	if validationErr.Field != "execution_class" {
		t.Fatalf("ValidationError.Field = %q, want execution_class", validationErr.Field)
	}
}

func TestParseRejectsInvalidPlannedAgainst(t *testing.T) {
	for _, value := range []string{"", strings.Repeat("a", 39), "HEAD", "main", strings.Repeat("g", 40)} {
		t.Run(value, func(t *testing.T) {
			_, _, err := Parse(writeBrief(t, "---\nplanned_against: "+value+"\n---\n# Title\n"))
			if err == nil || !strings.Contains(err.Error(), "planned_against") || !strings.Contains(err.Error(), value) {
				t.Fatalf("Parse() = %v, want planned_against error naming %q", err, value)
			}
		})
	}
}

func TestParseStripsQuotedExecutionMetadataScalars(t *testing.T) {
	commit := strings.Repeat("a", 40)
	d, present, err := Parse(writeBrief(t, "---\nexecution_class: 'mechanical'\nplanned_against: \""+commit+"\"\n---\n# Title\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !present || d.ExecutionClass != ExecutionClassMechanical || d.PlannedAgainst != commit {
		t.Fatalf("got present=%v declaration=%+v, want quoted values stripped", present, d)
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

func TestParseStripsQuotedScalars(t *testing.T) {
	for name, content := range map[string]string{
		"double": "---\nmodel: \"claude-opus-5\"\neffort: \"high\"\n---\n# Title\n",
		"single": "---\nmodel: 'claude-opus-5'\neffort: 'high'\n---\n# Title\n",
	} {
		t.Run(name, func(t *testing.T) {
			d, present, err := Parse(writeBrief(t, content))
			if err != nil {
				t.Fatal(err)
			}
			if !present || d.Model != "claude-opus-5" || d.Effort != "high" {
				t.Fatalf("got present=%v d=%+v, want surrounding quotes stripped", present, d)
			}
		})
	}
}

func TestParseKeepsUnpairedQuote(t *testing.T) {
	path := writeBrief(t, "---\nmodel: \"claude-opus-5\n---\n# Title\n")

	d, _, err := Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if d.Model != "\"claude-opus-5" {
		t.Fatalf("got %q, want an unpaired quote left alone", d.Model)
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

func TestParseUnclosedNewMetadataTreatedAsProse(t *testing.T) {
	path := writeBrief(t, "---\nexecution_class: invalid\nplanned_against: HEAD\n# Title\n")

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

func TestParseOversizedLineTreatedAsProse(t *testing.T) {
	huge := strings.Repeat("x", bufio.MaxScanTokenSize+1)
	for name, content := range map[string]string{
		"first line":  huge + "\n# Title\n",
		"inside body": "---\nmodel: claude-opus-5\n" + huge + "\n---\n# Title\n",
	} {
		t.Run(name, func(t *testing.T) {
			d, present, err := Parse(writeBrief(t, content))
			if err != nil {
				t.Fatalf("oversized line must not fail a spawn: %v", err)
			}
			if present || d != (Declaration{}) {
				t.Fatalf("got present=%v d=%+v, want no declaration", present, d)
			}
		})
	}
}

func TestParseMissingBrief(t *testing.T) {
	_, _, err := Parse(filepath.Join(t.TempDir(), "missing.md"))
	if err == nil {
		t.Fatal("expected error for missing brief")
	}
}

func TestDigestUnaffectedByHandsOwnAppendix(t *testing.T) {
	supervisorContent := "implement the fix"
	plain := writeBrief(t, supervisorContent)
	want, err := Digest(plain)
	if err != nil {
		t.Fatal(err)
	}

	appended := writeBrief(t, supervisorContent+"\n\n---\n\n"+AppendMarker+"\n\nThe worker report channel is /tmp/task-1.status.\n")
	got, err := Digest(appended)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Digest(appended) = %q, want %q: hand's own appendix must not change the digest", got, want)
	}
}

func TestDigestChangesWhenSupervisorEditsPrecedeHandsAppendix(t *testing.T) {
	rewritten := writeBrief(t, "ship it"+"\n\n---\n\n"+AppendMarker+"\n\nThe worker report channel is /tmp/task-1.status.\n")
	original := writeBrief(t, "implement the fix"+"\n\n---\n\n"+AppendMarker+"\n\nThe worker report channel is /tmp/task-1.status.\n")
	rewrittenDigest, err := Digest(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	originalDigest, err := Digest(original)
	if err != nil {
		t.Fatal(err)
	}
	if rewrittenDigest == originalDigest {
		t.Fatal("Digest must still distinguish two different supervisor-authored briefs that both carry hand's appendix")
	}
}
