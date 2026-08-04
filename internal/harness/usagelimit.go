package harness

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A harness's signature for its own usage-limit stop: the harness has refused the turn because the
// account is out of quota, and the pane goes quiet with the refusal on screen.
type usageLimit struct {
	Match *regexp.Regexp
	// Reads the instant the harness predicts the quota returns, out of the text from the freshest
	// refusal onwards, and reports false when that refusal names none. Only ever a prediction, so
	// nothing decides the limit is over from it - see internal/watcher's usagelimit.go for that.
	Reset func(text string, now time.Time) (time.Time, bool)
}

// The per-harness catalogue, and the reason adding a harness here is an implementation rather than one
// more branch in the poll loop. Only claude has a signature; every other harness declines the
// capability until someone catalogues its refusal against a real limited run, the bar firstRunPrompts holds.
var usageLimits = map[string]usageLimit{
	Claude: {
		// Three wordings claude has shipped: the REPL's "limit will reset at 3pm (UTC)", the
		// machine-readable "usage limit reached|<epoch>", and the per-window "5-hour limit reached".
		// Anchored on *reached*, never "limit" alone, so an approaching-your-limit warning cannot read as a stop.
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

// DetectUsageLimit reports whether text - a pane's recent scrollback - shows name's harness stopped on
// a usage limit, plus the reset instant the message names if it names one at all. A harness with no
// catalogued signature never reports a limit.
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
	// Scrollback holds every refusal the harness has printed, not only the one that stopped this turn,
	// so the reset is read from the last match onwards: an earlier refusal names a reset already come
	// and gone, and reading it would schedule off a prediction the harness has itself superseded.
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

// Reads the reset instant out of whichever wording carried it.
func parseClaudeReset(text string, now time.Time) (time.Time, bool) {
	// The epoch form is unambiguous, so it is tried first.
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

	// The clock form names an hour in a zone that may or may not be this host's, and an unparseable or
	// unloadable one falls back to this host's rather than failing: a wait computed in the wrong zone is
	// still a bounded wait, and the attempt-and-observe loop is what decides the limit is really over.
	loc := now.Location()
	if m[4] != "" {
		if named, err := time.LoadLocation(strings.TrimSpace(m[4])); err == nil {
			loc = named
		}
	}
	local := now.In(loc)
	reset := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)
	// A limit's reset is always ahead of the message announcing it, so an hour already past today
	// resolves to the next occurrence of that clock time.
	if !reset.After(now) {
		reset = reset.AddDate(0, 0, 1)
	}
	return reset, true
}
