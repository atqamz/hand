package axi

import (
	"strings"
	"testing"
)

func TestDocRendersFieldsRowsAndHelp(t *testing.T) {
	var d Doc
	d.Int("count", 2)
	d.Rows("tasks", []string{"id", "state"}, [][]string{
		{"fix-login", "busy"},
		{"ship-docs", "idle"},
	})
	d.Help("Run `hand status <id>` for detail")

	want := "count: 2\n" +
		"tasks[2]{id,state}:\n" +
		"  fix-login,busy\n" +
		"  ship-docs,idle\n" +
		"help[1]:\n" +
		"  - Run `hand status <id>` for detail\n"
	if got := d.String(); got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

// An empty result still carries its count and its schema header: that is the
// whole difference between "no tasks" and a command that broke.
func TestEmptyRowsBlockKeepsCountAndSchema(t *testing.T) {
	var d Doc
	d.Int("count", 0)
	d.Rows("tasks", []string{"id", "state"}, nil)

	want := "count: 0\ntasks[0]{id,state}:\n"
	if got := d.String(); got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestHelpWithNoLinesEmitsNothing(t *testing.T) {
	var d Doc
	d.Help()
	if got := d.String(); got != "" {
		t.Fatalf("Render() = %q, want empty", got)
	}
}

func TestValueQuotesOnlyWhatWouldBeAmbiguous(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"busy", "busy"},
		{"2h ago", "2h ago"},
		{"1.2.3", "1.2.3"},
		{"", `""`},
		{"a,b", `"a,b"`},
		{"done: shipped", `"done: shipped"`},
		{`say "hi"`, `"say \"hi\""`},
		{" padded ", `" padded "`},
		{"two\nlines", `"two\nlines"`},
	}
	for _, c := range cases {
		if got := Value(c.in); got != c.want {
			t.Errorf("Value(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestListItemCollapsesEmbeddedNewline(t *testing.T) {
	var d Doc
	d.List("history", []string{"working: one\nworking: two"})
	want := "history[1]:\n  - working: one working: two\n"
	if got := d.String(); got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestTruncateLeavesShortTextAlone(t *testing.T) {
	if got := Truncate("short", 200, "hand status x --full"); got != "short" {
		t.Fatalf("Truncate() = %q, want %q", got, "short")
	}
}

func TestTruncateCarriesSizeAndRecoveryHint(t *testing.T) {
	got := Truncate(strings.Repeat("a", 94), 10, "hand status x --full")
	want := "aaaaaaaaaa... (truncated, 94 chars total - use hand status x --full to see complete text)"
	if got != want {
		t.Fatalf("Truncate() = %q, want %q", got, want)
	}
}

// Counting runes, not bytes: a budget applied to bytes cuts a multi-byte
// character in half and reports a length no reader recognizes.
func TestTruncateCountsRunes(t *testing.T) {
	got := Truncate(strings.Repeat("é", 5), 2, "hand status x --full")
	want := "éé... (truncated, 5 chars total - use hand status x --full to see complete text)"
	if got != want {
		t.Fatalf("Truncate() = %q, want %q", got, want)
	}
}

type row struct {
	id    string
	state string
	kind  string
}

func testColumns() []Column[row] {
	return []Column[row]{
		{Name: "id", Value: func(r row) string { return r.id }},
		{Name: "state", Value: func(r row) string { return r.state }},
		{Name: "kind", Value: func(r row) string { return r.kind }},
	}
}

func TestSelectWithNoRequestKeepsEveryColumn(t *testing.T) {
	cols, err := Select(testColumns(), nil)
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got := strings.Join(Names(cols), ","); got != "id,state,kind" {
		t.Fatalf("Names() = %q, want %q", got, "id,state,kind")
	}
}

// The schema header has to stay honest about what was emitted, including the
// order the caller asked for it in.
func TestSelectNarrowsSchemaHeaderToRequestedFields(t *testing.T) {
	cols, err := Select(testColumns(), []string{"state", "id"})
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	var d Doc
	Table(&d, "tasks", []row{{id: "a", state: "busy", kind: "ship"}}, cols)
	want := "tasks[1]{state,id}:\n  busy,a\n"
	if got := d.String(); got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestSelectRejectsUnknownFieldAndNamesTheKnownOnes(t *testing.T) {
	_, err := Select(testColumns(), []string{"nope"})
	if err == nil {
		t.Fatal("Select() error = nil, want an unknown-field error")
	}
	if !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "id, state, kind") {
		t.Fatalf("Select() error = %v, want it to name the bad field and the known ones", err)
	}
}

func TestTableOnNoItemsStillEmitsSchema(t *testing.T) {
	var d Doc
	Table(&d, "tasks", []row(nil), testColumns())
	if got := d.String(); got != "tasks[0]{id,state,kind}:\n" {
		t.Fatalf("Render() = %q", got)
	}
}
