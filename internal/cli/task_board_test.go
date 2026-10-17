package cli_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

func TestTaskListAndTheTasksPageAgree(t *testing.T) {
	h := initWithProject(t)
	for _, title := range []string{"Fix login", "Fix inbox", "Fix search", "Write docs", "Fix docs links", "Fix runner"} {
		h.ok("task", "add", "hand", title)
	}
	h.ok("task", "start", "t1")
	h.ok("task", "start", "t3")
	h.ok("task", "done", "t3")
	h.ok("task", "abandon", "t5")
	h.ok("task", "start", "t6")
	h.ok("task", "done", "t6")
	st, err := state.Open(filepath.Join(h.home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const token = "0123456789abcdef0123456789abcdef"
	page := board.New(st, token, board.Options{})
	pageRefs := regexp.MustCompile(`class="ref"[^>]*>(t\d+)<`)
	for _, tc := range []struct{ q, status string }{
		{"fix", ""},
		{"fix", "all"},
		{"fix", "finished"},
		{"fix", "done"},
		{"fix", "abandoned"},
		{"fix", "active"},
		{"fix", "inbox"},
		{"docs", ""},
		{"t2", ""},
		{"nothing", ""},
		{"", "all"},
		{"", "inbox"},
	} {
		args := []string{"task", "list", "--q", tc.q, "--limit", "50"}
		query := url.Values{"q": {tc.q}}
		if tc.status != "" {
			args = append(args, "--status", tc.status)
			query.Set("status", tc.status)
		}
		var want []string
		for _, line := range strings.Split(h.ok(args...), "\n")[1:] {
			if id, _, ok := strings.Cut(strings.TrimSpace(line), ","); ok {
				want = append(want, id)
			}
		}
		req := httptest.NewRequest("GET", "/tasks?"+query.Encode(), nil)
		req.AddCookie(&http.Cookie{Name: "hand_board", Value: token})
		rec := httptest.NewRecorder()
		page.ServeHTTP(rec, req)
		var got []string
		for _, m := range pageRefs.FindAllStringSubmatch(rec.Body.String(), -1) {
			got = append(got, m[1])
		}
		if tc.q == "fix" && tc.status == "" && len(got) != 5 {
			t.Errorf("the page lists %v for a query that matches five tasks", got)
		}
		if !slices.Equal(got, want) {
			t.Errorf("q=%q status=%q: page %v, hand task list %v", tc.q, tc.status, got, want)
		}
	}
}
