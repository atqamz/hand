package toon

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

type Doc struct {
	blocks []block
}

func (d *Doc) Field(key, value string) {
	d.blocks = append(d.blocks, block{kind: blockField, name: key, value: value})
}

func (d *Doc) Bool(key string, v bool) {
	d.Field(key, strconv.FormatBool(v))
}

func (d *Doc) Rows(name string, fields []string, rows [][]string) {
	d.blocks = append(d.blocks, block{kind: blockRows, name: name, fields: fields, rows: rows})
}

func (d *Doc) List(name string, items []string) {
	d.blocks = append(d.blocks, block{kind: blockList, name: name, items: items})
}

func (d *Doc) Help(lines ...string) {
	if len(lines) == 0 {
		return
	}
	d.List("help", lines)
}

func (d *Doc) ReplaceHints(old, new string) {
	for i, b := range d.blocks {
		switch {
		case b.kind == blockList && b.name == "help":
			for j, item := range b.items {
				d.blocks[i].items[j] = strings.ReplaceAll(item, old, new)
			}
		case b.kind == blockField && strings.HasSuffix(b.name, "_more"):
			d.blocks[i].value = strings.ReplaceAll(b.value, old, new)
		}
	}
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

func Value(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ",:\"\n\r\t") || strings.TrimSpace(s) != s {
		return strconv.Quote(s)
	}
	return s
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
