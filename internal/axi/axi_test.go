package axi

import (
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"
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

func TestBoolRendersUnquoted(t *testing.T) {
	var d Doc
	d.Bool("update_available", true)
	d.Bool("updated", false)

	want := "update_available: true\nupdated: false\n"
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

// INV-PURE-2: Truncate never splits a UTF-8 rune, and budget bounds the
// retained prefix, not the returned string - the recovery annotation is
// appended past it on purpose. See TestTruncateMustBeAppliedToOriginalTextOnce.
func TestTruncateKeepsAnUnsplitPrefixBoundedByBudget(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "s")
		budget := rapid.IntRange(0, 200).Draw(t, "budget")

		got := Truncate(s, budget, "hand status x --full")
		runes := []rune(s)

		if len(runes) <= budget {
			if got != s {
				t.Fatalf("Truncate(%q, %d) = %q, want unchanged", s, budget, got)
			}
			return
		}

		want := string(runes[:budget])
		if !strings.HasPrefix(got, want) {
			t.Fatalf("Truncate(%q, %d) = %q, want it to start with the first %d runes %q unsplit",
				s, budget, got, budget, want)
		}
	})
}

// Truncate's only caller (cmd/status.go:82) applies it to original text
// exactly once - a second application corrupts the reported total, since
// it counts the prior output's length instead of the original text's.
func TestTruncateMustBeAppliedToOriginalTextOnce(t *testing.T) {
	original := "A"
	recovery := "hand status x --full"

	once := Truncate(original, 0, recovery)
	twice := Truncate(once, 0, recovery)

	corrupted := fmt.Sprintf("... (truncated, %d chars total - use %s to see complete text)", len([]rune(once)), recovery)
	if twice != corrupted {
		t.Fatalf("Truncate(Truncate(%q, 0, r), 0, r) = %q, want %q naming the prior output's length, not the original %q's",
			original, twice, corrupted, original)
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

// Biases toward the runes Value treats specially - separator, quote,
// boundary/embedded whitespace, non-ASCII - since a uniform rapid.String()
// almost never produces them.
func toonValueGen() *rapid.Generator[string] {
	tricky := rapid.RuneFrom([]rune{',', ':', '"', '\n', '\r', '\t', ' ', '\\', 'é', '日', '🎉', '\u0085'})
	boring := rapid.RuneFrom([]rune("abcXYZ019"))
	mixed := rapid.StringOfN(rapid.OneOf(boring, tricky), 0, 24, -1)
	return rapid.OneOf(rapid.String(), mixed)
}

// INV-OUT-1: a rendered block's [N] always equals the number of rows that
// follow it, checked by an independent count of the rendered lines rather
// than by re-reading the header Render itself computed from len(rows).
func TestRowsHeaderCountAgreesWithRenderedRowCount(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")
		rows := make([][]string, n)
		for i := range rows {
			rows[i] = []string{fmt.Sprintf("r%d", i)}
		}
		var d Doc
		d.Rows("items", []string{"v"}, rows)
		declared, parsed := parseRowsBlock(t, d.String(), "items")
		if declared != n {
			t.Fatalf("Render() = %q, header declares %d rows, want %d", d.String(), declared, n)
		}
		if len(parsed) != n {
			t.Fatalf("Render() = %q, %d row lines actually follow the header, want %d", d.String(), len(parsed), n)
		}
	})
}

// INV-OUT-2: rendering a Field is total over arbitrary values, including
// quotes, newlines, commas, and non-ASCII - the document that results always
// parses back to the same value.
func TestFieldValueRoundTripsThroughRender(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		value := toonValueGen().Draw(t, "value")
		var d Doc
		d.Field("f", value)
		if got := parseFieldValue(t, d.String(), "f"); got != value {
			t.Fatalf("Field(%q) rendered %q, parsed back %q", value, d.String(), got)
		}
	})
}

// INV-OUT-2 for row cells: the same totality claim over a Rows block's
// values - the "field values" the invariant map's Output rendering section
// names.
func TestRowValuesRoundTripThroughRender(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 4).Draw(t, "cols")
		values := make([]string, n)
		fields := make([]string, n)
		for i := range values {
			values[i] = toonValueGen().Draw(t, fmt.Sprintf("v%d", i))
			fields[i] = fmt.Sprintf("f%d", i)
		}

		var d Doc
		d.Rows("items", fields, [][]string{values})
		_, rows := parseRowsBlock(t, d.String(), "items")
		if len(rows) != 1 {
			t.Fatalf("Render() = %q, parsed %d rows, want 1", d.String(), len(rows))
		}
		for i, want := range values {
			if rows[0][i] != want {
				t.Fatalf("Render() = %q, cell %d parsed back %q, want %q", d.String(), i, rows[0][i], want)
			}
		}
	})
}

// INV-OUT-3: an empty result renders its header with a count of 0 rather
// than nothing, for any block name and field set - the general form of
// TestEmptyRowsBlockKeepsCountAndSchema and TestTableOnNoItemsStillEmitsSchema above.
func TestEmptyRowsRenderCountZeroWithSchemaHeader(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[a-z][a-z_]{0,9}`).Draw(t, "name")
		fields := rapid.SliceOfN(rapid.StringMatching(`[a-z][a-z_]{0,7}`), 0, 5).Draw(t, "fields")

		var d Doc
		d.Rows(name, fields, nil)
		want := fmt.Sprintf("%s[0]{%s}:\n", name, strings.Join(fields, ","))
		if got := d.String(); got != want {
			t.Fatalf("Render() = %q, want %q", got, want)
		}
	})
}

type projRow map[string]string

// INV-OUT-5: field selection is a projection. Select narrows which columns
// Table renders, in the order requested, but never touches a selected
// field's own value or which field a rendered cell belongs to.
func TestFieldSelectionProjectsValuesUnchangedInRequestedOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		names := rapid.SliceOfNDistinct(rapid.StringMatching(`[a-z][a-z_]{0,6}`), 1, 6, func(s string) string { return s }).Draw(t, "names")
		values := make(map[string]string, len(names))
		cols := make([]Column[projRow], len(names))
		for i, name := range names {
			values[name] = toonValueGen().Draw(t, "value_"+name)
			field := name
			cols[i] = Column[projRow]{Name: field, Value: func(r projRow) string { return r[field] }}
		}
		item := projRow(values)

		perm := rapid.Permutation(names).Draw(t, "perm")
		k := rapid.IntRange(1, len(perm)).Draw(t, "k")
		want := perm[:k]

		selected, err := Select(cols, want)
		if err != nil {
			t.Fatalf("Select(%v) error = %v", want, err)
		}
		var d Doc
		Table(&d, "items", []projRow{item}, selected)
		_, rows := parseRowsBlock(t, d.String(), "items")
		if len(rows) != 1 || len(rows[0]) != len(want) {
			t.Fatalf("Render() = %q, want exactly one row of %d cells", d.String(), len(want))
		}
		for i, name := range want {
			if rows[0][i] != values[name] {
				t.Fatalf("Render() = %q, field %q at position %d = %q, want %q unchanged from the source item",
					d.String(), name, i, rows[0][i], values[name])
			}
		}
	})
}
