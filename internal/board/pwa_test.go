package board_test

import (
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var (
	staticRef = regexp.MustCompile(`(?:href|src)="(/static/[^"]+)"`)
	buildLine = regexp.MustCompile(`^const BUILD = "[0-9a-f]{12}";\n`)
)

func proxiedHTS() map[string]string { return serving("operator@example.com") }

func installHost(t *testing.T) (*phoneHost, railFleet) {
	t.Helper()
	p := newPhoneHost(t, true, "alpha")
	return p, p.fleets[0]
}

func bare200(t *testing.T, rec *httptest.ResponseRecorder, ctype string) {
	t.Helper()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), ctype) {
		t.Fatalf("status %d, type %q, want 200 %q", rec.Code, rec.Header().Get("Content-Type"), ctype)
	}
	if len(rec.Result().Cookies()) != 0 || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("sets a cookie: %q", rec.Header().Get("Set-Cookie"))
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("no Content-Security-Policy")
	}
}

func TestTheManifestNamesTheAppAndItsIcons(t *testing.T) {
	p, _ := installHost(t)
	rec := p.send("GET", "/manifest.webmanifest", nil, nil, proxiedHTS())
	bare200(t, rec, "application/manifest+json")
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var m struct {
		ID, Name, ShortName, StartURL, Scope, Display string
		BackgroundColor, ThemeColor                   string
		Icons                                         []struct{ Src, Sizes, Type, Purpose string }
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"id": "/", "name": "Hand Board", "short_name": "Hand", "start_url": "/", "scope": "/", "display": "standalone", "background_color": "#ffffff", "theme_color": "#ffffff"} {
		if raw[k] != want {
			t.Errorf("%s = %v, want %q", k, raw[k], want)
		}
	}
	type want struct{ size, purpose string }
	var got []want
	for _, ic := range m.Icons {
		img := p.send("GET", ic.Src, nil, nil, nil)
		if img.Code != http.StatusOK || img.Header().Get("Content-Type") != "image/png" || ic.Type != "image/png" {
			t.Fatalf("icon %s = %d %q, declared %q", ic.Src, img.Code, img.Header().Get("Content-Type"), ic.Type)
		}
		if cc := img.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Errorf("icon %s Cache-Control = %q", ic.Src, cc)
		}
		cfg, err := png.DecodeConfig(img.Body)
		if err != nil {
			t.Fatal(err)
		}
		if declared := strings.Split(ic.Sizes, "x"); len(declared) != 2 || declared[0] != declared[1] || declared[0] != strconv.Itoa(cfg.Width) || cfg.Width != cfg.Height {
			t.Errorf("icon %s is %dx%d, declared %q", ic.Src, cfg.Width, cfg.Height, ic.Sizes)
		}
		got = append(got, want{ic.Sizes, ic.Purpose})
	}
	for _, w := range []want{{"192x192", "any"}, {"512x512", "any"}, {"512x512", "maskable"}} {
		if !slices.Contains(got, w) {
			t.Errorf("icons %v lack %v", got, w)
		}
	}
	if len(got) != 3 {
		t.Errorf("icons = %v", got)
	}
}

func TestTheWorkerIsServedFreshAndWithoutCookies(t *testing.T) {
	p, _ := installHost(t)
	rec := p.send("GET", "/sw.js", nil, nil, proxiedHTS())
	bare200(t, rec, "text/javascript")
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "" {
		t.Fatalf("Service-Worker-Allowed = %q", got)
	}
	if !buildLine.MatchString(rec.Body.String()) {
		t.Fatalf("no build line:\n%.80s", rec.Body.String())
	}
	if rec2 := p.send("GET", "/sw.js", nil, nil, nil); rec2.Body.String() != rec.Body.String() {
		t.Fatal("the worker differs between requests")
	}
	if rec := p.send("GET", "/static/sw.js", nil, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("/static/sw.js = %d", rec.Code)
	}
	page := p.send("GET", "/offline", nil, nil, nil).Body.String()
	for _, m := range staticRef.FindAllStringSubmatch(page, -1) {
		if strings.Contains(m[1], "/sw.") {
			t.Fatalf("the offline page links %s", m[1])
		}
	}
}

