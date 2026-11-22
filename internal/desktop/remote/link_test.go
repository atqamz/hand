package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	hostA = "4d92adf6fd9076ddd383c22e08f6ac6fb0ce48210b69c0f954e7e199b4d96ed6"
	hostB = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	code  = "K7QM3XRD2P"
	flt   = "f0123456789ab"
)

type vectorFile struct {
	Link struct {
		Reasons []string `json:"reasons"`
	} `json:"link"`
	Valid []struct {
		Name   string `json:"name"`
		Link   string `json:"link"`
		Parsed struct {
			EndpointID string  `json:"endpoint_id"`
			Relay      *string `json:"relay"`
			Name       *string `json:"name"`
			Fleet      string  `json:"fleet"`
			Code       string  `json:"code"`
		} `json:"parsed"`
	} `json:"valid"`
	Invalid []struct {
		Name   string `json:"name"`
		Link   string `json:"link"`
		Reject string `json:"reject"`
	} `json:"invalid"`
}

func vectors(t *testing.T) vectorFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "companion-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFile
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func reasonOf(t *testing.T, link string) string {
	t.Helper()
	_, err := Parse(link)
	var le *LinkError
	if !errors.As(err, &le) {
		t.Fatalf("%q: error %v is not a LinkError", link, err)
	}
	return le.Reason
}

func TestEveryValidVectorParsesAsTheFileSays(t *testing.T) {
	v := vectors(t)
	if len(v.Valid) < 12 {
		t.Fatalf("%d valid vectors", len(v.Valid))
	}
	for _, c := range v.Valid {
		l, err := Parse(c.Link)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		p := c.Parsed
		wantRelay, wantName := "", ""
		if p.Relay != nil {
			wantRelay = *p.Relay
		}
		if p.Name != nil {
			wantName = *p.Name
		}
		if l.Host != p.EndpointID || l.Fleet != p.Fleet || l.Code() != p.Code || l.Relay != wantRelay || l.Name != wantName {
			t.Errorf("%s: got host %s fleet %s relay %q name %q, want %+v", c.Name, l.Host, l.Fleet, l.Relay, l.Name, p)
		}
	}
}

func TestEveryInvalidVectorIsRejectedForItsReason(t *testing.T) {
	v := vectors(t)
	if len(v.Invalid) < 12 {
		t.Fatalf("%d invalid vectors", len(v.Invalid))
	}
	used := map[string]bool{}
	for _, c := range v.Invalid {
		if got := reasonOf(t, c.Link); got != c.Reject {
			t.Errorf("%s: rejected for %q, want %q", c.Name, got, c.Reject)
		}
		used[c.Reject] = true
	}
	for _, r := range v.Link.Reasons {
		if !used[r] {
			t.Errorf("no invalid vector for %q", r)
		}
	}
}

func fragment(parts ...string) string { return "handboard://c#" + strings.Join(parts, "&") }

func TestTheChecksRunInTheOrderOfTheContract(t *testing.T) {
	good := []string{"v=1", "e=" + hostA, "f=" + flt, "c=" + code}
	badE, badF, badC, badR := "e="+hostA[1:], "f=g0123456789ab", "c=K7QM3XRD2O", "r=http%3A%2F%2Fx.test"
	cases := []struct{ link, want string }{
		{"http://a b" + strings.Repeat("x", 220), "too_long"},
		{"http://a b", "characters"},
		{"https://c#v=1", "scheme"},
		{"http://a", "scheme"},
		{"handboard://d", "no_fragment"},
		{"handboard://d?x", "no_fragment"},
		{"handboard://d?x#v=1", "query"},
		{"handboard://d#e=1", "target"},
		{"handboard://c#e=1&v=2", "version_missing"},
		{"handboard://c#v=2&E=x&e=1&e=2", "version_unsupported"},
		{fragment("v=1", "e=1", "e=2", "bad"), "fragment"},
		{fragment("v=1", "n=a", "n=b"), "duplicate_key"},
		{fragment("v=1", "e=zz"), "missing_key"},
		{fragment("v=1", badE, badF, badC, badR), "endpoint_id"},
		{fragment("v=1", "e="+hostA, badF, badC, badR), "fleet"},
		{fragment("v=1", "e="+hostA, "f="+flt, badC, badR), "code"},
		{fragment(append(slices.Clone(good), badR)...), "relay"},
	}
	for _, c := range cases {
		if got := reasonOf(t, c.link); got != c.want {
			t.Errorf("%.60s: rejected for %q, want %q", c.link, got, c.want)
		}
	}
}

func TestAPastedLinkIsTrimmedAndNothingElse(t *testing.T) {
	link := fragment("v=1", "e="+hostA, "f="+flt, "c="+code)
	if _, err := ParsePasted("\r\n  " + link + " \t\n"); err != nil {
		t.Fatalf("a pasted link with whitespace around it: %v", err)
	}
	if got := reasonOf(t, " "+link); got != "characters" {
		t.Fatalf("Parse reads the exact text: %q", got)
	}
	if got := reasonOf(t, strings.Replace(link, "&f=", " &f=", 1)); got != "characters" {
		t.Fatalf("a space inside the link: %q", got)
	}
	_, err := ParsePasted(strings.Replace(link, "&f=", " &f=", 1))
	var le *LinkError
	if !errors.As(err, &le) || le.Reason != "characters" {
		t.Fatalf("a pasted link with a space inside: %v", err)
	}
}

