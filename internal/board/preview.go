package board

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxPretty   = 16 << 20
	maxIndented = 4 * maxPretty
	maxRows     = 2000
	maxCell     = 300
	sandbox     = "sandbox; default-src 'none'"
)

var (
	mediaExt = map[string][]string{
		"video/mp4":  {".mp4", ".m4v"},
		"video/webm": {".webm"},
		"audio/mpeg": {".mp3"},
		"audio/wave": {".wav"},
		"audio/ogg":  {".ogg", ".oga", ".opus"},
	}
	views = map[string][2]string{
		".html": {"Preview", "Source"},
		".htm":  {"Preview", "Source"},
		".svg":  {"Picture", "Source"},
		".csv":  {"Table", "Text"},
		".tsv":  {"Table", "Text"},
		".json": {"Pretty", "As written"},
	}
)

type choice struct {
	Name, Href string
	On         bool
}

func media(sniffed, name string) string {
	if sniffed == "application/ogg" {
		sniffed = "audio/ogg"
	}
	if slices.Contains(mediaExt[sniffed], strings.ToLower(path.Ext(name))) {
		return sniffed
	}
	return ""
}

func isPDF(head []byte, name string) bool {
	return bytes.HasPrefix(head, []byte("%PDF-")) && strings.EqualFold(path.Ext(name), ".pdf")
}

func isHTML(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".html" || ext == ".htm"
}

func (b *Board) raw(w http.ResponseWriter, r *http.Request, f *os.File, fi fs.FileInfo, name, sniffed string, head []byte) {
	h := w.Header()
	text := textHead(head)
	ctype, policy := "text/plain; charset=utf-8", sandbox
	_, image := imageExt[sniffed]
	switch {
	case image && imageLike.MatchString(name):
		ctype = sniffed
	case media(sniffed, name) != "":
		ctype = media(sniffed, name)
	case isPDF(head, name):
		ctype = "application/pdf"
		h.Set("X-Frame-Options", "SAMEORIGIN")
	case text && strings.EqualFold(path.Ext(name), ".svg"):
		ctype, policy = "image/svg+xml", sandbox+"; style-src 'unsafe-inline'"
	case text && isHTML(name):
		ctype, policy = "text/html; charset=utf-8", sandbox+"; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'"
		h.Set("X-Frame-Options", "SAMEORIGIN")
	case !text:
		ctype = "application/octet-stream"
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	h.Set("Content-Type", ctype)
	h.Set("Content-Security-Policy", policy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func indented(r io.Reader) ([]byte, string) {
	raw, err := io.ReadAll(io.LimitReader(r, maxPretty))
	if err != nil || !json.Valid(raw) {
		return nil, "This is not valid JSON, so the Board shows it as written."
	}
	if out := indent(raw, maxIndented); out != nil {
		return out, ""
	}
	return nil, "This JSON is too large to pretty-print, so the Board shows it as written."
}

func indent(raw []byte, limit int) []byte {
	var out bytes.Buffer
	depth, inString, escaped := 0, false, false
	line := func() {
		out.WriteByte('\n')
		for range depth {
			out.WriteString("  ")
		}
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case inString:
			out.WriteByte(c)
			inString, escaped = !(c == '"' && !escaped), c == '\\' && !escaped
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
		case c == '{' || c == '[':
			out.WriteByte(c)
			j := i + 1
			for j < len(raw) && (raw[j] == ' ' || raw[j] == '\t' || raw[j] == '\r' || raw[j] == '\n') {
				j++
			}
			if j < len(raw) && (raw[j] == '}' || raw[j] == ']') {
				out.WriteByte(raw[j])
				i = j
			} else {
				depth++
				line()
			}
		case c == '}' || c == ']':
			depth--
			line()
			out.WriteByte(c)
		case c == ',':
			out.WriteByte(c)
			line()
		case c == ':':
			out.WriteString(": ")
		default:
			out.WriteByte(c)
			inString = c == '"'
		}
		if out.Len() > limit {
			return nil
		}
	}
	return out.Bytes()
}

func cellText(s string) string {
	if len(s) <= maxCell {
		return s
	}
	i := 0
	for n := 0; n < maxCell && i < len(s); n++ {
		_, k := utf8.DecodeRuneInString(s[i:])
		i += k
	}
	if i == len(s) {
		return s
	}
	return s[:i] + "…"
}

func visible(s string) string {
	var out strings.Builder
	for _, r := range s {
		if bidi(r) {
			fmt.Fprintf(&out, "U+%04X", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func table(r io.Reader, comma rune) (rows [][]string, numeric []bool, cut bool, err error) {
	lr := &io.LimitedReader{R: r, N: maxWhole + 1}
	cr := csv.NewReader(bufio.NewReaderSize(lr, 64<<10))
	cr.Comma, cr.LazyQuotes, cr.FieldsPerRecord = comma, true, -1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, false, err
		}
		if len(rows) == maxRows+1 {
			cut = true
			break
		}
		for i := range rec {
			rec[i] = visible(cellText(rec[i]))
		}
		rows = append(rows, rec)
	}
	return rows, columns(rows), cut || lr.N == 0, nil
}

func columns(rows [][]string) []bool {
	var numeric, seen []bool
	for n, row := range rows {
		for len(numeric) < len(row) {
			numeric, seen = append(numeric, true), append(seen, false)
		}
		for i, c := range row {
			if c = strings.TrimSpace(c); n == 0 || c == "" {
				continue
			}
			seen[i] = true
			if _, err := strconv.ParseFloat(c, 64); err != nil || !strings.ContainsAny(c[:1], "+-.0123456789") || !strings.ContainsAny(c[len(c)-1:], "0123456789") {
				numeric[i] = false
			}
		}
	}
	for i := range numeric {
		numeric[i] = numeric[i] && seen[i]
	}
	return numeric
}