func TestTheWorkerCachesOnlyTheOfflinePage(t *testing.T) {
	p, _ := installHost(t)
	js := p.send("GET", "/sw.js", nil, nil, nil).Body.String()
	install, fetch, ok := strings.Cut(js, `self.addEventListener("fetch"`)
	if !ok {
		t.Fatal("no fetch handler")
	}
	for _, write := range []string{".put(", ".add(", ".addAll(", "caches.open(CACHE).then((cache) => cache.put"} {
		if strings.Contains(fetch, write) {
			t.Errorf("the fetch handler writes the cache with %q", write)
		}
	}
	if n := strings.Count(js, ".put("); n != 1 {
		t.Errorf("cache.put appears %d times", n)
	}
	if n := strings.Count(js, ".addAll("); n != 1 {
		t.Errorf("cache.addAll appears %d times", n)
	}
	for _, pin := range []string{
		`const OFFLINE = "/offline";`,
		`await fetch(OFFLINE, { cache: "no-store", credentials: "omit" })`,
		`await cache.put(OFFLINE, page)`,
		`/(?:href|src)="(\/static\/[A-Za-z0-9._-]+)"/g`,
		`new Request(url, { credentials: "omit" })`,
	} {
		if !strings.Contains(install, pin) {
			t.Errorf("the worker lacks %q", pin)
		}
	}
	for _, pin := range []string{
		`url.origin !== self.location.origin || request.method !== "GET"`,
		`request.mode === "navigate"`,
		`url.pathname.startsWith("/static/")`,
	} {
		if !strings.Contains(fetch, pin) {
			t.Errorf("the fetch handler lacks %q", pin)
		}
	}
	if n := strings.Count(fetch, "respondWith("); n != 2 {
		t.Errorf("respondWith appears %d times, want the navigation and the shipped files", n)
	}
	for _, never := range []string{"/events", "/doc", "/supervisor", "/connect", "/proof", "localStorage", "indexedDB", "PushManager", "sync", "BackgroundSync", "postMessage", "clone()"} {
		if strings.Contains(strings.ReplaceAll(fetch, "async", ""), never) {
			t.Errorf("the fetch handler mentions %q", never)
		}
	}
	page := p.send("GET", "/offline", nil, nil, nil).Body.String()
	var shell []string
	for _, m := range staticRef.FindAllStringSubmatch(page, -1) {
		shell = append(shell, regexp.MustCompile(`\.[0-9a-f]{12}\.`).ReplaceAllString(m[1], "."))
	}
	slices.Sort(shell)
	if want := []string{"/static/board.css", "/static/favicon.svg", "/static/offline.js"}; !slices.Equal(shell, want) {
		t.Errorf("the offline page needs %v, want %v", shell, want)
	}
}

func TestTheOfflinePageHasNothingOfTheFleet(t *testing.T) {
	p := newPhoneHost(t, true, "alpha-secret-name", "beta")
	cookie := p.pair(p.fleets[0], proxiedHTS())
	rec := p.send("GET", "/offline", nil, []*http.Cookie{cookie}, proxiedHTS())
	bare200(t, rec, "text/html")
	body := rec.Body.String()
	for _, f := range p.fleets {
		for _, secret := range []string{f.name, f.id, f.token, cookie.Value, "csrf", "token"} {
			if strings.Contains(body, secret) {
				t.Errorf("the offline page holds %q", secret)
			}
		}
	}
	for _, tag := range regexp.MustCompile(`<script[^>]*>`).FindAllString(body, -1) {
		if !strings.Contains(tag, " src=") {
			t.Errorf("inline script %q", tag)
		}
	}
	for _, bad := range []string{"<style", ` style="`, "http-equiv", "onclick=", `rel="manifest"`, "data-worker"} {
		if strings.Contains(body, bad) {
			t.Errorf("the offline page holds %q", bad)
		}
	}
	for _, want := range []string{"<title>Laptop unreachable</title>", "Laptop unreachable</h2>", "data-retry", "data-offline-state", `<script src="/static/offline.`} {
		if !strings.Contains(body, want) {
			t.Errorf("the offline page lacks %q", want)
		}
	}
	if rec := p.send("GET", "/offline", nil, nil, nil); rec.Code != http.StatusOK {
		t.Errorf("without a session = %d", rec.Code)
	}
	script := p.send("GET", staticRef.FindStringSubmatch(body[strings.Index(body, "<script"):])[1], nil, nil, nil).Body.String()
	for _, want := range []string{`location.pathname === "/offline" ? location.replace("/") : location.reload()`, `if (res?.ok) open();`, `addEventListener("online", probe)`} {
		if !strings.Contains(script, want) {
			t.Errorf("offline.js lacks %q", want)
		}
	}
}

func TestFunnelGetsNoManifestOrWorker(t *testing.T) {
	p, f := installHost(t)
	funnel := map[string]string{"X-Forwarded-For": "100.64.0.7", "X-Forwarded-Proto": "https", "Tailscale-Funnel-Request": "?1"}
	for _, path := range []string{"/manifest.webmanifest", "/sw.js", "/offline", "/", "/" + f.id + "/"} {
		rec := p.send("GET", path, nil, []*http.Cookie{p.laptop(f)}, funnel)
		if rec.Code != http.StatusForbidden {
			t.Errorf("Funnel GET %s = %d", path, rec.Code)
		}
		for _, bad := range []string{`rel="manifest"`, "data-worker", "apple-touch-icon", "data-install"} {
			if strings.Contains(rec.Body.String(), bad) {
				t.Errorf("Funnel GET %s holds %q", path, bad)
			}
		}
	}
}

