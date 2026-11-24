package desktop

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseTable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		src   string
		comma rune
		want  [][]string
	}{
		{"plain", "a,b\n1,2\n", ',', [][]string{{"a", "b"}, {"1", "2"}}},
		{"no final newline", "a,b\n1,2", ',', [][]string{{"a", "b"}, {"1", "2"}}},
		{"crlf", "a,b\r\n1,2\r\n", ',', [][]string{{"a", "b"}, {"1", "2"}}},
		{"quoted delimiter", "a,b\n\"x,y\",2\n", ',', [][]string{{"a", "b"}, {"x,y", "2"}}},
		{"quoted newline", "a,b\n\"x\ny\",2\nz,3\n", ',', [][]string{{"a", "b"}, {"x\ny", "2"}, {"z", "3"}}},
		{"quoted crlf newline", "a,b\r\n\"x\r\ny\",2\r\n", ',', [][]string{{"a", "b"}, {"x\ny", "2"}}},
		{"escaped quote", "a\n\"say \"\"hi\"\"\"\n", ',', [][]string{{"a"}, {`say "hi"`}}},
		{"bare quote", "a,b\nsay \"hi\",2\n", ',', [][]string{{"a", "b"}, {`say "hi"`, "2"}}},
		{"ragged", "a,b,c\n1\n1,2,3,4\n", ',', [][]string{{"a", "b", "c"}, {"1"}, {"1", "2", "3", "4"}}},
		{"blank lines skipped", "a,b\n\n1,2\n\n", ',', [][]string{{"a", "b"}, {"1", "2"}}},
		{"empty", "", ',', nil},
		{"only newlines", "\n\n", ',', nil},
		{"bom", "\xef\xbb\xbfa,b\n1,2\n", ',', [][]string{{"a", "b"}, {"1", "2"}}},
		{"bom before quote", "\xef\xbb\xbf\"a,b\",c\n", ',', [][]string{{"a,b", "c"}}},
		{"tsv", "a\tb\n1,5\t2\n", '\t', [][]string{{"a", "b"}, {"1,5", "2"}}},
		{"csv does not split tabs", "a\tb,c\n", ',', [][]string{{"a\tb", "c"}}},
		{"tsv does not split commas", "a,b\tc\n", '\t', [][]string{{"a,b", "c"}}},
		{"empty cells", "a,,c\n,,\n", ',', [][]string{{"a", "", "c"}, {"", "", ""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, more := parseTable(tc.src, tc.comma)
			if more {
				t.Fatal("more is set")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func records(n int) string {
	var b strings.Builder
	b.WriteString("id\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	return b.String()
}

func TestParseTableStopsAfterTheCap(t *testing.T) {
	for _, tc := range []struct {
		data     int
		wantRows int
		wantMore bool
	}{
		{tableRows - 1, tableRows, false},
		{tableRows, tableRows + 1, false},
		{tableRows + 1, tableRows + 1, true},
		{tableRows + 500, tableRows + 1, true},
	} {
		rows, more := parseTable(records(tc.data), ',')
		if len(rows) != tc.wantRows || more != tc.wantMore {
			t.Errorf("%d data rows: kept %d records, more %v; want %d, %v", tc.data, len(rows), more, tc.wantRows, tc.wantMore)
		}
	}
}

func TestParseTableCapCountsRecordsNotLines(t *testing.T) {
	var b strings.Builder
	b.WriteString("id,note\n")
	for i := 1; i <= tableRows; i++ {
		fmt.Fprintf(&b, "%d,\"a\nb\nc\"\n", i)
	}
	rows, more := parseTable(b.String(), ',')
	if len(rows) != tableRows+1 || more {
		t.Fatalf("kept %d records, more %v", len(rows), more)
	}
}

func TestClipCell(t *testing.T) {
	long := strings.Repeat("é", tableCell+5)
	for _, tc := range []struct{ in, want string }{
		{"plain", "plain"},
		{"a\nb\r\nc", "a b c"},
		{"  spaced \t out ", "spaced out"},
		{"‮evil", "U+202Eevil"},
		{strings.Repeat("x", tableCell), strings.Repeat("x", tableCell)},
		{strings.Repeat("x", tableCell+1), strings.Repeat("x", tableCell) + "…"},
		{long, strings.Repeat("é", tableCell) + "…"},
	} {
		if got := clipCell(tc.in); got != tc.want {
			t.Errorf("clipCell(%.20q) = %.30q, want %.30q", tc.in, got, tc.want)
		}
	}
}

func TestNewDocTableColumnsAndNumbers(t *testing.T) {
	tb := newDocTable([][]string{{"name", "qty", "price", "tag"}, {"a", "1", "-2.50", "x"}, {"b", "20", "3e2", ""}, {"c", "n/a", "4"}}, false)
	if tb.cols != 4 || tb.dataRows() != 3 {
		t.Fatalf("cols %d rows %d", tb.cols, tb.dataRows())
	}
	want := []bool{false, false, true, false}
	if !reflect.DeepEqual(tb.numeric, want) {
		t.Fatalf("numeric = %v, want %v", tb.numeric, want)
	}
	if tb.cell(3, 3) != "" || tb.cell(9, 0) != "" {
		t.Fatal("a missing cell is not empty")
	}
}

type windowModel struct {
	ReadModel
	lines []string
	size  int
	cut   map[int]int64
	calls []int
	err   error
}

func (m *windowModel) DocWindow(_ context.Context, _, _ string, from int) (DocWindow, error) {
	m.calls = append(m.calls, from)
	if m.err != nil {
		return DocWindow{}, m.err
	}
	end := min(from-1+m.size, len(m.lines))
	w := DocWindow{From: from, Lines: m.lines[from-1 : end]}
	for line, n := range m.cut {
		if line >= from && line <= end {
			w.Cut = append(w.Cut, DocCut{Line: line, Bytes: n})
		}
	}
	if end < len(m.lines) {
		w.Next = end + 1
	}
	return w, nil
}

func TestReadTableReadsOnlyTheWindowsItNeeds(t *testing.T) {
	m := &windowModel{lines: strings.Split(strings.TrimSpace(records(50000)), "\n"), size: 500}
	tb, err := readTable(context.Background(), m, "f", "x.csv", "csv")
	if err != nil {
		t.Fatal(err)
	}
	if tb.dataRows() != tableRows || !tb.more {
		t.Fatalf("rows %d more %v", tb.dataRows(), tb.more)
	}
	if len(m.calls) > tableWindows || len(m.calls) < 4 {
		t.Fatalf("read windows %v", m.calls)
	}
}

func TestReadTableStopsAtTheWindowLimit(t *testing.T) {
	lines := []string{"a,b"}
	for range 40 {
		lines = append(lines, "\"one\ntwo\ntwo\ntwo\ntwo\ntwo\ntwo\ntwo\",x")
	}
	m := &windowModel{lines: strings.Split(strings.Join(lines, "\n"), "\n"), size: 3}
	tb, err := readTable(context.Background(), m, "f", "x.csv", "csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.calls) != tableWindows || !tb.more {
		t.Fatalf("calls %v more %v rows %d", m.calls, tb.more, tb.dataRows())
	}
}

func TestReadTableSmallFileIsOneWindowAndNotMore(t *testing.T) {
	m := &windowModel{lines: []string{"a,b", "1,2"}, size: 500}
	tb, err := readTable(context.Background(), m, "f", "x.csv", "csv")
	if err != nil || tb.more || tb.dataRows() != 1 || len(m.calls) != 1 {
		t.Fatalf("%v %+v %v", err, tb, m.calls)
	}
}

func TestReadTableMarksACutLine(t *testing.T) {
	m := &windowModel{lines: []string{"a,b", "1,xxxxxxxx"}, size: 500, cut: map[int]int64{2: 4096}}
	tb, err := readTable(context.Background(), m, "f", "x.csv", "csv")
	if err != nil {
		t.Fatal(err)
	}
	if got := tb.cell(1, 1); !strings.HasSuffix(got, "4 KiB more on this line") {
		t.Fatalf("cell = %q", got)
	}
}

func TestReadTableSplitsOnTheKindsDelimiter(t *testing.T) {
	m := &windowModel{lines: []string{"a\tb,c", "1\t2,3"}, size: 500}
	tsv, _ := readTable(context.Background(), m, "f", "x.tsv", "tsv")
	csv, _ := readTable(context.Background(), m, "f", "x.csv", "csv")
	if tsv.cols != 2 || tsv.cell(0, 1) != "b,c" {
		t.Errorf("tsv: %d columns, %q", tsv.cols, tsv.cell(0, 1))
	}
	if csv.cols != 2 || csv.cell(0, 0) != "a b" {
		t.Errorf("csv: %d columns, %q", csv.cols, csv.cell(0, 0))
	}
}

func TestReadTableReportsAReadError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := readTable(context.Background(), &windowModel{err: boom}, "f", "x.csv", "csv"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
