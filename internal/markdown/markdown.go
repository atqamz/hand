package markdown

import (
	"html"
	"html/template"
	"net/url"
	"strings"
)

func Render(text string) template.HTML {
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	var b strings.Builder
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
		case item(line, false) != "":
			i = list(&b, lines, i, "ul", false)
		case item(line, true) != "":
			i = list(&b, lines, i, "ol", true)
		default:
			var parts []string
			for ; i < len(lines) && strings.TrimSpace(lines[i]) != "" && !fence(lines[i]) && item(lines[i], false) == "" && item(lines[i], true) == ""; i++ {
				parts = append(parts, inline(lines[i], true))
			}
			b.WriteString("<p>" + strings.Join(parts, "<br>") + "</p>")
		}
	}
	return template.HTML(b.String())
}

func fence(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "```") }

func list(b *strings.Builder, lines []string, i int, tag string, ordered bool) int {
	b.WriteString("<" + tag + ">")
	for ; i < len(lines) && item(lines[i], ordered) != ""; i++ {
		b.WriteString("<li>" + inline(item(lines[i], ordered), true) + "</li>")
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

func inline(s string, links bool) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if out, n := marker(s, i, links); n > 0 {
			b.WriteString(out)
			i += n
			continue
		}
		b.WriteString(html.EscapeString(s[i : i+1]))
		i++
	}
	return b.String()
}

func marker(s string, i int, links bool) (string, int) {
	rest := s[i:]
	switch {
	case rest[0] == '`':
		if j := strings.IndexByte(rest[1:], '`'); j > 0 {
			return "<code>" + html.EscapeString(rest[1:1+j]) + "</code>", j + 2
		}
	case strings.HasPrefix(rest, "**"):
		if j := strings.Index(rest[2:], "**"); j > 0 {
			return "<strong>" + inline(rest[2:2+j], links) + "</strong>", j + 4
		}
		return html.EscapeString("**"), 2
	case rest[0] == '*' || rest[0] == '_':
		if j := closing(s, i); j > 0 {
			return "<em>" + inline(s[i+1:j], links) + "</em>", j - i + 1
		}
	case rest[0] == '[' && links:
		if out, n := link(rest); n > 0 {
			return out, n
		}
	case links && (strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://")) && (i == 0 || !word(s[i-1]) && s[i-1] != '/'):
		if u, n := bare(rest); n > 0 {
			return anchor(u, html.EscapeString(u)), n
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
	return 0
}

func link(rest string) (string, int) {
	mid := strings.Index(rest, "](")
	if mid < 1 {
		return "", 0
	}
	end := strings.IndexByte(rest[mid+2:], ')')
	if end < 0 {
		return "", 0
	}
	u := rest[mid+2 : mid+2+end]
	if !safe(u) {
		return "", 0
	}
	return anchor(u, inline(rest[1:mid], false)), mid + 3 + end
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

func word(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}
