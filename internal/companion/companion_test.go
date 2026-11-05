package companion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

const (
	maxLink      = 213
	linkHead     = "handboard://c"
	codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

var reasons = []string{"too_long", "characters", "scheme", "no_fragment", "query", "target", "version_missing", "version_unsupported", "fragment", "duplicate_key", "missing_key", "endpoint_id", "fleet", "code", "relay"}

var (
	keyRe      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,15}$`)
	valueRe    = regexp.MustCompile(`^[A-Za-z0-9._~%-]+$`)
	endpointRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	codeRe     = regexp.MustCompile(`^[` + codeAlphabet + `]{10}$`)
	relayRe    = regexp.MustCompile(`^https://([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*\.?)(:([1-9][0-9]{0,4}))?/?$`)
)

type parsed struct {
	Version    int     `json:"version"`
	EndpointID string  `json:"endpoint_id"`
	Relay      *string `json:"relay"`
	Fleet      string  `json:"fleet"`
	Code       string  `json:"code"`
	OpenPath   string  `json:"open_path"`
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

func validRelay(v string) (string, bool) {
	url, ok := decode(v)
	if !ok {
		return "", false
	}
	m := relayRe.FindStringSubmatch(url)
	if m == nil || len(m[1]) > 253 {
		return "", false
	}
	if m[6] != "" {
		if port, _ := strconv.Atoi(m[6]); port > 65535 {
			return "", false
		}
	}
	return url, true
}

func parse(link string) (parsed, string) {
	var p parsed
	if len(link) > maxLink {
		return p, "too_long"
	}
	for i := 0; i < len(link); i++ {
		if link[i] < 0x21 || link[i] > 0x7e {
			return p, "characters"
		}
	}
	if !strings.HasPrefix(link, linkHead[:len(linkHead)-1]) {
		return p, "scheme"
	}
	head, fragment, ok := strings.Cut(link, "#")
	switch {
	case !ok:
		return p, "no_fragment"
	case strings.Contains(head, "?"):
		return p, "query"
	case head != linkHead:
		return p, "target"
	case !strings.HasPrefix(fragment, "v="):
		return p, "version_missing"
	}
	if version, _, _ := strings.Cut(fragment[2:], "&"); version != "1" {
		return p, "version_unsupported"
	}
	var keys []string
	values := map[string]string{}
	for _, part := range strings.Split(fragment, "&") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || !keyRe.MatchString(k) || !valueRe.MatchString(v) {
			return p, "fragment"
		}
		keys = append(keys, k)
		values[k] = v
	}
	for i, k := range keys {
		if slices.Contains(keys[:i], k) {
			return p, "duplicate_key"
		}
	}
	for _, k := range []string{"e", "f", "c"} {
		if _, ok := values[k]; !ok {
			return p, "missing_key"
		}
	}
	switch {
	case !endpointRe.MatchString(values["e"]):
		return p, "endpoint_id"
	case !state.FleetID.MatchString(values["f"]):
		return p, "fleet"
	case !codeRe.MatchString(values["c"]):
		return p, "code"
	}
	p = parsed{Version: 1, EndpointID: values["e"], Fleet: values["f"], Code: values["c"]}
	p.OpenPath = "/" + p.Fleet + "/pair#" + p.Code
	if r, ok := values["r"]; ok {
		url, good := validRelay(r)
		if !good {
			return parsed{}, "relay"
		}
		p.Relay = &url
	}
	return p, ""
}

func emit(p parsed) string {
	link := fmt.Sprintf("%s#v=1&e=%s&f=%s&c=%s", linkHead, p.EndpointID, p.Fleet, p.Code)
	if p.Relay != nil {
		link += "&r=" + encode(*p.Relay)
	}
	return link
}

type vectors struct {
	Contract int `json:"contract"`
	Note     string
	Wire     struct {
		ALPN                  string         `json:"alpn"`
		StreamsPerConnection  int            `json:"streams_per_connection"`
		StreamsPerSecond      int            `json:"streams_per_second"`
		UniStreams            int            `json:"uni_streams"`
		CloseCodes            map[string]int `json:"close_codes"`
		StreamCodes           map[string]int `json:"stream_codes"`
		MaxConnectionsDefault int            `json:"max_connections_default"`
		IdleSecondsDefault    int            `json:"idle_seconds_default"`
		HandshakesInFlight    int            `json:"handshakes_in_flight"`
		HandshakeSeconds      int            `json:"handshake_seconds"`
		ConnectionsPerSource  int            `json:"connections_per_source"`
		Preface               string         `json:"preface"`
	} `json:"wire"`
	Board struct {
		CodeAlphabet          string `json:"code_alphabet"`
		CodeLength            int    `json:"code_length"`
		CodeSeconds           int    `json:"code_seconds"`
		WrongTriesPerMinute   int    `json:"wrong_tries_per_peer_per_minute"`
		WrongTriesBeforeBurn  int    `json:"wrong_tries_before_all_codes_cancel"`
		PhoneDays             int    `json:"phone_days"`
		UnpairedRequests      int    `json:"unpaired_requests_per_connection"`
		UnpairedBytes         int    `json:"unpaired_bytes_per_connection"`
		UnpairedBodySeconds   int    `json:"unpaired_body_seconds"`
		IdleConnectionSeconds int    `json:"idle_connection_seconds"`
		FleetID               string `json:"fleet_id"`
		DeviceLogin           string `json:"device_login"`
	} `json:"board"`
	Link struct {
		MaxBytes int      `json:"max_bytes"`
		Prefix   string   `json:"prefix"`
		Reasons  []string `json:"reasons"`
	} `json:"link"`
	Valid []struct {
		Name      string `json:"name"`
		Link      string `json:"link"`
		Canonical bool   `json:"canonical"`
		Parsed    parsed `json:"parsed"`
	} `json:"valid"`
	Invalid []struct {
		Name   string `json:"name"`
		Link   string `json:"link"`
		Reject string `json:"reject"`
	} `json:"invalid"`
}

