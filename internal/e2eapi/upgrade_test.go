package e2eapi_test

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/state"
)

const (
	previous       = "v0.13.0"
	previousSchema = 16
	previousLuvus  = "0.14.3"
)

var (
	pidLine  = regexp.MustCompile(`^\d+ \S+ \S`)
	ready    = regexp.MustCompile(`(?m)^ready [0-9a-f]{64} \S+$`)
	backupRe = regexp.MustCompile(`^hand\.\d`)
)

type build struct {
	version, commit, sha string
	schema               int
}

type target struct {
	srv    *server
	commit string
}

type running struct {
	board, watcher       bool
	boardPid, watcherPid int
}

type scenario struct {
	b                       *box
	id, name                string
	from                    build
	to                      target
	running                 running
	report, repair, sidecar bool
	known                   bool
	before                  snap
	dbBackup, binBackup     string
}

type snap struct {
	tasks, decisions, attempts, reports [][]string
	counts                              string
	cursor                              int
}

func userVersion(t *testing.T, db string) int {
	t.Helper()
	f, err := os.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := f.ReadAt(head, 60); err != nil {
		t.Fatal(err)
	}
	return int(binary.BigEndian.Uint32(head))
}

func pidOf(text string) int {
	n, _ := strconv.Atoi(strings.Fields(text + " ")[0])
	return n
}

func released(t *testing.T) string {
	t.Helper()
	c := enabled(t)
	root := filepath.Join(c.tmp, "previous")
	dir := filepath.Join(root, "download", previous)
	if !exists(filepath.Join(dir, asset)) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		var got result
		for attempt := 1; attempt <= 3; attempt++ {
			got = run(t, "gh", []string{"release", "download", previous, "--repo", "atqamz/hand", "--clobber", "-p", asset, "-p", sums, "-D", dir}, opts{env: os.Environ(), timeout: 5 * time.Minute})
			if got.code == 0 {
				break
			}
			time.Sleep(time.Duration(attempt) * 5 * time.Second)
		}
		ok(t, got, "gh release download "+previous)
	}
	for _, script := range []string{"install.sh", "install.ps1"} {
		data, err := os.ReadFile(filepath.Join(c.dist, script))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, script), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var want string
	for _, l := range strings.Split(read(filepath.Join(dir, sums)), "\n") {
		if l = strings.TrimSpace(l); strings.HasSuffix(l, asset) {
			want = strings.Fields(l)[0]
		}
	}
	equal(t, want, sha(t, filepath.Join(dir, asset)), "the "+previous+" archive matches its published checksum")
	return root
}

func installOld(t *testing.T, b *box, previousRoot string) build {
	t.Helper()
	dir := filepath.Join(b.dir, "bin-old")
	srv := serve(t, previousRoot, "")
	ok(t, install(t, shells[len(shells)-1], srv.base(), dir, map[string]string{"HAND_INSTALL_VERSION": previous}, b), "the installer installs "+previous)
	srv.Close()
	check(t, exists(filepath.Join(dir, "hand"+exe)), previous+" is installed")
	b.bin = dir
	v := toon(ok(t, b.hand(t, "version"), "hand version").out).fields
	equal(t, []string{v["version"], v["channel"], v["schema"]}, []string{previous[1:], "stable", strconv.Itoa(previousSchema)}, "the installed build is "+previous)
	return build{version: previous[1:], commit: v["commit"], schema: previousSchema, sha: sha(t, filepath.Join(dir, "hand"+exe))}
}

func installCandidate(t *testing.T, b *box, srv *server) build {
	t.Helper()
	dir := filepath.Join(b.dir, "bin-candidate")
	ok(t, install(t, shells[len(shells)-1], srv.base(), dir, nil, b), "the installer installs the candidate")
	check(t, exists(filepath.Join(dir, "hand"+exe)), "the candidate is installed")
	b.bin = dir
	return build{version: version, commit: commit(t), schema: state.SchemaVersion, sha: sha(t, filepath.Join(dir, "hand"+exe))}
}

