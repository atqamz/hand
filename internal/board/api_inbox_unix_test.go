//go:build unix

package board_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIInboxRefusesALinkOutOfTheInboxLikeTheBoard(t *testing.T) {
	fx := apiInboxFixture(t)
	var sent sentTexts
	sent.control(fx)
	h := fx.handler()
	home := fx.options.Home
	outside := filepath.Join(t.TempDir(), "outside.txt")
	for p, data := range map[string]string{filepath.Join(home, "hand.db"): secret, outside: secret} {
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, "inbox"), 0o700); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"20261101-050000-9.png":          "../hand.db",
		"20261101-050000-8-abs.txt":      outside,
		"20261101-050000-7-sibling.txt":  "20261101-050000-6-real.txt",
		"20261101-050000-5-dangling.txt": "nowhere",
	}
	if err := os.WriteFile(filepath.Join(home, "inbox", "20261101-050000-6-real.txt"), []byte("real"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(home, "inbox", name)); err != nil {
			t.Fatal(err)
		}
	}
	for name := range links {
		page := request(h, "GET", "/inbox/"+name, nil, true)
		api := request(h, "GET", "/api/v1/inbox/"+name, nil, true)
		if strings.Contains(api.Body.String(), secret) {
			t.Fatalf("%s: a link served the file it points to", name)
		}
		if name != "20261101-050000-7-sibling.txt" {
			testAPIError(t, api, http.StatusNotFound, "not_found")
		}
		if api.Code != page.Code {
			t.Errorf("%s: Board %d, API %d", name, page.Code, api.Code)
		}
		rec := apiPost(t, h, "/api/v1/message", map[string]any{"text": "look", "attachments": []string{name}}, token, true)
		testAPIError(t, rec, http.StatusNotFound, "not_found")
	}
	if len(sent.texts) != 0 {
		t.Fatalf("a link was attached: %q", sent.texts)
	}
}
