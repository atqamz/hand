package board_test

import (
	"context"
	"net/http"
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
			`aria-labelledby="viewer-title" aria-label="Viewer"`,
			`<div class="viewer-top"><button type="button" class="viewer-back" aria-label="Back" disabled>`,
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

func dialogBlock(t *testing.T, js string) string {
	t.Helper()
	start, end := strings.Index(js, `if (dlg && body.dataset.page !== "doc") {`), strings.Index(js, "for (const [name, el] of regions) served.set(name, el.innerHTML);")
	if start < 0 || end < start {
		t.Fatal("app.js has no dialog block")
	}
	return js[start:end]
}

func TestTheViewerDialogOpensThingsInPlace(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const dlg = document.getElementById("viewer");`,
		`if (dlg && body.dataset.page !== "doc") {`,
		"if (u.origin !== location.origin || !u.pathname.startsWith(`${base}/`)) return null;",
		"const sent = u.pathname.startsWith(`${base}/inbox/`) && u.pathname.slice(base.length + \"/inbox/\".length);",
		"if (!route.test(u.pathname.slice(base.length)) || u.searchParams.has(\"raw\") || u.searchParams.has(\"download\")) return null;",
		`if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;`,
		`if (!a || a.hasAttribute("download") || a.matches(".viewer-tab")) return;`,
		`if (!dlg.contains(a)) return;`,
		`if (dlg.open && u.href === at.href) return;`,
		`load(u, "push");`,
		`load(u, "replace");`,
		`headers: { "X-Hand-Fetch": "1" }`,
		`dlg.showModal();`,
		`tab.target = "_blank";`,
		`const within = (id) => id && [...pane.querySelectorAll("[id]")].find((el) => el.id === id);`,
		"const to = res.redirected ? viewable(res.url) : null;",
		"to.hash = u.hash || (/^\\/ref\\/r\\d+$/.test(u.pathname.slice(base.length)) ? `#${u.pathname.split(\"/\").pop()}` : \"\");")
	lacks(t, "app.js", js, "docURL", "document.getElementById(u.hash", "document.getElementById(a.hash")
}

var routeLiteral = regexp.MustCompile(`const route = /(.+)/;`)

func TestTheViewerRouteTable(t *testing.T) {
	m := routeLiteral.FindStringSubmatch(asset(t, "app.js"))
	if m == nil {
		t.Fatal("app.js has no route pattern")
	}
	route := regexp.MustCompile(m[1])
	for path, want := range map[string]bool{
		"/task/t1":                    true,
		"/task/t420":                  true,
		"/attempt/a1":                 true,
		"/attempt/a12":                true,
		"/decision/d3":                true,
		"/ref/t1":                     true,
		"/ref/a2":                     true,
		"/ref/d3":                     true,
		"/ref/r4":                     true,
		"/doc/reports/t1/design.md":   true,
		"/doc/shots":                  true,
		"/doc/inbox/20261001-1-a.pdf": true,
		"/":                           false,
		"/tasks":                      false,
		"/supervisor/log":             false,
		"/connect":                    false,
		"/events":                     false,
		"/inbox/20261001-1-a.pdf":     false,
		"/static/app.js":              false,
		"/task":                       false,
		"/task/":                      false,
		"/task/t":                     false,
		"/task/1":                     false,
		"/task/a1":                    false,
		"/task/t1/":                   false,
		"/task/t1/x":                  false,
		"/attempt/t1":                 false,
		"/attempt/a1/keys":            false,
		"/decision/a1":                false,
		"/decision/d1/answer":         false,
		"/report/r1/ack":              false,
		"/ref/s1":                     false,
		"/ref/x9":                     false,
		"/ref/t":                      false,
		"/ref/t1/x":                   false,
		"/doc":                        false,
		"/doc/":                       false,
	} {
		if got := route.MatchString(path); got != want {
			t.Errorf("route %s = %v, want %v", path, got, want)
		}
	}
}

func TestEveryViewerRouteAnswersWithAThreadPage(t *testing.T) {
	m := routeLiteral.FindStringSubmatch(asset(t, "app.js"))
	if m == nil {
		t.Fatal("app.js has no route pattern")
	}
	route := regexp.MustCompile(m[1])
	st := open(t)
	seed(t, st)
	h := board.New(st, token, board.Options{})
	for _, path := range []string{"/task/t1", "/attempt/a1", "/decision/d1"} {
		if !route.MatchString(path) {
			t.Errorf("%s is not a viewer route", path)
		}
		contains(t, path, get(t, h, path), `<main class="thread`)
	}
	for ref, to := range map[string]string{"t1": "/task/t1", "a1": "/attempt/a1", "d1": "/decision/d1", "r1": "/task/t1#r1"} {
		rec := request(h, "GET", "/ref/"+ref, nil, true)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != to {
			t.Errorf("/ref/%s = %d to %q, want %q", ref, rec.Code, rec.Header().Get("Location"), to)
		}
		if !route.MatchString("/ref/"+ref) || !route.MatchString(strings.SplitN(to, "#", 2)[0]) {
			t.Errorf("/ref/%s or %s is not a viewer route", ref, to)
		}
	}
}

func TestTheViewerDialogFollowsTheHistory(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const stackMax = 20;`,
		"const session = Math.random().toString(36).slice(2);",
		`const mine = (s) => !!s?.viewer && s.token === session;`,
		`const depth = () => (mine(history.state) ? history.state.depth : 0);`,
		`const entry = (href, level) => ({ viewer: href, depth: level, token: session });`,
		`const level = how === "none" ? depth() || 1 : how === "push" && here < stackMax ? here + 1 : Math.max(here, 1);`,
		`if (how === "push" && level > here) history.pushState(entry(u.href, level), "", u.href);`,
		`else if (how !== "none") history.replaceState(entry(u.href, level), "", u.href);`,
		`history.replaceState(entry(to.href, level), "", to.href);`,
		`back.disabled = level < 2;`,
		`back.addEventListener("click", () => history.back());`,
		`if (depth()) history.go(-depth());`,
		`addEventListener("popstate", (e) => {`,
		`if (mine(e.state)) load(new URL(e.state.viewer), "none");`,
		`again.addEventListener("click", () => load(at, "none"));`,
		`if (opener && !opener.isConnected) opener = same(opener)[nth] ?? same(opener)[0];`,
		`opener?.focus();`,
		`dlg.append(toast);`,
		`if (e.target === dlg && pressed === dlg) close();`,
		`dlg.before(toast);`,
		"if (announce.parentElement !== hosted()) hosted().append(announce);",
		"const res = await fetch(home, { credentials: \"same-origin\" }).catch(() => null);",
		"new EventSource(`${base}/events${new URL(home).search}`)")
	lacks(t, "app.js", js, "fetch(location.href", "location.search", "if (history.state?.viewer) history.back();")
}

func TestTheViewerDialogRefreshesLiveThreadPagesAndStopsWithThem(t *testing.T) {
	js := asset(t, "app.js")
	block := dialogBlock(t, js)
	contains(t, "dialog block", block,
		`const pollEvery = 10000;`,
		`if (pulling || document.visibilityState === "hidden") return;`,
		"const res = await fetch(at.href, { credentials: \"same-origin\" }).catch(() => null);",
		`if (n !== seq || !main || main.innerHTML === served.get("viewer") || typing(pane)) return;`,
		`const top = pane.scrollTop;`,
		`const had = pane.contains(document.activeElement);`,
		`apply("viewer", main.innerHTML, pane);`,
		`if (had && !pane.contains(document.activeElement)) pane.focus({ preventScroll: true });`,
		`pane.scrollTop = top;`,
		`if (polling && document.visibilityState === "visible") pull();`,
		`if (res.ok && ["task", "attempt"].includes(main?.dataset.region)) {`,
		`polling = setInterval(pull, pollEvery);`,
		"const unwatch = () => {\n\t\t\tclearInterval(polling);\n\t\t\tpolling = 0;\n\t\t\tserved.delete(\"viewer\");\n\t\t};")
	if n := strings.Count(block, "setInterval("); n != 1 {
		t.Errorf("the dialog starts %d intervals, want 1", n)
	}
	if n := strings.Count(block, "unwatch();"); n != 2 {
		t.Errorf("the dialog stops its refresh in %d places, want 2 (a new page and the close)", n)
	}
	lacks(t, "dialog block", block, "EventSource", "setTimeout(", "regions.set(")
	contains(t, "app.js", js, "const apply = (name, html, el = regions.get(name)) => {")
}

func TestTheViewerDialogNeverPostsAForm(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`if (form.method === "post" && form.closest("dialog.viewer")) return e.preventDefault();`,
		`const forms = [...pane.querySelectorAll("form")].filter((f) => f.method === "post");`,
		`for (const f of forms) for (const c of f.elements) c.disabled = true;`,
		`pane.querySelector("form.answer") ? "Answer this on the page. " : "Mark read on the page. "`,
		`open.className = "viewer-tab";`,
		`open.textContent = "Open as a page";`,
		`pane.prepend(line);`)
	if n := strings.Count(dialogBlock(t, js), "freeze();"); n != 2 {
		t.Errorf("the dialog freezes a page's forms in %d places, want 2 (a new page and a refresh)", n)
	}
	contains(t, "app.js", js, `form.answer textarea[name=answer]:not(dialog *)`)
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
		".viewer-top{display:flex;flex:none;align-items:center;gap:8px;min-height:calc(var(--control) + 12px);padding:6px 8px;border-bottom:1px solid var(--line);background:var(--panel)}",
		".viewer-tab{display:inline-flex;align-items:center;min-height:var(--control);padding:0 12px;border:1px solid var(--line-strong);border-radius:var(--r-md);font-weight:600;white-space:nowrap}",
		".viewer-back,.viewer-close{width:var(--control);padding:0}",
		".viewer-body.thread.doc{flex:1;min-height:0;max-width:none;width:100%;margin:0;padding:16px 20px 32px;overflow:auto;overscroll-behavior:contain}",
		".viewer-body:focus{outline:none}",
		".viewer-state{min-width:0;overflow:hidden;color:var(--fg);font-family:var(--mono);text-overflow:ellipsis;white-space:nowrap}",
		".viewer .doc-picture img{max-height:calc(100dvh - 300px)}",
		".viewer .doc-picture.actual{max-height:calc(100dvh - 260px)}",
		".viewer .doc-frame{height:calc(100dvh - 300px)}",
		".viewer-body .text-bar{top:-16px}",
		".viewer-body form[method=post]{pointer-events:none}",
		".question{white-space:pre-wrap;max-height:40vh;overflow-y:auto;overflow-wrap:anywhere}",
		"@media (min-width:1000px){.viewer-body .task-side{top:0;max-height:calc(100dvh - 61px - var(--control))}}")
	phone := strings.Index(css, "@media (max-width:599px){.work>.panel")
	if phone < 0 {
		t.Fatal("board.css has no first max-width:599px block")
	}
	rule := strings.Index(css[phone:], "dialog.viewer[open]{width:100vw;height:100dvh;border:0;border-radius:0}\ndialog.viewer[open] .viewer-body.thread.doc{padding:12px 12px 24px}\ndialog.viewer[open] .text-bar{top:-12px}")
	if end := strings.Index(css[phone+1:], "\n@media"); rule < 0 || end >= 0 && rule > end {
		t.Fatalf("the phone rules are not in the first max-width:599px block (%d, %d, %d)", phone, rule, end)
	}
}
