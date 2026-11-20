package desktop_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func linksBoard(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/"+fleetID+"/proof":
			proof(w, r)
			return
		case r.URL.Path != "/api/v1/meta" && r.URL.Path != "/"+fleetID+"/api/v1/links":
			http.NotFound(w, r)
			return
		}
		if c, err := r.Cookie("hand_board_" + fleetID); err != nil || c.Value != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"status":401,"code":"unauthorized","message":"no session"}}`)
			return
		}
		seen = append(seen, r.URL.RequestURI())
		if r.URL.Path == "/api/v1/meta" {
			io.WriteString(w, `{"version":1,"generated_at":"2026-11-20T12:00:00Z","data":{"version":"1.0.0","host":"h","features":["events","links"]}}`)
			return
		}
		io.WriteString(w, `{"version":1,"fleet":{"id":"`+fleetID+`","name":"golden"},"cursor":"c1","generated_at":"2026-11-20T12:00:00Z","data":{"limit":50,"links":[{"rel":"reports/a.md","href":"/`+fleetID+`/doc/reports/a.md","dir":false},null]}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestHTTPClientAsksForLinksWithRepeatedPathValues(t *testing.T) {
	srv, seen := linksBoard(t)
	c := goldenClient(t, srv.URL)
	got, err := c.Links(context.Background(), fleetID, []string{"reports/a.md", "50%/a#b.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] == nil || got[0].Rel != "reports/a.md" || got[1] != nil {
		t.Fatalf("links = %+v", got)
	}
	u, err := url.Parse((*seen)[0])
	if err != nil || u.Path != "/"+fleetID+"/api/v1/links" || !slices.Equal(u.Query()["path"], []string{"reports/a.md", "50%/a#b.md"}) {
		t.Fatalf("request %q (%v)", (*seen)[0], err)
	}
	if strings.Contains((*seen)[0], "since=") || desktop.StoresCursor(c, (*seen)[0][len("/"+fleetID+"/api/v1"):]) {
		t.Fatal("a links read used or kept a cursor")
	}
}

func TestHTTPClientReadsTheBoardsFeaturesWithTheFleetsSession(t *testing.T) {
	srv, seen := linksBoard(t)
	c := goldenClient(t, srv.URL)
	got, err := c.Features(context.Background(), fleetID)
	if err != nil || !slices.Equal(got, []string{"events", "links"}) {
		t.Fatalf("features = %q, %v", got, err)
	}
	if len(*seen) != 1 || (*seen)[0] != "/api/v1/meta" {
		t.Fatalf("requests = %q", *seen)
	}
}

func TestHTTPClientReportsRefusedFeaturesAndLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/"+fleetID+"/proof" {
			proof(w, r)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":{"status":403,"code":"forbidden","message":"refused"}}`)
	}))
	t.Cleanup(srv.Close)
	c := goldenClient(t, srv.URL)
	if _, err := c.Features(context.Background(), fleetID); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("features err = %v", err)
	}
	if _, err := c.Links(context.Background(), fleetID, []string{"a/b"}); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("links err = %v", err)
	}
}
