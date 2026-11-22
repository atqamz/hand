package remote

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	maxLink  = 213
	linkHead = "handboard://c"
	alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

var (
	keyRe      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,15}$`)
	valueRe    = regexp.MustCompile(`^[A-Za-z0-9._~%-]+$`)
	endpointRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	fleetRe    = regexp.MustCompile(`^f[0-9a-f]{12}$`)
	codeRe     = regexp.MustCompile(`^[` + alphabet + `]{10}$`)
	nameRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)
	relayRe    = regexp.MustCompile(`^https://([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*\.?)(:([1-9][0-9]{0,4}))?/?$`)
)

type LinkError struct{ Reason string }

func (e *LinkError) Error() string { return "the pairing link is rejected: " + e.Reason }

type Link struct {
	Host, Fleet, Relay, Name string
	code                     func() string
}

func (l Link) Code() string {
	if l.code == nil {
		return ""
	}
	return l.code()
}

func (l Link) String() string { return "pairing link for " + Short(l.Host) }

func (l Link) Format(f fmt.State, _ rune) { _, _ = fmt.Fprint(f, l.String()) }

func (l Link) LogValue() slog.Value { return slog.StringValue(l.String()) }

func (l Link) MarshalJSON() ([]byte, error) { return json.Marshal(l.String()) }

func (l Link) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

func Short(id string) string {
	if len(id) < 8 {
		return id
	}
	return id[:4] + "…" + id[len(id)-4:]
}

func encode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func decode(v string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '%' {
			b.WriteByte(v[i])
			continue
		}
		if i+2 >= len(v) {
			return "", false
		}
		hi, lo := strings.IndexByte("0123456789ABCDEF", v[i+1]), strings.IndexByte("0123456789ABCDEF", v[i+2])
		if hi < 0 || lo < 0 {
			return "", false
		}
		b.WriteByte(byte(hi<<4 | lo))
		i += 2
	}
	out := b.String()
	return out, encode(out) == v
}

func numericLabel(label string) bool {
	if strings.Trim(label, "0123456789") == "" {
		return true
	}
	rest, ok := strings.CutPrefix(label, "0x")
	return ok && rest != "" && strings.Trim(rest, "0123456789abcdef") == ""
}

func relayURL(v string) (string, bool) {
	url, ok := decode(v)
	if !ok {
		return "", false
	}
	m := relayRe.FindStringSubmatch(url)
	if m == nil || len(m[1]) > 253 {
		return "", false
	}
	labels := strings.Split(strings.TrimSuffix(m[1], "."), ".")
	if numericLabel(labels[len(labels)-1]) {
		return "", false
	}
	if m[6] != "" {
		if port, _ := strconv.Atoi(m[6]); port > 65535 {
			return "", false
		}
	}
	return url, true
}

func reject(reason string) (Link, error) { return Link{}, &LinkError{reason} }

func ParsePasted(text string) (Link, error) { return Parse(strings.TrimSpace(text)) }

func Parse(link string) (Link, error) {
	if len(link) > maxLink {
		return reject("too_long")
	}
	for i := 0; i < len(link); i++ {
		if link[i] < 0x21 || link[i] > 0x7e {
			return reject("characters")
		}
	}
	if !strings.HasPrefix(link, "handboard://") {
		return reject("scheme")
	}
	head, fragment, ok := strings.Cut(link, "#")
	switch {
	case !ok:
		return reject("no_fragment")
	case strings.Contains(head, "?"):
		return reject("query")
	case head != linkHead:
		return reject("target")
	case !strings.HasPrefix(fragment, "v="):
		return reject("version_missing")
	}
	if version, _, _ := strings.Cut(fragment[2:], "&"); version != "1" {
		return reject("version_unsupported")
	}
	var keys []string
	values := map[string]string{}
	for _, part := range strings.Split(fragment, "&") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || !keyRe.MatchString(k) || !valueRe.MatchString(v) {
			return reject("fragment")
		}
		keys = append(keys, k)
		values[k] = v
	}
	for i, k := range keys {
		if slices.Contains(keys[:i], k) {
			return reject("duplicate_key")
		}
	}
	for _, k := range []string{"e", "f", "c"} {
		if _, ok := values[k]; !ok {
			return reject("missing_key")
		}
	}
	switch {
	case !endpointRe.MatchString(values["e"]):
		return reject("endpoint_id")
	case !fleetRe.MatchString(values["f"]):
		return reject("fleet")
	case !codeRe.MatchString(values["c"]):
		return reject("code")
	}
	pairing := values["c"]
	l := Link{Host: values["e"], Fleet: values["f"], code: func() string { return pairing }}
	if r, ok := values["r"]; ok {
		url, good := relayURL(r)
		if !good {
			return reject("relay")
		}
		l.Relay = url
	}
	if n := values["n"]; nameRe.MatchString(n) {
		l.Name = n
	}
	return l, nil
}
