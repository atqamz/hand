package e2eapi_test

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func missing(h string) string { return h + " not found on PATH" }

func first(row map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := row[k]; ok {
			return v
		}
	}
	return ""
}

func column(rows []map[string]string, keys ...string) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, first(r, keys...))
	}
	return out
}

func checkRow(rows []map[string]string, name string) map[string]string {
	for _, r := range rows {
		if r["check"] == name {
			return r
		}
	}
	return nil
}

func TestFirstRun(t *testing.T) {
	enabled(t)
	candidate(t)
	srv := serve(t, enabled(t).dist, "")
	b := newBox(t, "first")
	var m made
	step := func(name string, fn func(*testing.T)) {
		t.Helper()
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}
	step("cli", func(t *testing.T) { m = cliChecks(t, b) })
	step("board", func(t *testing.T) { boardChecks(t, b, m) })
	step("companion", func(t *testing.T) { companionChecks(t, b, m) })
	step("update", func(t *testing.T) { updateChecks(t, b, srv) })
}

func cliChecks(t *testing.T, b *box) made {
	v := toon(ok(t, b.hand(t, "version"), "hand version").out).fields
	equal(t, []string{v["version"], v["channel"], v["commit"], v["schema"], v["luvus"], v["iroh"]}, []string{version, "stable", commit(t), schema, luvusTested, irohVersion}, "hand version reports the candidate build")

	m := fixture(t, b)
	init := m.init.fields
	matches(t, init["id"], `^[0-9a-f]{13}$`, "hand init prints a fleet id")
	equal(t, init["created"], "true", "hand init created the fleet")
	matches(t, init["luvus"], `pinned`, "hand init pinned a Luvus")
	matches(t, init["luvus"], quote(luvusTested), "the pinned Luvus is the tested version")
	for _, f := range []string{"hand.db", "AGENTS.md", "routing.json", filepath.Join("memory", "operator.md")} {
		check(t, exists(filepath.Join(b.fleet, f)), "hand init wrote "+f)
	}
	again := toon(ok(t, b.hand(t, "init"), "a second hand init").out).fields
	equal(t, []string{again["created"], again["id"]}, []string{"false", init["id"]}, "a second hand init finds the same fleet")

	fleets := toon(ok(t, b.hand(t, "fleet", "list"), "hand fleet list").out).tables["fleets"]
	equal(t, [][]string{{fleets[0]["id"], fleets[0]["name"], fleets[0]["state"]}}, [][]string{{init["id"], "fleet", "ok"}}, "the fleet is registered and found")

	projects := toon(ok(t, b.hand(t, "project", "list"), "hand project list").out).tables["projects"]
	equal(t, column(projects, "name", "project"), []string{"alpha", "beta"}, "both projects are listed")
	tasks := toon(ok(t, b.hand(t, "task", "list", "--status", "all"), "hand task list").out).tables["tasks"]
	got := [][2]string{}
	for _, r := range tasks {
		got = append(got, [2]string{r["id"], r["title"]})
	}
	slices.SortFunc(got, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	equal(t, got, [][2]string{{"t1", "Fix the login redirect"}, {"t2", "Add a CSV export"}, {"t3", "Update the README"}}, "the three tasks are listed")
	ok(t, b.hand(t, "task", "add", "--parent", "t1", "alpha", "Fix the login redirect on Safari"), "hand task add --parent t1")
	nested := toon(ok(t, b.hand(t, "task", "list", "--status", "all"), "hand task list after nesting").out).tables["tasks"]
	equal(t, column(nested, "id"), []string{"t1", "t4", "t2", "t3"}, "hand task list puts a child under its parent")
	equal(t, column(nested, "parent"), []string{"", "t1", "", ""}, "hand task list names the parent of the child")
	flat := toon(ok(t, b.hand(t, "task", "list", "--flat", "--status", "all"), "hand task list --flat").out).tables["tasks"]
	check(t, !slices.ContainsFunc(flat, func(r map[string]string) bool { _, has := r["parent"]; return has }), "hand task list --flat has no parent column")
	shown := toon(ok(t, b.hand(t, "task", "show", "t1"), "hand task show t1").out)
	equal(t, []any{shown.fields["parent"], column(shown.tables["children"], "id")}, []any{"none", []string{"t4"}}, "hand task show lists the children")
	equal(t, toon(ok(t, b.hand(t, "task", "show", "t4"), "hand task show t4").out).fields["parent"], "t1", "hand task show names the parent")

	orient := ok(t, b.hand(t, "orient"), "hand orient")
	check(t, len(orient.out) < 6000, "hand orient stays under its byte budget ("+strconv.Itoa(len(orient.out))+" bytes)")
	matches(t, orient.out, `(?m)^tasks: inbox=3 active=1 done=0 abandoned=0$`, "hand orient counts the tasks")
	matches(t, orient.out, `(?m)^fleet: fleet \(`, "hand orient names the fleet")

	track := toon(ok(t, b.hand(t, "attempt", "track"), "hand attempt track exits 0 with no attempt").out).tables["track"]
	equal(t, len(track), 0, "hand attempt track prints an empty track table")
	equal(t, read(filepath.Join(b.fleet, "routing.json")), "{}\n", "hand init wrote an empty routing.json")

	models := toon(ok(t, b.hand(t, "models"), "hand models exits 0 with no harness installed").out)
	listed := [][2]string{}
	want := [][2]string{}
	for _, l := range models.tables["listings"] {
		listed = append(listed, [2]string{l["harness"], l["state"]})
	}
	for _, h := range harnesses {
		want = append(want, [2]string{h, missing(h)})
	}
	equal(t, listed, want, "hand models gives each harness a reason, not an error")
	check(t, slices.ContainsFunc(models.tables["models"], func(r map[string]string) bool { return r["harness"] == "claude" && r["source"] == "built-in" }), "hand models lists the built-in Claude models")
	check(t, !slices.ContainsFunc(models.tables["models"], func(r map[string]string) bool { return r["source"] == "discovered" }), "hand models discovers no model without a harness")

	doctor := b.hand(t, "doctor")
	ok(t, doctor, "hand doctor exits 0 with no harness installed and an empty routing.json")
	dd := toon(doctor.out)
	checks := dd.tables["checks"]
	state := func(name string) string {
		if r := checkRow(checks, name); r != nil {
			return r["state"]
		}
		return "(absent)"
	}
	for _, name := range []string{"luvus pin", "hand.version", "schema", "fleet", "routing.json", "memory", "project alpha", "project beta"} {
		equal(t, state(name), "ok", "hand doctor: "+name+" is ok")
	}
	if !win {
		check(t, state("modes") == "ok" || state("modes") == "warn", "hand doctor: the mode check reports a state")
	}
	wantRuntime := "(absent)"
	if win {
		wantRuntime = "ok"
	}
	equal(t, state("visual c++ runtime"), wantRuntime, "hand doctor: the Visual C++ runtime row is Windows only")
	git := checkRow(checks, "git")
	wantGit := []string{"ok", ""}
	if win {
		wantGit = []string{"warn", "core.longpaths is off; run `git config --global core.longpaths true`"}
	}
	equal(t, []string{git["state"], git["fix"]}, wantGit, "hand doctor: git row")
	check(t, !slices.ContainsFunc(checks, func(r map[string]string) bool { return r["state"] == "fail" }), "hand doctor: no check fails")
	equal(t, state("harness"), "warn", "hand doctor: the harness row warns that none is installed")
	equal(t, dd.fields["server"], "not running", "hand doctor: the Luvus server is not running yet")
	equal(t, state("iroh"), "info", "hand doctor: iroh is information only while the Board runs without --iroh")
	matches(t, dd.fields["iroh"], `^`+quote(irohVersion)+` `, "hand doctor: the installed sidecar and its version are listed")
	equal(t, state("skills"), "ok", "hand doctor: skills is ok with no managed skill")
	check(t, state("desktop") == "ok" || state("desktop") == "info", "hand doctor: the desktop row is ok or information only")
	return m
}

func assertHeaders(t *testing.T, r resp, msg string) {
	t.Helper()
	matches(t, r.header.Get("Content-Type"), `^application/json`, msg+": JSON")
	equal(t, r.header.Get("Cache-Control"), "no-store", msg+": no-store")
	matches(t, r.header.Get("Content-Security-Policy"), `default-src 'none'`, msg+": the strict CSP")
	equal(t, r.header.Get("X-Frame-Options"), "DENY", msg+": X-Frame-Options")
	check(t, r.header.Get("Referrer-Policy") != "", msg+": Referrer-Policy")
}

func dataOf(t *testing.T, r resp, msg string) any {
	t.Helper()
	if r.status != 200 {
		t.Fatalf("FAIL %s: status %d\n%s", msg, r.status, shorten(r.body))
	}
	t.Logf("ok - %s", msg)
	return at(parse(t, r.body), "data")
}

func refused(t *testing.T, r resp, status int, code, msg string) {
	t.Helper()
	gotStatus, gotCode := errorOf(t, r)
	equal(t, []any{gotStatus, gotCode, r.status}, []any{status, code, status}, msg)
}

func boardChecks(t *testing.T, b *box, m made) {
	fb := startBoard(t, b)
	id := fb.id
	equal(t, id, m.id, "the Board serves the fleet hand init named")
	equal(t, toon(b.hand(t, "open", "--print").out).fields["link"], "handboard://view/"+id+"/", "hand open --print prints the Desktop link, which holds no token")
	check(t, regexp.MustCompile(`^[0-9a-f]{48}$`).MatchString(fb.token), "the fleet token is 48 hex characters")
	addr, _ := b.read("board.addr")
	matches(t, strings.TrimSpace(addr), `^127\.0\.0\.1:[0-9]+$`, "board.addr names a loopback address")

	anon := newClient(fb.origin, nil)
	refused(t, anon.get(t, fb.api("/status")), 401, "unauthorized", "/api/v1/status refuses a request without a session")
	check(t, !strings.Contains(anon.get(t, fb.api("/tasks")).body, "Fix the login redirect"), "the refusal shows nothing of the fleet")
	wrong := newClient(fb.origin, nil)
	wrong.jar["hand_board_"+id] = strings.Repeat("0", 48)
	refused(t, wrong.get(t, fb.api("/status")), 401, "unauthorized", "a wrong session cookie is refused")

	nonce := "e2e-nonce-" + id
	proof := anon.get(t, "/"+id+"/proof?nonce="+nonce)
	equal(t, proof.status, 200, "the proof route answers without a cookie")
	check(t, strings.TrimSpace(proof.body) == hmacHex(fb.token, nonce), "the proof is the HMAC of the nonce keyed by the fleet token")

	host := fb.host()
	st := host.get(t, fb.api("/status"))
	data := dataOf(t, st, "/api/v1/status answers with the board session")
	assertHeaders(t, st, "/api/v1/status")
	env := parse(t, st.body)
	equal(t, []any{env["version"], at(env, "fleet.id")}, []any{float64(1), id}, "the API envelope names v1 and the fleet")
	cursor := str(env["cursor"])
	check(t, cursor != "" && cursor == st.header.Get("X-Hand-Cursor"), "the API response carries the same cursor header")
	check(t, at(data, "supervisor") != nil || at(data, "pill") != nil, "status carries the supervisor's state")
	not := host.get(t, fb.api("/status?since="+cursor))
	equal(t, []any{not.status, not.body, not.header.Get("X-Hand-Cursor")}, []any{304, "", cursor}, "the same cursor is not modified")
	refused(t, host.get(t, fb.api("/status?since=junk")), 400, "bad_cursor", "a cursor the board did not issue is refused")

	dataOf(t, host.get(t, fb.api("/tasks?all=1")), "/api/v1/tasks")
	text := host.get(t, fb.api("/tasks?all=1")).body
	for _, title := range []string{"Fix the login redirect", "Add a CSV export", "Update the README"} {
		check(t, strings.Contains(text, title), "the task list holds "+strconv.Quote(title))
	}
	projects := host.get(t, fb.api("/projects")).body
	check(t, strings.Contains(projects, `"alpha"`) && strings.Contains(projects, `"beta"`), "the project list holds both projects")
	task := dataOf(t, host.get(t, fb.api("/task/t2")), "/api/v1/task/t2")
	equal(t, str(at(task, "task.title")), "Add a CSV export", "the task detail names the task")
	check(t, strings.Contains(str(at(task, "plan")), "Add the endpoint"), "the task detail holds the plan")
	check(t, strings.Contains(host.get(t, fb.api("/task/t2")).body, "docs/export-notes.md"), "the task detail links the delivered file")
	decision := dataOf(t, host.get(t, fb.api("/decision/d1")), "/api/v1/decision/d1")
	check(t, strings.Contains(str(at(decision, "decision.question")), "Which delimiter should the CSV export use?"), "the decision detail holds the question")
	equal(t, len(list(at(decision, "options"))), 3, "the decision detail lists three options")
	refused(t, host.get(t, fb.api("/task/t999")), 404, "not_found", "an unknown task is 404")
	refused(t, host.get(t, fb.api("/nowhere")), 404, "not_found", "an unknown route is 404")

	session := dataOf(t, host.get(t, fb.api("/session")), "/api/v1/session")
	csrf := str(at(session, "csrf"))
	check(t, csrf == fb.token, "a host session's form token is the fleet token")
	refused(t, host.post(t, fb.api("/decision/d1/answer"), `{"choice":"1. Comma — what most tools expect"}`, ""), 403, "csrf_required", "an action without the form token is refused")
	refused(t, host.post(t, fb.api("/decision/d1/answer"), `{"choice":"x"}`, strings.Repeat("0", 48)), 403, "csrf_required", "an action with a wrong form token is refused")
	refused(t, host.post(t, fb.api("/status"), `{}`, csrf), 405, "method_not_allowed", "a POST outside the action paths is 405")
	equal(t, host.do(t, http.MethodDelete, fb.api("/status"), reqOpts{}).status, 405, "DELETE is 405")
	refused(t, anon.post(t, fb.api("/decision/d1/answer"), `{"choice":"x"}`, csrf), 401, "unauthorized", "an action without the session cookie is 401")
	answered := dataOf(t, host.post(t, fb.api("/decision/d1/answer"), `{"choice":"1. Comma — what most tools expect","note":"e2e"}`, csrf), "the host answers decision d1")
	equal(t, str(at(answered, "decision.answered_by")), "operator (desktop)", "the answer names the Desktop operator")
	refused(t, host.post(t, fb.api("/decision/d1/answer"), `{"choice":"1. Comma — what most tools expect"}`, csrf), 409, "conflict", "a second answer is a conflict")
	equal(t, toon(ok(t, b.hand(t, "decision", "show", "d1"), "hand decision show d1").out).fields["status"], "answered", "the CLI reads the API's answer")

	metaData := dataOf(t, host.get(t, "/api/v1/meta"), "/api/v1/meta answers with the board session")
	refused(t, anon.get(t, "/api/v1/meta"), 401, "unauthorized", "/api/v1/meta refuses a request without a session")
	check(t, str(at(metaData, "version")) != "" && len(list(at(metaData, "features"))) > 0, "meta names the version and the features")
	dataOf(t, host.get(t, "/api/v1/fleets"), "/api/v1/fleets answers with the board session")
	check(t, strings.Contains(host.get(t, "/api/v1/fleets").body, `"`+id+`"`), "the fleet list holds the fleet")
	check(t, !strings.Contains(anon.get(t, "/api/v1/fleets").body, id), "the fleet list names no fleet without a session")

	code, _, seen := host.stream(t, fb.api("/events"), 40*time.Second, func(s string) bool { return strings.Contains(s, "event: beat") })
	equal(t, code, 200, "/api/v1/events answers 200")
	matches(t, seen, `(?m)^event: hello$`, "the stream sends hello first")
	matches(t, seen, `(?m)^event: status$`, "the stream sends the status")
	matches(t, seen, `(?m)^event: timeline$`, "the stream sends the timeline")
	matches(t, seen, `(?m)^event: beat$`, "the stream sends a heartbeat when nothing else changed")
	refused(t, anon.get(t, fb.api("/events")), 401, "unauthorized", "the stream refuses a request without a session")

	file := dataOf(t, host.get(t, fb.api("/doc/docs/export-notes.md")), "the document endpoint serves a file inside the fleet folder")
	equal(t, []any{str(at(file, "kind")), str(at(file, "name"))}, []any{"markdown", "export-notes.md"}, "the document is Markdown")
	check(t, strings.HasPrefix(host.get(t, fb.api("/doc/docs/export-notes.md?raw")).body, "# Export notes"), "the raw file is served too")
	raw := host.get(t, fb.api("/doc/docs/export-notes.md?raw"))
	equal(t, raw.header.Get("X-Content-Type-Options"), "nosniff", "the raw file is served with nosniff")
	matches(t, raw.header.Get("Content-Security-Policy"), `default-src 'none'`, "the raw file is served with a locked policy")
	folder := dataOf(t, host.get(t, fb.api("/doc/docs")), "the document endpoint lists a folder")
	entries := []string{}
	for _, e := range list(at(folder, "entries")) {
		entries = append(entries, str(at(e, "name")))
	}
	equal(t, entries, []string{"nested", "export-notes.md"}, "the folder lists its entries, folders first")
	lines := dataOf(t, host.get(t, fb.api("/doc/docs/nested/log.txt?from=2&lines=1")), "the document endpoint reads a text file by line")
	equal(t, []any{list(at(lines, "lines")), at(lines, "next")}, []any{[]any{"line two"}, nil}, "the window holds the second line and reaches the end")
	for _, hidden := range []string{"hand.db", "board.token", "AGENTS.md", "luvus/note.txt", "locks/note.txt", "projects/note.txt", "backups/note.txt", "cache/note.txt", ".claude"} {
		r := host.get(t, fb.api("/doc/"+hidden))
		gotStatus, gotCode := errorOf(t, r)
		check(t, gotStatus == 403 && gotCode == "forbidden" && strings.Contains(r.body, "does not show Hand") && !strings.Contains(r.body, "SQLite format") && !strings.Contains(r.body, fb.token) && !strings.Contains(r.body, "inside "), "the document endpoint refuses "+hidden+" with the reason")
	}
	for _, climb := range []string{"/doc/docs/../hand.db", "/doc/docs/%2e%2e/hand.db", "/doc/docs/..%2fhand.db"} {
		r := host.get(t, fb.api(climb))
		check(t, r.status != 200 && !strings.Contains(r.body, "SQLite format"), "the document endpoint does not serve hand.db through "+climb+" ("+strconv.Itoa(r.status)+")")
	}
	refused(t, anon.get(t, fb.api("/doc/docs/export-notes.md")), 401, "unauthorized", "the document endpoint refuses a request without a session")

	equal(t, toon(b.hand(t, "open", "--print", "--doc", "docs/export-notes.md").out).fields["link"], "handboard://view/"+id+"/doc/docs/export-notes.md", "hand open --print --doc links the Desktop document viewer")
	for _, no := range []string{"../hand.db", "/etc/hosts", "hand.db", "luvus/note.txt", "locks/note.txt", "projects/note.txt", "backups/note.txt", "cache/note.txt", ".claude"} {
		fails(t, b.hand(t, "open", "--print", "--doc", no), "hand open --doc refuses "+no, 2)
	}

	funnel := newClient(fb.origin, map[string]string{"Tailscale-Funnel-Request": "?1"})
	funnel.jar["hand_board_"+id] = fb.token
	refused(t, funnel.get(t, fb.api("/status")), 403, "forbidden", "a Tailscale Funnel request is refused, whatever its cookie")
	proxied := newClient(fb.origin, map[string]string{"X-Forwarded-For": "100.64.0.2", "X-Forwarded-Proto": "https"})
	proxied.jar["hand_board_"+id] = fb.token
	refused(t, proxied.get(t, fb.api("/status")), 403, "forbidden", "a proxied request is refused, whatever its cookie")
	refused(t, proxied.get(t, "/api/v1/fleets"), 403, "forbidden", "a proxied request is refused the fleet list")

	phone := dataOf(t, host.get(t, fb.api("/phone")), "the host reads the Companion state")
	check(t, at(phone, "iroh") == nil, "the Companion state has no iroh section while the Board runs without --iroh")
	equal(t, len(list(at(phone, "devices"))), 0, "no Companion is paired yet")
	refused(t, host.post(t, fb.api("/phone/app"), ``, csrf), 409, "conflict", "no app link is made while the sidecar is not ready")
	refused(t, host.post(t, fb.api("/phone/code"), `{"base":"http://127.0.0.1/path"}`, csrf), 400, "bad_request", "a pairing base with a path is refused")
	minted := dataOf(t, host.post(t, fb.api("/phone/code"), `{"base":"`+fb.origin+`"}`, csrf), "the host mints a pairing code")
	matches(t, str(at(minted, "code.code")), `^[A-Z0-9]{5}-[A-Z0-9]{5}$`, "the code has the pairing shape")
	check(t, strings.Contains(str(at(minted, "code.link")), "/"+id+"/pair#"), "the pairing link holds the code after the #")
	refused(t, host.post(t, fb.api("/pair"), `{"code":"`+str(at(minted, "code.code"))+`","name":"loopback"}`, ""), 403, "forbidden", "a pairing request on the loopback listener is refused and spends no code")
	refused(t, anon.post(t, fb.api("/phone/code"), `{"base":"`+fb.origin+`"}`, csrf), 401, "unauthorized", "a pairing code is not minted without the session")

	doctor := toon(b.hand(t, "doctor").out).tables["checks"]
	equal(t, checkRow(doctor, "board")["state"], "ok", "hand doctor: the board answers")
	matches(t, read(b.file("board.pid")), `(?m)^\d+ \S+ `+quote(filepath.Join(b.bin, "hand"+exe))+`\s*$`, "board.pid names the installed hand")
	equal(t, toon(ok(t, b.hand(t, "watch", "stop"), "hand watch stop").out).fields["watcher"], "not running", "no watcher runs without a supervisor")
	equal(t, toon(ok(t, b.hand(t, "board", "stop"), "hand board stop").out).fields["board"], "stopped", "hand board stop stops the board")
	check(t, !exists(b.file("board.pid")), "board.pid is gone")
	_, err := (&http.Client{Timeout: 3 * time.Second}).Get(fb.origin + "/api/v1/meta")
	check(t, err != nil, "the board no longer answers")
	equal(t, toon(ok(t, b.hand(t, "board", "stop"), "a second hand board stop").out).fields["board"], "not running", "a second stop says not running")
}

func companionChecks(t *testing.T, b *box, m made) {
	id := m.id
	endpoint := strings.Repeat("ab", 32)
	other := strings.Repeat("cd", 32)
	write(t, b.file("board.iroh"), "stopped\n")
	fb := startBoard(t, b)
	waitFor(t, "a Board restarted with the recorded setting starts the sidecar", func() bool {
		return regexp.MustCompile(`(?m)^ready [0-9a-f]{64} https://relay\.example\.test\./$`).MatchString(read(b.file("board.iroh")))
	})
	doctor := toon(ok(t, b.hand(t, "doctor"), "hand doctor with the sidecar running").out)
	equal(t, checkRow(doctor.tables["checks"], "iroh")["state"], "ok", "hand doctor: iroh is ok while the sidecar is ready")
	equal(t, doctor.fields["iroh_id"], endpoint, "hand doctor prints the endpoint ID")

	host := fb.host()
	csrf := str(at(dataOf(t, host.get(t, fb.api("/session")), "the host's session"), "csrf"))
	meta := dataOf(t, host.get(t, "/api/v1/meta"), "/api/v1/meta with the sidecar running")
	check(t, strings.Contains(host.get(t, "/api/v1/meta").body, endpoint) || at(meta, "iroh") != nil, "meta reports the sidecar")
	phone := dataOf(t, host.get(t, fb.api("/phone")), "the host reads the Companion state")
	equal(t, []any{str(at(phone, "iroh.state")), str(at(phone, "iroh.id"))}, []any{"ready", endpoint}, "the Companion state shows the sidecar ready, with its endpoint ID")
	app := dataOf(t, host.post(t, fb.api("/phone/app"), ``, csrf), "the host asks for a Hand app link")
	matches(t, str(at(app, "app.link")), `^handboard://c#v=1&e=`+endpoint+`&f=`+id+`&c=[0-9A-Z]{10}&r=https%3A%2F%2Frelay\.example\.test\.%2F`, "the Hand app link is canonical, with this fleet and the relay")

	args := strings.Split(read(b.file("iroh", "stub.args")), "\n")
	flags := []string{}
	for i := 0; i < 6 && i < len(args); i += 2 {
		flags = append(flags, args[i])
	}
	equal(t, flags, []string{"--board", "--secret-file", "--key-dir"}, "the sidecar was started with the board address, the secret file and the key folder")
	equal(t, []any{filepath.Base(args[3]), filepath.Base(args[5]), filepath.Dir(args[3]) == filepath.Dir(args[5])}, []any{"board.peer", "iroh", true}, "the secret file and the key folder sit together in the Hand home")
	matches(t, args[1], `^127\.0\.0\.1:[0-9]+$`, "the sidecar is pointed at a loopback peer listener")
	secret := strings.TrimSpace(read(b.file("board.peer")))
	check(t, secret != "" && !strings.Contains(strings.Join(args, "\n"), secret), "the secret is in neither the sidecar arguments nor its environment")
	check(t, func() bool {
		for _, l := range args[6:] {
			if l != "" && !regexp.MustCompile(`(?i)^SystemRoot=`).MatchString(l) {
				return false
			}
		}
		return true
	}(), "the sidecar runs with an empty environment")

	peer := func(endpoint string) *client { return peerClient("http://"+args[1], args[1], endpoint, secret) }
	mint := func() string {
		d := dataOf(t, host.post(t, fb.api("/phone/code"), `{"base":"`+fb.origin+`"}`, csrf), "the host mints a pairing code")
		return str(at(d, "code.code"))
	}
	pair := func(c *client, code, name string) resp {
		body, _ := jsonString(obj{"code": code, "name": name})
		return c.post(t, fb.api("/pair"), body, "")
	}
	code := mint()
	phoneA := peer(endpoint)
	refused(t, pair(phoneA, "AAAAA-AAAAA", "E2E phone"), 403, "wrong_code", "a wrong code is refused")
	refused(t, phoneA.post(t, fb.api("/pair"), `{"code":"`+code+`"}`, ""), 400, "bad_request", "a pairing body with no name is bad_request")
	refused(t, pair(phoneA, code, "bad\nname"), 400, "bad_name", "a name with a control character is refused before the code is spent")
	equal(t, phoneA.get(t, fb.api("/status")).status, 403, "an unpaired Companion is refused by the peer listener")
	paired := pair(phoneA, strings.ToLower(code), "E2E phone")
	pd := dataOf(t, paired, "the right code pairs the Companion")
	cookie := str(at(pd, "fleets.0.cookie_name"))
	value := str(at(pd, "fleets.0.cookie_value"))
	equal(t, cookie, "hand_phone_"+id, "the Companion gets the fleet's phone cookie name")
	check(t, len(value) > 20 && value != fb.token && !strings.Contains(paired.body, fb.token), "the Companion gets a device secret, never the fleet token")
	refused(t, pair(peer(other), code, "second phone"), 403, "code_used", "a code works once")
	phoneA.jar[cookie] = value

	psess := dataOf(t, phoneA.get(t, fb.api("/session")), "the paired Companion reads its session")
	pcsrf := str(at(psess, "csrf"))
	check(t, pcsrf != "" && pcsrf != fb.token, "the Companion's form token is its own")
	status := phoneA.get(t, fb.api("/status"))
	equal(t, status.status, 200, "the paired Companion reads the status")
	assertHeaders(t, status, "a Companion's status")
	alerts := dataOf(t, phoneA.get(t, fb.api("/alerts")), "the paired Companion reads the alerts")
	check(t, at(alerts, "stats.total") != nil, "the alerts answer carries its stats")
	equal(t, phoneA.get(t, fb.api("/tasks")).status, 200, "the paired Companion reads the tasks")
	refused(t, phoneA.get(t, fb.api("/phone")), 403, "forbidden", "a paired Companion cannot read the Companion state")
	refused(t, phoneA.post(t, fb.api("/phone/code"), `{"base":"`+fb.origin+`"}`, pcsrf), 403, "forbidden", "a paired Companion cannot mint a code")
	refused(t, phoneA.post(t, fb.api("/session/revoke"), ``, ""), 403, "csrf_required", "signing out needs the Companion's form token")
	refused(t, host.post(t, fb.api("/session/revoke"), ``, csrf), 403, "forbidden", "the host session cannot sign itself out")
	onLoopback := newClient(fb.origin, nil)
	onLoopback.jar[cookie] = value
	check(t, onLoopback.get(t, fb.api("/status")).status != 200, "a Companion's cookie does not work on the loopback listener")
	check(t, peer(endpoint).get(t, fb.api("/status")).status != 200, "a Companion's cookie is not kept by the peer connection")
	wrongEndpoint := peer(other)
	wrongEndpoint.jar[cookie] = value
	check(t, wrongEndpoint.get(t, fb.api("/status")).status != 200, "a Companion's cookie does not work from another endpoint")

	listed := dataOf(t, host.get(t, fb.api("/phone")), "the host lists the paired Companion")
	devices := list(at(listed, "devices"))
	equal(t, len(devices), 1, "one Companion is paired")
	equal(t, []any{str(at(devices[0], "name")), str(at(devices[0], "login"))}, []any{"E2E phone", "iroh:" + endpoint}, "the host lists the Companion by name and its iroh login")
	label := str(at(devices[0], "phone"))
	matches(t, label, `^[0-9a-f]{16}$`, "the Companion has a label to revoke by")

	second := peer(other)
	secondPaired := dataOf(t, pair(second, mint(), "E2E tablet"), "a second Companion pairs with a second code")
	second.jar[cookie] = str(at(secondPaired, "fleets.0.cookie_value"))
	scsrf := str(at(dataOf(t, second.get(t, fb.api("/session")), "the second Companion reads its session"), "csrf"))
	equal(t, at(dataOf(t, second.post(t, fb.api("/session/revoke"), ``, scsrf), "the second Companion signs itself out"), "accepted"), true, "sign out answers accepted")
	gone := second.get(t, fb.api("/status"))
	check(t, gone.status == 403 && gone.header.Get("X-Hand-Error") != "", "a signed-out Companion is refused at once, with the reason in X-Hand-Error")
	equal(t, len(list(at(dataOf(t, host.get(t, fb.api("/phone")), "the host lists the Companions again"), "devices"))), 1, "the other Companion stays")
	equal(t, phoneA.get(t, fb.api("/status")).status, 200, "the other Companion still reads")

	refused(t, host.post(t, fb.api("/phone/revoke"), `{"phone":"`+label+`"}`, ""), 403, "csrf_required", "a revoke without the form token is refused")
	refused(t, host.post(t, fb.api("/phone/revoke"), `{}`, csrf), 400, "bad_request", "a revoke names a Companion")
	revoked := dataOf(t, host.post(t, fb.api("/phone/revoke"), `{"phone":"`+label+`"}`, csrf), "the host revokes the Companion")
	equal(t, len(list(at(revoked, "revoke.revoked"))), 1, "the revoke reached this fleet")
	away := phoneA.get(t, fb.api("/status"))
	check(t, away.status == 403 && away.header.Get("X-Hand-Error") != "", "the revoked Companion is refused at once, with the reason in X-Hand-Error")
	equal(t, phoneA.get(t, fb.api("/alerts")).status, 403, "the revoked Companion is refused the alerts")
	equal(t, host.get(t, fb.api("/status")).status, 200, "the host stays signed in after the revoke")
	equal(t, len(list(at(dataOf(t, host.get(t, fb.api("/phone")), "the host lists no Companion"), "devices"))), 0, "no Companion is left")

	_ = os.Remove(b.file("iroh", "stub.stopped"))
	equal(t, toon(ok(t, b.hand(t, "board", "stop"), "hand board stop with the sidecar running").out).fields["board"], "stopped", "hand board stop stops the board")
	waitFor(t, "the sidecar exits when the Board is gone: its input closed", func() bool { return exists(b.file("iroh", "stub.stopped")) })
	_ = os.Remove(b.file("board.iroh"))
}

func updateChecks(t *testing.T, b *box, srv *server) {
	res := ok(t, b.handWith(t, handOpts{extra: map[string]string{"HAND_INSTALL_BASE": srv.base()}}, "update", "--check"), "hand update --check against the local release server")
	doc := toon(res.out)
	equal(t, doc.fields["status"], "up to date", "hand update --check says up to date")
	build := version + " stable " + commit(t)
	equal(t, doc.fields["from"], build, "the running build is the candidate")
	equal(t, doc.fields["to"], build, "the served build is the same commit")
	equal(t, doc.fields["luvus"], luvusTested+" kept", "the Luvus pin stays")
	check(t, srv.saw(func(r string) bool { return strings.HasPrefix(r, "/latest/download/") }), "hand update fetched from the stable channel")
}
