package board_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/sidecar"
)

type fleetsReply struct {
	Version int
	Data    board.APIFleets
}

func fleetItems(t *testing.T, body string) []board.APIFleetItem {
	t.Helper()
	var got fleetsReply
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Version != 1 {
		t.Fatalf("fleets = %v, version %d:\n%s", err, got.Version, body)
	}
	return got.Data.Fleets
}

func fleetItemIDs(items []board.APIFleetItem) []string {
	out := []string{}
	for _, i := range items {
		out = append(out, i.ID)
	}
	return out
}

func jsonKind(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "invalid"
	}
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case nil:
		return "null"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func cookies(cs ...*http.Cookie) []*http.Cookie { return cs }

func TestFleetsListsWhatALaptopSessionProves(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta", "gamma")
	a, b, c := p.fleets[0], p.fleets[1], p.fleets[2]
	rec := p.send("GET", "/api/v1/fleets", nil, cookies(p.laptop(a), p.laptop(b)), nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("fleets = %d:\n%s", rec.Code, rec.Body)
	}
	got := fleetItems(t, rec.Body.String())
	if want := []string{a.id, b.id}; !slices.Equal(fleetItemIDs(got), want) || got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Fatalf("fleets = %+v, want %v", got, want)
	}
	if body := rec.Body.String(); strings.Contains(body, c.id) || strings.Contains(body, "gamma") {
		t.Fatalf("an unproven fleet is listed:\n%s", body)
	}
	one := fleetItems(t, p.send("GET", "/api/v1/fleets", nil, cookies(p.laptop(c)), nil).Body.String())
	if !slices.Equal(fleetItemIDs(one), []string{c.id}) {
		t.Fatalf("one cookie lists %v", fleetItemIDs(one))
	}
	wrong := &http.Cookie{Name: "hand_board_" + a.id, Value: b.token}
	testAPIError(t, p.send("GET", "/api/v1/fleets", nil, cookies(wrong), nil), http.StatusUnauthorized, "unauthorized")
	testAPIError(t, p.send("GET", "/api/v1/fleets", nil, nil, nil), http.StatusUnauthorized, "unauthorized")
}

func TestFleetsListsOnlyTheFleetsAPairedPhoneHolds(t *testing.T) {
	r := newPeerRig(t, "alpha", "beta")
	a, b := r.fleets[0], r.fleets[1]
	c := r.as(peerA)
	phoneA := r.mustPair(c, a)
	rep := c.do("GET", "/api/v1/fleets", nil, nil, phoneA)
	if got := fleetItems(t, rep.Body); rep.Code != http.StatusOK || !slices.Equal(fleetItemIDs(got), []string{a.id}) {
		t.Fatalf("one pairing = %d, %v:\n%s", rep.Code, fleetItemIDs(got), rep.Body)
	}
	if strings.Contains(rep.Body, b.id) || strings.Contains(rep.Body, "beta") {
		t.Fatalf("a phone paired with alpha learns of beta:\n%s", rep.Body)
	}
	borrowed := phoneNamed("hand_phone_"+b.id, phoneA)
	rep = c.do("GET", "/api/v1/fleets", nil, nil, phoneA, borrowed)
	if got := fleetItemIDs(fleetItems(t, rep.Body)); !slices.Equal(got, []string{a.id}) || strings.Contains(rep.Body, b.id) {
		t.Fatalf("alpha's secret under beta's name lists %v:\n%s", got, rep.Body)
	}
	phoneB := r.mustPair(c, b)
	rep = c.do("GET", "/api/v1/fleets", nil, nil, phoneA, phoneB)
	if got := fleetItemIDs(fleetItems(t, rep.Body)); !slices.Equal(got, []string{a.id, b.id}) {
		t.Fatalf("two pairings list %v:\n%s", got, rep.Body)
	}
}

func TestFleetsRefusesAPhonePairedWithoutItsOwnKey(t *testing.T) {
	r := newPeerRig(t, "alpha")
	a := r.fleets[0]
	bare := phoneNamed("hand_phone_"+a.id, r.pair(a, serving()))
	rep := r.as(peerA).do("GET", "/api/v1/fleets", nil, nil, bare)
	if rep.Code != http.StatusForbidden || !strings.Contains(rep.Body, `"forbidden"`) || strings.Contains(rep.Body, a.id) {
		t.Fatalf("a keyless phone over the peer = %d:\n%s", rep.Code, rep.Body)
	}
	if rep := r.as(peerA).do("GET", "/api/v1/meta", nil, nil, bare); rep.Code != http.StatusForbidden {
		t.Fatalf("meta for a keyless phone = %d", rep.Code)
	}
}

