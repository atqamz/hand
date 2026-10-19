package board_test

import (
	"regexp"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

func TestTheScrollToLatestButtonComesFromTheScript(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`down: ["M8 3v10", "M3.5 8.5L8 13l4.5-4.5"],`,
		`const host = document.querySelector(".console-box, .sup > .hint.read-only");`,
		`button.className = "latest";`,
		`badge.setAttribute("aria-hidden", "true");`,
		`live.setAttribute("role", "status");`,
		`host.prepend(button, live);`,
		`const off = gap(tl) > 48;`,
		`if (!off) unread = 0;`,
		`button.toggleAttribute("data-away", off);`,
		`const news = () => `+"`${unread} new ${unread === 1 ? \"message\" : \"messages\"}`;",
		"`Scroll to latest, ${news()}` : \"Scroll to latest message\"",
		`badge.textContent = unread > 99 ? "99+" : unread;`,
		`spoken = setTimeout(() => (live.textContent = unread ? news() : ""), 1000);`,
		`frame ||= requestAnimationFrame(sync);`,
		`tl.addEventListener("scroll", queue, { passive: true });`,
		`addEventListener("scroll", queue, { passive: true });`,
		`addEventListener("resize", queue, { passive: true });`,
		`button.addEventListener("mousedown", (e) => e.preventDefault());`,
		`const behavior = reduced.matches ? "instant" : "smooth";`,
		`tl.scrollTo({ top: reversed(tl) ? 0 : tl.scrollHeight, behavior });`,
		`else page().scrollTo({ top: page().scrollHeight, behavior });`,
		`if (e.detail !== 0) return;`,
		`box.focus({ preventScroll: true });`,
		`tl.focus({ preventScroll: true });`,
		`if (d.matches("[data-role=supervisor]")) fresh++;`,
		`if (name === "timeline" && !pinned && fresh) tail?.bump(fresh);`,
		`if (name === "timeline") tail?.queue();`,
	)
	lacks(t, "app.js", js, `addEventListener("scroll", queue)`, "button.innerHTML", `setAttribute("style"`, "style.cssText")
}

func TestARefreshKeepsTheMessageBeingRead(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const read = name === "timeline" && !pinned ? reading(el) : null;`,
		`const readTop = read?.getBoundingClientRect().top;`,
		`const again = [...el.children].find((c) => c.dataset.no === read.dataset.no);`,
		`if (again) (fills.matches ? el : page()).scrollTop += again.getBoundingClientRect().top - readTop;`,
		`if (pinned) pin(el);`,
	)
}

func TestALinkedOlderMessageIsNotPinnedAway(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`const aimed = !!timeline?.contains(document.getElementById(location.hash.slice(1)));`,
		`if (timeline && current === "chat" && !aimed) pin(timeline);`,
		`let near = !aimed;`,
	)
}

func TestTheScrollToLatestButtonStyles(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "button", css,
		".sup>.hint.read-only{position:relative}",
		".latest{position:absolute;right:-1px;bottom:calc(100% + 13px);width:var(--control);padding:0;border-radius:999px;opacity:0;visibility:hidden;transform:translateY(8px);transition:opacity .15s,transform .15s,visibility 0s .15s}",
		".latest[data-away]{opacity:1;visibility:visible;transform:none;transition-delay:0s}",
		".latest>.badge{position:absolute;top:-6px;right:-4px;padding-inline:5px;--tone:var(--on-primary);--tone-bg:var(--primary)}",
		".latest>.badge[hidden]{display:none}",
	)
	contains(t, "reduced motion", block(t, css, "@media (prefers-reduced-motion:reduce){"), ".latest{transition:none}")
	contains(t, "forced colours", css, "@media (forced-colors:active){.latest{border-color:ButtonText;color:ButtonText}.latest>.badge{border-color:ButtonText}}")
	lacks(t, "button", css, ".latest{box-shadow")
}

func TestTheScrollToLatestButtonIsNotInTheMarkup(t *testing.T) {
	fx := newFixture(t)
	fx.supervisor(t, state.AttemptRunning, "gen-1")
	claudeLog(t, fx, []string{userRecord("go"), reply("done")})
	h := fx.handler()
	for _, path := range []string{"/", "/supervisor/log"} {
		if m := regexp.MustCompile(`class="latest"|Scroll to latest`).FindString(get(t, h, path)); m != "" {
			t.Fatalf("%s has %q without script", path, m)
		}
	}
}
