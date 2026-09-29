package markdown

import (
	"html"
	"html/template"
	"net/url"
	"regexp"
	"strings"
)

func Render(text string) template.HTML {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	var b strings.Builder
	blocks(&b, lines, true, "")
	return template.HTML(b.String())
}

func RenderRefs(text, base string) template.HTML {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	var b strings.Builder
	blocks(&b, lines, true, base+"/ref/")
	return template.HTML(b.String())
}

func blocks(b *strings.Builder, lines []string, quotes bool, refs string) {
	for i := 0; i < len(lines); {
		line := lines[i]
		switch {
		case strings.TrimSpace(line) == "":
			i++
		case fence(line):
			j := i + 1
			for j < len(lines) && !fence(lines[j]) {
				j++
			}
			b.WriteString("<pre><code>" + html.EscapeString(strings.Join(lines[i+1:j], "\n")) + "</code></pre>")
			i = j + 1
		case rule(line):
			b.WriteString("<hr>")
			i++
		case heading(line) > 0:
			tag := "h3"
			if heading(line) > 2 {
				tag = "h4"
			}
			b.WriteString("<" + tag + ">" + inline(strings.TrimSpace(line[heading(line):]), true, refs) + "</" + tag + ">")
			i++
		case quotes && strings.HasPrefix(line, ">"):
			var inner []string
			for ; i < len(lines) && strings.HasPrefix(lines[i], ">"); i++ {
				inner = append(inner, strings.TrimPrefix(lines[i][1:], " "))
			}
			b.WriteString("<blockquote>")
			blocks(b, inner, false, refs)
			b.WriteString("</blockquote>")
		case tableAt(lines, i):
			i = table(b, lines, i, refs)
		case item(line, false) != "":
			i = list(b, lines, i, "ul", false, refs)
		case item(line, true) != "":
			i = list(b, lines, i, "ol", true, refs)
		default:
			var parts []string
			for ; i < len(lines) && strings.TrimSpace(lines[i]) != "" && (len(parts) == 0 || !starts(lines, i, quotes)); i++ {
				parts = append(parts, inline(lines[i], true, refs))
			}
			b.WriteString("<p>" + strings.Join(parts, "<br>") + "</p>")
		}
	}
}

func starts(lines []string, i int, quotes bool) bool {
	line := lines[i]
	return fence(line) || rule(line) || heading(line) > 0 || quotes && strings.HasPrefix(line, ">") || tableAt(lines, i) || item(line, false) != "" || item(line, true) != ""
}

func tableAt(lines []string, i int) bool {
	if i+1 >= len(lines) || !strings.Contains(lines[i], "|") {
		return false
	}
	delim := cells(lines[i+1])
	for _, d := range delim {
		if !align.MatchString(d) {
			return false
		}
	}
	return len(delim) == len(cells(lines[i]))
}

var align = regexp.MustCompile(`^:?-+:?$`)

func cells(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	if strings.HasSuffix(t, "|") && !strings.HasSuffix(t, "\\|") {
		t = t[:len(t)-1]
	}
	var out []string
	start := 0
	for j := 0; j < len(t); j++ {
		if t[j] == '|' && (j == 0 || t[j-1] != '\\') {
			out = append(out, t[start:j])
			start = j + 1
		}
	}
	out = append(out, t[start:])
	for k, c := range out {
		out[k] = strings.ReplaceAll(strings.TrimSpace(c), "\\|", "|")
	}
	return out
}

