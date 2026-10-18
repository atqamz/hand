package board_test

import "testing"

func TestSupervisorRepliesAreBorderedBubbles(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "bubble", css,
		".timeline>.from-sup{align-self:flex-start;max-width:min(100%,calc(72ch + 30px));padding:9px 13px 11px;border:1px solid var(--line-strong);border-radius:var(--r-lg);background:var(--card);font-size:15px}",
		".from-sup .msg-head .who{color:var(--muted)}",
		".from-sup .md :is(code,pre){background:var(--bg)}",
		".from-sup .md pre{background:linear-gradient(to left,var(--bg) 30%,transparent) right/40px 100% no-repeat local,linear-gradient(to left,var(--shadow),transparent) right/14px 100% no-repeat scroll,var(--bg)}",
		".from-sup .md blockquote{border-left-color:var(--line-strong)}",
		".from-sup .md hr{border-top-color:var(--line-strong)}",
		".timeline>.card{min-width:0;padding:0;border:0}",
		".hand-line{display:grid;",
	)
	lacks(t, "bubble", css, "border:1px solid var(--line);border-radius:var(--r-lg);background:var(--card);font-size:15px")
}
