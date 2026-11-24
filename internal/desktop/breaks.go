package desktop

import (
	"regexp"
	"slices"
	"strings"
)

const maxQuoteDepth = 32

type lineKind uint8

const (
	kindOther lineKind = iota
	kindPlain
	kindItem
)

var (
	bulletItem  = regexp.MustCompile(`^[-+*]([ \t]|$)`)
	orderedItem = regexp.MustCompile(`^[0-9]{1,9}[.)]([ \t]|$)`)
	atxHeading  = regexp.MustCompile(`^#{1,6}([ \t]|$)`)
	refDef      = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:`)
	underline   = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
	delimRow    = regexp.MustCompile(`^[ \t]*\|?[ \t]*:?-+:?[ \t]*(\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$`)
)

func softBreaks(src string) string {
	if !strings.ContainsAny(src, "\n\r") {
		return src
	}
	text := strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
	return strings.Join(markBreaks(strings.Split(text, "\n"), 0), "\n")
}

func markBreaks(lines []string, depth int) []string {
	out := slices.Clone(lines)
	kinds := make([]lineKind, len(lines))
	fence, fenceIndent, codeIndent, math, mathEnds := "", 0, 0, "", -1
	inList, table, afterDef := false, false, false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimLeft(l, " \t")
		ind := indentOf(l)
		prev, prevBlank := kindOther, i == 0
		if i > 0 {
			prev, prevBlank = kinds[i-1], strings.TrimSpace(lines[i-1]) == ""
		}
		wasDef := afterDef
		afterDef = false
		if math != "" && t != "" && ind <= mathEnds {
			math = ""
		}
		switch {
		case math != "":
			if strings.HasSuffix(strings.TrimRight(l, " \t"), math) {
				math = ""
			}
		case fence != "":
			if ind < fenceIndent+4 && closesFence(t, fence) {
				fence = ""
			}
		case t == "":
			table = false
		case wasDef:
		case codeIndent > 0 && ind >= codeIndent:
		case table && strings.Contains(l, "|"):
		case ind >= 4 && prev == kindOther && !inList:
			codeIndent = 4
		case inList && prevBlank && ind >= 6:
			codeIndent = 6
		default:
			codeIndent, table = 0, false
			if ind == 0 && prevBlank && !bulletItem.MatchString(t) && !orderedItem.MatchString(t) {
				inList = false
			}
			closer, mathDone, mathOK := mathOpens(t)
			mathOK = mathOK && ind < 4
			switch {
			case opensFence(t) != "":
				fence, fenceIndent = opensFence(t), ind
			case mathOK:
				if !mathDone {
					math, mathEnds = closer, -1
					if inList && ind > 0 {
						mathEnds = ind - 1
					}
				}
			case refDef.MatchString(l):
				afterDef = strings.HasSuffix(strings.TrimSpace(l), ":")
			case dashRule(t) && (prev == kindPlain || prev == kindItem && ind > 0):
				out[i] = l[:len(l)-len(t)] + "***"
			case rule(t):
			case strings.HasPrefix(t, ">") && depth >= maxQuoteDepth:
			case strings.HasPrefix(t, ">"):
				j := i
				var prefixes, inner []string
				for ; j < len(lines); j++ {
					u := strings.TrimLeft(lines[j], " \t")
					if !strings.HasPrefix(u, ">") {
						break
					}
					rest := strings.TrimPrefix(u[1:], " ")
					prefixes = append(prefixes, lines[j][:len(lines[j])-len(rest)])
					inner = append(inner, rest)
				}
				for k, s := range markBreaks(inner, depth+1) {
					out[i+k] = prefixes[k] + s
				}
				i = j - 1
			case atxHeading.MatchString(t), underline.MatchString(l):
			case bulletItem.MatchString(t) || orderedItem.MatchString(t):
				inList = true
				c, d, ok := mathOpens(itemContent(t))
				if f := opensFence(itemContent(t)); f != "" {
					fence, fenceIndent = f, ind
				} else if ok {
					if !d {
						math, mathEnds = c, ind
					}
				} else {
					kinds[i] = kindItem
				}
			case tableHead(lines, i):
				table = true
			default:
				kinds[i] = kindPlain
			}
		}
	}
	for i := 0; i < len(lines); {
		if kinds[i] == kindOther {
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && kinds[j] == kindPlain {
			j++
		}
		inCode := codeSpanNewlines(lines[i:j])
		for k := i; k+1 < j; k++ {
			if !inCode[k-i] && !hardEnd(lines[k]) {
				out[k] += `\`
			}
		}
		i = j
	}
	return out
}

func indentOf(l string) int {
	n := 0
	for _, c := range []byte(l) {
		switch c {
		case ' ':
			n++
		case '\t':
			n += 4 - n%4
		default:
			return n
		}
	}
	return n
}

func mathOpens(t string) (closer string, done, ok bool) {
	open := "$$"
	closer = "$$"
	switch {
	case strings.HasPrefix(t, "$$"):
	case strings.HasPrefix(t, `\[`):
		open, closer = `\[`, `\]`
	default:
		return "", false, false
	}
	body := t[len(open):]
	j := strings.Index(body, closer)
	if j < 0 {
		return closer, false, true
	}
	if strings.Trim(body[j+len(closer):], " \t") != "" {
		return "", false, false
	}
	return closer, true, true
}

func opensFence(t string) string {
	for _, c := range []string{"`", "~"} {
		run := len(t) - len(strings.TrimLeft(t, c))
		if run >= 3 && t[0] == c[0] && !(c == "`" && strings.Contains(t[run:], "`")) {
			return strings.Repeat(c, run)
		}
	}
	return ""
}

func itemContent(t string) string {
	t = strings.TrimLeft(t, "0123456789")
	return strings.TrimLeft(t[1:], " \t")
}

func closesFence(t, open string) bool {
	return strings.HasPrefix(t, open) && strings.Trim(strings.TrimRight(t, " \t"), open[:1]) == ""
}

func rule(t string) bool {
	s := strings.ReplaceAll(strings.ReplaceAll(t, " ", ""), "\t", "")
	return len(s) >= 3 && strings.Trim(s, s[:1]) == "" && strings.Contains("-*_", s[:1])
}

func dashRule(t string) bool {
	s := strings.TrimRight(t, " \t")
	return len(s) >= 3 && strings.Trim(s, "-") == ""
}

func tableHead(lines []string, i int) bool {
	if i+1 >= len(lines) || !strings.Contains(lines[i], "|") || !strings.Contains(lines[i+1], "|") || !delimRow.MatchString(lines[i+1]) {
		return false
	}
	cells := func(s string) int { return strings.Count(strings.Trim(strings.TrimSpace(s), "|"), "|") }
	return cells(lines[i]) == cells(lines[i+1])
}

func hardEnd(l string) bool {
	if strings.HasSuffix(l, "  ") {
		return true
	}
	return (len(l)-len(strings.TrimRight(l, `\`)))%2 == 1
}

func codeSpanNewlines(lines []string) []bool {
	in := make([]bool, len(lines))
	text := strings.Join(lines, "\n")
	none := map[int]bool{}
	line := 0
	for i := 0; i < len(text); {
		switch c := text[i]; {
		case c == '\n':
			line++
			i++
		case c == '\\' && i+1 < len(text) && text[i+1] != '\n':
			i += 2
		case c == '`':
			n := runLen(text, i)
			j := -1
			if !none[n] {
				j = closingRun(text, i+n, n)
				none[n] = j < 0
			}
			if j < 0 {
				i += n
				continue
			}
			for k := i + n; k < j; k++ {
				if text[k] == '\n' {
					in[line] = true
					line++
				}
			}
			i = j + n
		default:
			i++
		}
	}
	return in
}

func runLen(s string, i int) int {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	return n
}

func closingRun(s string, from, n int) int {
	for j := from; j < len(s); {
		if s[j] != '`' {
			j++
			continue
		}
		m := runLen(s, j)
		if m == n {
			return j
		}
		j += m
	}
	return -1
}
