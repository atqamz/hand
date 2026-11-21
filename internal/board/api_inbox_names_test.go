package board_test

import (
	"encoding/binary"
	"hash/crc32"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/inbox"
)

func pngOfSize(w, h uint32) []byte {
	ihdr := binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, w), h)
	ihdr = append(ihdr, 8, 0, 0, 0, 0)
	b := []byte("\x89PNG\r\n\x1a\n")
	b = binary.BigEndian.AppendUint32(b, uint32(len(ihdr)))
	b = append(b, "IHDR"...)
	b = append(b, ihdr...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...)))
}

func TestAPIInboxRefusesAPictureOverThePixelCapAsTooLarge(t *testing.T) {
	fx := apiInboxFixture(t)
	h := fx.handler()
	if got := apiStored(t, h, "map.png", pngOfSize(6000, 6000)); got.Kind != "image" {
		t.Fatalf("a picture at the cap = %+v", got)
	}
	before := len(inboxFiles(t, fx.options.Home))
	e := testAPIError(t, apiUpload(h, "file", "map.png", pngOfSize(6000, 6001), token, true), http.StatusRequestEntityTooLarge, "too_large")
	if e.Message != "an image can be at most 36 megapixels" {
		t.Errorf("message = %q", e.Message)
	}
	if res := upload(h, "map.png", pngOfSize(6001, 6000), token); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("the Board page took a picture over the cap: %d", res.StatusCode)
	}
	if got := len(inboxFiles(t, fx.options.Home)); got != before {
		t.Fatalf("a refused picture left %d files in the inbox", got-before)
	}
}

func TestAUTF8NameSurvivesUploadFetchAndMessage(t *testing.T) {
	fx := apiInboxFixture(t)
	var sent sentTexts
	sent.control(fx)
	h := fx.handler()
	up := apiStored(t, h, "日本語 メモ.txt", []byte("memo"))
	if !strings.HasSuffix(up.Name, "-日本語_メモ.txt") || up.Label != "日本語_メモ.txt" {
		t.Fatalf("stored %+v", up)
	}
	if !inbox.Stored.MatchString(up.Name) {
		t.Fatalf("%q is not in the name grammar", up.Name)
	}
	for _, prefix := range []string{"/api/v1/inbox/", "/inbox/"} {
		res := request(h, "GET", prefix+url.PathEscape(up.Name), nil, true)
		_, params, err := mime.ParseMediaType(res.Header().Get("Content-Disposition"))
		if res.Code != 200 || res.Body.String() != "memo" || err != nil || params["filename"] != up.Label {
			t.Errorf("GET %s = %d %q, disposition %q (%v)", prefix, res.Code, res.Body, res.Header().Get("Content-Disposition"), err)
		}
	}
	_, tl := apiBody[board.APITimeline](t, apiPost(t, h, "/api/v1/message", map[string]any{"text": "see [attachment 1] here", "attachments": []string{up.Name}}, token, true))
	want := "see " + inbox.Line(fx.options.Home, 1, up.Name) + " here"
	if len(sent.texts) != 1 || sent.texts[0] != want {
		t.Fatalf("sent %q, want %q", sent.texts, want)
	}
	var shots []board.APIShot
	for _, e := range tl.Entries {
		if e.Queued {
			shots = e.Shots
		}
	}
	if len(shots) != 1 || shots[0].Name != up.Name || shots[0].Label != up.Label || shots[0].Missing {
		t.Fatalf("shots %+v", shots)
	}
}

func TestTheAppScriptReadsTheSameNameGrammarAsTheServer(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js, inbox.StoredExpr)
	contains(t, "app.js", js, "inbox[\\\\/]("+inbox.StoredExpr+"))\\]$/u;")
}
