package board_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

var retiredMarkers = []string{"data-worker", `rel="manifest"`, `rel="apple-touch-icon"`, "data-install", "manifest-src", "worker-src", "serviceWorker"}

func TestTheManifestAndTheOfflinePageAreGone(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	f := p.fleets[0]
	for name, headers := range map[string]map[string]string{"loopback": nil, "proxied https": serving()} {
		for _, path := range []string{"/manifest.webmanifest", "/offline", "/" + f.id + "/offline", "/" + f.id + "/manifest.webmanifest"} {
			rec := p.send("GET", path, nil, []*http.Cookie{p.host(f)}, headers)
			if rec.Code == http.StatusTemporaryRedirect {
				rec = p.send("GET", rec.Header().Get("Location"), nil, []*http.Cookie{p.host(f)}, headers)
			}
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s GET %s = %d, want 404", name, path, rec.Code)
			}
		}
	}
}

func TestOnlyTheSelfRemovingWorkerIsServed(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	for name, headers := range map[string]map[string]string{"loopback": nil, "proxied https": serving()} {
		rec := p.send("GET", "/sw.js", nil, nil, headers)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
			t.Fatalf("%s: status %d, type %q", name, rec.Code, rec.Header().Get("Content-Type"))
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", name, got)
		}
		body := rec.Body.String()
		for _, want := range []string{`addEventListener("install"`, "skipWaiting()", `addEventListener("activate"`, "caches.delete(", "registration.unregister()"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: worker lacks %q", name, want)
			}
		}
		for _, bad := range []string{`"fetch"`, "fetch(", "cache.put", "addAll"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s: worker holds %q", name, bad)
			}
		}
	}
}

func TestNoPageOffersTheApp(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	f := p.fleets[0]
	https := serving()
	phone := p.pair(f, https)
	for name, headers := range map[string]map[string]string{"loopback": nil, "proxied https": https} {
		for _, path := range []string{"/", "/" + f.id + "/", "/" + f.id + "/connect", "/" + f.id + "/tasks", "/" + f.id + "/pair"} {
			rec := p.send("GET", path, nil, []*http.Cookie{p.host(f), phone}, headers)
			whole := wholeResponse(rec)
			for _, bad := range retiredMarkers {
				if strings.Contains(whole, bad) {
					t.Errorf("%s GET %s holds %q", name, path, bad)
				}
			}
		}
	}
	for _, path := range []string{"/", "/" + f.id + "/"} {
		rec := p.send("GET", path, nil, nil, https)
		for _, bad := range retiredMarkers {
			if strings.Contains(wholeResponse(rec), bad) {
				t.Errorf("signed-out GET %s holds %q", path, bad)
			}
		}
	}
}

func TestTheScriptNoLongerRegistersAWorker(t *testing.T) {
	p := newPhoneHost(t, true, "alpha")
	f := p.fleets[0]
	page := p.send("GET", "/"+f.id+"/", nil, []*http.Cookie{p.host(f)}, nil).Body.String()
	app := regexp.MustCompile(`/static/app\.[0-9a-f]{12}\.js`).FindString(page)
	if app == "" {
		t.Fatal("the fleet page names no app.js")
	}
	src := p.send("GET", app, nil, nil, nil).Body.String()
	for _, bad := range []string{"serviceWorker", "beforeinstallprompt", "data-worker", "data-install"} {
		if strings.Contains(src, bad) {
			t.Errorf("app.js holds %q", bad)
		}
	}
}
