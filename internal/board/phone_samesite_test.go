package board_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

func setCookie(t *testing.T, rec *httptest.ResponseRecorder) (string, []string) {
	t.Helper()
	got := rec.Result().Header.Values("Set-Cookie")
	if len(got) != 1 {
		t.Fatalf("Set-Cookie = %q", got)
	}
	parts := strings.Split(got[0], "; ")
	slices.Sort(parts[1:])
	return parts[0], parts[1:]
}

func withoutMaxAge(attrs []string) []string {
	return slices.DeleteFunc(slices.Clone(attrs), func(a string) bool { return strings.HasPrefix(a, "Max-Age=") })
}

func TestThePhoneCookieIsLaxSoALinkFromAnotherAppKeepsTheSession(t *testing.T) {
	p := newPhoneRig(t)
	rec := p.tryPair(p.code(), "100.64.0.7")
	first, attrs := setCookie(t, rec)
	if !strings.HasPrefix(first, "__Host-hand_phone=") || !slices.Equal(attrs, []string{"HttpOnly", "Max-Age=7776000", "Path=/", "SameSite=Lax", "Secure"}) {
		t.Fatalf("pairing over https sets %q with %q", first, attrs)
	}
	phone := phoneCookieOf(rec)

	plain := p.do("POST", "/pair", url.Values{"code": {p.code()}}, nil, map[string]string{"X-Forwarded-For": "127.0.0.1"})
	first, attrsPlain := setCookie(t, plain)
	if !strings.HasPrefix(first, "hand_phone=") || !slices.Equal(attrsPlain, []string{"HttpOnly", "Max-Age=7776000", "Path=/", "SameSite=Lax"}) {
		t.Fatalf("pairing over plain http sets %q with %q", first, attrsPlain)
	}

	own := csrfRe.FindStringSubmatch(p.do("GET", "/connect", nil, phone, viaServe).Body.String())[1]
	out := p.do("POST", "/connect/revoke", url.Values{"csrf": {own}, "id": {p.deviceID(phone)}}, phone, viaServe)
	first, cleared := setCookie(t, out)
	if first != "__Host-hand_phone=" || !slices.Contains(cleared, "Max-Age=0") || !slices.Equal(withoutMaxAge(cleared), withoutMaxAge(attrs)) {
		t.Fatalf("sign out clears %q with %q, want the attributes of %q", first, cleared, attrs)
	}
}

