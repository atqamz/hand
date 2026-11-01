package harness

import (
	"regexp"
	"strings"
)

type Permission struct {
	Tool    string
	Command string
	Approve string
	Always  string
	Deny    string
	Pattern string
}

var (
	optionLine  = regexp.MustCompile(`^(?:[❯›]\s*)?([1-9])\.\s+(.+?)$`)
	ruleLine    = regexp.MustCompile(`^\s*(?:[─━]{10,}\s*|╭.*)$`)
	boxEdge     = regexp.MustCompile(`^\s*[│┃]|\s*[│┃]?\s*$`)
	autoDenyRun = regexp.MustCompile(`deny this request in \d+:\d{2}`)
	alwaysText  = regexp.MustCompile(`^Yes, and (?:don't|don’t|do not) ask again for (.+?) commands?(?: (?:in|on) .*)?$`)
	patternText = regexp.MustCompile(`^[A-Za-z0-9_./=@%+-]+(?: [A-Za-z0-9_./=@%+-]+)*$`)
)

func ClaudePermission(screen string) (Permission, bool) {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if ruleLine.MatchString(lines[i]) {
			lines = lines[i+1:]
			break
		}
	}
	clean := make([]string, len(lines))
	for i, l := range lines {
		clean[i] = boxEdge.ReplaceAllString(l, "")
	}
	end := -1
	for i := len(clean) - 1; i >= 0 && end < 0; i-- {
		if optionLine.MatchString(strings.TrimSpace(clean[i])) {
			end = i
		}
	}
	if end < 0 {
		return Permission{}, false
	}
	start := end
	for start > 0 && optionLine.MatchString(strings.TrimSpace(clean[start-1])) {
		start--
	}
	var p Permission
	for i, l := range clean[start : end+1] {
		m := optionLine.FindStringSubmatch(strings.TrimSpace(l))
		if m[1] != string(rune('1'+i)) {
			return Permission{}, false
		}
		text := strings.TrimSpace(m[2])
		switch {
		case strings.EqualFold(text, "yes"):
			p.Approve = m[1]
		case strings.HasPrefix(text, "No"):
			p.Deny = m[1]
		case alwaysText.MatchString(text):
			p.Always = m[1]
			p.Pattern = alwaysText.FindStringSubmatch(text)[1]
		}
	}
	if p.Approve == "" || p.Deny == "" {
		return Permission{}, false
	}
	body, question := clean[:start], -1
	for i, l := range body {
		if strings.HasSuffix(l, "?") && strings.TrimSpace(l) != "" {
			question = i
		}
	}
	if question < 1 {
		return Permission{}, false
	}
	header, rest := "", []string{}
	for _, l := range body[:question] {
		switch {
		case header == "" && strings.TrimSpace(l) != "":
			header = strings.TrimSpace(l)
		case header != "" && !autoDenyRun.MatchString(l):
			rest = append(rest, l)
		}
	}
	p.Command = dedent(rest)
	if header == "" || p.Command == "" {
		return Permission{}, false
	}
	p.Tool = strings.TrimSpace(strings.TrimSuffix(header, "command"))
	p.Pattern = strings.TrimSuffix(p.Pattern, ":*")
	if p.Tool != "Bash" || !patternText.MatchString(p.Pattern) || strings.Contains(" "+p.Pattern+" ", " and ") {
		p.Always, p.Pattern = "", ""
	}
	return p, true
}

func ClaudeAllowRule(pattern string) string { return "Bash(" + pattern + ":*)" }

func dedent(lines []string) string {
	indent := -1
	for _, l := range lines {
		if t := strings.TrimLeft(l, " \t"); t != "" && (indent < 0 || len(l)-len(t) < indent) {
			indent = len(l) - len(t)
		}
	}
	if indent < 0 {
		return ""
	}
	for i, l := range lines {
		lines[i] = strings.TrimRight(l[min(indent, len(l)):], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
