package board_test

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

const (
	newTab     = `target="_blank" rel="noopener"`
	refInPlace = `<a class="ref" href="/ref/t1">t1</a>`
)

func TestAwayOpensLiveRefsInANewTab(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{
		userRecord(`[hand v1 wake]\ndecision.answered d1`),
		reply("started t1, see [the PR](https://github.com/a/b/pull/1)"),
	})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "timeline", tl,
		`decision answered <a class="ref" `+newTab+` href="/ref/d1">d1</a>`,
		`started <a class="ref" `+newTab+` href="/ref/t1">t1</a>`,
		`<a href="https://github.com/a/b/pull/1" target="_blank" rel="noopener noreferrer">the PR</a>`)
}

func TestTheLogAndThreadPagesKeepRefsInPlace(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("go"), reply("started t1, see [the PR](https://github.com/a/b/pull/1)")})
	log := get(t, fx.handler(), "/supervisor/log")
	contains(t, "log", log, refInPlace, `<a href="https://github.com/a/b/pull/1" target="_blank" rel="noopener noreferrer">the PR</a>`)
	lacks(t, "log", log, `<a class="ref" `+newTab)

	task := active(t, fx.st, "Fix login")
	if _, err := fx.st.Ask(context.Background(), task.ID, "Keep it?\n\nDoes t1 still hold?"); err != nil {
		t.Fatal(err)
	}
	contains(t, "decision", get(t, fx.handler(), "/decision/d1"), `<a class="ref" href="/ref/t1" title="Fix login">t1</a>`)
	for _, page := range []string{"/decision/d1", "/task/" + state.TaskRef(task.ID)} {
		body := get(t, fx.handler(), page)
		contains(t, page, body, `<a class="ref" href="/`)
		lacks(t, page, body, `<a class="ref" `+newTab)
	}
}

func TestTheTasksPageBrowsesInPlace(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	done := active(t, st, "Finished work")
	if _, err := st.Transition(ctx, done.ID, state.StatusDone); err != nil {
		t.Fatal(err)
	}
	h := board.New(st, token, board.Options{})
	page := get(t, h, "/tasks")
	contains(t, "tasks page", page, `<a class="check-title" title="Finished work" href="/task/t1">Finished work</a>`, `<a class="ref" tabindex="-1" href="/task/t1">t1</a>`)
	lacks(t, "tasks page", page, `target="_blank"`)
	contains(t, "fleet page", get(t, h, "/?all=1"), `<a class="check-title" title="Finished work" href="/task/t1" `+newTab+`>Finished work</a>`, `<a class="ref" tabindex="-1" href="/task/t1" `+newTab+`>t1</a>`)
}

func TestNeedsLinksOpenANewTab(t *testing.T) {
	st := open(t)
	task := active(t, st, "Fix login")
	if _, err := st.Ask(context.Background(), task.ID, "Keep it?\n\nDoes t1 still hold?"); err != nil {
		t.Fatal(err)
	}
	queue := region(get(t, board.New(st, token, board.Options{}), "/"), "queue")
	contains(t, "queue", queue, `<a class="ref" `+newTab+` href="/ref/t1" title="Fix login">t1</a>`, `<a href="/decision/d1" `+newTab+`>Open d1</a>`)
}

func TestOutsideLinksCarryACornerMark(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css, `a.badge[data-tone=link][target=_blank]::after,.md a[target=_blank]:not(.ref,.path)::after{content:"";display:inline-block;width:5px;height:5px;margin:0 1px 0 4px;border-top:1.5px solid currentColor;border-right:1.5px solid currentColor;vertical-align:.3em;opacity:.7}`)
	lacks(t, "board.css", css, "a.ref[target=_blank]::after")
}

func TestPathLinksOpenANewTab(t *testing.T) {
	home := fleetFolder(t, pathFiles)
	for page, body := range pathPages(t, home, "See "+filepath.Join(home, "reports", "t1", "design.md")+" and `"+filepath.Join(home, "shots", "t2")+"`.") {
		tags := regexp.MustCompile(`<a class="path"[^>]*>`).FindAllString(body, -1)
		if len(tags) == 0 {
			t.Fatalf("%s has no path link", page)
		}
		for _, tag := range tags {
			if !strings.HasSuffix(tag, `" `+newTab+`>`) {
				t.Errorf("%s: %s does not open a new tab", page, tag)
			}
		}
	}
}

func TestPathLinksCarryNoCornerMark(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css, `.md a[target=_blank]:not(.ref,.path)::after{`)
	lacks(t, "board.css", css, "a.path::after", "a.path[target=_blank]::after")
}