func TestAProxiedHTTPSPageRegistersTheWorker(t *testing.T) {
	p, f := installHost(t)
	phone := p.pair(f, proxiedHTS())
	for name, c := range map[string]*http.Cookie{"phone": phone, "laptop": p.laptop(f)} {
		for _, path := range []string{"/" + f.id + "/", "/" + f.id + "/connect", "/" + f.id + "/tasks"} {
			rec := p.send("GET", path, nil, []*http.Cookie{c}, proxiedHTS())
			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("%s GET %s = %d", name, path, rec.Code)
			}
			for _, want := range []string{`<link rel="manifest" href="/manifest.webmanifest">`, `<link rel="apple-touch-icon" href="/static/icon-192.`, `name="theme-color" content="#ffffff" media="(prefers-color-scheme: light)"`, `name="theme-color" content="#111317" media="(prefers-color-scheme: dark)"`, `name="apple-mobile-web-app-capable"`, " data-worker>", `<script src="/static/app.`} {
				if !strings.Contains(body, want) {
					t.Errorf("%s GET %s lacks %q", name, path, want)
				}
			}
			csp := rec.Header().Get("Content-Security-Policy")
			if !strings.Contains(csp, "manifest-src 'self'") || !strings.Contains(csp, "worker-src 'self'") {
				t.Errorf("%s GET %s csp = %q", name, path, csp)
			}
		}
	}
	rec := p.send("GET", "/", nil, []*http.Cookie{phone}, proxiedHTS())
	if !strings.Contains(rec.Body.String(), `<link rel="manifest" href="/manifest.webmanifest">`) {
		t.Error("the fleet list of a proxied phone has no manifest")
	}
	rec = p.send("GET", "/"+f.id+"/", nil, nil, proxiedHTS())
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "data-worker") {
		t.Errorf("a signed-out proxied phone = %d, want the neutral page with the worker", rec.Code)
	}
}

func TestPagesWithoutAProxiedHTTPSChangeNothing(t *testing.T) {
	p, f := installHost(t)
	phone := p.pair(f, proxiedHTS())
	cases := map[string]map[string]string{
		"loopback":     nil,
		"http proxy":   {"X-Forwarded-For": "100.64.0.7", "X-Forwarded-Proto": "http"},
		"no proto":     {"X-Forwarded-For": "100.64.0.7"},
		"https alone":  {"X-Forwarded-Proto": "https"},
		"funnel proxy": {"X-Forwarded-For": "100.64.0.7", "X-Forwarded-Proto": "https", "Tailscale-Funnel-Request": "?1"},
	}
	for name, headers := range cases {
		for _, path := range []string{"/" + f.id + "/", "/" + f.id + "/connect", "/"} {
			rec := p.send("GET", path, nil, []*http.Cookie{p.laptop(f), phone}, headers)
			body := rec.Body.String()
			for _, bad := range []string{`rel="manifest"`, "apple-touch-icon", "theme-color", "apple-mobile-web-app", "data-worker", "data-install", "Add to Home Screen"} {
				if strings.Contains(body, bad) {
					t.Errorf("%s GET %s holds %q", name, path, bad)
				}
			}
		}
	}
}

func TestTheInstallSectionIsOnThePhonePage(t *testing.T) {
	p, f := installHost(t)
	phone := p.pair(f, proxiedHTS())
	for name, c := range map[string]*http.Cookie{"phone": phone, "laptop": p.laptop(f)} {
		body := p.send("GET", "/"+f.id+"/connect", nil, []*http.Cookie{c}, proxiedHTS()).Body.String()
		for _, want := range []string{`data-install>`, `<h3 id="app-head">Install</h3>`, `data-install-offer hidden>`, `data-install-button>Install the Board app</button>`, "Share, then Add to Home Screen", "its own storage", "pair it again inside the app", "Laptop unreachable"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s Phone page lacks %q", name, want)
			}
		}
	}
	js := get(t, newFixture(t).handler(), "/")
	app := regexp.MustCompile(`/static/app\.[0-9a-f]{12}\.js`).FindString(js)
	src := p.send("GET", app, nil, nil, nil).Body.String()
	for _, want := range []string{`body.hasAttribute("data-worker") && isSecureContext && "serviceWorker" in navigator`, `register("/sw.js", { scope: "/" })`, `addEventListener("beforeinstallprompt"`} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	if strings.Count(src, `register(`) != 1 {
		t.Error("app.js registers more than one worker")
	}
}