func TestFleetsSummaryMatchesStatus(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	task := active(t, a.st, "Fix login")
	if _, err := a.st.Ask(context.Background(), task.ID, "Keep the old cookie name?"); err != nil {
		t.Fatal(err)
	}
	both := cookies(p.laptop(a), p.laptop(b))
	items := fleetItems(t, p.send("GET", "/api/v1/fleets", nil, both, nil).Body.String())
	for i, f := range []railFleet{a, b} {
		var env struct{ Data board.APIStatus }
		rec := p.send("GET", "/"+f.id+"/api/v1/status", nil, both, nil)
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("status %s = %d, %v", f.name, rec.Code, err)
		}
		n, got := env.Data.Needs, items[i]
		want := board.APIFleetSummary{Pill: env.Data.Pill, Line: env.Data.Line, Needs: board.APIFleetNeeds{
			Waiting: n.Waiting, Worst: n.Worst, WorstWord: n.WorstWord, WorstCount: n.WorstCount, Summary: n.Summary,
		}}
		if got.APIFleetSummary == nil || *got.APIFleetSummary != want || got.Unavailable {
			t.Fatalf("%s = %+v, status says %+v", f.name, got, want)
		}
	}
	if w := items[0].Needs; w.Waiting == 0 || w.Worst == "" || w.WorstWord == "" || items[1].Needs.Waiting != 0 {
		t.Fatalf("the needs are not exercised: %+v / %+v", items[0].Needs, items[1].Needs)
	}
}

