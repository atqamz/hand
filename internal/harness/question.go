package harness

import (
	"regexp"
	"strings"
)

const QuestionPrefix = "question"

var (
	codexQuestion  = regexp.MustCompile(`(?m)^[ \t]*\?[ \t]+\d+[ \t]+questions?\b`)
	queuedHeader   = regexp.MustCompile(`^\W*Queued follow-up inputs\b`)
	questionBullet = regexp.MustCompile(`^[•●\-*\s]+`)
)

const questionMax = 200

func Question(name, screen string) (string, bool) {
	if name != "codex" {
		return "", false
	}
	all := codexQuestion.FindAllStringIndex(screen, -1)
	if all == nil {
		return "", false
	}
	lines := strings.Split(screen[:all[len(all)-1][0]], "\n")
	text := ""
	for i := len(lines) - 1; i >= 0 && text == ""; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" && !queuedHeader.MatchString(t) {
			text = questionBullet.ReplaceAllString(t, "")
		}
	}
	if r := []rune(text); len(r) > questionMax {
		text = string(r[:questionMax]) + "…"
	}
	if text == "" {
		return QuestionPrefix, true
	}
	return QuestionPrefix + ": " + text, true
}