func table(b *strings.Builder, lines []string, i int, refs string) int {
	head := cells(lines[i])
	classes := make([]string, len(head))
	for k, d := range cells(lines[i+1]) {
		switch {
		case strings.HasPrefix(d, ":") && strings.HasSuffix(d, ":"):
			classes[k] = "c"
		case strings.HasSuffix(d, ":"):
			classes[k] = "r"
		case strings.HasPrefix(d, ":"):
			classes[k] = "l"
		}
	}
	var rows [][]string
	for i += 2; i < len(lines) && strings.TrimSpace(lines[i]) != "" && strings.Contains(lines[i], "|") && !fence(lines[i]); i++ {
		row := cells(lines[i])
		for len(row) < len(head) {
			row = append(row, "")
		}
		rows = append(rows, row[:len(head)])
	}
	for k := range head {
		if classes[k] == "" && numericColumn(rows, k) {
			classes[k] = "r"
		}
	}
	b.WriteString(`<div class="table" role="region" aria-label="Table" tabindex="0"><table><thead><tr>`)
	for k, c := range head {
		b.WriteString(cell("th", classes[k], c, refs))
	}
	b.WriteString("</tr></thead><tbody>")
	for _, row := range rows {
		b.WriteString("<tr>")
		for k, c := range row {
			b.WriteString(cell("td", classes[k], c, refs))
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table></div>")
	return i
}

func cell(tag, class, text, refs string) string {
	open := "<" + tag + ">"
	if class != "" {
		open = "<" + tag + ` class="` + class + `">`
	}
	return open + inline(text, true, refs) + "</" + tag + ">"
}

func numericColumn(rows [][]string, k int) bool {
	seen := false
	for _, row := range rows {
		if row[k] == "" {
			continue
		}
		if !numeric(row[k]) {
			return false
		}
		seen = true
	}
	return seen
}

func numeric(text string) bool {
	digit := false
	for _, r := range text {
		switch {
		case r >= '0' && r <= '9':
			digit = true
		case strings.ContainsRune("*_ .,%kKMx×~≈<>+-–—→", r):
		default:
			return false
		}
	}
	return digit
}

func rule(line string) bool {
	t := strings.ReplaceAll(strings.TrimSpace(line), " ", "")
	return len(t) >= 3 && strings.Trim(t, t[:1]) == "" && strings.ContainsAny(t[:1], "-*_")
}

func heading(line string) int {
	n := 0
	for n < len(line) && n < 7 && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n == len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

func fence(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "```") }

func list(b *strings.Builder, lines []string, i int, tag string, ordered bool, refs string) int {
	b.WriteString("<" + tag + ">")
	for ; i < len(lines) && item(lines[i], ordered) != ""; i++ {
		b.WriteString("<li>" + inline(item(lines[i], ordered), true, refs) + "</li>")
	}
	b.WriteString("</" + tag + ">")
	return i
}

func item(line string, ordered bool) string {
	if !ordered {
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			return line[2:]
		}
		return ""
	}
	n := 0
	for n < len(line) && line[n] >= '0' && line[n] <= '9' {
		n++
	}
	if n > 0 && strings.HasPrefix(line[n:], ". ") {
		return line[n+2:]
	}
	return ""
}

type scan struct {
	s     string
	links bool
	refs  string
	dead  map[string]bool
}

func inline(s string, links bool, refs string) string {
	sc := scan{s: s, links: links, refs: refs, dead: map[string]bool{}}
	var b strings.Builder
	for i := 0; i < len(s); {
		if out, n := sc.marker(i); n > 0 {
			b.WriteString(out)
			i += n
			continue
		}
		b.WriteString(html.EscapeString(s[i : i+1]))
		i++
	}
	return b.String()
}

func (sc scan) marker(i int) (string, int) {
	s, links := sc.s, sc.links
	rest := s[i:]
	switch {
	case rest[0] == '`' && !sc.dead["`"]:
		if j := strings.IndexByte(rest[1:], '`'); j > 0 {
			return "<code>" + html.EscapeString(rest[1:1+j]) + "</code>", j + 2
		} else if j < 0 {
			sc.dead["`"] = true
		}
	case strings.HasPrefix(rest, "~~"):
		if !sc.dead["~~"] {
			if j := strings.Index(rest[2:], "~~"); j > 0 {
				return "<del>" + inline(rest[2:2+j], links, sc.refs) + "</del>", j + 4
			} else if j < 0 {
				sc.dead["~~"] = true
			}
		}
		return html.EscapeString("~~"), 2
	case strings.HasPrefix(rest, "**"):
		if !sc.dead["**"] {
			if j := strings.Index(rest[2:], "**"); j > 0 {
				return "<strong>" + inline(rest[2:2+j], links, sc.refs) + "</strong>", j + 4
			} else if j < 0 {
				sc.dead["**"] = true
			}
		}
		return html.EscapeString("**"), 2
	case (rest[0] == '*' || rest[0] == '_') && !sc.dead[rest[:1]]:
		if j := closing(s, i); j > 0 {
			return "<em>" + inline(s[i+1:j], links, sc.refs) + "</em>", j - i + 1
		} else if j < 0 {
			sc.dead[rest[:1]] = true
		}
	case rest[0] == '[' && links && !sc.dead["["]:
		if out, n := link(rest); n > 0 {
			return out, n
		} else if n < 0 {
			sc.dead["["] = true
		}
	case links && (strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://")) && (i == 0 || !word(s[i-1]) && s[i-1] != '/'):
		if u, n := bare(rest); n > 0 {
			return anchor(u, html.EscapeString(u)), n
		}
	case sc.refs != "" && strings.IndexByte("tdar", rest[0]) >= 0 && (i == 0 || !word(s[i-1]) && s[i-1] != '/'):
		if n := refLen(rest); n > 0 {
			return `<a class="ref" href="` + html.EscapeString(sc.refs+rest[:n]) + `">` + rest[:n] + `</a>`, n
		}
	}
	return "", 0
}

func closing(s string, i int) int {
	c := s[i]
	if i+1 >= len(s) || s[i+1] == ' ' || c == '_' && i > 0 && word(s[i-1]) {
		return 0
	}
	for j := i + 2; j < len(s); j++ {
		if s[j] == c && s[j-1] != ' ' && (c == '*' || j+1 == len(s) || !word(s[j+1])) {
			return j
		}
	}
	return -1
}

func link(rest string) (string, int) {
	mid := strings.Index(rest, "](")
	if mid < 0 {
		return "", -1
	}
	if mid < 1 {
		return "", 0
	}
	end := strings.IndexByte(rest[mid+2:], ')')
	if end < 0 {
		return "", -1
	}
	u := rest[mid+2 : mid+2+end]
	if !safe(u) {
		return "", 0
	}
	return anchor(u, inline(rest[1:mid], false, "")), mid + 3 + end
}

func bare(rest string) (string, int) {
	n := strings.IndexAny(rest, " \t\n<>\"'`")
	if n < 0 {
		n = len(rest)
	}
	u := strings.TrimRight(rest[:n], ".,;:!?")
	if strings.HasSuffix(u, ")") && !strings.Contains(u, "(") {
		u = strings.TrimRight(u[:len(u)-1], ".,;:!?")
	}
	if !safe(u) {
		return "", 0
	}
	return u, len(u)
}

func safe(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func anchor(raw, text string) string {
	u, _ := url.Parse(raw)
	return `<a href="` + html.EscapeString(u.String()) + `" rel="noopener noreferrer">` + text + `</a>`
}

func refLen(rest string) int {
	n := 1
	for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
		n++
	}
	if n == 1 || rest[1] == '0' || n < len(rest) && word(rest[n]) {
		return 0
	}
	return n
}

func word(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}
