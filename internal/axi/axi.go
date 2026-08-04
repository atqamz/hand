// Package axi renders command output as TOON: scalar fields, count-prefixed
// row blocks with a schema header, and help[] next-step lines. SPECS.md's
// "Output shape" section owns the contract; https://axi.md owns the principles.
package axi

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

type blockKind int

const (
	blockField blockKind = iota
	blockRows
	blockList
)

type block struct {
	kind   blockKind
	name   string
	value  string
	fields []string
	rows   [][]string
	items  []string
}

// Doc accumulates blocks and renders them in the order they were added, so a
// command's output shape is the order its code states, not a map's.
type Doc struct {
	blocks []block
}

func (d *Doc) Field(key, value string) {
	d.blocks = append(d.blocks, block{kind: blockField, name: key, value: value})
}

func (d *Doc) Int(key string, n int) {
	d.Field(key, strconv.Itoa(n))
}

func (d *Doc) Bool(key string, v bool) {
	d.Field(key, strconv.FormatBool(v))
}

// Rows emits the `name[N]{f1,f2}:` header even when rows is empty, which is
// what makes an empty result a positive statement rather than the silence a
// broken command also produces.
func (d *Doc) Rows(name string, fields []string, rows [][]string) {
	d.blocks = append(d.blocks, block{kind: blockRows, name: name, fields: fields, rows: rows})
}

func (d *Doc) List(name string, items []string) {
	d.blocks = append(d.blocks, block{kind: blockList, name: name, items: items})
}

// Help is skipped when there is nothing to suggest: an empty help[0] block
// costs context and tells a caller nothing.
func (d *Doc) Help(lines ...string) {
	if len(lines) == 0 {
		return
	}
	d.List("help", lines)
}

func (d *Doc) Render(w io.Writer) error {
	var b strings.Builder
	for _, blk := range d.blocks {
		switch blk.kind {
		case blockField:
			fmt.Fprintf(&b, "%s: %s\n", blk.name, Value(blk.value))
		case blockRows:
			fmt.Fprintf(&b, "%s[%d]{%s}:\n", blk.name, len(blk.rows), strings.Join(blk.fields, ","))
			for _, row := range blk.rows {
				cells := make([]string, len(row))
				for i, cell := range row {
					cells[i] = Value(cell)
				}
				fmt.Fprintf(&b, "  %s\n", strings.Join(cells, ","))
			}
		case blockList:
			fmt.Fprintf(&b, "%s[%d]:\n", blk.name, len(blk.items))
			for _, item := range blk.items {
				fmt.Fprintf(&b, "  - %s\n", oneLine(item))
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func (d *Doc) String() string {
	var b strings.Builder
	_ = d.Render(&b)
	return b.String()
}

// Value quotes only what would otherwise be ambiguous: the field separator,
// the quote itself, whitespace that a reader would trim, and the empty string,
// which unquoted is indistinguishable from a field nobody emitted.
func Value(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ",:\"\n\r\t") || strings.TrimSpace(s) != s {
		return strconv.Quote(s)
	}
	return s
}

// A list item runs to end of line, so an embedded newline would silently
// become a second item.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Truncate caps a text field at budget runes and names both the full size and
// the command that recovers it, so a cut field can never be mistaken for a
// short one nor leave a caller guessing how to get the rest.
func Truncate(s string, budget int, recovery string) string {
	runes := []rune(s)
	if len(runes) <= budget {
		return s
	}
	return fmt.Sprintf("%s... (truncated, %d chars total - use %s to see complete text)",
		string(runes[:budget]), len(runes), recovery)
}

// Column is one selectable field of a row type: its schema-header name and how
// to read it off an item.
type Column[T any] struct {
	Name  string
	Value func(T) string
}

// Select narrows cols to what --fields asked for, in the order it asked, and
// rejects a name no column carries rather than silently emitting a schema
// header that promises less than the caller requested.
func Select[T any](cols []Column[T], want []string) ([]Column[T], error) {
	if len(want) == 0 {
		return cols, nil
	}
	known := make(map[string]Column[T], len(cols))
	names := make([]string, len(cols))
	for i, c := range cols {
		known[c.Name] = c
		names[i] = c.Name
	}
	out := make([]Column[T], 0, len(want))
	for _, name := range want {
		c, ok := known[name]
		if !ok {
			return nil, fmt.Errorf("unknown field %q: want one of %s", name, strings.Join(names, ", "))
		}
		out = append(out, c)
	}
	return out, nil
}

func Names[T any](cols []Column[T]) []string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return names
}

// Table renders items through cols, so the schema header and the cells can
// never disagree about which fields were emitted.
func Table[T any](d *Doc, name string, items []T, cols []Column[T]) {
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		row := make([]string, len(cols))
		for i, c := range cols {
			row[i] = c.Value(item)
		}
		rows = append(rows, row)
	}
	d.Rows(name, Names(cols), rows)
}
