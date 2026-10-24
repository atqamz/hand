package board_test

import (
	"strings"
	"testing"
)

func TestTheDraftIsKeptInLocalStorageByFleet(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		"const draftPrefix = `hand.draft.${base.slice(1)}`;",
		"const draftTtl = 30 * 24 * 3600 * 1000;",
		"const draftMax = 200000;",
		"localStorage.setItem(key, JSON.stringify({ text, at: Date.now() }));",
		"text && text.length <= draftMax",
		"localStorage.removeItem(key);\n\t\t} catch {\n\t\t\ttry {\n\t\t\t\tlocalStorage.removeItem(key);\n\t\t\t} catch {}\n\t\t}",
		"localStorage.getItem(key)",
		"Date.now() - d.at < draftTtl",
		"if (raw !== null && !d) localStorage.removeItem(key);",
		"`${draftPrefix}.${m[1]}`",
		`/\/decision\/([^/]+)\/answer$/`,
		`const draftFields = "#composer-text, form.answer textarea[name=answer]:not(dialog *)";`,
		`restore(document);`,
		`restore(el);`,
		`if (!key || f.value) continue;`,
		"f.setSelectionRange(text.length, text.length);",
		"saving = setTimeout(flush, 300);",
		`if (document.visibilityState === "hidden") flush();`,
		`addEventListener("pagehide", flush);`,
		`addEventListener("beforeunload", flush);`,
		`if (!key || f.value === seen.get(key)?.text`,
		`addEventListener("storage", (e) => {`,
		"e.storageArea !== localStorage || !e.key || !ours(e.key)",
		"const take = d ? !f.value || (f.value === s?.text && !s.mine) : !!f.value && f.value === was?.text;",
		"else if (!d && f.value) seen.set(e.key, { text: null, mine: true });",
		`"Draft restored"`,
		"`Draft saved ${clock(Date.now())}`",
		`const box = form.matches("#composer, .answer") && form.querySelector("textarea");`,
		"const typed = box?.value;",
		`if (form.id === "composer" && !edited) form.reset();`,
		"if (box && !edited) forget(box);")
	lacks(t, "app.js", js, "sessionStorage", "kept(")
	success, failure, _ := strings.Cut(js, "if (res.status === 409")
	if !strings.Contains(success, "if (box && !edited) forget(box);") || strings.Contains(failure, "forget(") {
		t.Fatal("the draft must clear only on a successful send or answer, never on a failed one")
	}
}

func TestAttachedFilesAreKeptAsReferencesBesideTheDraft(t *testing.T) {
	js := asset(t, "app.js")
	contains(t, "app.js", js,
		"const filesPrefix = `hand.files.${base.slice(1)}`;",
		"const filesKey = (key) => filesPrefix + key.slice(draftPrefix.length);",
		"const ours = (key) => [draftPrefix, filesPrefix].some((p) => key === p || key.startsWith(`${p}.`));",
		"stash(key, \"\");\n\t\tstash(filesKey(key), \"\");",
		"const listKey = filesKey(textKey);",
		"stored = parsed(localStorage.getItem(listKey))?.text.split(\"\\n\").filter((l) => line.test(l)) ?? [];",
		`{ method: "HEAD", credentials: "same-origin" }`,
		"return res?.status !== 404;",
		"no longer in inbox/` : \"Draft restored\");",
		"said.textContent = `${gone.map((l) => label(line.exec(l)[3])).join(\", \")} ${gone.length === 1 ? \"is\" : \"are\"} no longer in inbox/`;")
	contains(t, "keep", js[strings.Index(js, "const keep = () => {"):strings.Index(js, "const busy = (n) => {")],
		"const ok = stash(listKey, attached.join(\"\\n\"));",
		"if (ok) noteFor(textKey, `Draft saved ${clock(Date.now())}`);",
		"else if (attached.length || !text.value) noteFor(textKey, \"\");")
	lacks(t, "app.js", js, "heldKey", `"hand.draft.".length`, "|| text.value ? `Draft saved")
}

func TestTheComposerShowsADraftNote(t *testing.T) {
	contains(t, "fleet page", get(t, newFixture(t).handler(), "/"), `<span class="draft-note"></span>`)
	contains(t, "board.css", asset(t, "board.css"),
		"border-radius:var(--r-lg);background:var(--bg);position:relative}",
		".draft-note{position:absolute;top:-9px;right:14px;padding:0 6px;background:var(--bg);color:var(--muted);font-size:12px;line-height:18px;white-space:nowrap;pointer-events:none}",
		".draft-note:empty{display:none}")
}
