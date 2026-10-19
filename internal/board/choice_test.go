package board_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

const pinQuestion = "Pin luvus to which version?\n\nCI broke when luvus 0.15 changed its output; see t1.\n\n1. Pin 0.14.2 — reproducible builds; we bump it by hand (recommended)\n2. Track 0.14.x — patch fixes arrive by themselves\n3. Other (write your own)"

func asked(t *testing.T, fx *fixture, question string) state.Decision {
	t.Helper()
	d, err := fx.st.Ask(context.Background(), active(t, fx.st, "Pin luvus").ID, question)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func fieldset(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<fieldset class="options">`)
	if i < 0 {
		t.Fatalf("no choice list:\n%s", body)
	}
	return body[i : i+strings.Index(body[i:], "</fieldset>")]
}

func TestTheChoiceListIsOneComponentInNeedsAndOnThePage(t *testing.T) {
	fx := newFixture(t)
	asked(t, fx, pinQuestion)
	needs := region(get(t, fx.handler(), "/"), "queue")
	page := get(t, fx.handler(), "/decision/d1")
	contains(t, "page", page, `<form class="answer" method="post" action="/decision/d1/answer" data-fetch data-choose>`,
		`<legend class="sr">Options for d1</legend>`,
		`<input class="sr" type="radio" name="choice" value="1. Pin 0.14.2">`,
		`<span class="option-key" aria-hidden="true">1</span><span class="option-text"><span class="option-head"><span class="option-label">Pin 0.14.2</span><span class="badge" data-tone="pass">Recommended</span></span><span class="option-note">reproducible builds; we bump it by hand</span></span>`,
		`value="3. Other (write your own)" data-other>`,
		`<textarea name="answer" rows="1" placeholder="Add a note (optional)" aria-label="Note"></textarea>`,
		`<button class="primary">Send answer</button>`, `<p class="form-error" role="alert"></p>`)
	lacks(t, "page", page, "data-fill", "<ol>", "<li>1.", " required>", `class="open-page"`, "Open d1", "keys-hint", "<kbd>")
	contains(t, "needs", needs, `<form class="answer" method="post" action="/decision/d1/answer" data-fetch data-choose>`, `<a href="/decision/d1" target="_blank" rel="noopener">Open d1</a>`, `<a class="ref" `)
	lacks(t, "needs", needs, "data-fill", "<ol>", "keys-hint", "<kbd>")
	if a, b := fieldset(t, needs), fieldset(t, page); a != b {
		t.Fatalf("Needs and the page differ:\n%s\n%s", a, b)
	}
	fx.options.Controls = false
	contains(t, "read-only board", region(get(t, fx.handler(), "/"), "queue"), `data-choose>`, `name="choice" value="2. Track 0.14.x"`)
}

func TestADecisionWithoutOptionsKeepsThePlainAnswerBox(t *testing.T) {
	fx := newFixture(t)
	asked(t, fx, "Ship it?\n\nCI is green.\n- tests pass\n- reviews are clean\n1 not an option")
	for _, body := range []string{get(t, fx.handler(), "/decision/d1"), region(get(t, fx.handler(), "/"), "queue")} {
		contains(t, "plain form", body, `<form class="answer" method="post" action="/decision/d1/answer" data-fetch>`, `<textarea name="answer" rows="3" placeholder="Your answer" aria-label="Your answer" required></textarea>`, `<button class="primary">Send answer</button>`, "<li>tests pass</li>", "1 not an option")
		lacks(t, "plain form", body, "<fieldset", "data-choose", "keys-hint", `type="radio"`)
	}
}

func TestALongOptionKeepsItsMarkdownAndNeverInjectsMarkup(t *testing.T) {
	fx := newFixture(t)
	asked(t, fx, "Which?\n\n1. **Keep** the arm64 job in the nightly run and add one automatic retry with a fresh `module cache`, so a flaky linker run no longer turns the whole night red\n2. <script>alert(1)</script> — see [the log](https://example.com/log) first")
	opts := fieldset(t, get(t, fx.handler(), "/decision/d1"))
	contains(t, "options", opts, `<label class="option long">`, `<strong>Keep</strong> the arm64 job`, "<code>module cache</code>", `<label class="option">`, "&lt;script&gt;alert(1)&lt;/script&gt;")
	lacks(t, "options", opts, "<script>", `<a href="https://example.com/log"`)
}

func TestTheAnswerJoinsTheChoiceAndTheNote(t *testing.T) {
	fx := newFixture(t)
	for _, c := range []struct {
		form url.Values
		want string
	}{
		{url.Values{"choice": {"2. Track 0.14.x"}, "answer": {"use the patch channel"}}, "2. Track 0.14.x\n\nuse the patch channel"},
		{url.Values{"choice": {"1. Pin 0.14.2"}, "answer": {"  "}}, "1. Pin 0.14.2"},
		{url.Values{"choice": {" 3. Other (write your own) "}, "answer": {"\nvendor it\n"}}, "3. Other (write your own)\n\nvendor it"},
		{url.Values{"answer": {" yes "}}, "yes"},
	} {
		d := asked(t, fx, pinQuestion)
		if rec := fetchPost(fx.handler(), "/decision/"+state.DecisionRef(d.ID)+"/answer", c.form); rec.Code != http.StatusNoContent {
			t.Fatalf("answer %v = %d:\n%s", c.form, rec.Code, rec.Body.String())
		}
		got, err := fx.st.Decision(context.Background(), d.ID)
		if err != nil || got.Status != state.DecisionAnswered || got.Answer != c.want || got.AnsweredBy != "operator (board)" {
			t.Fatalf("answer %v stored %+v, %v; want %q", c.form, got, err, c.want)
		}
	}
}

func TestAnEmptyChoiceAndNoteAreRefused(t *testing.T) {
	fx := newFixture(t)
	d := asked(t, fx, pinQuestion)
	for _, form := range []url.Values{{}, {"choice": {"  "}, "answer": {" \n "}}} {
		rec := fetchPost(fx.handler(), "/decision/d1/answer", form)
		msg, _ := url.PathUnescape(rec.Header().Get("X-Hand-Error"))
		if rec.Code != http.StatusBadRequest || msg != "pick an option or write an answer" {
			t.Fatalf("empty answer %v = %d %q", form, rec.Code, msg)
		}
		contains(t, "in place", rec.Body.String(), `<form class="answer" method="post" action="/decision/d1/answer" data-fetch data-choose>`, `<p class="form-error" role="alert">Pick an option or write an answer.</p>`)
	}
	if got, _ := fx.st.Decision(context.Background(), d.ID); got.Status != state.DecisionOpen {
		t.Fatalf("a refused answer closed the decision: %+v", got)
	}
	plain := request(fx.handler(), "POST", "/decision/d1/answer", url.Values{"csrf": {token}}, true)
	contains(t, "no script", plain.Body.String(), `<p class="form-error" role="alert">Pick an option or write an answer.</p>`, "<fieldset")
	if plain.Code != http.StatusBadRequest {
		t.Fatalf("empty form post = %d", plain.Code)
	}
}

func TestAnOtherChoiceNeedsItsNote(t *testing.T) {
	fx := newFixture(t)
	d := asked(t, fx, pinQuestion)
	rec := fetchPost(fx.handler(), "/decision/d1/answer", url.Values{"choice": {"3. Other (write your own)"}, "answer": {"  "}})
	msg, _ := url.PathUnescape(rec.Header().Get("X-Hand-Error"))
	if rec.Code != http.StatusBadRequest || msg != "write your answer: that option asks for one" {
		t.Fatalf("Other without a note = %d %q", rec.Code, msg)
	}
	contains(t, "in place", rec.Body.String(), `<p class="form-error" role="alert">Write your answer: that option asks for one.</p>`, "<fieldset")
	if got, _ := fx.st.Decision(context.Background(), d.ID); got.Status != state.DecisionOpen {
		t.Fatalf("a refused answer closed the decision: %+v", got)
	}
	if rec := fetchPost(fx.handler(), "/decision/d1/answer", url.Values{"choice": {"2. Track 0.14.x"}}); rec.Code != http.StatusNoContent {
		t.Fatalf("a plain row needs no note: %d", rec.Code)
	}
	other := asked(t, fx, pinQuestion)
	if rec := fetchPost(fx.handler(), "/decision/"+state.DecisionRef(other.ID)+"/answer", url.Values{"choice": {"3. Other (write your own)"}, "answer": {"vendor it"}}); rec.Code != http.StatusNoContent {
		t.Fatalf("Other with a note = %d", rec.Code)
	}
}

func TestTheAnsweredStateMarksTheChosenRow(t *testing.T) {
	fx := newFixture(t)
	d := asked(t, fx, pinQuestion)
	ctx := context.Background()
	if _, err := fx.st.Answer(ctx, d.ID, "2. Track 0.14.x\n\nuse the patch channel", "operator (board)"); err != nil {
		t.Fatal(err)
	}
	page := get(t, fx.handler(), "/decision/d1")
	contains(t, "answered", page, `<ol class="options done">`,
		`<li class="option picked"><span class="option-key" aria-hidden="true">2</span><span class="option-text"><span class="option-head"><span class="option-label">Track 0.14.x</span><span class="badge" data-tone="accent">Your answer</span></span><span class="option-note">patch fixes arrive by themselves</span></span></li>`,
		`<li class="option"><span class="option-key" aria-hidden="true">1</span><span class="option-text"><span class="option-head"><span class="option-label">Pin 0.14.2</span><span class="badge" data-tone="pass">Recommended</span></span><span class="option-note">reproducible builds; we bump it by hand</span></span></li>`,
		`<article class="card from-you" data-role="operator"><header class="slug"><span class="who">you</span><span class="meta">note by operator (board)</span>`, `<div class="copy plain">use the patch channel</div>`, `data-state="passing"`)
	lacks(t, "answered", page, "<form", "<fieldset", `type="radio"`, "Answered by")
	if strings.Count(page, `class="option picked"`) != 1 {
		t.Fatalf("picked rows:\n%s", page)
	}

	only := asked(t, fx, pinQuestion)
	if _, err := fx.st.Answer(ctx, only.ID, "1. Pin 0.14.2", "operator (board)"); err != nil {
		t.Fatal(err)
	}
	page = get(t, fx.handler(), "/decision/d2")
	contains(t, "choice only", page, `<li class="option picked"><span class="option-key" aria-hidden="true">1</span>`, `<p class="meta answered-by">Answered by operator (board) · <time datetime="`)
	lacks(t, "choice only", page, `class="card from-you"`)
}

func TestAnAnswerThatNamesNoOptionShowsAsText(t *testing.T) {
	fx := newFixture(t)
	ctx := context.Background()
	for _, answer := range []string{"yes, pin it", "9. Nine\n\nnot an option here"} {
		d := asked(t, fx, pinQuestion)
		if _, err := fx.st.Answer(ctx, d.ID, answer, "operator"); err != nil {
			t.Fatal(err)
		}
		page := get(t, fx.handler(), "/decision/"+state.DecisionRef(d.ID))
		contains(t, "free answer", page, `<span class="meta">answered</span>`, `<div class="copy plain">`+answer+`</div>`)
		lacks(t, "free answer", page, "option picked", "Your answer")
	}
	plain := asked(t, fx, "Keep it?")
	if _, err := fx.st.Answer(ctx, plain.ID, "2. keep", "operator"); err != nil {
		t.Fatal(err)
	}
	contains(t, "no options", get(t, fx.handler(), "/decision/"+state.DecisionRef(plain.ID)), `<div class="copy plain">2. keep</div>`)
}

func TestADecisionAnsweredElsewhereSwapsInPlace(t *testing.T) {
	fx := newFixture(t)
	d := asked(t, fx, pinQuestion)
	if _, err := fx.st.Answer(context.Background(), d.ID, "2. Track 0.14.x", "operator (cli)"); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"choice": {"1. Pin 0.14.2"}}
	for name, rec := range map[string]*httptest.ResponseRecorder{"fetch": fetchPost(fx.handler(), "/decision/d1/answer", form), "no script": request(fx.handler(), "POST", "/decision/d1/answer", url.Values{"csrf": {token}, "choice": {"1. Pin 0.14.2"}}, true)} {
		if msg, _ := url.PathUnescape(rec.Header().Get("X-Hand-Error")); rec.Code != http.StatusConflict || msg != "d1 is already answered: 2. Track 0.14.x" {
			t.Fatalf("%s: %d %q", name, rec.Code, msg)
		}
		body := rec.Body.String()
		contains(t, name, body, `<section class="answered-block" aria-label="Answer">`, `<p class="form-error" role="alert">d1 is already answered.</p>`, `<li class="option picked"><span class="option-key" aria-hidden="true">2</span>`, "Answered by operator (cli)")
		lacks(t, name, body, `action="/decision/d1/answer"`, "<fieldset")
	}
	if _, err := fx.st.Answer(context.Background(), d.ID, "x", "operator"); err == nil {
		t.Fatal("the decision took a second answer")
	}
	w := asked(t, fx, "Keep it?\n\n1. Yes\n2. No")
	if _, err := fx.st.Withdraw(context.Background(), w.ID); err != nil {
		t.Fatal(err)
	}
	rec := fetchPost(fx.handler(), "/decision/d2/answer", url.Values{"choice": {"1. Yes"}})
	contains(t, "withdrawn", rec.Body.String(), `<p class="form-error" role="alert">d2 is already withdrawn.</p>`, "Withdrawn by the supervisor")
	if rec.Code != http.StatusConflict {
		t.Fatalf("withdrawn answer = %d", rec.Code)
	}
}

