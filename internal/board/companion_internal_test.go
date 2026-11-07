package board

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type companionVectors struct {
	Valid []struct {
		Name      string `json:"name"`
		Link      string `json:"link"`
		Canonical bool   `json:"canonical"`
		Parsed    struct {
			Version    int     `json:"version"`
			EndpointID string  `json:"endpoint_id"`
			Relay      *string `json:"relay"`
			Fleet      string  `json:"fleet"`
			Code       string  `json:"code"`
			OpenPath   string  `json:"open_path"`
		} `json:"parsed"`
	} `json:"valid"`
}

func loadCompanionVectors(t *testing.T) companionVectors {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "companion-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v companionVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Valid) == 0 {
		t.Fatal("the vectors file holds no valid link")
	}
	return v
}

func TestTheEmitterReproducesEveryCanonicalVector(t *testing.T) {
	v := loadCompanionVectors(t)
	canonical := 0
	for _, c := range v.Valid {
		if !c.Canonical {
			continue
		}
		canonical++
		relay := ""
		if c.Parsed.Relay != nil {
			relay = *c.Parsed.Relay
		}
		got, err := appLink(c.Parsed.EndpointID, relay, c.Parsed.Fleet, c.Parsed.Code)
		if err != nil || got != c.Link {
			t.Errorf("%s: appLink = %q, %v; want %q", c.Name, got, err, c.Link)
		}
	}
	if canonical == 0 {
		t.Fatal("no canonical vector")
	}
}

func TestTheEmitterCanonicalisesANonCanonicalVector(t *testing.T) {
	for _, c := range loadCompanionVectors(t).Valid {
		if c.Canonical {
			continue
		}
		relay := ""
		if c.Parsed.Relay != nil {
			relay = *c.Parsed.Relay
		}
		got, err := appLink(c.Parsed.EndpointID, relay, c.Parsed.Fleet, c.Parsed.Code)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if got == c.Link {
			t.Fatalf("%s: the emitter kept the non-canonical link %q", c.Name, c.Link)
		}
		if !strings.HasPrefix(got, appLinkHead+"v=1&e=") || strings.Contains(got, "&x_note=") || strings.Contains(got, "&ui=") {
			t.Fatalf("%s: the emitter wrote %q", c.Name, got)
		}
		if c.Parsed.Relay != nil && !strings.Contains(got, "&r="+appEscape(*c.Parsed.Relay)) {
			t.Fatalf("%s: the emitter dropped the relay: %q", c.Name, got)
		}
		return
	}
	t.Fatal("no non-canonical valid vector")
}

func TestTheEmitterMovesEachField(t *testing.T) {
	base, err := appLink(strings.Repeat("1", 64), "https://relay.example.test/", "f0123456789ab", "K7QM3XRD2P")
	if err != nil {
		t.Fatal(err)
	}
	want := "handboard://c#v=1&e=" + strings.Repeat("1", 64) + "&f=f0123456789ab&c=K7QM3XRD2P&r=https%3A%2F%2Frelay.example.test%2F"
	if base != want {
		t.Fatalf("appLink = %q, want %q", base, want)
	}
	for name, c := range map[string]struct {
		endpoint, relay, fleet, code, want string
	}{
		"endpoint": {strings.Repeat("2", 64), "https://relay.example.test/", "f0123456789ab", "K7QM3XRD2P", "handboard://c#v=1&e=" + strings.Repeat("2", 64) + "&f=f0123456789ab&c=K7QM3XRD2P&r=https%3A%2F%2Frelay.example.test%2F"},
		"relay":    {strings.Repeat("1", 64), "https://relay.example.test:7842/", "f0123456789ab", "K7QM3XRD2P", "handboard://c#v=1&e=" + strings.Repeat("1", 64) + "&f=f0123456789ab&c=K7QM3XRD2P&r=https%3A%2F%2Frelay.example.test%3A7842%2F"},
		"fleet":    {strings.Repeat("1", 64), "https://relay.example.test/", "fa1b2c3d4e5f6", "K7QM3XRD2P", "handboard://c#v=1&e=" + strings.Repeat("1", 64) + "&f=fa1b2c3d4e5f6&c=K7QM3XRD2P&r=https%3A%2F%2Frelay.example.test%2F"},
		"code":     {strings.Repeat("1", 64), "https://relay.example.test/", "f0123456789ab", "0123456789", "handboard://c#v=1&e=" + strings.Repeat("1", 64) + "&f=f0123456789ab&c=0123456789&r=https%3A%2F%2Frelay.example.test%2F"},
	} {
		got, err := appLink(c.endpoint, c.relay, c.fleet, c.code)
		if err != nil || got != c.want {
			t.Errorf("a moved %s: appLink = %q, %v; want %q", name, got, err, c.want)
		}
	}
	if got, err := appLink(strings.Repeat("1", 64), "https://use1-1.relay.n0.iroh.link./", "f0123456789ab", "K7QM3XRD2P"); err != nil || strings.Contains(got, "&r=") {
		t.Fatalf("a public relay is written into the link: %q, %v", got, err)
	}
	if got, err := appLink(strings.Repeat("1", 64), "none", "f0123456789ab", "K7QM3XRD2P"); err != nil || strings.Contains(got, "&r=") {
		t.Fatalf("a missing relay is written into the link: %q, %v", got, err)
	}
}

func TestTheEmitterRefusesWhatTheLinkCannotHold(t *testing.T) {
	long := "https://" + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "."
	for name, c := range map[string]struct {
		endpoint, relay, fleet, code string
	}{
		"a short endpoint": {strings.Repeat("1", 63), "https://relay.example.test/", "f0123456789ab", "K7QM3XRD2P"},
		"a bad fleet":      {strings.Repeat("1", 64), "https://relay.example.test/", "0123456789abc", "K7QM3XRD2P"},
		"a short code":     {strings.Repeat("1", 64), "https://relay.example.test/", "f0123456789ab", "K7QM3XRD2"},
		"an http relay":    {strings.Repeat("1", 64), "http://relay.example.test/", "f0123456789ab", "K7QM3XRD2P"},
		"an IP relay":      {strings.Repeat("1", 64), "https://127.0.0.1/", "f0123456789ab", "K7QM3XRD2P"},
		"a link too long":  {strings.Repeat("1", 64), long, "f0123456789ab", "K7QM3XRD2P"},
	} {
		if got, err := appLink(c.endpoint, c.relay, c.fleet, c.code); err == nil {
			t.Errorf("%s: appLink = %q, want a refusal", name, got)
		}
	}
}
