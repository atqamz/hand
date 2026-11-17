package board_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
)

const linkedWords = "this file has more than one hard link, so the Board does not show it; copy it to show it"

func hardLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Link(target, at); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
}

func TestAHardLinkToAProtectedFileIsRefused(t *testing.T) {
	h, home := apiDocBoard(t, map[string]string{"hand.db": secret, "board.token": secret, "locks/watch.lock": secret, "reports/t1/ok.txt": "fine\n", "reports/t1/a.txt": "a\n", "reports/t1/z.txt": "z\n"})
	t1 := filepath.Join(home, "reports", "t1")
	hardLink(t, filepath.Join(home, "hand.db"), filepath.Join(t1, "db"))
	hardLink(t, filepath.Join(home, "board.token"), filepath.Join(t1, "token"))
	hardLink(t, filepath.Join(home, "locks", "watch.lock"), filepath.Join(t1, "lock.txt"))
	for _, name := range []string{"db", "token", "lock.txt"} {
		refused(t, h, "/doc/reports/t1/"+name, linkedWords)
		for _, q := range []string{"?raw", "?download", "?from=1"} {
			rec := request(h, "GET", "/doc/reports/t1/"+name+q, nil, true)
			if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), secret) {
				t.Errorf("/doc/reports/t1/%s%s = %d", name, q, rec.Code)
			}
		}
		for _, q := range []string{"", "?raw", "?from=1"} {
			rec := request(h, "GET", "/api/v1/doc/reports/t1/"+name+q, nil, true)
			e := testAPIError(t, rec, http.StatusForbidden, "forbidden")
			if e.Message != linkedWords || strings.Contains(rec.Body.String(), secret) {
				t.Errorf("/api/v1/doc/reports/t1/%s%s = %q:\n%s", name, q, e.Message, rec.Body)
			}
		}
	}
	_, got := apiInto[board.APIDocFile](t, h, "/api/v1/doc/reports/t1/ok.txt")
	if got.Prev == nil || got.Prev.Name != "a.txt" || got.Next == nil || got.Next.Name != "z.txt" {
		t.Errorf("ok.txt neighbours = %+v %+v", got.Prev, got.Next)
	}
	contains(t, "ok.txt", get(t, h, "/doc/reports/t1/ok.txt"), "fine")
}

func TestAFileWithOneLinkIsStillServed(t *testing.T) {
	h, home := apiDocBoard(t, map[string]string{"reports/t1/design.md": design, "reports/t1/data.txt": "payload\n"})
	t1 := filepath.Join(home, "reports", "t1")
	contains(t, "plain", get(t, h, "/doc/reports/t1/data.txt"), "payload")
	other := filepath.Join(t1, "again.txt")
	hardLink(t, filepath.Join(t1, "data.txt"), other)
	refused(t, h, "/doc/reports/t1/data.txt", linkedWords)
	refused(t, h, "/doc/reports/t1/again.txt", linkedWords)
	if err := os.Remove(other); err != nil {
		t.Fatal(err)
	}
	contains(t, "after the other name is gone", get(t, h, "/doc/reports/t1/data.txt"), "payload")
	_, file := apiInto[board.APIDocFile](t, h, "/api/v1/doc/reports/t1/data.txt")
	if file.Kind != "text" || file.Size != 8 {
		t.Errorf("one link = %+v", file)
	}
}
