package board

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
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
	nonCanonical := 0
	for _, c := range loadCompanionVectors(t).Valid {
		if c.Canonical {
			continue
		}
		nonCanonical++
		relay := ""
		if c.Parsed.Relay != nil {
			relay = *c.Parsed.Relay
		}
		got, err := appLink(c.Parsed.EndpointID, relay, c.Parsed.Fleet, c.Parsed.Code)
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if got == c.Link {
			t.Errorf("%s: the emitter kept the non-canonical link %q", c.Name, c.Link)
		}
		if !strings.HasPrefix(got, appLinkHead+"v=1&e=") || strings.Contains(got, "&x_note=") || strings.Contains(got, "&ui=") {
			t.Errorf("%s: the emitter wrote %q", c.Name, got)
		}
		if c.Parsed.Relay != nil && !strings.Contains(got, "&r="+appEscape(*c.Parsed.Relay)) {
			t.Errorf("%s: the emitter dropped the relay: %q", c.Name, got)
		}
	}
	if nonCanonical == 0 {
		t.Fatal("no non-canonical valid vector")
	}
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

func pairBoard(t *testing.T, token string) (*Board, *state.Store, string) {
	t.Helper()
	st, err := state.Open(filepath.Join(t.TempDir(), "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f, err := st.CreateFleet(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	return New(st, token, Options{Base: "/" + f.ID}).(*Board), st, f.ID
}

func grant(fleets ...string) *Redeemed {
	return &Redeemed{fleets: fleets, done: map[string]bool{}, secret: strings.Repeat("a", 64), name: "Pixel", login: "operator@example.com", label: "0123456789abcdef"}
}

func TestAGrantPairsOnlyTheFleetsOfItsCodeAndOnlyOnceEach(t *testing.T) {
	ctx := context.Background()
	a, sa, aid := pairBoard(t, strings.Repeat("1", 32))
	c, sc, _ := pairBoard(t, strings.Repeat("2", 32))
	rd := grant(aid)
	if _, err := c.PairCompanion(ctx, rd); !errors.Is(err, errNotInCode) {
		t.Fatalf("a fleet outside the code = %v", err)
	}
	if list, _ := sc.Devices(ctx); len(list) != 0 {
		t.Fatalf("a fleet outside the code holds %v", list)
	}
	got, err := a.PairCompanion(ctx, rd)
	if err != nil || got.ID != aid || got.Name != "test" || got.Device != 1 {
		t.Fatalf("PairCompanion = %+v, %v", got, err)
	}
	if _, err := a.PairCompanion(ctx, rd); !errors.Is(err, errNotInCode) {
		t.Fatalf("the same grant twice = %v", err)
	}
	list, _ := sa.Devices(ctx)
	if len(list) != 1 || list[0].Phone != "0123456789abcdef" || list[0].Login != "operator@example.com" {
		t.Fatalf("records = %+v", list)
	}
	if _, ok, err := sa.DeviceWithSecret(ctx, 1, a.secretHash(rd.secret)); err != nil || !ok {
		t.Fatalf("the fleet's own hash does not open its record: %v, %v", ok, err)
	}
	if a.secretHash(rd.secret) == c.secretHash(rd.secret) {
		t.Fatal("two fleets hash one secret to one value")
	}
	if _, ok, _ := sa.DeviceWithSecret(ctx, 1, c.secretHash(rd.secret)); ok {
		t.Fatal("another fleet's hash opens the record")
	}
}

func TestAGrantNobodyRedeemedPairsNothing(t *testing.T) {
	ctx := context.Background()
	a, sa, _ := pairBoard(t, strings.Repeat("1", 32))
	var none *Redeemed
	for what, rd := range map[string]*Redeemed{"nil": none, "empty": {}} {
		if _, err := a.PairCompanion(ctx, rd); !errors.Is(err, errNotInCode) {
			t.Errorf("%s grant = %v", what, err)
		}
	}
	if list, _ := sa.Devices(ctx); len(list) != 0 {
		t.Fatalf("records = %v", list)
	}
}

func TestOneGrantNamesEachFleetOnceUnderRacingCalls(t *testing.T) {
	ctx := context.Background()
	a, sa, aid := pairBoard(t, strings.Repeat("1", 32))
	rd := grant(aid)
	var wg sync.WaitGroup
	var mu sync.Mutex
	paired := 0
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.PairCompanion(ctx, rd); err == nil {
				mu.Lock()
				paired++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if list, _ := sa.Devices(ctx); paired != 1 || len(list) != 1 {
		t.Fatalf("%d calls paired and %d records exist", paired, len(list))
	}
}

func TestACodeKeepsItsOwnCopyOfTheFleetSet(t *testing.T) {
	a, _, aid := pairBoard(t, strings.Repeat("1", 32))
	set := []string{aid, "f000000000001"}
	code, until := a.mint(time.Now(), set)
	set[1] = "f0000000000ff"
	res, got := a.redeem("100.64.0.1", code, until.Add(-time.Minute))
	if res != pairOK || len(got) != 2 || got[1] != "f000000000001" {
		t.Fatalf("redeem = %v, %v", res, got)
	}
	got[1] = "f0000000000ee"
	if again, _ := a.redeem("100.64.0.1", code, until.Add(-time.Minute)); again != pairUsed {
		t.Fatalf("second redeem = %v", again)
	}
}

func TestADeviceWithATailnetLoginIsNeverSignedInAgain(t *testing.T) {
	ctx := context.Background()
	b, st, id := pairBoard(t, strings.Repeat("1", 32))
	secret := strings.Repeat("a", 64)
	d, err := st.AddDevice(ctx, "Pixel", "operator@example.com", b.secretHash(secret), "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: companionCookieName(id, false), Value: strconv.FormatInt(d.ID, 10) + "." + secret})
	req.Header.Set("X-Forwarded-For", "100.64.0.7")
	req.Header.Set("Tailscale-User-Login", "operator@example.com")
	if _, fault := b.session(req); fault != companionOther {
		t.Fatalf("fault = %v, want companionOther", fault)
	}
}

func TestOnlyAProxyHeaderOrAPeerMakesARequestForwarded(t *testing.T) {
	for _, c := range []struct {
		header string
		want   bool
	}{
		{"Tailscale-User-Login", false},
		{"X-Forwarded-For", true},
		{"Forwarded", true},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set(c.header, "x")
		if got := forwarded(r); got != c.want {
			t.Errorf("%s: forwarded = %v, want %v", c.header, got, c.want)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	if forwarded(r) {
		t.Error("plain request: forwarded")
	}
	r = r.WithContext(context.WithValue(r.Context(), peerKey{}, &peerConn{}))
	if !forwarded(r) {
		t.Error("peer request: not forwarded")
	}
}
