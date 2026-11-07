package harness

import (
	"regexp"
	"strings"
)

var (
	waitingChoice = regexp.MustCompile(`^(?:[❯›>]\s*)?(?:[1-9]\.\s+\S|(?:Yes|No)(?:,|$)|Trust and continue|Trust this folder|Continue\b|Cancel\b|Quit\b|Enter\b|Esc\b)`)
	waitingAction = regexp.MustCompile(`^(?:Allow once|Allow always|Always allow|Approve|Deny|Reject)\b`)
	waitingAsk    = regexp.MustCompile(`\?\s*$`)
	waitingTrust  = regexp.MustCompile(`(?:Quick safety check:|trust this folder\?|Do you trust .*\?)`)
	waitingFooter = regexp.MustCompile(`(?i)(Enter to confirm|Enter to select|Enter continue|Esc to cancel|Esc to close|Tab to amend|Press enter)`)
)

const waitingBottom = 8

func Waiting(name, screen string) bool {
	if _, ok := Question(name, screen); ok {
		return true
	}
	if _, ok := ClaudePermission(screen); ok || ClaudeDialog(screen) {
		return true
	}
	rows := strings.Split(screen, "\n")
	var asked, choice []int
	footer := false
	for i, l := range rows {
		if ruleLine.MatchString(l) || dashRule.MatchString(l) {
			continue
		}
		t := strings.TrimSpace(boxEdge.ReplaceAllString(l, ""))
		if waitingAction.MatchString(t) {
			return true
		}
		if waitingAsk.MatchString(t) || waitingTrust.MatchString(t) {
			asked = append(asked, i)
		}
		if waitingChoice.MatchString(t) {
			choice = append(choice, i)
		}
		footer = footer || waitingFooter.MatchString(t)
	}
	if footer && len(choice) > 0 {
		return true
	}
	floor := len(rows) - waitingBottom
	for _, q := range asked {
		for _, c := range choice {
			if c > q && c-q <= 2 || q >= floor && c >= floor {
				return true
			}
		}
	}
	return false
}
