package board

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

const (
	appLinkMax  = 213
	appLinkHead = "handboard://c#"
	appCodeLen  = 10
)

var (
	errAppLink = errors.New("this fleet cannot show a pairing link")
	appIDre    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	appCodeRe  = regexp.MustCompile(`^[` + codeChars + `]{10}$`)
	appRelayRe = regexp.MustCompile(`^https://([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*\.?)(:([1-9][0-9]{0,4}))?/?$`)
)

func appLink(endpoint, relay, fleet, code string) (string, error) {
	code = strings.ReplaceAll(code, "-", "")
	if !appIDre.MatchString(endpoint) || !state.FleetID.MatchString(fleet) || len(code) != appCodeLen || !appCodeRe.MatchString(code) {
		return "", errAppLink
	}
	link := appLinkHead + "v=1&e=" + endpoint + "&f=" + fleet + "&c=" + code
	if !publicRelay(relay) {
		if !appRelay(relay) {
			return "", errAppLink
		}
		link += "&r=" + appEscape(relay)
	}
	if len(link) > appLinkMax {
		return "", errAppLink
	}
	return link, nil
}

func publicRelay(relay string) bool {
	if relay == "" || relay == "none" {
		return true
	}
	u, err := url.Parse(relay)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return host == "n0.iroh.link" || strings.HasSuffix(host, ".n0.iroh.link")
}

func appRelay(relay string) bool {
	m := appRelayRe.FindStringSubmatch(relay)
	if m == nil || len(m[1]) > 253 {
		return false
	}
	labels := strings.Split(strings.TrimSuffix(m[1], "."), ".")
	if numericLabel(labels[len(labels)-1]) {
		return false
	}
	if m[6] != "" {
		if port, _ := strconv.Atoi(m[6]); port > 65535 {
			return false
		}
	}
	return true
}

func numericLabel(label string) bool {
	if strings.Trim(label, "0123456789") == "" {
		return true
	}
	rest, ok := strings.CutPrefix(label, "0x")
	return ok && rest != "" && strings.Trim(rest, "0123456789abcdef") == ""
}

func appEscape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
