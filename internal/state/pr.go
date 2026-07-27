package state

import (
	"regexp"
	"strings"
)

// prURLPattern is the sole boundary between an untrusted string and a PR URL
// hand will ever store: strictly anchored end-to-end, no substring matching,
// since a stored value feeds gh pr merge and ghutil.PRIsMerged directly - a
// loose match here is a command-injection-adjacent risk, not just a validation
// nicety.
var prURLPattern = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/pull/([0-9]+)$`)

func ValidatePRURL(url string) bool {
	return prURLPattern.MatchString(url)
}

// ParsePRURL extracts the "owner/repo" slug from a valid PR URL.
func ParsePRURL(url string) (repoSlug string, ok bool) {
	m := prURLPattern.FindStringSubmatch(url)
	if m == nil {
		return "", false
	}
	return m[1] + "/" + m[2], true
}

// FindPRURLs returns every whitespace-delimited token in line that is exactly a
// valid PR URL, for the watcher's auto-record path.
func FindPRURLs(line string) []string {
	var urls []string
	for _, tok := range strings.Fields(line) {
		if ValidatePRURL(tok) {
			urls = append(urls, tok)
		}
	}
	return urls
}
