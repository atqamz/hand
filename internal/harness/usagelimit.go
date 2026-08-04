package harness

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// usageLimit is a harness's signature for its own usage-limit stop: the harness has
// refused the turn because the account is out of quota, and the pane goes quiet with
// the refusal on screen. Match recognizes that refusal; Reset reads the instant the
// harness predicts the quota returns out of the text from the freshest refusal
// onwards, and reports false when that refusal names none.
//
// A reset instant is only ever a prediction, so nothing decides from it whether the
// limit is over - it decides only when to start trying. See internal/watcher's
// usagelimit.go for what does the deciding.
type usageLimit struct {
	Match *regexp.Regexp
	Reset func(text string, now time.Time) (time.Time, bool)
}

// usageLimits is the per-harness catalogue, and the reason adding a harness here is
// an implementation rather than one more branch in the poll loop. Only claude has a
// signature: its wordings below are the ones its own limit paths emit, and every
// other harness declines the capability until someone catalogues its refusal against
// a real limited run - the same bar firstRunPrompts holds.
var usageLimits = map[string]usageLimit{
	Claude: {
		// Three wordings, all of which claude has shipped: the interactive REPL's
		// "Claude usage limit reached. Your limit will reset at 3pm (UTC).", the
		// machine-readable "Claude AI usage limit reached|<epoch>", and the
		// per-window forms ("5-hour limit reached", "you've reached your weekly
		// limit for Opus"). Deliberately anchored on the quota being *reached*, never
		// on the word "limit" alone, so claude's own approaching-your-limit warning -
		// which does not stop the turn - cannot be read as a stop.
		Match: regexp.MustCompile(`(?i)usage limit reached|\d+-hour limit reached|reached your (?:\w+ )*limit`),
		Reset: parseClaudeReset,
	},
}

// SupportsUsageLimit reports whether name's harness has a catalogued usage-limit
// signature. It gates the pane read the detection needs, so a harness that declines
// the capability costs nothing per poll rather than being probed and never matched.
func SupportsUsageLimit(name string) bool {
	return usageLimits[name].Match != nil
}

// DetectUsageLimit reports whether text - a pane's recent scrollback - shows name's
// harness stopped on a usage limit, plus the reset instant the message names if it
// names one at all. A harness with no catalogued signature never reports a limit.
//
// Scrollback holds every refusal the harness has printed, not only the one that
// stopped the current turn, so the reset is read from the last match onwards: an
// earlier refusal names a reset that has already come and gone, and reading it would
// schedule the next attempt off a prediction the harness has itself superseded.
func DetectUsageLimit(name, text string, now time.Time) (time.Time, bool) {
	limit := usageLimits[name]
	if limit.Match == nil {
		return time.Time{}, false
	}
	found := limit.Match.FindAllStringIndex(text, -1)
	if found == nil {
		return time.Time{}, false
	}
	if limit.Reset == nil {
		return time.Time{}, true
	}
	reset, ok := limit.Reset(text[found[len(found)-1][0]:], now)
	if !ok {
		return time.Time{}, true
	}
	return reset, true
}

var (
	claudeResetEpoch = regexp.MustCompile(`(?i)usage limit reached\|(\d{10})\b`)
	claudeResetClock = regexp.MustCompile(`(?i)reset(?:s|ting)?(?:\s+at)?\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\s*(?:\(([^)\n]{1,40})\))?`)
)

// parseClaudeReset reads the reset instant out of whichever wording carried it. The
// epoch form is unambiguous and is tried first; the clock form names an hour in a
// zone that may or may not be this host's, and resolves to the next occurrence of
// that clock time, since a limit's reset is always ahead of the message announcing
// it. An unparseable or unloadable zone falls back to this host's rather than
// failing: a wait computed in the wrong zone is still a bounded wait, and the
// attempt-and-observe loop is what decides the limit is actually over.
func parseClaudeReset(text string, now time.Time) (time.Time, bool) {
	if m := claudeResetEpoch.FindStringSubmatch(text); m != nil {
		secs, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(secs, 0).UTC(), true
	}

	m := claudeResetClock.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, false
	}
	hour, err := strconv.Atoi(m[1])
	if err != nil || hour > 23 {
		return time.Time{}, false
	}
	minute := 0
	if m[2] != "" {
		if minute, err = strconv.Atoi(m[2]); err != nil || minute > 59 {
			return time.Time{}, false
		}
	}
	switch strings.ToLower(m[3]) {
	case "pm":
		if hour < 12 {
			hour += 12
		}
	case "am":
		if hour == 12 {
			hour = 0
		}
	}
	if hour > 23 {
		return time.Time{}, false
	}

	loc := now.Location()
	if m[4] != "" {
		if named, err := time.LoadLocation(strings.TrimSpace(m[4])); err == nil {
			loc = named
		}
	}
	local := now.In(loc)
	reset := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	if !reset.After(now) {
		reset = reset.AddDate(0, 0, 1)
	}
	return reset, true
}
