package harness

import (
	"regexp"
	"strings"
	"time"
)

var (
	limitLines = map[string]*regexp.Regexp{
		"claude": regexp.MustCompile(`(?m)^[^\w\n]*(You've hit your [^\n]*limit[^\n]*)`),
		"codex":  regexp.MustCompile(`(?m)^[^\w\n]*(You've hit your usage limit[^\n]*)`),
		"agy":    regexp.MustCompile(`(?m)^[^\w\n]*(Individual quota reached\.(?:[^\n]*\n){0,2}?[^\n]*?Resets\s+in\s+(\w+)\.)`),
	}
	wrapped = regexp.MustCompile(`\s*\n\s*`)
)

func Limit(name, screen string) (string, bool) {
	re, ok := limitLines[name]
	if !ok {
		return "", false
	}
	m := re.FindStringSubmatch(screen)
	if m == nil {
		return "", false
	}
	line := wrapped.ReplaceAllString(strings.TrimSpace(m[1]), " ")
	if len(m) > 2 {
		if d, err := time.ParseDuration(m[2]); err == nil {
			line += " (~" + time.Now().Add(d).UTC().Format(time.RFC3339) + ")"
		}
	}
	return line, true
}
