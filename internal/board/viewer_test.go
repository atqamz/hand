package board_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/state"
)

const toastMarkup = `<div id="toast" role="status" aria-live="polite"></div>`

func TestTheViewerDialogSitsAfterTheToastOutsideEveryRegion(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	h := fx.handler()
	for _, page := range []string{"/", "/tasks", "/supervisor/log", "/missing/page"} {
		body := request(h, "GET", page, nil, true).Body.String()
		if n := strings.Count(body, "<dialog"); n != 1 {
			t.Fatalf("%s holds %d dialogs", page, n)
		}
		toast, dlg := strings.Index(body, toastMarkup), strings.Index(body, `<dialog class="viewer" id="viewer"`)
		if toast < 0 || dlg != toast+len(toastMarkup)+1 {
			t.Fatalf("%s: the dialog does not follow #toast (%d, %d)", page, toast, dlg)
		}
		if i := strings.LastIndex(body, "</main>"); i > toast {
			t.Fatalf("%s: #toast sits inside <main>", page)
		}
		end := strings.Index(body[dlg:], "</dialog>")
		seg := body[dlg : dlg+end]
		contains(t, page+" dialog", seg,
			`aria-labelledby="viewer-title" aria-label="File viewer"`,
			`<span class="viewer-state meta" id="viewer-title"></span>`,
			`<a class="viewer-tab">Open as a page</a>`,
			`<button type="button" class="viewer-close" aria-label="Close the viewer">`,
			`<div class="viewer-body thread doc" tabindex="-1"></div>`)
		lacks(t, page+" dialog", seg, "style=", "<script", "onclick", "data-region")
		for _, name := range []string{"status", "console", "timeline", "queue", "tasks"} {
			lacks(t, page+" "+name, region(body, name), `id="viewer"`, "<dialog")
		}
	}
}

func TestEveryLinkToTheViewerKeepsItsHrefAndNewTab(t *testing.T) {
	st := open(t)
	home := fleetFolder(t, map[string]string{"reports/t1/design.md": design, "reports/t1/run.log": "ok\n"})
	task := active(t, st, "Export")
	a := attempt(t, st, task.ID, state.AttemptRunning, "")
	if _, err := st.AddReport(context.Background(), a.ID, state.ReportProgress, "The log is "+home+"/reports/t1/run.log\n\nDelivered: "+home+"/reports/t1/design.md"); err != nil {
		t.Fatal(err)
	}
	page := get(t, board.New(st, token, board.Options{Home: home}), "/task/t1")
	links := regexp.MustCompile(`<a [^>]*href="/doc/[^"]*"[^>]*>`).FindAllString(page, -1)
	if len(links) < 3 {
		t.Fatalf("task page holds %d viewer links:\n%s", len(links), page)
	}
	for _, l := range links {
		contains(t, "link", l, `target="_blank" rel="noopener"`)
	}
}

func TestTheViewerDialogOpensDocLinksInPlace(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const dlg = document.getElementById("viewer");`,
		`if (dlg && body.dataset.page !== "doc") {`,
		"const sent = u.pathname.startsWith(`${base}/inbox/`) && u.pathname.slice(base.length + \"/inbox/\".length);",
		"if (!u.pathname.startsWith(`${base}/doc/`) || u.searchParams.has(\"raw\") || u.searchParams.has(\"download\")) return null;",
		`if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;`,
		`if (!a || a.hasAttribute("download") || a.matches(".viewer-tab")) return;`,
		`load(u, dlg.open ? "replace" : "push");`,
		`headers: { "X-Hand-Fetch": "1" }`,
		`dlg.showModal();`,
		`tab.target = "_blank";`)
}

func TestTheViewerDialogFollowsTheHistory(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`history.pushState({ viewer: u.href }, "", u.href);`,
		`history.replaceState({ viewer: u.href }, "", u.href);`,
		`if (history.state?.viewer) history.back();`,
		`addEventListener("popstate", (e) => {`,
		`if (e.state?.viewer) load(new URL(e.state.viewer), "none");`,
		`opener?.focus();`,
		`dlg.append(toast);`,
		`if (e.target === dlg && pressed === dlg) close();`,
		`dlg.before(toast);`,
		"if (announce.parentElement !== hosted()) hosted().append(announce);",
		"const res = await fetch(home, { credentials: \"same-origin\" }).catch(() => null);",
		"new EventSource(`${base}/events${new URL(home).search}`)")
	lacks(t, "app.js", js, "fetch(location.href", "location.search")
}

func TestTheViewerDialogKeepsTheDocKeys(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const scope = document.querySelector("dialog.viewer[open]") || (body.dataset.page === "doc" ? document : null);`,
		`if (!scope || e.defaultPrevented || e.repeat || e.altKey || e.ctrlKey || e.metaKey) return;`,
		`const find = e.key === "/" && !field && scope.querySelector(".text-find input");`,
		`if (!step || e.shiftKey || field || (e.target instanceof Element && e.target.closest("video, audio, .lines, .doc-picture")) || !getSelection()?.isCollapsed) return;`,
		"scope.querySelector(`.doc-nav a[rel=${step}]`)?.click();")
}

func TestTheViewerDialogIsStyled(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		"dialog.viewer{width:min(1200px,calc(100vw - 48px));height:calc(100dvh - 48px);max-width:none;max-height:none;padding:0;border:1px solid var(--line);border-radius:var(--r-lg);background:var(--bg);color:var(--fg);box-shadow:0 8px 24px var(--shadow);overflow:hidden}",
		"dialog.viewer[open]{display:flex;flex-direction:column}",
		"dialog.viewer::backdrop{background:rgb(0 0 0 / .32)}",
		"html:has(dialog.viewer:modal){overflow:hidden}",
		".viewer-top{display:flex;flex:none;align-items:center;gap:8px;min-height:calc(var(--control) + 12px);padding:6px 8px 6px 16px;border-bottom:1px solid var(--line);background:var(--panel)}",
		".viewer-tab{display:inline-flex;align-items:center;min-height:var(--control);white-space:nowrap}",
		".viewer-close{width:var(--control);padding:0}",
		".viewer-body.thread.doc{flex:1;min-height:0;max-width:none;width:100%;margin:0;padding:16px 20px 32px;overflow:auto;overscroll-behavior:contain}",
		".viewer-body:focus{outline:none}",
		".viewer-state{min-width:0;overflow:hidden;color:var(--fg);font-family:var(--mono);text-overflow:ellipsis;white-space:nowrap}",
		".viewer .doc-picture img{max-height:calc(100dvh - 300px)}",
		".viewer .doc-picture.actual{max-height:calc(100dvh - 260px)}",
		".viewer .doc-frame{height:calc(100dvh - 300px)}",
		".viewer-body .text-bar{top:-16px}")
	phone := strings.Index(css, "@media (max-width:599px){.work>.panel")
	if phone < 0 {
		t.Fatal("board.css has no first max-width:599px block")
	}
	rule := strings.Index(css[phone:], "dialog.viewer[open]{width:100vw;height:100dvh;border:0;border-radius:0}\ndialog.viewer[open] .viewer-body.thread.doc{padding:12px 12px 24px}\ndialog.viewer[open] .text-bar{top:-12px}")
	if end := strings.Index(css[phone+1:], "\n@media"); rule < 0 || end >= 0 && rule > end {
		t.Fatalf("the phone rules are not in the first max-width:599px block (%d, %d, %d)", phone, rule, end)
	}
}