func TestTheChoiceListIsDrivenFromTheKeyboardAndKeepsItsState(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		`at.closest("form[data-choose]") ?? at.closest("details.wait[open]")?.querySelector("form[data-choose]")`, `regions.size === 0 ? document.querySelector("form[data-choose]")`,
		`e.key === "Enter" && e.target instanceof HTMLInputElement && e.target.name === "choice"`, "if (!e.repeat) form.requestSubmit();",
		`.querySelector(".option-key").textContent.toLowerCase() === e.key.toLowerCase()`, "radio.checked = true;", "radio.focus();",
		`if (radio.hasAttribute("data-other")) form.querySelector("textarea[name=answer]").focus();`,
		`e.detail > 0 && e.target instanceof HTMLInputElement && e.target.hasAttribute("data-other")`,
		`addEventListener("pageshow", () => {`, `const fields = "textarea, select, input:not([type=hidden]):not([type=radio])";`, `input[type=radio]:checked`, `if (picked.has(`, `otherwise(f);`,
		`(form.action.endsWith("/keys") || form.action.endsWith("/answer")) && form.closest("details.wait")?.id`, `data.get("choice")?.split(".")[0] || "answer"`,
		`complain(form, "Pick an option or write an answer.");`, `if (res.status === 409 && form.matches(".answer")) {`, `.querySelector(".answered-block")`, "form.replaceWith(next);", `document.querySelector(".thread-head .status")?.replaceWith(status);`, "local(next);",
		`const out = form.matches(".answer") && form.querySelector(".form-error");`)
	lacks(t, "app.js", js, "data-fill", "button[data-fill]")
}

