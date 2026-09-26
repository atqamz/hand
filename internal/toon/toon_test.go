package toon

import "testing"

func TestRenderFieldsRowsListsAndHelp(t *testing.T) {
	var d Doc
	d.Field("home", "/tmp/h")
	d.Field("count", "2")
	d.Rows("tasks", []string{"id", "title"}, [][]string{{"t1", "fix, now"}, {"t2", ""}})
	d.List("memory", []string{"line one", "line\ntwo"})
	d.Help("run `hand orient`")
	want := "home: /tmp/h\n" +
		"count: 2\n" +
		"tasks[2]{id,title}:\n" +
		"  t1,\"fix, now\"\n" +
		"  t2,\"\"\n" +
		"memory[2]:\n" +
		"  - line one\n" +
		"  - line two\n" +
		"help[1]:\n" +
		"  - run `hand orient`\n"
	if got := d.String(); got != want {
		t.Fatalf("render =\n%q\nwant\n%q", got, want)
	}
}

func TestEmptyRowsStillRenderHeader(t *testing.T) {
	var d Doc
	d.Rows("active", []string{"id"}, nil)
	if got, want := d.String(), "active[0]{id}:\n"; got != want {
		t.Fatalf("render = %q, want %q", got, want)
	}
}

func TestReplaceHintsTouchesOnlyHelpAndMoreLines(t *testing.T) {
	var d Doc
	d.Field("title", "`hand x` stays")
	d.Field("inbox_more", "10 of 12 shown; run `hand task list`")
	d.Help("Run `hand orient`", "then `hand wait`")
	d.ReplaceHints("`hand ", "`hand-next ")
	want := "title: `hand x` stays\ninbox_more: 10 of 12 shown; run `hand-next task list`\nhelp[2]:\n  - Run `hand-next orient`\n  - then `hand-next wait`\n"
	if got := d.String(); got != want {
		t.Fatalf("doc = %q, want %q", got, want)
	}
}
