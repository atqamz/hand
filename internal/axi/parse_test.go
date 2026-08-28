package axi

import (
	"strconv"
	"strings"
)

// Both *testing.T and *rapid.T satisfy this, so a helper works the same
// inside a rapid.Check property as in a plain test.
type fataler interface {
	Fatalf(format string, args ...any)
}

// An unquoted value can never itself start with `"` - Value quotes anything
// that contains one - so that leading byte alone tells raw and quoted tokens
// apart.
func decodeTOONValue(tb fataler, token string) string {
	if !strings.HasPrefix(token, `"`) {
		return token
	}
	v, err := strconv.Unquote(token)
	if err != nil {
		tb.Fatalf("unquote %q: %v", token, err)
	}
	return v
}

func parseFieldValue(tb fataler, doc, key string) string {
	prefix := key + ": "
	for _, line := range strings.Split(doc, "\n") {
		if v, ok := strings.CutPrefix(line, prefix); ok {
			return decodeTOONValue(tb, v)
		}
	}
	tb.Fatalf("no field %q in document %q", key, doc)
	return ""
}

// A leading `"` opens a Go-quoted token that may itself carry a literal
// comma - the same assumption Value's quoting guarantees for an unquoted
// cell, which can never contain one.
func splitTOONCells(line string) []string {
	var cells []string
	for len(line) > 0 {
		if line[0] == '"' {
			end := quotedTokenEnd(line)
			cells = append(cells, line[:end])
			line = strings.TrimPrefix(line[end:], ",")
			continue
		}
		if i := strings.IndexByte(line, ','); i >= 0 {
			cells = append(cells, line[:i])
			line = line[i+1:]
			continue
		}
		cells = append(cells, line)
		line = ""
	}
	return cells
}

func quotedTokenEnd(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(s)
}

func rowsHeaderCount(tb fataler, header string) int {
	open := strings.IndexByte(header, '[')
	close := strings.IndexByte(header, ']')
	if open < 0 || close < open {
		tb.Fatalf("malformed rows header %q", header)
	}
	n, err := strconv.Atoi(header[open+1 : close])
	if err != nil {
		tb.Fatalf("malformed row count in %q: %v", header, err)
	}
	return n
}

// Stops only at the next line not indented by two spaces, independent of
// the header's own declared count, so a caller can compare the two
// (INV-OUT-1) instead of trusting the header.
func parseRowsBlock(tb fataler, doc, name string) (declared int, rows [][]string) {
	lines := strings.Split(doc, "\n")
	header := name + "["
	for i, line := range lines {
		if !strings.HasPrefix(line, header) {
			continue
		}
		declared = rowsHeaderCount(tb, line)
		for _, body := range lines[i+1:] {
			cell, ok := strings.CutPrefix(body, "  ")
			if !ok {
				break
			}
			cells := splitTOONCells(cell)
			decoded := make([]string, len(cells))
			for c, raw := range cells {
				decoded[c] = decodeTOONValue(tb, raw)
			}
			rows = append(rows, decoded)
		}
		return declared, rows
	}
	tb.Fatalf("no rows block %q in document %q", name, doc)
	return 0, nil
}