func TestFleetsPinsTheNeedsNames(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	task := active(t, a.st, "Fix login")
	if _, err := a.st.Ask(context.Background(), task.ID, "Keep the old cookie name?"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.st.AddProject(context.Background(), "hand", handRepo, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.st.AddTask(context.Background(), "hand", "Read the queue", ""); err != nil {
		t.Fatal(err)
	}
	body := p.send("GET", "/api/v1/fleets", nil, cookies(p.laptop(a), p.laptop(b)), nil).Body.String()
	var got struct {
		Data struct {
			Fleets []struct {
				Needs map[string]json.RawMessage `json:"needs"`
			} `json:"fleets"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	want := []map[string]string{
		{"waiting": "number", "worst": "string", "worst_word": "string", "worst_count": "number"},
		{"waiting": "number", "worst_count": "number", "summary": "string"},
	}
	if len(got.Data.Fleets) != len(want) {
		t.Fatalf("fleets = %d, want %d:\n%s", len(got.Data.Fleets), len(want), body)
	}
	for i, names := range want {
		needs := got.Data.Fleets[i].Needs
		if len(needs) != len(names) {
			t.Fatalf("fleet %d needs = %v, want exactly %v", i, needs, names)
		}
		for name, kind := range names {
			raw, ok := needs[name]
			if !ok {
				t.Fatalf("fleet %d needs = %v, want %q", i, needs, name)
			}
			if got := jsonKind(raw); got != kind {
				t.Fatalf("fleet %d needs.%s = %s %s, want %s", i, name, got, raw, kind)
			}
		}
	}
}

func TestFleetsLogsAFailedFleetOncePerState(t *testing.T) {
	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	both := cookies(p.laptop(a), p.laptop(b))
	p.down[b.id] = true
	for range 2 {
		p.send("GET", "/api/v1/fleets", nil, both, nil)
	}
	if n := strings.Count(logged.String(), b.id); n != 1 {
		t.Fatalf("a broken fleet logged %d times:\n%s", n, logged.String())
	}
	p.down[b.id] = false
	p.send("GET", "/api/v1/fleets", nil, both, nil)
	if n := strings.Count(logged.String(), b.id); n != 2 || !strings.Contains(logged.String(), "again") {
		t.Fatalf("a recovered fleet logged:\n%s", logged.String())
	}
	p.send("GET", "/api/v1/fleets", nil, both, nil)
	if n := strings.Count(logged.String(), b.id); n != 2 {
		t.Fatalf("a healthy fleet logged again:\n%s", logged.String())
	}
}

func TestFleetsTreatsABrokenFleetAsTheRailDoes(t *testing.T) {
	p := newPhoneHost(t, true, "alpha", "beta")
	a, b := p.fleets[0], p.fleets[1]
	both := cookies(p.laptop(a), p.laptop(b))
	p.down[b.id] = true
	items := fleetItems(t, p.send("GET", "/api/v1/fleets", nil, both, nil).Body.String())
	if len(items) != 2 || items[0].Unavailable || !items[1].Unavailable || items[1].Name != "beta" || items[1].APIFleetSummary != nil {
		t.Fatalf("a fleet that will not open = %+v", items)
	}
	p.gone[b.id] = true
	if got := fleetItemIDs(fleetItems(t, p.send("GET", "/api/v1/fleets", nil, both, nil).Body.String())); !slices.Equal(got, []string{a.id}) {
		t.Fatalf("a fleet that left the list still shows: %v", got)
	}
}

func TestMetaNamesTheVersionTheFeaturesAndTheSidecar(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	a := p.fleets[0]
	p.iroh = func() sidecar.Status {
		return sidecar.Status{State: sidecar.Ready, ID: strings.Repeat("e", 64), Relay: "https://relay.example"}
	}
	p.restart()
	var env struct{ Data board.APIMeta }
	rec := p.send("GET", "/api/v1/meta", nil, cookies(p.laptop(a)), nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("meta = %d, %v:\n%s", rec.Code, err, rec.Body)
	}
	m := env.Data
	if m.Version != "9.9.9-test" || !slices.IsSorted(m.Features) || !slices.Equal(m.Features, []string{"events", "fleets", "pair"}) {
		t.Fatalf("meta = %+v", m)
	}
	if m.Sidecar == nil || m.Sidecar.State != sidecar.Ready || m.Sidecar.ID != strings.Repeat("e", 64) || m.Sidecar.Relay != "https://relay.example" {
		t.Fatalf("sidecar = %+v", m.Sidecar)
	}
	testAPIError(t, p.send("GET", "/api/v1/meta", nil, nil, nil), http.StatusUnauthorized, "unauthorized")

	r := servePeerRig(t, p)
	c := r.as(peerA)
	rep := c.do("GET", "/api/v1/meta", nil, nil, r.mustPair(c, a))
	if rep.Code != http.StatusOK || !strings.Contains(rep.Body, `"9.9.9-test"`) || strings.Contains(rep.Body, "sidecar") || strings.Contains(rep.Body, strings.Repeat("e", 64)) {
		t.Fatalf("a phone's meta = %d:\n%s", rep.Code, rep.Body)
	}
}

func TestEveryFeatureIsNamedInTheAPIDocs(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	var env struct{ Data board.APIMeta }
	rec := p.send("GET", "/api/v1/meta", nil, cookies(p.laptop(p.fleets[0])), nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../../docs/api.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range env.Data.Features {
		if !strings.Contains(string(doc), "| `"+name+"` |") {
			t.Errorf("docs/api.md does not name the feature %q in its features table", name)
		}
	}
}

func TestFleetsAndMetaRefuseWhatEveryAPIReadRefuses(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	a := p.fleets[0]
	for name, headers := range map[string]map[string]string{
		"forwarded":   {"X-Forwarded-For": "100.64.0.7"},
		"forwarded 2": {"Forwarded": "for=100.64.0.7"},
		"funnel":      {"Tailscale-Funnel-Request": "?1"},
	} {
		for _, path := range []string{"/api/v1/fleets", "/api/v1/meta"} {
			rec := p.send("GET", path, nil, cookies(p.laptop(a)), headers)
			testAPIError(t, rec, http.StatusForbidden, "forbidden")
			if strings.Contains(rec.Body.String(), a.id) {
				t.Fatalf("%s %s names a fleet", name, path)
			}
		}
	}
	net := newPhoneHost(t, false, "alpha")
	for _, path := range []string{"/api/v1/fleets", "/api/v1/meta"} {
		testAPIError(t, net.send("GET", path, nil, cookies(net.laptop(net.fleets[0])), nil), http.StatusForbidden, "forbidden")
	}
}

func TestFleetsAndMetaTakeOnlyReads(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	a := p.fleets[0]
	for _, path := range []string{"/api/v1/fleets", "/api/v1/meta"} {
		rec := p.send("POST", path, nil, cookies(p.laptop(a)), nil)
		testAPIError(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
		if rec.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("POST %s Allow = %q", path, rec.Header().Get("Allow"))
		}
		if rec := p.send("HEAD", path, nil, cookies(p.laptop(a)), nil); rec.Code != http.StatusOK {
			t.Fatalf("HEAD %s = %d", path, rec.Code)
		}
	}
}