func read(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func load(t *testing.T) vectors {
	t.Helper()
	var v vectors
	d := json.NewDecoder(bytes.NewReader(read(t, "docs/companion-vectors.json")))
	d.DisallowUnknownFields()
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEveryVectorParsesAsItsFileSays(t *testing.T) {
	v := load(t)
	if len(v.Valid) < 12 || len(v.Invalid) < 12 {
		t.Fatalf("%d valid and %d invalid vectors; the contract needs 12 of each", len(v.Valid), len(v.Invalid))
	}
	seen := map[string]bool{}
	longest := false
	for _, c := range v.Valid {
		if seen[c.Name] || seen[c.Link] {
			t.Errorf("%q repeats a name or a link", c.Name)
		}
		seen[c.Name], seen[c.Link] = true, true
		got, reason := parse(c.Link)
		if reason != "" || !sameParsed(got, c.Parsed) {
			t.Errorf("%s: got %+v, %q; want %+v", c.Name, got, reason, c.Parsed)
			continue
		}
		if c.Canonical && emit(got) != c.Link {
			t.Errorf("%s: a canonical link is emitted as %q", c.Name, emit(got))
		}
		longest = longest || len(c.Link) == maxLink
	}
	if !longest {
		t.Error("no valid vector is exactly the longest link")
	}
	used := map[string]bool{}
	over := false
	for _, c := range v.Invalid {
		if seen[c.Name] || seen[c.Link] {
			t.Errorf("%q repeats a name or a link", c.Name)
		}
		seen[c.Name], seen[c.Link] = true, true
		if _, reason := parse(c.Link); reason != c.Reject {
			t.Errorf("%s: rejected for %q, want %q", c.Name, reason, c.Reject)
		}
		used[c.Reject] = true
		over = over || len(c.Link) == maxLink+1
	}
	if !over {
		t.Error("no invalid vector is one byte over the longest link")
	}
	if !slices.Equal(v.Link.Reasons, reasons) {
		t.Errorf("the file lists reasons %v, the parser has %v", v.Link.Reasons, reasons)
	}
	doc := string(read(t, "docs/companion.md"))
	for _, r := range reasons {
		if !used[r] {
			t.Errorf("no invalid vector is rejected for %q", r)
		}
		if !strings.Contains(doc, "`"+r+"`") {
			t.Errorf("docs/companion.md does not name the reason %q", r)
		}
	}
	if count := fmt.Sprintf("%d valid and %d invalid links", len(v.Valid), len(v.Invalid)); !strings.Contains(doc, count) {
		t.Errorf("docs/companion.md does not say %q", count)
	}
	if v.Contract != 1 || v.Link.MaxBytes != maxLink || v.Link.Prefix != linkHead+"#" {
		t.Errorf("link header of the file is %+v", v.Link)
	}
}

func sameParsed(a, b parsed) bool {
	if (a.Relay == nil) != (b.Relay == nil) || a.Relay != nil && *a.Relay != *b.Relay {
		return false
	}
	a.Relay, b.Relay = nil, nil
	return a == b
}

func match(t *testing.T, file, pattern string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindSubmatch(read(t, file))
	if m == nil {
		t.Fatalf("%s no longer matches %s: update docs/companion.md and the vectors file with the code", file, pattern)
	}
	return string(m[1])
}

func number(t *testing.T, file, pattern string, times int) int {
	t.Helper()
	n, err := strconv.Atoi(match(t, file, pattern))
	if err != nil {
		t.Fatal(err)
	}
	return n * times
}

func TestTheNumbersInTheFileAreTheOnesInTheCode(t *testing.T) {
	v := load(t)
	const forward, args, phone, peer = "iroh/src/forward.rs", "iroh/src/args.rs", "internal/board/phone.go", "internal/board/peer.go"
	code := func(name string) string {
		return `pub const ` + name + `: VarInt = VarInt::from_u32\((\d+)\);`
	}
	ints := []struct {
		name      string
		got       int
		file, re  string
		multiples int
	}{
		{"streams per connection", v.Wire.StreamsPerConnection, forward, `STREAMS_PER_CONNECTION: u32 = (\d+);`, 1},
		{"streams per second", v.Wire.StreamsPerSecond, forward, `STREAMS_PER_SECOND: f64 = (\d+)\.0;`, 1},
		{"unidirectional streams", v.Wire.UniStreams, "iroh/src/main.rs", `max_concurrent_uni_streams\(VarInt::from_u32\((\d+)\)\)`, 1},
		{"close code full", v.Wire.CloseCodes["full"], forward, code("FULL_CODE"), 1},
		{"close code idle", v.Wire.CloseCodes["idle"], forward, code("IDLE_CODE"), 1},
		{"stream code board unreachable", v.Wire.StreamCodes["board_unreachable"], forward, code("BOARD_UNREACHABLE_CODE"), 1},
		{"stream code stream idle", v.Wire.StreamCodes["stream_idle"], forward, code("STREAM_IDLE_CODE"), 1},
		{"stream code board error", v.Wire.StreamCodes["board_error"], forward, code("BOARD_ERROR_CODE"), 1},
		{"handshakes in flight", v.Wire.HandshakesInFlight, forward, `handshakes: (\d+),\n`, 1},
		{"handshake seconds", v.Wire.HandshakeSeconds, forward, `handshake_wait: Duration::from_secs\((\d+)\),`, 1},
		{"connections per source", v.Wire.ConnectionsPerSource, forward, `per_source: (\d+),\n`, 1},
		{"max connections", v.Wire.MaxConnectionsDefault, args, `MAX_CONNECTIONS_DEFAULT: usize = (\d+);`, 1},
		{"idle seconds", v.Wire.IdleSecondsDefault, args, `IDLE_DEFAULT: Duration = Duration::from_secs\((\d+)\);`, 1},
		{"code seconds", v.Board.CodeSeconds, phone, `pairLife\s+= (\d+) \* time\.Minute`, 60},
		{"wrong tries per minute", v.Board.WrongTriesPerMinute, phone, `addrFails\s+= (\d+)\n`, 1},
		{"wrong tries before all codes cancel", v.Board.WrongTriesBeforeBurn, phone, `pairFails\s+= (\d+)\n`, 1},
		{"phone days", v.Board.PhoneDays, phone, `phoneLife\s+= (\d+) \* 24 \* time\.Hour`, 1},
		{"unpaired requests", v.Board.UnpairedRequests, peer, `peerRequests\s+= (\d+)\n`, 1},
		{"unpaired bytes", v.Board.UnpairedBytes, peer, `peerBytes\s+= (\d+) << 10`, 1024},
		{"unpaired body seconds", v.Board.UnpairedBodySeconds, peer, `peerBodyWait\s+= (\d+) \* time\.Second`, 1},
		{"idle connection seconds", v.Board.IdleConnectionSeconds, peer, `peerIdleWait\s+= (\d+) \* time\.Second`, 1},
	}
	for _, c := range ints {
		if want := number(t, c.file, c.re, c.multiples); c.got != want {
			t.Errorf("%s: the file says %d, the code says %d", c.name, c.got, want)
		}
	}
	if want := match(t, forward, `pub const ALPN: &\[u8\] = b"([^"]+)";`); v.Wire.ALPN != want {
		t.Errorf("ALPN: the file says %q, the code says %q", v.Wire.ALPN, want)
	}
	if want := match(t, phone, `codeChars\s+= "([^"]+)"`); v.Board.CodeAlphabet != want || want != codeAlphabet || len(want) != 32 {
		t.Errorf("code alphabet: the file says %q, the code says %q", v.Board.CodeAlphabet, want)
	}
	if v.Board.CodeLength != 10 || !codeRe.MatchString(strings.Repeat("0", v.Board.CodeLength)) {
		t.Errorf("code length %d is not the parser's", v.Board.CodeLength)
	}
	if v.Board.FleetID != state.FleetID.String() {
		t.Errorf("fleet ID: the file says %q, the code says %q", v.Board.FleetID, state.FleetID)
	}
	preface := strings.NewReplacer("<endpoint id>", "{peer}", "<secret>", "{}", "\n", `\n`).Replace(v.Wire.Preface)
	if !strings.Contains(string(read(t, "iroh/src/preface.rs")), `"`+preface+`"`) {
		t.Errorf("preface %q is not the one iroh/src/preface.rs writes", v.Wire.Preface)
	}
	if !strings.Contains(string(read(t, peer)), `"`+strings.SplitN(v.Wire.Preface, "1", 2)[0]+`"`) {
		t.Errorf("the Board does not read the preface word of %q", v.Wire.Preface)
	}
	if login := strings.TrimSuffix(v.Board.DeviceLogin, "<endpoint id>"); !strings.Contains(string(read(t, peer)), `"`+login+`" + c.id`) {
		t.Errorf("the Board does not build the device login %q", v.Board.DeviceLogin)
	}
}
