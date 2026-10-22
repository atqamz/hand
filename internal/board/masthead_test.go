package board_test

import "testing"

func TestTheMastheadSticksOnEveryPage(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "board.css", css,
		"--pane:clamp(360px,var(--chat,clamp(400px,38vw,720px)),calc(100% - 440px));--mast:48px}",
		"html{-webkit-text-size-adjust:100%;scroll-padding-top:var(--mast)}",
		".masthead{position:sticky;top:0;z-index:4;background:var(--bg);",
		"body[data-page=fleet]:not([data-shell]) .sup{position:sticky;top:var(--mast);height:calc(100dvh - var(--mast))}")
	lacks(t, "board.css", css,
		"#needs,#tasks,#chat{scroll-margin-top:48px}",
		".tabs{position:sticky",
		".masthead .tabs{position:static",
		".console-box{position:sticky;bottom:0;z-index:2}\n.masthead{position:sticky")
	contains(t, "app.js", asset(t, "app.js"),
		`new ResizeObserver(() => document.documentElement.style.setProperty("--mast", `+"`${masthead.getBoundingClientRect().height}px`"+`)).observe(masthead)`)
}