func TestTheChoiceListRows(t *testing.T) {
	css := asset(t, "board.css")
	contains(t, "rows", css,
		".options{display:grid;min-width:0;margin:0;padding:0;border:1px solid var(--line);border-radius:var(--r-lg);",
		".option{position:relative;display:grid;grid-template-columns:24px minmax(0,1fr);column-gap:12px;align-items:start;min-height:48px;padding:12px 14px}",
		".option+.option{padding-top:11px;border-top:1px solid var(--line)}",
		".option-key{display:grid;place-items:center;width:24px;height:24px;border:1px solid var(--line-strong);border-radius:50%;",
		".option-head{display:flex;flex-wrap:wrap;align-items:flex-start;gap:4px 12px}", ".option-head>.badge{flex:none;margin-top:1px}",
		".option-label{flex:1 1 auto;min-width:0;color:var(--fg);font-size:14px;font-weight:600;line-height:22px;overflow-wrap:anywhere}", ".option.long .option-label{font-weight:400}",
		".option-note{color:var(--muted);font-size:14px;line-height:20px;overflow-wrap:anywhere}",
		".option:has(input:checked) .option-key,.options.done .picked .option-key{border-color:var(--accent);background:var(--accent);color:var(--bg)}",
		".option:has(input:focus-visible){outline:2px solid var(--accent);outline-offset:-2px}",
		".badge[data-tone=accent]{--tone:var(--accent);--tone-bg:var(--accent-bg)}",
		".answer[data-choose] textarea{min-height:var(--control);padding-block:5px;resize:none}",
		"@supports (text-box:trim-both cap alphabetic){.option-key{display:block;line-height:18px;text-align:center;text-box:trim-both cap alphabetic;padding-block:calc((24px - 2px - 1cap)/2)}}",
		"@media (pointer:coarse){.option{min-height:52px;padding-block:14px}.option+.option{padding-top:13px}")
	lacks(t, "chips", css, ".chips", ".chip{", "data-fill", ".keys-hint")
}
