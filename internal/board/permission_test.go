package board_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func capture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "harness", "testdata", "claude-2.1.316-"+name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func blockedWorker(t *testing.T, fx *fixture, title string) state.Attempt {
	t.Helper()
	a := workerAttempt(t, fx.st, active(t, fx.st, title).ID)
	if err := fx.st.NoteAttempt(context.Background(), a.ID, "blocked", "Esc to cancel · Tab to amend"); err != nil {
		t.Fatal(err)
	}
	fx.panes = map[string]string{"3": "blocked"}
	return a
}

var keyButton = regexp.MustCompile(`<button name="key" value="([^"]+)">([^<]+)</button>`)

func buttons(q string) [][2]string {
	var out [][2]string
	for _, m := range keyButton.FindAllStringSubmatch(q, -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}

func TestABlockedWorkersPermissionScreenOffersOnlyTheGenericKeyButtons(t *testing.T) {
	fx := newFixture(t)
	blockedWorker(t, fx, "Fix login")
	fx.screen = capture(t, "commit")
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "permission", q, `data-kind="worker"`, `<pre class="screen"><code>`, "git commit")
	lacks(t, "permission", q, ">Approve</button>", ">Deny</button>", "Approve always")
	var got []string
	for _, b := range buttons(q) {
		got = append(got, b[0])
	}
	if !slices.Equal(got, state.SupervisorKeys) {
		t.Fatalf("buttons = %q, want %q", got, state.SupervisorKeys)
	}
}

func TestAStaleScreenIsRefused(t *testing.T) {
	fx := newFixture(t)
	blockedWorker(t, fx, "Fix login")
	fx.screen = capture(t, "commit")
	fx.fail = fmt.Errorf("%w: the screen changed since revision 6; nothing was sent; read it again", state.ErrConflict)
	rec := request(fx.handler(), "POST", "/attempt/a1/keys", url.Values{"csrf": {token}, "revision": {"6"}, "key": {"1"}}, true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "The screen changed, try again") {
		t.Fatalf("stale press = %d:\n%s", rec.Code, rec.Body.String())
	}
	fx.fail = errors.New("boom")
	if rec := request(fx.handler(), "POST", "/attempt/a1/keys", url.Values{"csrf": {token}, "revision": {"7"}, "key": {"1"}}, true); rec.Code == http.StatusSeeOther {
		t.Fatalf("a failed press redirected")
	}
}
