package desktop_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/desktop"
)

func TestAPairedClientNeverShowsALoopbackAddressWhateverTheFailure(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	reset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	t.Cleanup(reset.Close)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(slow.Close)
	for _, tc := range []struct {
		name string
		base string
		http *http.Client
		lost func() error
	}{
		{"closed port, no lost state", closedURL, nil, func() error { return nil }},
		{"closed port, lost not set", closedURL, nil, nil},
		{"connection reset", reset.URL, reset.Client(), func() error { return nil }},
		{"timeout", slow.URL, &http.Client{Timeout: 50 * time.Millisecond}, func() error { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &desktop.HTTPClient{
				Paired: true,
				Base:   tc.base,
				HTTP:   tc.http,
				Lost:   tc.lost,
				Known:  []desktop.Fleet{{ID: rtA, Name: "a", Host: rtHostA}},
				Token:  func(string) (string, error) { return rtCookieA, nil },
			}
			_, port, _ := net.SplitHostPort(strings.TrimPrefix(tc.base, "http://"))
			calls := map[string]error{}
			_, calls["status"] = c.Status(context.Background(), rtA)
			_, calls["name"] = c.Name(context.Background(), rtA)
			_, calls["timeline"] = c.Timeline(context.Background(), rtA, 0)
			_, calls["features"] = c.Features(context.Background(), rtA)
			_, calls["send"] = c.SendMessage(context.Background(), rtA, "hi", nil)
			for name, err := range calls {
				if err == nil {
					t.Fatalf("%s: no error", name)
				}
				text := err.Error()
				for _, bad := range []string{"127.0.0.1", "localhost", port, "http://", "dial tcp", rtCookieA} {
					if strings.Contains(text, bad) {
						t.Fatalf("%s: the error shows %q: %s", name, bad, text)
					}
				}
				if !strings.Contains(text, "Can't reach host") {
					t.Fatalf("%s: error = %s", name, text)
				}
			}
		})
	}
}
