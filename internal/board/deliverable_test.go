package board_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func TestDeliverables(t *testing.T) {
	d := func(ref, kind string) board.Deliverable { return board.Deliverable{Ref: ref, Kind: kind} }
	for _, c := range []struct {
		name string
		body string
		want []board.Deliverable
	}{
		{"explicit url", "Done.\nDelivered: https://docs.example.com/notes/q3-review", []board.Deliverable{d("https://docs.example.com/notes/q3-review", "link")}},
		{"explicit posix path", "Delivered: /home/me/work/notes/2026-10-03-findings.md", []board.Deliverable{d("/home/me/work/notes/2026-10-03-findings.md", "file")}},
		{"explicit windows drive path", `Delivered: C:\Users\me\out\report.docx`, []board.Deliverable{d(`C:\Users\me\out\report.docx`, "file")}},
		{"explicit windows forward slashes", "Delivered: C:/Users/me/out", []board.Deliverable{d("C:/Users/me/out", "file")}},
		{"explicit unc path", `Delivered: \\server\share\out\report.docx`, []board.Deliverable{d(`\\server\share\out\report.docx`, "file")}},
		{"path with spaces", "Delivered: /home/me/My Notes/a b.md", []board.Deliverable{d("/home/me/My Notes/a b.md", "file")}},
		{"explicit change request keeps its kind", "Delivered: https://github.com/atqamz/hand/pull/800", []board.Deliverable{d("https://github.com/atqamz/hand/pull/800", "PR")}},
		{"explicit merge request", "Delivered: https://gitlab.com/g/sub/proj/-/merge_requests/5", []board.Deliverable{d("https://gitlab.com/g/sub/proj/-/merge_requests/5", "MR")}},
		{"backticks, bullet and CRLF", "- Delivered: `https://example.com/a`\r\nDelivered: /tmp/x\r\n", []board.Deliverable{d("https://example.com/a", "link"), d("/tmp/x", "file")}},
		{"file scheme ignored", "Delivered: file:///home/me/a.md", nil},
		{"other scheme ignored", "Delivered: ftp://example.com/a\nDelivered: javascript:alert(1)\nDelivered: mailto:a@b.c", nil},
		{"forward-slash unc ignored", "Delivered: //host/share/a.md", nil},
		{"relative path ignored", "Delivered: notes/a.md\nDelivered: ./a.md\nDelivered: ~/a.md", nil},
		{"prose ignored", "Delivered: the report\nDelivered:\nDelivered: https://", nil},
		{"url with a space ignored", "Delivered: https://example.com/a b", nil},
		{"not at the line start", "I Delivered: /tmp/x", nil},
		{"fallback github", "PR: https://github.com/atqamz/hand/pull/42 opened", []board.Deliverable{d("https://github.com/atqamz/hand/pull/42", "PR")}},
		{"fallback gitea and forgejo", "https://codeberg.org/o/r/pulls/7", []board.Deliverable{d("https://codeberg.org/o/r/pulls/7", "PR")}},
		{"fallback gitlab with subgroups", "see (https://gitlab.com/g/sub/deeper/proj/-/merge_requests/12).", []board.Deliverable{d("https://gitlab.com/g/sub/deeper/proj/-/merge_requests/12", "MR")}},
		{"fallback bitbucket", "https://bitbucket.org/w/r/pull-requests/3", []board.Deliverable{d("https://bitbucket.org/w/r/pull-requests/3", "PR")}},
		{"fallback stops at the number", "https://github.com/a/b/pull/9/files#diff", []board.Deliverable{d("https://github.com/a/b/pull/9", "PR")}},
		{"github issue is not a deliverable", "Fixes https://github.com/atqamz/hand/issues/796 and https://example.com/docs", nil},
		{"explicit wins over fallback", "https://github.com/a/b/pull/1\nDelivered: /tmp/out.md\nhttps://github.com/a/b/pull/2", []board.Deliverable{d("/tmp/out.md", "file")}},
		{"an invalid explicit line still blocks the fallback", "Delivered: pending\nhttps://github.com/a/b/pull/2", nil},
		{"duplicates removed", "https://github.com/a/b/pull/1 https://github.com/a/b/pull/2 https://github.com/a/b/pull/1", []board.Deliverable{d("https://github.com/a/b/pull/1", "PR"), d("https://github.com/a/b/pull/2", "PR")}},
		{"explicit duplicates removed", "Delivered: /tmp/a\nDelivered: /tmp/b\nDelivered: /tmp/a", []board.Deliverable{d("/tmp/a", "file"), d("/tmp/b", "file")}},
	} {
		if got := board.Deliverables(c.body); !slices.Equal(got, c.want) {
			t.Errorf("%s: Deliverables = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTaskCardChipsByKind(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	task := active(t, st, "Mixed deliverables")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	for _, body := range []string{
		"First.\nDelivered: https://github.com/atqamz/hand/pull/800",
		"Second.\nDelivered: https://gitlab.com/g/p/-/merge_requests/4\nDelivered: https://docs.example.com/q3\nDelivered: /home/me/out/notes.md",
		"Old style https://github.com/atqamz/hand/pull/9",
	} {
		if _, err := st.AddReport(ctx, a.ID, state.ReportProgress, body); err != nil {
			t.Fatal(err)
		}
	}
	slug := get(t, board.New(st, token, board.Options{}), "/task/t1")
	var seen []string
	for _, want := range []string{
		`<a class="badge" data-tone="link" href="https://github.com/atqamz/hand/pull/9" target="_blank" rel="noopener noreferrer">PR</a>`,
		`<span class="file" title="/home/me/out/notes.md">file: notes.md</span>`,
		`<a class="badge" data-tone="link" href="https://docs.example.com/q3" target="_blank" rel="noopener noreferrer">link</a>`,
		`<a class="badge" data-tone="link" href="https://gitlab.com/g/p/-/merge_requests/4" target="_blank" rel="noopener noreferrer">MR</a>`,
		`<a class="badge" data-tone="link" href="https://github.com/atqamz/hand/pull/800" target="_blank" rel="noopener noreferrer">PR</a>`,
	} {
		i := strings.Index(slug, want)
		if i < 0 {
			t.Fatalf("task page lacks %q:\n%s", want, slug)
		}
		seen = append(seen, want)
		if len(seen) > 1 && i < strings.Index(slug, seen[len(seen)-2]) {
			t.Fatalf("chips are not newest first at %q", want)
		}
	}
	if strings.Contains(slug, `href="/home/me/out/notes.md"`) {
		t.Fatalf("a path chip is a link:\n%s", slug)
	}
}

func TestPathChipIsEscapedText(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	task := active(t, st, "Odd path")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	if _, err := st.AddReport(ctx, a.ID, state.ReportProgress, `Delivered: /tmp/a<b&c"d.md`); err != nil {
		t.Fatal(err)
	}
	h := board.New(st, token, board.Options{})
	for _, page := range []string{get(t, h, "/task/t1"), checks(get(t, h, "/"))["t1"]} {
		for _, want := range []string{`title="/tmp/a&lt;b&amp;c&#34;d.md"`, `file: a&lt;b&amp;c&#34;d.md</span>`} {
			if !strings.Contains(page, want) {
				t.Fatalf("page lacks %q:\n%s", want, page)
			}
		}
		if strings.Contains(page, "a<b") || strings.Contains(page, `href="/tmp`) {
			t.Fatalf("path chip is not escaped text:\n%s", page)
		}
	}
}

func TestQueueRowShowsTheNewestDeliverableOnly(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	task := active(t, st, "Two deliverables")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	for _, body := range []string{"https://github.com/atqamz/hand/pull/7", "Delivered: https://docs.example.com/a\nDelivered: https://docs.example.com/b"} {
		if _, err := st.AddReport(ctx, a.ID, state.ReportProgress, body); err != nil {
			t.Fatal(err)
		}
	}
	row := checks(get(t, board.New(st, token, board.Options{}), "/"))["t1"]
	if n := strings.Count(row, `data-tone="link"`); n != 1 || !strings.Contains(row, `href="https://docs.example.com/b" target="_blank" rel="noopener noreferrer">link</a>`) {
		t.Fatalf("row = %s", row)
	}
}

func TestOldReportWithOnlyAPullRequestKeepsItsPRChip(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	task := active(t, st, "Old report")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	if _, err := st.AddReport(ctx, a.ID, state.ReportDone, "Cookie fixed\nPR: https://github.com/atqamz/hand/pull/42"); err != nil {
		t.Fatal(err)
	}
	h := board.New(st, token, board.Options{})
	want := `<a class="badge" data-tone="link" href="https://github.com/atqamz/hand/pull/42" target="_blank" rel="noopener noreferrer">PR</a>`
	if row := checks(get(t, h, "/"))["t1"]; !strings.Contains(row, want) {
		t.Fatalf("row lacks the PR chip: %s", row)
	}
	if page := get(t, h, "/task/t1"); !strings.Contains(page, want) {
		t.Fatalf("task page lacks the PR chip:\n%s", page)
	}
}

func TestLongPathChipStaysOnTheMetaLine(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	task := active(t, st, "Long path")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	name := strings.Repeat("n", 190) + ".md"
	if _, err := st.AddReport(ctx, a.ID, state.ReportProgress, "Delivered: /"+strings.Repeat("d/", 3)+name); err != nil {
		t.Fatal(err)
	}
	row := checks(get(t, board.New(st, token, board.Options{}), "/"))["t1"]
	chip := `<span class="file" title="/d/d/d/` + name + `">file: ` + name + `</span>`
	if body := strings.Index(row, `<span class="check-body">`); body < 0 || !strings.Contains(row[body:], chip+`</span></span>`) {
		t.Fatalf("chip is outside .check-body: %s", row)
	}
}

func TestFileChipIsPlainCappedText(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css, ".file{display:inline-block;max-width:32ch;overflow:hidden;color:var(--muted);font-family:var(--mono);font-size:12px;line-height:18px;text-overflow:ellipsis;white-space:nowrap;vertical-align:bottom}")
	lacks(t, "board.css", css, "min(100%,32ch)", ".archive .task-row{")
}
