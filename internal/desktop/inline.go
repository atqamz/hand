package desktop

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var attachToken = regexp.MustCompile(`\[attachment ([1-9][0-9]{0,3})\]`)

type inlinePart struct {
	text string
	shot int
}

func tokenNumber(tok string) int {
	n, _ := strconv.Atoi(tok[len("[attachment ") : len(tok)-1])
	return n
}

func inlineParts(text string, shots []Shot) (parts []inlinePart, placed []bool) {
	placed = make([]bool, len(shots))
	last := 0
	for _, m := range attachToken.FindAllStringIndex(text, -1) {
		n := tokenNumber(text[m[0]:m[1]])
		i := slices.IndexFunc(shots, func(s Shot) bool { return s.N == n })
		if i < 0 {
			continue
		}
		if seg := strings.TrimSpace(text[last:m[0]]); seg != "" {
			parts = append(parts, inlinePart{text: seg, shot: -1})
		}
		parts = append(parts, inlinePart{shot: i})
		placed[i], last = true, m[1]
	}
	if last == 0 {
		return []inlinePart{{text: text, shot: -1}}, placed
	}
	if seg := strings.TrimSpace(text[last:]); seg != "" {
		parts = append(parts, inlinePart{text: seg, shot: -1})
	}
	return parts, placed
}

func insertToken(text string, start, end, n int) (string, int) {
	rs := []rune(text)
	end = min(max(end, 0), len(rs))
	start = min(max(start, 0), end)
	tok := []rune(fmt.Sprintf("[attachment %d]", n))
	return string(slices.Concat(rs[:start], tok, rs[end:])), start + len(tok)
}

func dropToken(text string, gone int) string {
	return attachToken.ReplaceAllStringFunc(text, func(tok string) string {
		switch n := tokenNumber(tok); {
		case n == gone:
			return ""
		case n > gone:
			return fmt.Sprintf("[attachment %d]", n-1)
		}
		return tok
	})
}

func (a *App) chipSlot(id int) int {
	n := 0
	for _, ch := range a.Chips {
		if ch.state != chipFailed {
			n++
		}
		if ch.id == id {
			if ch.state == chipFailed {
				return 0
			}
			return n
		}
	}
	return 0
}

func (a *App) chipNumber(id int) int {
	for _, ch := range a.Chips {
		if ch.state != chipReady {
			return 0
		}
		if ch.id == id {
			return a.chipSlot(id)
		}
	}
	return 0
}

func (a *App) orphanToken() int {
	if !strings.Contains(a.Draft, "[attachment ") {
		return 0
	}
	have := 0
	for _, ch := range a.Chips {
		if ch.state != chipFailed {
			have++
		}
	}
	for _, m := range attachToken.FindAllString(a.Draft, -1) {
		if n := tokenNumber(m); n > have {
			return n
		}
	}
	return 0
}
