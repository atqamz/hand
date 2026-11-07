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

func Waiting(name, screen string) bool {
	if _, ok := Question(name, screen); ok {
		return true
	}
	if _, ok := ClaudePermission(screen); ok || ClaudeDialog(screen) {
		return true
	}
	asked, choice, footer := false, false, false
	for _, l := range strings.Split(screen, "\n") {
		if ruleLine.MatchString(l) || dashRule.MatchString(l) {
			continue
		}
		t := strings.TrimSpace(boxEdge.ReplaceAllString(l, ""))
		if waitingAction.MatchString(t) {
			return true
		}
		asked = asked || waitingAsk.MatchString(t) || waitingTrust.MatchString(t)
		choice = choice || waitingChoice.MatchString(t)
		footer = footer || waitingFooter.MatchString(t)
	}
	return choice && (asked || footer)
}
