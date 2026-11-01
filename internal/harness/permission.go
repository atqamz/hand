package harness

import (
	"regexp"
	"slices"
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
	dashRule    = regexp.MustCompile(`^\s*╌{10,}\s*$`)
	boxEdge     = regexp.MustCompile(`^\s*[│┃]|\s*[│┃]?\s*$`)
	autoDenyRun = regexp.MustCompile(`deny this request in \d+:\d{2}`)
	alwaysText  = regexp.MustCompile(`^Yes, and (?:don't|don’t) ask again for: (.+) \*$`)
	patternText = regexp.MustCompile(`^[A-Za-z0-9_./=@%+-]+(?: [A-Za-z0-9_./=@%+-]+)*$`)
	questionRun = regexp.MustCompile(`^Do you want .*\?$`)
	tipLine     = regexp.MustCompile(`^\s*Tip:`)
	otherDialog = regexp.MustCompile(`^(?:New MCP server found in this project\b|Exit plan mode\?)`)
)

var safeSubcommands = map[string][]string{
	"git":   {"commit", "add", "status", "diff", "log", "checkout", "branch", "stash"},
	"go":    {"test", "build", "vet"},
	"npm":   {"test", "install"},
	"cargo": {"test", "build"},
}

func safeAlways(pattern string) bool {
	w := strings.Fields(pattern)
	return len(w) > 1 && slices.Contains(safeSubcommands[w[0]], w[1])
}

func ClaudeDialog(screen string) bool {
	asked, found := false, false
	for _, l := range strings.Split(screen, "\n") {
		t := strings.TrimSpace(boxEdge.ReplaceAllString(l, ""))
		switch m := optionLine.FindStringSubmatch(t); {
		case ruleLine.MatchString(l):
			asked, found = false, false
		case otherDialog.MatchString(t):
			asked, found = false, true
		case questionRun.MatchString(t):
			asked = true
		case asked && m != nil && m[1] == "1" && strings.HasPrefix(m[2], "Yes"):
			asked, found = false, true
		}
	}
	return found
}

func ClaudePermission(screen string) (Permission, bool) {
	lines := strings.Split(screen, "\n")
	cut := false
	for i := len(lines) - 1; i >= 0 && !cut; i-- {
		if ruleLine.MatchString(lines[i]) {
			lines, cut = lines[i+1:], true
		}
	}
	if !cut {
		return Permission{}, false
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
	for start > 0 && (optionLine.MatchString(strings.TrimSpace(clean[start-1])) || optionWrap(clean[start-1])) {
		start--
	}
	for start < end && !optionLine.MatchString(strings.TrimSpace(clean[start])) {
		start++
	}
	var p Permission
	n := 0
	for i := start; i <= end; i++ {
		m := optionLine.FindStringSubmatch(strings.TrimSpace(clean[i]))
		if m == nil {
			return Permission{}, false
		}
		text := strings.TrimSpace(m[2])
		for ; i+1 < len(clean) && optionWrap(clean[i+1]); i++ {
			text += " " + strings.TrimSpace(clean[i+1])
		}
		if m[1] != string(rune('1'+n)) {
			return Permission{}, false
		}
		n++
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
	var dashes []int
	for i, l := range lines[:question] {
		if dashRule.MatchString(l) {
			dashes = append(dashes, i)
		}
	}
	headerAt := slices.IndexFunc(body[:question], func(l string) bool { return strings.TrimSpace(l) != "" })
	if headerAt < 0 {
		return Permission{}, false
	}
	p.Tool = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(body[headerAt]), "command"))
	bash := p.Tool == "Bash"
	var rest []string
	for i := headerAt + 1; i < question; i++ {
		switch {
		case bash && len(dashes) >= 2:
			if i > dashes[0] && i < dashes[len(dashes)-1] {
				rest = append(rest, body[i])
			}
		case !dashRule.MatchString(lines[i]) && !tipLine.MatchString(body[i]) && !autoDenyRun.MatchString(body[i]):
			rest = append(rest, body[i])
		}
	}
	p.Command = dedent(rest)
	if p.Command == "" {
		return Permission{}, false
	}
	if !bash {
		p.Command += "\n" + strings.TrimSpace(body[question])
	}
	if !bash || !patternText.MatchString(p.Pattern) || strings.Contains(" "+p.Pattern+" ", " and ") || !safeAlways(p.Pattern) {
		p.Always, p.Pattern = "", ""
	}
	return p, true
}

func optionWrap(l string) bool {
	return strings.TrimSpace(l) != "" && len(l)-len(strings.TrimLeft(l, " \t")) >= 4 && !optionLine.MatchString(strings.TrimSpace(l))
}

func ClaudeAllowRule(pattern string) string { return "Bash(" + pattern + " *)" }

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
