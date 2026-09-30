package board_test

import (
	"context"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func block(t *testing.T, css, start string) string {
	t.Helper()
	i := strings.Index(css, start)
	if i < 0 {
		t.Fatalf("board.css has no %q", start)
	}
	return css[i : i+strings.Index(css[i:], "}}")+2]
}

func TestThePhoneRowCountsTheWorstKind(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	ctx := context.Background()
	attempt(t, fx.st, active(t, fx.st, "Flaky test").ID, state.AttemptFailed, "launch did not finish")
	one := active(t, fx.st, "Fix login")
	for _, q := range []string{"Keep it?", "Ship it?"} {
		if _, err := fx.st.Ask(ctx, one.ID, q); err != nil {
			t.Fatal(err)
		}
	}
	contains(t, "phone row", region(get(t, fx.handler(), "/"), "status"), `data-worst="failure"><span class="n">1</span> failed`)
}

func TestAPressClosesTheItemInPlace(t *testing.T) {
	contains(t, "app.js", asset(t, "app.js"), "item.open = false", "summary.dataset.sent")
	css := asset(t, "board.css")
	contains(t, "board.css", css, `.wait[data-sent]>summary::after{content:"Sent " attr(data-sent) " · waiting…"`, ".wait[data-sent] .countdown{display:none}", ".wait::details-content{", "interpolate-size:allow-keywords")
	contains(t, "reduced motion", block(t, css, "@media (prefers-reduced-motion:reduce){"), ".wait::details-content{transition:none}")
}

func TestTheMastheadAlignsWithItsColumn(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "masthead", css, "body[data-page=fleet] .masthead-inner{padding-inline:max(16px,calc((100% - 760px)/2))}", "body:not([data-page=fleet]) .masthead-inner{padding-inline:max(16px,calc((100% - 848px)/2))}")
	contains(t, "desk", block(t, css, "@media (min-width:1280px){"), "body[data-page=fleet] .masthead-inner{padding-inline:max(16px,calc((100% - 1192px)/2))}")
}

func TestNoSupervisorWaitsInAmber(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptStopped, "gen-1")
	workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	q := region(get(t, fx.handler(), "/"), "queue")
	item := q[strings.Index(q, `data-kind="nosup"`):]
	contains(t, "nosup", item[:strings.IndexByte(item, '>')], `data-tone="wait"`)
}

func TestLifecycleLinesSayItOnce(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	if _, err := fx.st.EndSupervisor(context.Background(), 1, state.AttemptStopped, "stopped by operator"); err != nil {
		t.Fatal(err)
	}
	body := get(t, fx.handler(), "/")
	contains(t, "said once", body, "s1 stopped by operator")
	lacks(t, "said once", body, "stopped: stopped")
}

func TestShortScreensMergeTheTabs(t *testing.T) {
	fx := newFixture(t)
	body := get(t, fx.handler(), "/")
	head, tabs, end := strings.Index(body, `<header class="masthead">`), strings.Index(body, `<nav class="tabs"`), strings.Index(body, "</header>")
	if head < 0 || tabs < head || tabs > end {
		t.Fatalf("the tabs sit outside the masthead (%d %d %d)", head, tabs, end)
	}
	contains(t, "short screens", block(t, asset(t, "board.css"), "@media (max-height:599px){"), ".masthead{display:flex;")
}

func TestCountsFollowTheSeverityRamp(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "counts", css, ".queue-head[data-worst=failure] .count", ".queue-head[data-worst=decision] .count", ".tabs .n[data-worst=failure]", ".tabs .n[data-worst=decision]", ".needs-count[data-worst=nosup]")
	lacks(t, "counts", css, ".count:not([data-waiting=\"0\"])")
}

func TestHeadersTintTheirMeta(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "tint", css, ".wait[data-tone=fail]>summary .slug{color:color-mix(in srgb,var(--fail) 55%,var(--fg))}", ".wait[data-tone=wait]>summary .slug{color:color-mix(in srgb,var(--wait) 55%,var(--fg))}", "::after{content:\"·\";display:inline-block;margin-left:8px;opacity:.5}")
	i := strings.Index(css, ".menu-body label{")
	if i < 0 || strings.Contains(css[i:i+strings.IndexByte(css[i:], '}')], "--mono") {
		t.Fatal("menu labels still wear mono caps")
	}
}

func TestBrowserSurfacesAreThemed(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "scrollbars", css, "::-webkit-scrollbar-button{display:none}", "@supports not selector(::-webkit-scrollbar){")
	i := strings.Index(css, ".menu-body{")
	if i < 0 || !strings.Contains(css[i:i+strings.IndexByte(css[i:], '}')], "box-shadow:0 8px 24px var(--shadow)") {
		t.Fatal("menus lack the toast's shadow")
	}
	contains(t, "phone", block(t, css, "@media (max-width:599px){"), ".ctx{display:none}")
}

func TestHandLinesStayOnOneLine(t *testing.T) {
	contains(t, "hand lines", asset(t, "board.css"), "-webkit-line-clamp:1;overflow:hidden}", ".hand-line .ref{padding:0;border:0;background:none;")
}

func TestASentItemStaysClosedOnlyForItsScreen(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js, "const sent = new Map()", `sent.set(id, { label, screen: data.get("screen") || "" })`, "sent.delete(id)", "collapse(document.getElementById(id))")
	lacks(t, "app.js", js, "chosen.set(item.id, false)")
}

func TestThePhoneMastheadStaysOneRow(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "phone", block(t, css, "@media (max-width:599px){"), ".masthead .strip{flex:none;")
	contains(t, "status line", css, ".strip .status-line{display:flex;align-items:center;gap:10px;min-width:0;white-space:nowrap;flex-wrap:nowrap}")
}

func TestStuckReportsWearTheFailTone(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "stuck", css, ".thread>.card.report[data-status=stuck]>header.slug{background:var(--fail-bg)}")
	lacks(t, "stuck", css, "data-status=blocked]", "data-status=failed]")
}

func TestStartingLabelsFollowEveryStartForm(t *testing.T) {
	contains(t, "app.js", asset(t, "app.js"), "form.dataset.starting")
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptStopped, "gen-1")
	workerAttempt(t, fx.st, active(t, fx.st, "Fix login").ID)
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "nosup forms", q, `action="/supervisor/resume" data-fetch data-starting="resuming…"`, `action="/supervisor/start" data-fetch data-starting="starting…"`)
}

func TestErrorPagesSpeakLowerCase(t *testing.T) {
	fx := newFixture(t)
	body := request(fx.handler(), "GET", "/task/t99", nil, true).Body.String()
	lacks(t, "error", body, ">ERROR<")
	contains(t, "error", body, `<span class="kind">error</span>`)
}
