package cli_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
)

var skillRoots = []string{".claude", ".agents", ".grok", ".pi"}

func skillGone(t *testing.T, base, name string) {
	t.Helper()
	for _, root := range skillRoots {
		if _, err := os.Stat(filepath.Dir(skillPath(base, root, name))); !os.IsNotExist(err) {
			t.Errorf("%s/%s is still there (%v)", root, name, err)
		}
	}
}

func skillThere(t *testing.T, base, name string) {
	t.Helper()
	for _, root := range skillRoots {
		if _, err := os.Stat(skillPath(base, root, name)); err != nil {
			t.Errorf("%s/%s: %v", root, name, err)
		}
	}
}

func subscribed(t *testing.T, base string) string {
	t.Helper()
	return readSkill(t, filepath.Join(base, "skills.json"))
}

func TestSkillsAPIAddsAnOwnSkillAndRecordsTheRow(t *testing.T) {
	f := newProposeFixture(t, "")
	a := newSkillsAPI(t, f.h)
	code, run, receipt, raw := a.post("add", map[string]string{"scope": "fleet", "source": "triage"})
	head := f.refs()["refs/heads/main"]
	if code != 200 || receipt != "Skills add: 1 added" || run.Scope != "fleet" || run.Refused != 0 || len(run.Results) != 1 {
		t.Fatalf("add = %d %q %+v %s", code, receipt, run, raw)
	}
	if r := resultOf(run, "triage"); r.State != "added" || r.Detail != head {
		t.Fatalf("row = %+v, want commit %s", r, head)
	}
	skillThere(t, f.h.home, "triage")
	if e := skillState(t, f.h.home)["triage"]; e.Commit != head || e.ComputedHash == "" {
		t.Fatalf("lock = %+v", e)
	}
	if !strings.Contains(subscribed(t, f.h.home), `"triage"`) {
		t.Fatalf("skills.json = %s", subscribed(t, f.h.home))
	}
	if s := a.skill("fleet", "", "triage"); !s.Managed || !s.Trusted || s.State != "current" || s.Ref != "main" {
		t.Fatalf("list = %+v", s)
	}
	code, _, _, raw = a.post("add", map[string]string{"scope": "fleet", "source": "triage"})
	if code != http.StatusConflict || !strings.Contains(raw, "already exists") {
		t.Fatalf("a second add = %d %s", code, raw)
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPIAddTakesARefOfTheOwnRepositoryAndRefusesAnOddOne(t *testing.T) {
	f := newProposeFixture(t, "")
	old := f.refs()["refs/heads/main"]
	skillGit(t, f.hub.bare, "update-ref", "refs/heads/old", old)
	moved := f.hub.commit(map[string]string{"skills/other/SKILL.md": skillFiles("other", "v2")["SKILL.md"]})
	a := newSkillsAPI(t, f.h)
	run := a.ok("add", map[string]string{"scope": "fleet", "source": "other", "ref": "old"})
	if r := resultOf(run, "other"); r.State != "added" || r.Detail != old || old == moved {
		t.Fatalf("add at a ref = %+v", run)
	}
	if got := readSkill(t, skillPath(f.h.home, ".claude", "other")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("copy = %q", got)
	}
	for _, ref := range []string{"refs/heads/main", "pull/1/head", "merge-requests/1/head", "a..b", "-x", "--upload-pack=x"} {
		if code, _, _, raw := a.post("add", map[string]string{"scope": "fleet", "source": "triage", "ref": ref}); code != http.StatusBadRequest {
			t.Errorf("ref %q = %d %s", ref, code, raw)
		}
	}
	skillGone(t, f.h.home, "triage")
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPIAddRefusesEverySourceButABareSkillNameBeforeAnyFetch(t *testing.T) {
	f := newProposeFixture(t, "")
	a := newSkillsAPI(t, f.h)
	for _, src := range []string{
		"https://pat:s3cret@github.com/acme/tool.git",
		"https://github.com/acme/tool?x=1",
		"https://github.com/acme/tool#frag",
		"https://github.com/acme/tool",
		"https://github.com/atqamz/skills",
		skillsURL,
		"http://github.com/acme/tool",
		"file://" + filepath.ToSlash(f.hub.bare),
		"git@github.com:acme/tool.git",
		"ssh://git@github.com/acme/tool.git",
		"ext::sh -c id",
		"./triage",
		"../triage",
		"/abs/triage",
		"skills/triage",
		`a\b`,
		"tri:age",
		"-triage",
		".hidden",
		"triage.git",
		"a b",
	} {
		code, _, _, raw := a.post("add", map[string]string{"scope": "fleet", "source": src})
		if code != http.StatusBadRequest || !strings.Contains(raw, "by its bare name") {
			t.Errorf("source %q = %d %s", src, code, raw)
		}
		if strings.Contains(raw, "s3cret") || strings.Contains(raw, "ext::") || strings.Contains(raw, "acme") {
			t.Errorf("source %q is echoed: %s", src, raw)
		}
	}
	if _, err := os.Stat(fleetLock(f.h.home)); !os.IsNotExist(err) {
		t.Fatalf("a refused source wrote a lock: %v", err)
	}
	skillGone(t, f.h.home, "triage")
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPIAddInAProjectEditsTheCheckoutAndCommitsNothing(t *testing.T) {
	f := newBumpFixture(t)
	before := gitIn(t, f.repo, "rev-list", "--count", "HEAD")
	a := newSkillsAPI(t, f.ph)
	run := a.ok("add", map[string]string{"scope": "project", "project": "app", "source": "triage"})
	if r := resultOf(run, "triage"); r.State != "added" || run.Project != "app" {
		t.Fatalf("add = %+v", run)
	}
	if !strings.Contains(f.skillText("triage"), "triage") {
		t.Fatal("the checkout holds no copy")
	}
	if got := gitIn(t, f.repo, "rev-list", "--count", "HEAD"); got != before {
		t.Fatalf("commits %s -> %s", before, got)
	}
	if st := gitIn(t, f.repo, "status", "--porcelain"); !strings.Contains(st, ".agents") || !strings.Contains(st, "skills-lock.json") {
		t.Fatalf("status = %q", st)
	}
	writeFiles(t, filepath.Join(f.repo, ".agents", "skills", "other"), map[string]string{"notes.md": "dirty\n"})
	code, _, _, raw := a.post("add", map[string]string{"scope": "project", "project": "app", "source": "other"})
	if code != http.StatusConflict || !strings.Contains(raw, "uncommitted changes") {
		t.Fatalf("a dirty folder = %d %s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(f.repo, ".agents", "skills", "other", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("a refused add left a copy: %v", err)
	}
	code, _, _, raw = a.post("add", map[string]string{"scope": "project", "project": "nope", "source": "other"})
	if code != http.StatusNotFound {
		t.Fatalf("an unknown project = %d %s", code, raw)
	}
}

func TestSkillsAPIAddsAndRemovesInTheUserScope(t *testing.T) {
	f := newProposeFixture(t, "")
	home := f.h.vars["HOME"]
	a := newSkillsAPI(t, f.h)
	run := a.ok("add", map[string]string{"scope": "user", "source": "triage"})
	if resultOf(run, "triage").State != "added" || run.Scope != "user" {
		t.Fatalf("add = %+v", run)
	}
	skillThere(t, home, "triage")
	if _, ok := lockEntries(t, userLock(home))["triage"]; !ok {
		t.Fatal("no lock entry in the user lock")
	}
	skillGone(t, f.h.home, "triage")
	run = a.ok("remove", map[string]string{"scope": "user", "name": "triage"})
	if resultOf(run, "triage").State != "removed" {
		t.Fatalf("remove = %+v", run)
	}
	skillGone(t, home, "triage")
}

func TestSkillsAPIRemovesOneNamedSkillFromEveryRootAndForgetsIt(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	f.h.ok("skills", "add", "--fleet", "other")
	a := newSkillsAPI(t, f.h)
	code, run, receipt, raw := a.post("remove", map[string]string{"scope": "fleet", "name": "triage"})
	if code != 200 || receipt != "Skills remove: 1 removed" || run.Refused != 0 || len(run.Results) != 1 || run.Results[0].State != "removed" {
		t.Fatalf("remove = %d %q %+v %s", code, receipt, run, raw)
	}
	skillGone(t, f.h.home, "triage")
	skillThere(t, f.h.home, "other")
	if _, ok := skillState(t, f.h.home)["triage"]; ok {
		t.Fatal("the lock still records triage")
	}
	if _, ok := skillState(t, f.h.home)["other"]; !ok {
		t.Fatal("the lock lost the other skill")
	}
	if sub := subscribed(t, f.h.home); strings.Contains(sub, `"triage"`) || !strings.Contains(sub, `"other"`) {
		t.Fatalf("skills.json = %s", sub)
	}
	run = a.ok("sync", map[string]string{"scope": "fleet"})
	if len(run.Results) != 1 || resultOf(run, "other").State != "current" {
		t.Fatalf("a later sync = %+v", run)
	}
	skillGone(t, f.h.home, "triage")
}

func TestSkillsAPIRemoveNeverTakesEverySkill(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	f.h.ok("skills", "add", "--fleet", "other")
	a := newSkillsAPI(t, f.h)
	for _, body := range []map[string]string{
		{"scope": "fleet"},
		{"scope": "fleet", "name": "  "},
		{"scope": "fleet", "name": "triage", "yes": "triage@x"},
		{"scope": "fleet", "name": "triage", "source": "triage"},
		{"scope": "fleet", "name": "triage", "ref": "main"},
		{"scope": "fleet", "name": "-x"},
		{"scope": "fleet", "name": "../other"},
		{"scope": "fleet", "name": "a/b"},
	} {
		if code, _, _, raw := a.post("remove", body); code != http.StatusBadRequest {
			t.Errorf("%v = %d %s", body, code, raw)
		}
	}
	skillThere(t, f.h.home, "triage")
	skillThere(t, f.h.home, "other")
	if len(skillState(t, f.h.home)) != 2 {
		t.Fatalf("lock = %+v", skillState(t, f.h.home))
	}
}

func TestSkillsAPIRemoveRefusesLikeTheCLIAndLeavesTheFiles(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	f.h.ok("skills", "add", "--fleet", "other")
	edited := f.edit("triage")
	a := newSkillsAPI(t, f.h)
	run := a.ok("remove", map[string]string{"scope": "fleet", "name": "triage"})
	if r := resultOf(run, "triage"); r.State != "refused" || !strings.Contains(r.Detail, "changed locally") || run.Refused != 1 {
		t.Fatalf("an edited copy = %+v", run)
	}
	if got := readSkill(t, skillPath(f.h.home, ".claude", "triage")); got != edited {
		t.Fatalf("an edited copy was touched: %q", got)
	}
	skillThere(t, f.h.home, "triage")
	run = a.ok("remove", map[string]string{"scope": "fleet", "name": "secondhand"})
	if r := resultOf(run, "secondhand"); r.State != "refused" || run.Refused != 1 {
		t.Fatalf("an unmarked skill = %+v", run)
	}
	skillThere(t, f.h.home, "secondhand")
	run = a.ok("remove", map[string]string{"scope": "fleet", "name": "nothing"})
	if r := resultOf(run, "nothing"); r.State != "refused" || !strings.Contains(r.Detail, "not a skill Hand manages") || run.Refused != 1 {
		t.Fatalf("an unknown skill = %+v", run)
	}
	skillThere(t, f.h.home, "other")
	if len(skillState(t, f.h.home)) != 2 || !strings.Contains(subscribed(t, f.h.home), `"triage"`) {
		t.Fatalf("a refusal changed the record: %+v", skillState(t, f.h.home))
	}
}

func TestSkillsAPIRemoveInAProjectRefusesADirtyFolderAndEditsTheCheckout(t *testing.T) {
	f := newBumpFixture(t)
	a := newSkillsAPI(t, f.ph)
	a.ok("add", map[string]string{"scope": "project", "project": "app", "source": "triage"})
	run := a.ok("remove", map[string]string{"scope": "project", "project": "app", "name": "triage"})
	if r := resultOf(run, "triage"); r.State != "refused" || !strings.Contains(r.Detail, "uncommitted changes") || run.Refused != 1 {
		t.Fatalf("an uncommitted copy = %+v", run)
	}
	if !strings.Contains(f.skillText("triage"), "triage") {
		t.Fatal("a refused remove took the copy")
	}
	f.publish("skills")
	before := gitIn(t, f.repo, "rev-list", "--count", "HEAD")
	run = a.ok("remove", map[string]string{"scope": "project", "project": "app", "name": "triage"})
	if resultOf(run, "triage").State != "removed" {
		t.Fatalf("remove = %+v", run)
	}
	if _, err := os.Stat(filepath.Join(f.repo, ".agents", "skills", "triage")); !os.IsNotExist(err) {
		t.Fatalf("the copy is still there: %v", err)
	}
	if got := gitIn(t, f.repo, "rev-list", "--count", "HEAD"); got != before {
		t.Fatalf("commits %s -> %s", before, got)
	}
}

func TestSkillsAPIAddsAfterAPlantedLockEntryWithoutTrustingIt(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	editJSON(t, filepath.Join(f.h.home, "skills.json"), func(skills map[string]map[string]any) {
		skills["triage"]["source"] = "someone/else"
	})
	a := newSkillsAPI(t, f.h)
	a.ok("add", map[string]string{"scope": "fleet", "source": "other"})
	if s := a.skill("fleet", "", "triage"); s.Trusted {
		t.Fatalf("a planted subscription is trusted after an add: %+v", s)
	}
	if s := a.skill("fleet", "", "other"); !s.Trusted {
		t.Fatalf("the added skill is not trusted: %+v", s)
	}
}

func TestSkillsAPIAddDropsAPathAndALoginFromAFailingClone(t *testing.T) {
	f := newProposeFixture(t, "")
	nowhere := filepath.Join(t.TempDir(), "planted-dir-x")
	route := func(to string) {
		text := "[url \"" + to + "\"]\n\tinsteadOf = " + skillsURL + "\n[user]\n\tname = Pat\n\temail = pat@example.com\n"
		if err := os.WriteFile(f.cfg, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := newSkillsAPI(t, f.h)
	route("file://" + filepath.ToSlash(nowhere))
	code, _, _, raw := a.post("add", map[string]string{"scope": "fleet", "source": "triage"})
	if code != http.StatusInternalServerError || strings.Contains(raw, filepath.ToSlash(filepath.Dir(nowhere))) || strings.Contains(raw, "planted-dir-x/") {
		t.Fatalf("a failing clone = %d %s", code, raw)
	}
	route("https://pat:s3@cret@127.0.0.1:1/atqamz/skills.git")
	code, _, _, raw = a.post("add", map[string]string{"scope": "fleet", "source": "triage"})
	if code != http.StatusInternalServerError || !strings.Contains(raw, "127.0.0.1:1") || strings.Contains(raw, "cret") || strings.Contains(raw, "pat:") || strings.Contains(raw, "pat@") {
		t.Fatalf("a login in a clone error = %d %s", code, raw)
	}
	skillGone(t, f.h.home, "triage")
	if _, err := os.Stat(fleetLock(f.h.home)); !os.IsNotExist(err) {
		t.Fatalf("a failed add wrote a lock: %v", err)
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestBoardStopsASkillsAddWhenItShutsDownAndLeavesNothing(t *testing.T) {
	f := newProposeFixture(t, "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan struct{}, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
			select {
			case accepted <- struct{}{}:
			default:
			}
		}
	}()
	text := "[url \"git://" + ln.Addr().String() + "/atqamz/skills.git\"]\n\tinsteadOf = " + skillsURL + "\n[user]\n\tname = Pat\n\temail = pat@example.com\n"
	if err := os.WriteFile(f.cfg, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	base, stop := startBoard(t, f.h, "127.0.0.1")
	fleetURL, token := fleetPage(t, base, f.h)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	res, err := client.Get(fleetURL + "/?token=" + token)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	res, err = client.Get(fleetURL + "/api/v1/session")
	if err != nil {
		t.Fatal(err)
	}
	var sess struct{ Data board.APISession }
	err = json.NewDecoder(res.Body).Decode(&sess)
	_ = res.Body.Close()
	if err != nil || sess.Data.CSRF == "" {
		t.Fatalf("session = %d, %v", res.StatusCode, err)
	}
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		req, _ := http.NewRequest("POST", fleetURL+"/api/v1/skills/add", strings.NewReader(`{"scope":"fleet","source":"triage"}`))
		req.Header.Set("X-Hand-CSRF", sess.Data.CSRF)
		if res, err := client.Do(req); err == nil {
			_ = res.Body.Close()
		}
	}()
	select {
	case <-accepted:
	case <-time.After(20 * time.Second):
		t.Fatal("the add never reached the remote")
	}
	start := time.Now()
	stop()
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("board took %v to stop with an add running", d)
	}
	select {
	case <-answered:
	case <-time.After(3 * time.Second):
		t.Fatal("the add request was still running after the board stopped")
	}
	skillGone(t, f.h.home, "triage")
	if _, err := os.Stat(fleetLock(f.h.home)); !os.IsNotExist(err) {
		t.Fatalf("a stopped add wrote a lock: %v", err)
	}
	if left := tempLeft(t, f.tmp); runtime.GOOS != "windows" && len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}
