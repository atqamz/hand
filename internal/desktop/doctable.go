package desktop

import (
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const (
	tableRows    = 2000
	tableCell    = 300
	tableWindows = 8
	tableMinCol  = 72
	tableMaxCol  = 360
	tableEmpty   = "This file is empty."
)

type docTable struct {
	rows    [][]string
	cols    int
	numeric []bool
	widths  []float32
	more    bool
	state   ui.ListState
	chosen  int
	focus   ui.Handle
}

func delimiter(kind string) rune {
	if kind == "tsv" {
		return '\t'
	}
	return ','
}

func parseTable(src string, comma rune) ([][]string, bool) {
	src = strings.TrimPrefix(src, "\ufeff")
	cr := csv.NewReader(strings.NewReader(src))
	cr.Comma, cr.LazyQuotes, cr.FieldsPerRecord, cr.ReuseRecord = comma, true, -1, false
	var rows [][]string
	for {
		rec, err := cr.Read()
		if err != nil {
			return rows, false
		}
		if len(rows) == tableRows+1 {
			return rows, true
		}
		rows = append(rows, rec)
	}
}

func clipCell(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(shownText(s), "\t", " ")), " ")
	n := 0
	for i := range s {
		if n == tableCell {
			return s[:i] + "…"
		}
		n++
	}
	return s
}

func numericColumns(rows [][]string, cols int) []bool {
	numeric, seen := make([]bool, cols), make([]bool, cols)
	for i := range numeric {
		numeric[i] = true
	}
	for n, row := range rows {
		if n == 0 {
			continue
		}
		for i, c := range row {
			if c == "" {
				continue
			}
			seen[i] = true
			if _, err := strconv.ParseFloat(c, 64); err != nil || !strings.ContainsAny(c[:1], "+-.0123456789") ||
				!strings.ContainsAny(c[len(c)-1:], "0123456789") {
				numeric[i] = false
			}
		}
	}
	for i := range numeric {
		numeric[i] = numeric[i] && seen[i]
	}
	return numeric
}

func newDocTable(raw [][]string, more bool) *docTable {
	t := &docTable{more: more, chosen: -1}
	for _, rec := range raw {
		row := make([]string, len(rec))
		for i, c := range rec {
			row[i] = clipCell(c)
		}
		t.cols = max(t.cols, len(row))
		t.rows = append(t.rows, row)
	}
	t.numeric = numericColumns(t.rows, t.cols)
	t.state.Selected = &t.chosen
	return t
}

func (t *docTable) dataRows() int { return max(len(t.rows)-1, 0) }

func (t *docTable) cell(row, col int) string {
	if row < 0 || row >= len(t.rows) || col >= len(t.rows[row]) {
		return ""
	}
	return t.rows[row][col]
}

func (t *docTable) sizeColumns(c *ui.Context) {
	if t.widths != nil {
		return
	}
	t.widths = make([]float32, t.cols)
	for col := range t.widths {
		w := float32(tableMinCol)
		for row := 0; row < min(len(t.rows), 60); row++ {
			if s := t.cell(row, col); s != "" {
				tw, _ := c.MeasureText(1e6, ui.Span{Text: s, Size: kit.Body.Size, Weight: 600})
				w = max(w, tw+2*kit.Space12+kit.Space8)
			}
		}
		t.widths[col] = min(w, tableMaxCol)
	}
}

func readTable(ctx context.Context, m ReadModel, fleet, rel, kind string) (*docTable, error) {
	var lines []string
	from, more := 1, false
	for range tableWindows {
		w, err := m.DocWindow(ctx, fleet, rel, from)
		if err != nil {
			return nil, err
		}
		base := len(lines)
		lines = append(lines, w.Lines...)
		for _, cut := range w.Cut {
			if i := cut.Line - w.From; i >= 0 && i < len(w.Lines) {
				lines[base+i] += "… " + docSize(cut.Bytes) + " more on this line"
			}
		}
		more = w.Next > 0
		rows, over := parseTable(strings.Join(lines, "\n"), delimiter(kind))
		if over || !more {
			return newDocTable(rows, over), nil
		}
		from = w.Next
	}
	rows, _ := parseTable(strings.Join(lines, "\n"), delimiter(kind))
	return newDocTable(rows, true), nil
}

func (a *App) docGridPage(c *ui.Context, k kit.Colors, p palette) {
	d, t := a.doc.doc, a.doc.table
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(kit.Space16).Padding(kit.Space16, kit.Space20, kit.Space8).Children(func() {
			a.docHead(c, k, p, d)
			a.docGridNote(c, p, t)
		})
		if t.cols == 0 {
			return
		}
		ui.Column(c).Grow(1).MinHeight(120).Padding(0, kit.Space20, kit.Space16).Children(func() {
			a.docGrid(c, k, t)
		})
	})
}

func (a *App) docGridNote(c *ui.Context, p palette, t *docTable) {
	switch {
	case t.cols == 0:
		a.quiet(c, p, tableEmpty)
	case t.dataRows() == 0:
		a.quiet(c, p, "This table has a header and no rows.")
	default:
		note := fmt.Sprintf("%s rows, %d columns", thousands(t.dataRows()), t.cols)
		if t.dataRows() == 1 {
			note = fmt.Sprintf("1 row, %d columns", t.cols)
		}
		if t.cols == 1 {
			note = strings.TrimSuffix(note, "s")
		}
		if t.more {
			note = fmt.Sprintf("The first %s rows are shown. Download saves the whole file.", thousands(t.dataRows()))
		}
		ui.Text(c, note).FontSize(kit.Meta.Size).FixedLineHeight(kit.Meta.Line).TextColor(p.neutral).FontFeatures("tnum")
	}
}

func (a *App) docGrid(c *ui.Context, k kit.Colors, t *docTable) {
	t.sizeColumns(c)
	cols := make([]ui.TableColumn, t.cols)
	for i := range cols {
		cols[i] = ui.TableColumn{Title: t.cell(0, i), ID: strconv.Itoa(i), Width: t.widths[i], MinWidth: tableMinCol}
		if i == t.cols-1 {
			cols[i].MinWidth, cols[i].Width = t.widths[i], 0
		}
		if t.numeric[i] {
			cols[i].Align = ui.End
		}
	}
	ui.Table(c, &t.state, cols, t.dataRows(), func(row, col int) {
		ui.Text(c, t.cell(row+1, col)).FontSize(kit.Body.Size).TextColor(k.FG).SingleLine().Ellipsis("…").
			FontFeatures("tnum")
	}).Grow(1).Bind(&t.focus).Label("Table of " + a.docName())
	if a.viewerFocus {
		a.viewerFocus = false
		t.focus.Focus(c)
	}
}
