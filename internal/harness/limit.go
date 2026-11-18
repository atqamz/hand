package harness

import (
	"regexp"
	"strings"
	"time"
)

var (
	limitLines = map[string]*regexp.Regexp{
		"claude":  regexp.MustCompile(`(?m)^[^\w\n]*(You've hit your [^\n]*limit[^\n]*)`),
		"codex":   regexp.MustCompile(`(?m)^[^\w\n]*(You've hit your usage limit[^\n]*)`),
		"agy":     regexp.MustCompile(`(?m)^[^\w\n]*(Individual quota reached\.(?:[^\n]*\n){0,2}?[^\n]*?Resets\s+in\s+(\w+)\.)`),
		"copilot": regexp.MustCompile(`(?m)^[ \t]*(?:●[ \t]*)?(Session\s+limit\s+reached\.\s+Increase\s+or\s+unset\s+the\s+session\s+limit\s+to\s+continue\.)`),
	}
	apiErrorLines = map[string]*regexp.Regexp{
		"claude": regexp.MustCompile(`(?m)^[^\w\n]*(API Error:[^\n]*)`),
	}
	turnBullet = regexp.MustCompile(`(?m)^●`)
	ompBanner  = regexp.MustCompile(`(?m)^((?:Error: |✘ )[^\n]*(?:\n[ \t]*\S[^\n]*)*?)\nDismissed when you send your next message\.[ \t]*(?:\n[^\n]*){0,6}\z`)
	ompEntry   = regexp.MustCompile(`^(?:Error: |✘ Retry failed after \d+ attempts?: )(.*)$`)
	ompNoRetry = regexp.MustCompile(`(?i)^(?:[a-z][\w .-]{0,40}: )?40[12]\b|insufficient[_ ](?:balance|funds|credits?|quota)|credit balance|payment|billing|(?:invalid|expired)[_ ](?:x-)?api[_ -]?key|api[_ -]?key[_ ](?:is[_ ])?(?:invalid|expired)`)
	wrapped    = regexp.MustCompile(`\s*\n\s*`)
)

func Limit(name, screen string) (string, bool) {
	re, ok := limitLines[name]
	if !ok {
		return "", false
	}
	all := re.FindAllStringSubmatch(screen, -1)
	if all == nil {
		return "", false
	}
	m := all[len(all)-1]
	line := wrapped.ReplaceAllString(strings.TrimSpace(m[1]), " ")
	if len(m) > 2 {
		if d, err := time.ParseDuration(m[2]); err == nil {
			line += " (~" + time.Now().Add(d).UTC().Format(time.RFC3339) + ")"
		}
	}
	return line, true
}

const apiErrorMax = 160

func APIError(name, screen string) (string, bool) {
	if name == "omp" {
		line, ok := ompFailure(screen)
		if r := []rune(line); len(r) > apiErrorMax {
			line = string(r[:apiErrorMax]) + "…"
		}
		return line, ok
	}
	re, ok := apiErrorLines[name]
	if !ok {
		return "", false
	}
	all := re.FindAllStringSubmatchIndex(screen, -1)
	if all == nil {
		return "", false
	}
	m := all[len(all)-1]
	if turnBullet.MatchString(screen[m[1]:]) {
		return "", false
	}
	line := strings.TrimSpace(screen[m[2]:m[3]])
	if r := []rune(line); len(r) > apiErrorMax {
		line = string(r[:apiErrorMax]) + "…"
	}
	return line, true
}

func Refusal(name, screen string) (string, bool) {
	if name != "omp" {
		return "", false
	}
	line, ok := ompFailure(screen)
	return line, ok && ompNoRetry.MatchString(line)
}

func ompFailure(screen string) (string, bool) {
	all := ompBanner.FindAllStringSubmatch(strings.TrimRight(strings.ReplaceAll(screen, "\r", ""), " \t\n"), -1)
	if all == nil {
		return "", false
	}
	var entries []string
	for _, row := range strings.Split(all[len(all)-1][1], "\n") {
		row = strings.TrimSpace(row)
		if strings.HasPrefix(row, "Error: ") || strings.HasPrefix(row, "✘ ") || len(entries) == 0 {
			entries = append(entries, row)
		} else {
			entries[len(entries)-1] += " " + row
		}
	}
	m := ompEntry.FindStringSubmatch(entries[len(entries)-1])
	if m == nil {
		return "", false
	}
	return m[1], true
}
