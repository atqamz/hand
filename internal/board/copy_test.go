package board_test

import (
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestSupervisorRepliesAreBorderedBubbles(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "bubble", css,
		".timeline>.from-sup{align-self:flex-start;max-width:min(100%,calc(72ch + 30px));padding:9px 13px 11px;border:1px solid var(--line-strong);border-radius:var(--r-lg);background:var(--card);font-size:15px}",
		".from-sup .msg-head .who{color:var(--muted)}",
		".from-sup .md :is(code,pre){background:var(--bg)}",
		".from-sup .md pre code{background:none}",
		".from-sup .md pre{background:linear-gradient(to left,var(--bg) 30%,transparent) right/40px 100% no-repeat local,linear-gradient(to left,var(--shadow),transparent) right/14px 100% no-repeat scroll,var(--bg)}",
		".from-sup .md blockquote{border-left-color:var(--line-strong)}",
		".from-sup .md hr{border-top-color:var(--line-strong)}",
		".timeline>.card{min-width:0;padding:0;border:0}",
		".hand-line{display:grid;",
	)
	lacks(t, "bubble", css, "border:1px solid var(--line);border-radius:var(--r-lg);background:var(--card);font-size:15px")
}

func TestCodeBlocksStayPlainInTheMarkup(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("go"), reply("run it:\\n\\n```sh\\n\\tmake\\n```")})
	tl := region(get(t, fx.handler(), "/"), "timeline")
	contains(t, "code", tl, "<pre><code>")
	lacks(t, "code", tl, `class="code"`, `class="copy"`, "<svg")
}

func TestCodeBlocksGetACopyButtonFromTheScript(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`for (const pre of root.querySelectorAll(".md pre")) {`,
		`if (pre.parentElement.matches(".code")) continue;`,
		`wrap.className = "code";`,
		`button.type = "button";`,
		`button.className = "copy";`,
		`wrap.append(pre, button);`,
		`document.createElementNS(svgNS, "svg")`,
		`announce.setAttribute("role", "status");`,
		`const copyFor = 1500;`,
		`say(button, label);`,
		`if (spoken === button) say(null, "");`,
		`spokenTimer = setTimeout(() => (announce.textContent = text), 50);`,
		`await navigator.clipboard.writeText(text);`,
		`document.execCommand("copy")`,
		`const ok = await writeText(button.previousElementSibling.textContent);`,
		`outcome(button, "copied", "Copied")`,
		`outcome(button, "failed", "Copy failed")`,
		`button.dataset.state = state;`,
		"enhance(root);",
		"copyKey(document.activeElement, el)",
		`copies(el).find((c) => copyKey(c, el) === copyFocus)?.focus();`,
	)
	lacks(t, "app.js", js, "alert(", `setAttribute("style"`, "style.cssText", "button.innerHTML", "wrap.innerHTML")
}

func TestCopyButtonStyles(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "code block", css,
		".md .code{--surface:var(--card);position:relative;margin:0 0 8px;letter-spacing:normal;text-transform:none}",
		".from-sup .md .code{--surface:var(--bg)}",
		".md>.code:last-child{margin-bottom:0}",
		".md .code>pre{margin:0;padding-right:calc(var(--control) + 12px);min-height:calc(var(--control) + 8px)}",
		".timeline .card .copy>:not(.table):not(pre):not(.code){max-width:72ch}",
	)
	contains(t, "button", css,
		".code>.copy{position:absolute;top:4px;right:4px;width:var(--control);padding:0;background:var(--surface);color:var(--muted);opacity:0;transition:opacity .15s}",
		".code:hover>.copy,.code:focus-within>.copy,.code>.copy[data-state]{opacity:1}",
		".code>.copy[data-state=copied]{color:var(--pass)}",
		".code>.copy[data-state=failed]{color:var(--fail)}",
		".code>.copy:not([data-state])>[data-glyph=copy],.code>.copy[data-state=copied]>[data-glyph=copied],.code>.copy[data-state=failed]>[data-glyph=failed]{display:block}",
		"body>.sr{position:fixed;top:0;left:0;",
	)
	contains(t, "touch", block(t, css, "@media (pointer:coarse){textarea,input,select{font-size:16px}"), ".code>.copy{opacity:1}")
	contains(t, "reduced motion", block(t, css, "@media (prefers-reduced-motion:reduce){"), ".code>.copy{transition:none}")
	contains(t, "forced colours", css, "@media (forced-colors:active){.code>.copy{border-color:ButtonText;color:ButtonText}}")
}