func snapshot(t *testing.T, b *box) snap {
	t.Helper()
	var s snap
	for _, a := range toon(ok(t, b.hand(t, "attempt", "list"), "hand attempt list").out).tables["attempts"] {
		s.attempts = append(s.attempts, []string{a["id"], a["task"], a["harness"], a["model"], a["status"]})
	}
	for _, r := range toon(ok(t, b.hand(t, "report", "list"), "hand report list").out).tables["reports"] {
		shown := toon(ok(t, b.hand(t, "report", "show", r["id"]), "hand report show "+r["id"]).out).fields
		s.reports = append(s.reports, []string{r["id"], r["attempt"], r["task"], r["status"], shown["body"]})
	}
	for _, task := range toon(ok(t, b.hand(t, "task", "list", "--status", "inbox,active,done,abandoned"), "hand task list").out).tables["tasks"] {
		shown := toon(ok(t, b.hand(t, "task", "show", task["id"]), "hand task show "+task["id"]).out)
		f := shown.fields
		plan := ""
		if f["plan"] != "none" {
			plan = toon(ok(t, b.hand(t, "plan", "show", task["id"]), "hand plan show "+task["id"]).out).fields["body"]
		}
		row := []string{task["id"], f["project"], f["title"], f["status"], f["goal"], f["plan"], plan, f["attempt"], f["report"]}
		for _, d := range shown.tables["decisions"] {
			row = append(row, d["id"]+"|"+d["status"]+"|"+d["question"])
		}
		s.tasks = append(s.tasks, row)
	}
	for _, d := range toon(ok(t, b.hand(t, "decision", "list", "--all"), "hand decision list").out).tables["decisions"] {
		shown := toon(ok(t, b.hand(t, "decision", "show", d["id"]), "hand decision show "+d["id"]).out).fields
		s.decisions = append(s.decisions, []string{d["id"], d["task"], d["status"], d["question"], shown["answer"], shown["answered_by"], shown["closed_at"]})
	}
	orient := toon(ok(t, b.hand(t, "orient"), "hand orient").out)
	s.counts = orient.fields["tasks"]
	s.cursor, _ = strconv.Atoi(orient.fields["cursor"])
	return s
}

