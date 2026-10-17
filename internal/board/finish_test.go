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
	contains(t, "masthead", css, "body:not([data-page=fleet]) .masthead-inner{padding-inline:max(16px,calc((100% - 848px)/2))}")
	lacks(t, "masthead", css, "body[data-page=fleet] .masthead-inner{padding-inline:max(")
	contains(t, "board", block(t, css, "@media (min-width:900px){"), "body[data-page=fleet] .masthead-inner{padding-inline:24px}")
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
	contains(t, "counts", css, ".badge:is([data-tone=fail],[data-state=failing],[data-status=stuck],[data-worst=blocked],[data-worst=worker],[data-worst=failure],[data-worst=resume]){--tone:var(--fail);--tone-bg:var(--fail-bg)}", ".badge:is([data-tone=wait],[data-state=waiting],[data-status=progress],[data-worst=decision],[data-worst=nosup],[data-worst=limited],[data-worst=stalled]){--tone:var(--wait);--tone-bg:var(--wait-bg)}")
	contains(t, "bands", css, ".band[data-tone=fail]{color:var(--fail)}", ".band[data-tone=wait]{color:var(--wait)}")
	lacks(t, "counts", css, ".count:not([data-waiting=\"0\"])")
}

func TestHeadersTintTheirMeta(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "tint", css, ".wait[open][data-tone=fail]>summary .slug{color:color-mix(in srgb,var(--fail) 55%,var(--fg))}", ".wait[open][data-tone=wait]>summary .slug{color:color-mix(in srgb,var(--wait) 55%,var(--fg))}")
	lacks(t, "tint", css, ".wait[data-tone=fail]>summary .slug{")
	lacks(t, "separator dots", css, `content:"·"`)
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
	contains(t, "hand lines", asset(t, "board.css"), "-webkit-line-clamp:1;overflow:hidden}", ".hand-line .ref{color:inherit}")
	contains(t, "wakes summary", asset(t, "board.css"), ".wakes>summary{grid-column:1/-1;display:grid;grid-template-columns:8px minmax(0,1fr) auto;", ".wakes>summary>.span{white-space:nowrap}")
}

func TestASentItemStaysClosedOnlyForItsScreen(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js, "const sent = new Map()", `sent.set(id, { label, screen: data.get("screen") || "" })`, "sent.delete(id)", "collapse(document.getElementById(id))")
	lacks(t, "app.js", js, "chosen.set(item.id, false)")
}

func TestThePhoneMastheadStaysOneRow(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "phone", block(t, css, "@media (max-width:599px){"), ".masthead .strip{flex:0 1 auto;min-width:0;display:flex;flex-wrap:wrap;justify-content:flex-end;padding-block:4px;overflow-x:clip}", ".masthead .strip-note{flex-basis:100%}")
	contains(t, "status line", css, ".strip .status-line{display:flex;align-items:center;gap:10px;min-width:0;overflow-x:clip;white-space:nowrap;flex-wrap:nowrap}")
	contains(t, "short screens", block(t, css, "@media (max-height:599px){"), ".masthead .strip{display:flex;flex-wrap:wrap;align-items:center;gap:8px;min-width:0;padding-block:4px;overflow-x:clip}", ".masthead .strip-note{flex-basis:100%;")
}

func TestStuckReportsWearTheFailTone(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "stuck", css, ":is(.thread,.task-main)>.card.report[data-status=stuck]>header.slug{background:var(--fail-bg)}")
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

func TestAnswerChipsReadAsOptions(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "chips", css, ".chips{display:flex;flex-direction:column;align-items:flex-start;gap:6px;margin:0 0 8px}", ".chips .option{display:flex;align-items:center;justify-content:flex-start;max-width:100%;height:auto;min-height:var(--control);padding:5px 12px;border-radius:var(--r-lg);font-weight:400;text-align:left;white-space:normal}")
}

func TestTheFleetIDSharesTheNameBaseline(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	contains(t, "brand", get(t, fx.handler(), "/"), `<h1 class="fleet-name" title="test">test</h1><span class="ref fleet-id">`)
	contains(t, "css", asset(t, "board.css"), ".brand{display:flex;align-items:baseline;", ".brand>.mark{align-self:center}")
}

func TestReportHeadersCentreTheirMeta(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "report head", css, ":is(.thread,.task-main)>.card>header.slug{display:flex;flex-wrap:wrap;align-items:center;")
}

func TestNeedsAffordances(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		".wait>summary:focus-visible{outline-offset:-2px}",
		".wait>summary{display:flex;align-items:center;gap:10px;min-height:44px;padding:0 16px;cursor:pointer;list-style:none}",
		".wait>summary>.icon:last-child{order:2;",
		".wait[open]>summary>.icon:last-child{transform:rotate(180deg)}",
		".work{scrollbar-gutter:stable both-edges}")
	lacks(t, "board.css", css, ".wait>summary>.icon:last-child{grid-column")
	contains(t, "reduced motion", block(t, css, "@media (prefers-reduced-motion:reduce){"), ".wait>summary>.icon:last-child{transition:none}")
	contains(t, "tabs", block(t, css, "@media (max-width:899px){"), "#needs .panel-head{display:none}")
}