func TestNoGetARemotePhoneSessionReachesChangesAnything(t *testing.T) {
	p := newPhoneRig(t)
	p.fx.supervisor(t, state.AttemptRunning, "gen-1")
	seed(t, p.fx.st)
	phone := p.pair()
	ctx := context.Background()
	before := func() (int64, []state.Device, state.Decision, state.Report) {
		seq, err := p.fx.st.LastEventSeq(ctx)
		if err != nil {
			t.Fatal(err)
		}
		devices, err := p.fx.st.Devices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		d, err := p.fx.st.Decision(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		r, err := p.fx.st.Report(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		return seq, devices, d, r
	}
	seq, devices, decision, report := before()

	reads := []string{"/", "/supervisor/log", "/tasks", "/task/t1", "/attempt/a1", "/ref/t1", "/decision/d1", "/connect", "/pair", "/doc/", "/inbox/20261022-120000-1.png", "/static/app.js"}
	for _, path := range reads {
		for _, method := range []string{"GET", "HEAD"} {
			if rec := p.do(method, path, nil, phone, viaServe); rec.Code >= 500 {
				t.Errorf("%s %s = %d", method, path, rec.Code)
			}
		}
	}
	req := httptest.NewRequest("GET", "/events", nil)
	watch, cancel := context.WithTimeout(req.Context(), 200*time.Millisecond)
	defer cancel()
	req = req.WithContext(watch)
	req.AddCookie(phone)
	for k, v := range viaServe {
		req.Header.Set(k, v)
	}
	p.h.ServeHTTP(httptest.NewRecorder(), req)

	actions := []string{"/decision/d1/answer", "/report/r1/ack", "/connect/code", "/connect/revoke", "/connect/revoke/in", "/connect/revoke/everywhere", "/supervisor/start", "/supervisor/resume", "/supervisor/stop", "/supervisor/interrupt", "/supervisor/force", "/supervisor/keys", "/supervisor/send", "/supervisor/image", "/supervisor/switch", "/models/refresh", "/attempt/a1/keys"}
	for _, path := range actions {
		for _, method := range []string{"GET", "HEAD"} {
			if rec := p.do(method, path, nil, phone, viaServe); rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405", method, path, rec.Code)
			}
		}
	}

	seq2, devices2, decision2, report2 := before()
	if seq2 != seq || len(devices2) != len(devices) || decision2.Status != decision.Status || report2.AckedAt != report.AckedAt {
		t.Fatalf("a GET changed the store: events %d to %d, devices %d to %d, decision %q to %q, report acked %q to %q", seq, seq2, len(devices), len(devices2), decision.Status, decision2.Status, report.AckedAt, report2.AckedAt)
	}
	if len(p.fx.srv.Calls("agent.send")) != 0 {
		t.Fatal("a GET reached the supervisor")
	}
}

func TestAPhonePostNeedsItsTokenWhateverCookieItCarries(t *testing.T) {
	p := newPhoneRig(t)
	seed(t, p.fx.st)
	phone, other := p.pair(), p.pair()
	own := csrfRe.FindStringSubmatch(p.do("GET", "/connect", nil, phone, viaServe).Body.String())[1]
	ctx := context.Background()

	routes := []struct {
		name, path string
		form       url.Values
		done       int
	}{
		{"answer", "/decision/d1/answer", url.Values{"answer": {"yes"}}, http.StatusSeeOther},
		{"ack", "/report/r1/ack", url.Values{}, http.StatusSeeOther},
		{"sign out", "/connect/revoke", url.Values{"id": {p.deviceID(phone)}}, http.StatusOK},
	}
	with := func(form url.Values, key, value string) url.Values {
		out := url.Values{}
		for k, v := range form {
			out[k] = v
		}
		if key != "" {
			out.Set(key, value)
		}
		return out
	}
	for _, r := range routes {
		refused := map[string]*httptest.ResponseRecorder{
			"no token":              p.do("POST", r.path, with(r.form, "", ""), phone, viaServe),
			"an empty token":        p.do("POST", r.path, with(r.form, "csrf", ""), phone, viaServe),
			"the fleet token":       p.do("POST", r.path, with(r.form, "csrf", token), phone, viaServe),
			"another phone's token": p.do("POST", r.path, with(r.form, "csrf", csrfRe.FindStringSubmatch(p.do("GET", "/connect", nil, other, viaServe).Body.String())[1]), phone, viaServe),
			"the token in the URL":  p.do("POST", r.path+"?csrf="+own, with(r.form, "", ""), phone, viaServe),
			"its token, no cookie":  p.do("POST", r.path, with(r.form, "csrf", own), nil, viaServe),
		}
		for what, rec := range refused {
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s with %s = %d", r.name, what, rec.Code)
			}
		}
	}
	if d, _ := p.fx.st.Decision(ctx, 1); d.Status != state.DecisionOpen {
		t.Fatalf("a refused POST answered the decision: %+v", d)
	}
	if rep, _ := p.fx.st.Report(ctx, 1); rep.AckedAt != "" {
		t.Fatalf("a refused POST acknowledged the report: %+v", rep)
	}
	if rec := p.do("GET", "/", nil, phone, viaServe); rec.Code != http.StatusOK {
		t.Fatalf("a refused POST signed the phone out = %d", rec.Code)
	}

	for _, r := range routes {
		if rec := p.do("POST", r.path, with(r.form, "csrf", own), phone, viaServe); rec.Code != r.done {
			t.Errorf("%s with its token = %d, want %d", r.name, rec.Code, r.done)
		}
	}
	if d, _ := p.fx.st.Decision(ctx, 1); d.Status != state.DecisionAnswered {
		t.Fatalf("the decision after its own token: %+v", d)
	}
}

func TestAPairPostNeedsACodeNotACookie(t *testing.T) {
	p := newPhoneRig(t)
	phone := p.pair()
	before, _ := p.fx.st.Devices(context.Background())
	for name, form := range map[string]url.Values{"no code": {"name": {"Pixel"}}, "a wrong code": {"code": {"AAAAA-AAAAA"}, "name": {"Pixel"}}} {
		rec := p.do("POST", "/pair", form, phone, viaServe)
		if rec.Code != http.StatusForbidden || len(rec.Result().Cookies()) != 0 {
			t.Errorf("pairing with %s and a phone cookie = %d with cookies %v", name, rec.Code, rec.Result().Cookies())
		}
	}
	if after, _ := p.fx.st.Devices(context.Background()); len(after) != len(before) {
		t.Fatalf("a refused pairing added a phone: %d to %d", len(before), len(after))
	}
}