func seed(t *testing.T, b *box, label string, withReport bool, pinned string) string {
	t.Helper()
	alpha := b.repo(t, "alpha")
	beta := b.repo(t, "beta")
	seeded := seedLuvus(t, b, pinned)
	msg := label + " hand init downloads the pinned Luvus"
	if seeded {
		msg = label + " hand init with the cached Luvus"
	}
	id := toon(initialize(t, b, msg).out).fields["id"]
	saveLuvus(t, b)
	ok(t, b.hand(t, "project", "add", "alpha", alpha), "project add alpha")
	ok(t, b.hand(t, "project", "add", "beta", beta), "project add beta")
	ok(t, b.hand(t, "task", "add", "--goal", "People land on the home page after signing in.", "alpha", "Fix the login redirect"), "task add t1")
	ok(t, b.hand(t, "task", "add", "--goal", "Let an operator download every task as CSV.", "beta", "Add a CSV export"), "task add t2")
	ok(t, b.hand(t, "task", "add", "--goal", "Describe the install steps.", "alpha", "Update the README"), "task add t3")
	ok(t, b.hand(t, "task", "start", "t2"), "task start t2")
	ok(t, b.hand(t, "plan", "set", "--body", "1. Add the endpoint.\n2. Add the button.\n", "t2"), "plan set t2")
	ok(t, b.handWith(t, handOpts{input: "Which delimiter should the CSV export use?\n\n1. Comma — what most tools expect\n2. Semicolon — what spreadsheet locales in Europe expect\n"}, "decision", "ask", "--file", "-", "t2"), "decision ask d1")
	ok(t, b.hand(t, "decision", "answer", "d1", "1. Comma — what most tools expect"), "decision answer d1")
	ok(t, b.handWith(t, handOpts{input: "Should the export include abandoned tasks?\n\n1. Yes\n2. No\n"}, "decision", "ask", "--file", "-", "t2"), "decision ask d2")
	ok(t, b.hand(t, "task", "start", "t3"), "task start t3")
	ok(t, b.hand(t, "task", "done", "t3"), "task done t3")
	if withReport {
		brief := filepath.Join(b.dir, "brief.md")
		if err := os.WriteFile(brief, []byte("Change the redirect so that people land on the home page after signing in.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ok(t, b.hand(t, "task", "start", "t1"), "task start t1")
		started := ok(t, b.handWith(t, handOpts{harness: stub(t, b), timeout: 3 * time.Minute}, "attempt", "start", "--harness", "claude", "--model", "sonnet", "--effort", "low", "--prompt-file", brief, "t1"), "attempt start with a stand-in harness")
		matches(t, started.out, `(?m)^attempt: a1$`, "the attempt is a1")
		ok(t, b.hand(t, "report", "add", "--attempt", "a1", "--status", "done", "--text", "Changed the redirect.\n\nDelivered: "+filepath.Join(b.fleet, "AGENTS.md")), "report add a1")
		ok(t, b.hand(t, "attempt", "stop", "a1"), "attempt stop a1")
	}
	return id
}

func watcher(t *testing.T, b *box) int {
	t.Helper()
	spawn(t, b, "watch", "--notify=false")
	pidFile := filepath.Join(b.fleet, "watch.pid")
	text := ""
	waitFor(t, "a watcher runs and records watch.pid", func() bool {
		text = read(pidFile)
		return pidLine.MatchString(text)
	})
	return pidOf(text)
}

func apiTasks(t *testing.T, f fleetBox) string {
	t.Helper()
	res := f.host().get(t, f.api("/tasks"))
	equal(t, res.status, 200, "the Board serves the tasks through the API")
	return res.body
}

func login(t *testing.T, f fleetBox) resp {
	t.Helper()
	c := newClient(f.origin, nil)
	first := c.get(t, "/"+f.id+"/?token="+f.token)
	for _, line := range first.header.Values("Set-Cookie") {
		if kv := strings.SplitN(strings.SplitN(line, ";", 2)[0], "=", 2); len(kv) == 2 {
			c.jar[kv[0]] = kv[1]
		}
	}
	return c.get(t, "/"+f.id+"/")
}

func backupInventory(t *testing.T, b *box) []string {
	t.Helper()
	out := []string{}
	for _, d := range []struct{ label, dir string }{{"machine", filepath.Join(b.root, "backups")}, {"fleet", filepath.Join(b.fleet, "backups")}} {
		entries, err := os.ReadDir(d.dir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, e := range entries {
			out = append(out, d.label+"/"+e.Name())
		}
	}
	slices.Sort(out)
	return out
}

func update(t *testing.T, s *scenario) {
	t.Helper()
	b, name, from, to, run := s.b, s.name, s.from, s.to, s.running
	bin := filepath.Join(b.bin, "hand"+exe)
	backups := filepath.Join(b.root, "backups")
	db := filepath.Join(b.fleet, "hand.db")
	equal(t, userVersion(t, db), from.schema, fmt.Sprintf("%s: the fleet's database is at schema %d", name, from.schema))

	inventory := backupInventory(t, b)
	base := map[string]string{"HAND_INSTALL_BASE": to.srv.base()}
	checked := toon(ok(t, b.handWith(t, handOpts{cwd: b.dir, extra: base}, "update", "--check"), name+": hand update --check").out)
	equal(t, checked.fields["status"], "checked", name+": --check reports what would change")
	check(t, !exists(backups), name+": --check backed nothing up")
	equal(t, backupInventory(t, b), inventory, name+": --check added no backup under the machine or the fleet")
	equal(t, sha(t, bin), from.sha, name+": --check changed no binary")
	check(t, !exists(b.file("update.json")), name+": --check wrote no journal")

	to.srv.take()
	s.known = run.board && run.watcher && from.schema < state.SchemaVersion
	res := b.handWith(t, handOpts{cwd: b.dir, extra: base, timeout: 5 * time.Minute}, "update")
	if s.known {
		fails(t, res, name+": hand update exits non-zero", 1)
	} else {
		ok(t, res, name+": hand update")
	}
	doc := toon(res.out)
	equal(t, doc.fields["status"], "updated", name+": the update says updated")
	equal(t, doc.fields["from"], from.version+" stable "+from.commit, name+": it started from the installed build")
	equal(t, doc.fields["to"], version+" stable "+to.commit, name+": it moved to the served build")
	equal(t, doc.fields["luvus"], luvusTested+" kept", name+": the Luvus pin stays")
	asked := to.srv.take()
	check(t, slices.ContainsFunc(asked, func(r string) bool { return strings.HasPrefix(r, "/latest/download/") }), name+": it fetched the stable channel")
	if s.sidecar {
		equal(t, doc.fields["iroh"], irohVersion+" installed", name+": the update installs the sidecar of the new build")
		check(t, slices.Contains(asked, "/latest/download/"+irohAsset), name+": it fetched the sidecar from the same release")
		ran := run0(t, b, filepath.Join(b.bin, "hand-iroh"+exe))
		matches(t, ran.out, `^hand-iroh `+quote(irohVersion)+` iroh `, name+": the installed sidecar reports the version hand needs")
	}
	if s.repair {
		check(t, !exists(filepath.Join(b.bin, "hand-iroh"+exe)), name+": 0.13.0's update installs no sidecar")
	}
	made := doc.lists["backups"]
	equal(t, len(made), 2, name+": it lists two backups")
	var dbBackup, binBackup string
	for _, p := range made {
		switch base := filepath.Base(p); {
		case strings.HasPrefix(base, "hand.db."):
			dbBackup = p
		case backupRe.MatchString(base):
			binBackup = p
		}
	}
	inFleet := dbBackup != "" && filepath.Base(filepath.Dir(dbBackup)) == "backups" && filepath.Base(filepath.Dir(filepath.Dir(dbBackup))) == filepath.Base(b.fleet)
	where := "the fleet's backups folder"
	located := inFleet
	if s.repair {
		where = "backups/<fleet id> of the machine, where 0.13.0 makes it"
		located = strings.Contains(dbBackup, filepath.Join("backups", s.id))
	}
	check(t, dbBackup != "" && exists(dbBackup) && located, name+": the hand.db backup exists under "+where)
	check(t, binBackup != "" && exists(binBackup) && filepath.Dir(binBackup) == backups, name+": the old-binary backup exists under backups")
	equal(t, userVersion(t, dbBackup), from.schema, fmt.Sprintf("%s: the hand.db backup keeps schema %d", name, from.schema))
	equal(t, sha(t, binBackup), from.sha, name+": the binary backup is the old binary")
	var fleets [][]string
	for _, f := range doc.tables["fleets"] {
		fleets = append(fleets, []string{f["name"], f["init"]})
	}
	equal(t, fleets, [][]string{{"fleet", "ok"}}, name+": hand init ran in the fleet")
	var units [][]string
	for _, u := range doc.tables["units"] {
		unit := strings.Fields(u["name"])
		if len(unit) == 0 {
			t.Fatalf("FAIL %s: a row of the update's units table has no name: %v", name, u)
		}
		units = append(units, []string{unit[0], u["action"], u["result"]})
	}
	var wanted [][]string
	if run.watcher {
		wanted = append(wanted, []string{"watch", "stop", "ok"})
	}
	if run.watcher && s.known {
		wanted = append(wanted, []string{"watch", "start", "ok"})
	}
	if run.board {
		wanted = append(wanted, []string{"board", "stop", "ok"}, []string{"board", "start", "ok"})
	}
	if run.watcher && !s.known {
		wanted = append(wanted, []string{"watch", "start", "ok"})
	}
	if s.known {
		check(t, len(units) > 0, name+": the update lists its units")
		equal(t, units[len(units)-1][:2], []string{"board", "start"}, name+": the step that failed is the Board's start")
		matches(t, units[len(units)-1][2], fmt.Sprintf(`^failed: invalid: state schema %d is newer than this hand \(%d\)$`, state.SchemaVersion, from.schema), name+": the old updater cannot restart the Board once the watcher migrated the database")
		equal(t, units[:len(units)-1], wanted[:len(wanted)-1], name+": every other step ran")
		t.Logf("known - %s: the Board did not restart: %s's update opens hand.db after the new watcher migrated it; run hand board start after the update", name, from.version)
	} else {
		equal(t, units, wanted, name+": the stop and start steps are the ones that were running")
		check(t, len(doc.lists["help"]) == 0 && !strings.Contains(res.out, "failed"), name+": no step failed and no help line")
	}
	check(t, !exists(b.file("update.json")), name+": the journal is gone after a clean update")
	if win {
		check(t, exists(bin+".old"), name+": Windows renamed the running executable to hand.exe.old")
	}

	v := toon(ok(t, b.hand(t, "version"), name+": hand version after the update").out).fields
	equal(t, []string{v["version"], v["channel"], v["commit"], v["schema"]}, []string{version, "stable", to.commit, schema}, name+": the installed binary is the served build")
	if win {
		cleaned(t, b.bin, func() { b.hand(t, "version") })
		want := []string{"hand.exe"}
		if s.sidecar {
			want = []string{"hand-iroh.exe", "hand.exe"}
		}
		equal(t, names(t, b.bin), want, name+": the next hand run removed hand.exe.old and hand-iroh.exe.old")
	}
	after := snapshot(t, b)
	equal(t, after.tasks, s.before.tasks, name+": every task, plan body, decision and answer is unchanged")
	equal(t, after.decisions, s.before.decisions, name+": the decision list is unchanged")
	equal(t, after.attempts, s.before.attempts, name+": every attempt is unchanged")
	equal(t, after.reports, s.before.reports, name+": every report is unchanged")
	equal(t, after.counts, s.before.counts, name+": the task counts are unchanged")
	check(t, after.cursor >= s.before.cursor, fmt.Sprintf("%s: the event cursor did not go back (%d to %d)", name, s.before.cursor, after.cursor))
	s.dbBackup, s.binBackup = dbBackup, binBackup
}

func run0(t *testing.T, b *box, bin string) result {
	t.Helper()
	return run(t, bin, []string{"--version"}, opts{env: b.env(nil, nil, "")})
}

func afterwards(t *testing.T, s *scenario) {
	t.Helper()
	b, name, to, run := s.b, s.name, s.to, s.running
	bin := filepath.Join(b.bin, "hand"+exe)
	restarted := ""
	if run.board && !s.known {
		restarted, _ = b.read("board.pid")
		matches(t, restarted, `(?m)^\d+ \S+ `+quote(bin)+`\s*$`, name+": the updater left a Board running on the updated binary")
		check(t, pidOf(restarted) != run.boardPid, name+": the updater restarted the Board as a new process")
		addr, _ := b.read("board.addr")
		addr = strings.TrimSpace(addr)
		waitFor(t, name+": the Board the updater restarted answers on "+addr, func() bool {
			conn, err := net.DialTimeout("tcp", addr, time.Second)
			if err != nil {
				return false
			}
			conn.Close()
			return true
		})
	}
	if s.sidecar {
		waitFor(t, name+": the sidecar is ready after the update", func() bool { text, _ := b.read("board.iroh"); return ready.MatchString(text) })
	}
	store := filepath.Join(b.fleet, "luvus", runtime.GOOS+"-"+goarch)
	var pinned struct{ Path string }
	if err := json.Unmarshal([]byte(read(filepath.Join(store, "pin.json"))), &pinned); err != nil {
		t.Fatal(err)
	}
	check(t, !filepath.IsAbs(pinned.Path) && exists(filepath.Join(store, pinned.Path)), name+": the fleet holds its own Luvus copy and pin.json names it relative to its folder")
	hasBackup := false
	for _, n := range names(t, filepath.Join(b.fleet, "backups")) {
		hasBackup = hasBackup || strings.HasPrefix(n, "hand.db.")
	}
	check(t, exists(filepath.Join(b.fleet, "cache", "models")) && hasBackup, name+": the fleet holds the update's hand.db backup and a cache folder")
	routing := filepath.Join(b.fleet, "routing.json")
	var policy map[string]any
	if err := json.Unmarshal([]byte(read(routing)), &policy); err != nil {
		t.Fatal(err)
	}
	if _, has := policy["profiles"]; has {
		window := b.hand(t, "doctor")
		fails(t, window, name+": hand doctor fails while routing.json still holds profiles", 1)
		fix := ""
		for _, r := range toon(window.out).tables["checks"] {
			if r["check"] == "routing.json" {
				fix = r["fix"]
			}
		}
		matches(t, fix, `"profiles" was removed; delete the key`, name+": the routing.json row names the removed key")
		delete(policy, "profiles")
		data, _ := json.Marshal(policy)
		if err := os.WriteFile(routing, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	doctor := ok(t, b.hand(t, "doctor"), name+": hand doctor exits 0 with no harness installed")
	rows := map[string]string{}
	var failing []map[string]string
	for _, c := range toon(doctor.out).tables["checks"] {
		rows[c["check"]] = c["state"]
		if c["state"] == "fail" {
			failing = append(failing, c)
		}
	}
	equal(t, len(failing), 0, name+": hand doctor fails nothing")
	equal(t, rows["harness"], "warn", name+": hand doctor warns that no harness is installed")
	for _, c := range []string{"hand.version", "schema", "fleet", "luvus pin", "memory"} {
		equal(t, rows[c], "ok", name+": hand doctor: "+c+" is ok")
	}
	for _, c := range []string{"machine-local", "restore points"} {
		equal(t, rows[c], "info", name+": hand doctor: "+c+" is information")
	}
	if !win {
		check(t, rows["modes"] == "ok" || rows["modes"] == "warn", name+": hand doctor: the mode check reports a state")
	}
	if s.sidecar {
		equal(t, rows["iroh"], "ok", name+": hand doctor: iroh is ok while the running sidecar is ready")
		equal(t, toon(doctor.out).fields["iroh_id"], strings.Repeat("ab", 32), name+": hand doctor prints the running sidecar's endpoint ID")
	} else {
		equal(t, rows["iroh"], "info", name+": hand doctor: iroh is information only while the Board runs without --iroh")
	}
	check(t, rows["desktop"] == "ok" || rows["desktop"] == "info", name+": hand doctor: the desktop row is ok or information only")
	orient := ok(t, b.hand(t, "orient"), name+": hand orient")
	check(t, len(orient.out) < 6000, name+": hand orient stays under its byte budget")

	f := startBoard(t, b)
	marker, _ := b.read("board.pid")
	matches(t, marker, `(?m)^\d+ \S+ `+quote(bin)+`\s*$`, name+": board.pid names the updated binary")
	if run.board && !s.known {
		check(t, pidOf(marker) == pidOf(restarted), name+": starting the Board again kept the updater's process")
	}
	body := apiTasks(t, f)
	matches(t, body, `Add a CSV export`, name+": the Board shows the migrated tasks through the API")
	status := f.host().get(t, f.api("/status"))
	equal(t, status.status, 200, name+": the Board serves the fleet status from the updated binary")
	if run.watcher {
		now := read(filepath.Join(b.fleet, "watch.pid"))
		matches(t, now, `(?m)^\d+ \S+ `+quote(bin)+`\s*$`, name+": watch.pid names the updated binary")
		check(t, pidOf(now) != run.watcherPid, name+": the watcher is a new process")
	}

	to.srv.take()
	inventory := backupInventory(t, b)
	base := map[string]string{"HAND_INSTALL_BASE": to.srv.base()}
	again := toon(ok(t, b.handWith(t, handOpts{cwd: b.dir, extra: base}, "update"), name+": a second hand update").out)
	equal(t, again.fields["status"], "up to date", name+": the second update says up to date")
	equal(t, len(again.tables["units"]), 0, name+": it touched neither the Board nor a watcher")
	after, _ := b.read("board.pid")
	equal(t, after, marker, name+": the Board kept running through the second update")
	n := 0
	for _, e := range names(t, filepath.Join(b.root, "backups")) {
		if backupRe.MatchString(e) {
			n++
		}
	}
	equal(t, n, 1, name+": it made no new backup")
	equal(t, backupInventory(t, b), inventory, name+": it added no backup under the machine or the fleet")
	if s.repair {
		equal(t, again.fields["iroh"], irohVersion+" installed", name+": the second update installs the sidecar 0.13.0's update left out")
		check(t, slices.Contains(to.srv.take(), "/latest/download/"+irohAsset), name+": it fetched the sidecar from the same release")
		ran := run0(t, b, filepath.Join(b.bin, "hand-iroh"+exe))
		matches(t, ran.out, `^hand-iroh `+quote(irohVersion)+` iroh `, name+": the repaired sidecar reports the version hand needs")
		equal(t, toon(ok(t, b.hand(t, "board", "stop"), name+": hand board stop before starting with --iroh").out).fields["board"], "stopped", name+": the Board stops")
		if err := os.WriteFile(b.file("board.iroh"), []byte("stopped\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		spawn(t, b, "board", "--iroh", "--addr", "127.0.0.1:0")
		waitFor(t, name+": hand board --iroh starts the repaired sidecar", func() bool { text, _ := b.read("board.iroh"); return ready.MatchString(text) })
	}
}

func restore(t *testing.T, s *scenario) {
	t.Helper()
	b, name, from := s.b, s.name, s.from
	bin := filepath.Join(b.bin, "hand"+exe)
	db := filepath.Join(b.fleet, "hand.db")
	equal(t, toon(ok(t, b.hand(t, "board", "stop"), name+": hand board stop").out).fields["board"], "stopped", name+": the Board stops")
	ok(t, b.hand(t, "watch", "stop"), name+": hand watch stop")

	if from.schema < state.SchemaVersion {
		refusing := filepath.Join(b.dir, "hand-"+from.version+exe)
		copyFile(t, s.binBackup, refusing)
		refused := run(t, refusing, []string{"orient"}, opts{dir: b.fleet, env: b.env(nil, []string{b.dir}, "")})
		fails(t, refused, name+": the old binary refuses the migrated database", 0)
		check(t, refused.code > 0, name+": it exits with a code, not on a timeout")
		matches(t, refused.out+refused.err, fmt.Sprintf(`newer than this hand \(%d\)`, from.schema), name+": it says the schema is newer than this hand")
		equal(t, userVersion(t, db), state.SchemaVersion, fmt.Sprintf("%s: the refusal changed nothing, and the database is at schema %d", name, state.SchemaVersion))
	} else {
		equal(t, userVersion(t, db), state.SchemaVersion, fmt.Sprintf("%s: the database is at schema %d", name, state.SchemaVersion))
	}

	for _, f := range []string{"hand.db-wal", "hand.db-shm"} {
		_ = os.Remove(filepath.Join(b.fleet, f))
	}
	copyFile(t, s.dbBackup, db)
	copyFile(t, s.binBackup, bin)
	v := toon(ok(t, b.hand(t, "version"), name+": hand version after the restore").out).fields
	equal(t, []string{v["version"], v["schema"]}, []string{from.version, strconv.Itoa(from.schema)}, name+": the restored binary is the previous one")
	equal(t, userVersion(t, db), from.schema, fmt.Sprintf("%s: the restored database is at schema %d", name, from.schema))
	restored := snapshot(t, b)
	equal(t, restored.tasks, s.before.tasks, name+": the restored fleet has every task, plan and decision")
	equal(t, restored.decisions, s.before.decisions, name+": the restored fleet has every decision with its answer")
	equal(t, restored.attempts, s.before.attempts, name+": the restored fleet has every attempt")
	equal(t, restored.reports, s.before.reports, name+": the restored fleet has every report")
	equal(t, restored.counts, s.before.counts, name+": the restored task counts match")
	equal(t, restored.cursor, s.before.cursor, name+": the restored event cursor matches")

	_ = os.Remove(b.file("board.iroh"))
	f := startBoard(t, b)
	page := login(t, f)
	equal(t, page.status, 200, name+": the previous Board serves the restored fleet")
	marker, _ := b.read("board.pid")
	addr, _ := b.read("board.addr")
	addr = strings.TrimSpace(addr)
	if p, err := os.FindProcess(pidOf(marker)); err == nil {
		_ = p.Kill()
	}
	waitFor(t, name+": the previous Board's port closes", func() bool {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return true
		}
		c.Close()
		return false
	})
	_ = os.Remove(b.file("board.pid"))
	_ = os.Remove(b.file("board.addr"))
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func play(t *testing.T, s *scenario) {
	t.Helper()
	b, name, run := s.b, s.name, s.running
	xdg := false
	for _, e := range b.env(nil, nil, "") {
		xdg = xdg || strings.HasPrefix(strings.ToUpper(e), "XDG_")
	}
	check(t, !xdg, name+": the environment carries no XDG variable, so hand update cannot reach the real user folders")
	s.before = snapshot(t, b)
	check(t, len(s.before.tasks) == 3 && len(s.before.decisions) == 2, name+": the fleet holds three tasks and two decisions")
	want := []int{0, 0}
	if s.report {
		want = []int{1, 1}
	}
	equal(t, []int{len(s.before.attempts), len(s.before.reports)}, want, name+": the fleet holds the attempts and reports it was seeded with")
	if run.board {
		if s.sidecar {
			if err := os.WriteFile(b.file("board.iroh"), []byte("stopped\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		startBoard(t, b)
		marker, _ := b.read("board.pid")
		s.running.boardPid = pidOf(marker)
		check(t, s.running.boardPid != 0, name+": a Board runs on the old binary")
	} else {
		_, has := b.read("board.pid")
		check(t, !has, name+": no Board runs")
	}
	if run.watcher {
		s.running.watcherPid = watcher(t, b)
	} else {
		check(t, !exists(filepath.Join(b.fleet, "watch.pid")), name+": no watcher runs")
	}
	update(t, s)
	afterwards(t, s)
	restore(t, s)
}

func TestUpgrade(t *testing.T) {
	c := enabled(t)
	candidate(t)
	previousRoot := released(t)
	current := serve(t, c.dist, "")
	next := serve(t, filepath.Join(c.dist, "next"), "")
	for _, sc := range []struct {
		slug    string
		running running
		report  bool
	}{{"stopped", running{}, true}, {"board running", running{board: true}, false}, {"board and watcher running", running{board: true, watcher: true}, false}} {
		t.Run(sc.slug, func(t *testing.T) {
			b := newBox(t, "upgrade-"+strings.ReplaceAll(sc.slug, " ", "-"))
			from := installOld(t, b, previousRoot)
			id := seed(t, b, previous, sc.report, previousLuvus)
			s := &scenario{b: b, id: id, report: sc.report, name: previous + " to the candidate, " + sc.slug, from: from, to: target{srv: current, commit: commit(t)}, running: sc.running, repair: true}
			play(t, s)
			stopAll(t, b)
		})
	}
	t.Run("candidate to next", func(t *testing.T) {
		b := newBox(t, "upgrade-candidate")
		from := installCandidate(t, b, current)
		id := seed(t, b, "the candidate", true, luvusTested)
		s := &scenario{b: b, id: id, report: true, name: "the candidate to the next build", from: from, to: target{srv: next, commit: nextCommit(t)}, running: running{board: true, watcher: true}, sidecar: true}
		play(t, s)
		stopAll(t, b)
	})
}