func TestNeedsRowsAreCalmAndTheOpenItemIsACard(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "rows", css,
		".wait{overflow-wrap:anywhere;border-bottom:1px solid var(--line);background:var(--bg)}",
		".wait>summary:hover{background:var(--hover)}",
		".wait-title{flex:1 1 0;overflow:hidden;color:var(--fg);text-overflow:ellipsis;white-space:nowrap}",
		".wait>summary .slug>.code,.wait:not([open])>summary .slug>.meta{display:none}")
	lacks(t, "rows", css, ".wait[data-tone=fail]>summary{", ".wait[data-tone=wait]>summary{", ".wait:not([open]) .wait-title{")
	contains(t, "card", css,
		".wait[open]{margin:6px 0 10px;border:1px solid var(--line);border-radius:var(--r-lg);overflow:hidden}",
		".wait[open][data-tone=fail]>summary{background:var(--fail-bg)}",
		".wait[open]>summary{flex-wrap:wrap;align-items:flex-start;gap:2px 10px;padding:12px 15px 10px}",
		".wait-body{display:grid;grid-template-columns:minmax(0,1fr);gap:10px;padding:10px 15px 15px 41px}")
	contains(t, "two lines", block(t, css, "@container (width < 480px){"), ".wait:not([open])>summary{flex-wrap:wrap;gap:0 10px;min-height:52px;padding-block:7px}", ".wait:not([open])>summary>.slug{order:5;flex:1 0 100%;padding-left:26px}")
	contains(t, "container", css, "#needs{container-type:inline-size}")
	contains(t, "touch", block(t, css, "@media (pointer:coarse){textarea"), ".wait>summary{min-height:52px}")
}

func TestNeedsSeverityHoldsInForcedColours(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "forced colours", block(t, css, "@media (forced-colors:active){.wait"),
		".wait[open]{border-color:CanvasText}",
		".wait[open]>summary{border-bottom:1px solid CanvasText}")
}

func TestTheMisreadDisclosureHidesTheForceForm(t *testing.T) {
	fx := newFixture(t)
	fx.status, fx.hint, fx.screen = "blocked", "Esc to cancel · Tab to amend", permission
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	q := region(get(t, fx.handler(), "/"), "queue")
	contains(t, "misread", q, `<details class="misread" id="misread-s1"><summary>Screen misread?</summary><form class="force" method="post" action="/supervisor/force" data-fetch>`, "Type queued messages anyway")
	disclosure := q[strings.Index(q, `class="misread"`):]
	contains(t, "misread", disclosure[:strings.Index(disclosure, "</details>")], "Type queued messages anyway")
	lan := newFixture(t)
	lan.options.Controls = false
	lan.status, lan.hint, lan.screen = "blocked", "Esc to cancel · Tab to amend", permission
	lan.supervisor(t, state.AttemptRunning, "gen-1")
	lacks(t, "LAN misread", region(get(t, lan.handler(), "/"), "queue"), "Screen misread?", "Type queued messages anyway")
}

func TestChipsTheComposerAndTheMastheadStayInsideTheirBoxes(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "composer", css,
		"#chat{container-type:inline-size}",
		"@container (max-width:519px){[data-send-key]{display:none}}")
	contains(t, "short screens", block(t, css, "@media (max-height:599px){"), ".masthead-inner{min-height:36px;padding:0 12px;overflow-x:clip}")
	contains(t, "controls", css, ".filters :is(input,select),.menu-body label select{font-size:14px;font-weight:400;width:100%;min-width:0}")
	if strings.Index(css, ".menu-body label select{font-size:14px") > strings.Index(css, "@media (pointer:coarse){textarea") {
		t.Fatal("the 14px control rule comes after the coarse 16px one and wins on touch")
	}
	contains(t, "thread pages", css, ".thread-head .question{max-height:none;overflow:visible}", ".thread>.timeline{max-height:none;overflow:visible;mask-image:none}")
	contains(t, "touch", block(t, css, "@media (pointer:coarse){textarea"),
		".filters :is(input,select),.menu-body label select{font-size:16px}",
		".crumbs a:not(.ref),.back a,.need a:not(.ref),.decision a:not(.ref){display:inline-flex;align-items:center}",
		"a.badge{position:relative}",
		`a.badge::before{content:"";position:absolute;inset:-12px 0}`)
}

func TestThePhoneConsoleWrapsInThePaneHeadAndTheModelMenuIsAGrid(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "phone", block(t, css, "@media (max-width:599px){"), ".console{padding:8px 12px}.console>.pending{flex:1 1 100%}", ".console-box,.sup>.hint.read-only{margin:0 12px 12px}")
	contains(t, "model menu", css,
		".menu-body form{display:grid;gap:8px}",
		".menu-body form:has(>input[name=harness]){grid-template-columns:repeat(2,minmax(max-content,1fr))}",
		".menu-body form:has(>input[name=harness])>button,.menu-body form>select:only-of-type{grid-column:1/-1}",
		".field-label{margin:4px 0 0;")
	contains(t, "panels", css, ".work:focus-visible{outline:0}", ".sup>.hint.read-only{margin:0 max(16px,calc((100% - 760px)/2)) 16px;")
}

func TestEveryNeedsMotionHasAReducedMotionOverride(t *testing.T) {
	css := asset(t, "board.css")
	reduced := block(t, css, "@media (prefers-reduced-motion:reduce){")
	contains(t, "reduced motion", reduced,
		".wait[data-new]>summary{animation:none}",
		".wait::details-content{transition:none}",
		".wait>summary>.icon:last-child{transition:none}",
		".misread>summary::before{transition:none}")
}