func TestAnyOrderAfterTheVersionAndAnUnknownKeyAreAccepted(t *testing.T) {
	l, err := Parse(fragment("v=1", "zz=1", "c="+code, "f="+flt, "e="+hostB))
	if err != nil || l.Host != hostB || l.Fleet != flt || l.Code() != code {
		t.Fatalf("got %+v, %v", l, err)
	}
}

func TestABrokenNameIsDroppedNotRejected(t *testing.T) {
	l, err := Parse(fragment("v=1", "e="+hostA, "f="+flt, "c="+code, "n=-bad"))
	if err != nil || l.Name != "" {
		t.Fatalf("got name %q, %v", l.Name, err)
	}
}

func TestALinkAndItsErrorsNeverPrintTheCodeTheFleetOrTheLink(t *testing.T) {
	text := fragment("v=1", "e="+hostA, "f="+flt, "c="+code, "r=https%3A%2F%2Frelay.example.test%2F", "n=laptop")
	l, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		out = append(out, fmt.Sprintf(verb, l), fmt.Sprintf(verb, &l), fmt.Sprintf(verb, []Link{l}), fmt.Sprintf(verb, map[string]Link{"k": l}))
	}
	out = append(out, fmt.Sprint(l, &l), fmt.Sprintln(l))
	_, bad := Parse(strings.Replace(text, "e="+hostA, "e="+hostA[1:], 1))
	out = append(out, bad.Error(), fmt.Sprintf("%+v", bad), fmt.Errorf("wrapped: %w", bad).Error())
	for _, s := range out {
		for _, secret := range []string{code, flt, "relay.example", "handboard://", hostA} {
			if strings.Contains(s, secret) {
				t.Errorf("%q holds %q", s, secret)
			}
		}
	}
	if got := l.String(); !strings.Contains(got, "4d92…6ed6") {
		t.Errorf("the link prints as %q, not the short ID", got)
	}
}

type holder struct{ l Link }

type exported struct{ L Link }

func TestTheCodeNeverPrintsOnAnyPath(t *testing.T) {
	l, err := Parse(fragment("v=1", "e="+hostA, "f="+flt, "c="+code, "r=https%3A%2F%2Frelay.example.test%2F", "n=laptop"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		out = append(out,
			fmt.Sprintf(verb, holder{l}), fmt.Sprintf(verb, &holder{l}), fmt.Sprintf(verb, []holder{{l}}),
			fmt.Sprintf(verb, map[string]holder{"k": {l}}), fmt.Sprintf(verb, exported{l}),
			fmt.Sprintf(verb, l.code),
		)
	}
	out = append(out, fmt.Sprintln(holder{l}), fmt.Sprint([]holder{{l}}))
	for _, v := range []any{l, &l, holder{l}, &holder{l}, exported{l}, []holder{{l}}} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	for name, h := range map[string]func(*bytes.Buffer) slog.Handler{
		"text": func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
		"json": func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
	} {
		var b bytes.Buffer
		log := slog.New(h(&b))
		log.Info("x", "link", l, "ptr", &l, "held", holder{l}, "heldptr", &holder{l}, "out", exported{l})
		out = append(out, name+": "+b.String())
	}
	for _, s := range out {
		if strings.Contains(s, code) {
			t.Errorf("%q holds the code", s)
		}
	}
	if l.Code() != code || (Link{}).Code() != "" {
		t.Errorf("Code() = %q, zero %q", l.Code(), (Link{}).Code())
	}
}

func TestADirectLinkPrintsAsTheShortIDUnderEveryPath(t *testing.T) {
	l, err := Parse(fragment("v=1", "e="+hostA, "f="+flt, "c="+code, "n=laptop"))
	if err != nil {
		t.Fatal(err)
	}
	want := "pairing link for 4d92…6ed6"
	raw, _ := json.Marshal(l)
	var b bytes.Buffer
	slog.New(slog.NewJSONHandler(&b, nil)).Info("x", "link", l)
	var rec struct{ Link string }
	if err := json.Unmarshal(b.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	b.Reset()
	slog.New(slog.NewTextHandler(&b, nil)).Info("x", "link", l)
	for name, got := range map[string]string{
		"%v": fmt.Sprintf("%v", l), "%+v": fmt.Sprintf("%+v", l), "%#v": fmt.Sprintf("%#v", l),
		"json": string(raw), "slog json": rec.Link, "slog text": b.String(),
	} {
		if !strings.Contains(got, want) || strings.Contains(got, flt) || strings.Contains(got, "laptop") {
			t.Errorf("%s prints %q", name, got)
		}
	}
}

func TestShortKeepsTheFirstAndLastFour(t *testing.T) {
	if got := Short(hostA); got != "4d92…6ed6" {
		t.Fatal(got)
	}
	if got := Short("abc"); got != "abc" {
		t.Fatal(got)
	}
}
