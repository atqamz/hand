package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

const skillsAPIToken = "skills-api-token"

type skillsAPI struct {
	t    *testing.T
	h    http.Handler
	csrf string
}

func newSkillsAPI(t *testing.T, h *harness) *skillsAPI {
	t.Helper()
	st, err := state.Open(filepath.Join(h.home, "hand.db"), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	read, act := cli.SkillsHooks(h.env(io.Discard, io.Discard), h.home, "", st)
	a := &skillsAPI{t: t, h: board.New(st, skillsAPIToken, board.Options{Controls: true, Home: h.home, Now: h.clock, Skills: read, SkillsRun: act})}
	rec := a.do(httptest.NewRequest("GET", "/api/v1/session", nil))
	var env struct{ Data board.APISession }
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Data.CSRF == "" {
		t.Fatalf("session = %d %s", rec.Code, rec.Body)
	}
	a.csrf = env.Data.CSRF
	return a
}

func (a *skillsAPI) do(req *http.Request) *httptest.ResponseRecorder {
	req.AddCookie(&http.Cookie{Name: "hand_board", Value: skillsAPIToken})
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func (a *skillsAPI) list(query string) board.APISkills {
	a.t.Helper()
	rec := a.do(httptest.NewRequest("GET", "/api/v1/skills"+query, nil))
	var env struct{ Data board.APISkills }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &env) != nil {
		a.t.Fatalf("GET skills%s = %d %s", query, rec.Code, rec.Body)
	}
	return env.Data
}

func (a *skillsAPI) post(verb string, body map[string]string) (int, board.APISkillsRun, string, string) {
	a.t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/v1/skills/"+verb, bytes.NewReader(raw))
	req.Header.Set("X-Hand-CSRF", a.csrf)
	rec := a.do(req)
	var env struct {
		Data    board.APISkillsRun
		Receipt string
	}
	if rec.Code == 200 {
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			a.t.Fatalf("POST %s: %v %s", verb, err, rec.Body)
		}
	}
	return rec.Code, env.Data, env.Receipt, rec.Body.String()
}

func (a *skillsAPI) ok(verb string, body map[string]string) board.APISkillsRun {
	a.t.Helper()
	code, run, _, raw := a.post(verb, body)
	if code != 200 {
		a.t.Fatalf("POST %s %v = %d %s", verb, body, code, raw)
	}
	return run
}

func (a *skillsAPI) skill(scope, project, name string) board.APISkill {
	a.t.Helper()
	for _, sc := range a.list("").Scopes {
		if sc.Scope == scope && sc.Project == project {
			for _, s := range sc.Skills {
				if s.Name == name {
					return s
				}
			}
		}
	}
	a.t.Fatalf("no skill %s/%s/%s", scope, project, name)
	return board.APISkill{}
}

func resultOf(run board.APISkillsRun, name string) board.APISkillResult {
	for _, r := range run.Results {
		if r.Name == name {
			return r
		}
	}
	return board.APISkillResult{}
}

func TestSkillsAPIListsEveryScopeWithTheDriftStatesTheCLIPrints(t *testing.T) {
	f := newProposeFixture(t, "")
	f.hub.commit(map[string]string{
		"skills/cur/SKILL.md": skillFiles("cur", "v1")["SKILL.md"], "skills/beh/SKILL.md": skillFiles("beh", "v1")["SKILL.md"],
		"skills/edi/SKILL.md": skillFiles("edi", "v1")["SKILL.md"], "skills/clash/SKILL.md": skillFiles("clash", "v1")["SKILL.md"],
	})
	h := f.h
	repo := gitRepo(t)
	h.ok("project", "add", "app", repo)
	for _, name := range []string{"cur", "beh", "edi", "clash"} {
		h.ok("skills", "add", "--fleet", name)
	}
	f.edit("edi")
	f.edit("clash")
	moved := f.hub.commit(map[string]string{"skills/beh/SKILL.md": skillFiles("beh", "v2")["SKILL.md"], "skills/clash/SKILL.md": skillFiles("clash", "v2")["SKILL.md"]})
	old := skillState(t, h.home)["cur"].Commit
	a := newSkillsAPI(t, h)
	got := a.list("")
	if len(got.Scopes) != 3 || got.Scopes[0].Scope != "fleet" || got.Scopes[1].Scope != "project" || got.Scopes[1].Project != "app" || got.Scopes[2].Scope != "user" {
		t.Fatalf("scopes = %+v", got.Scopes)
	}
	cli := h.ok("skills", "list", "--fleet")
	for name, want := range map[string]string{"cur": "current", "beh": "behind", "edi": "edited", "clash": "conflict"} {
		s := a.skill("fleet", "", name)
		if s.State != want || !strings.HasPrefix(row(cli, name), name+",\""+skillsURL+"#skills/"+name+"\","+old+",fleet,yes,main,"+want) {
			t.Errorf("%s: API %+v, CLI %q", name, s, row(cli, name))
		}
		if !s.Managed || !s.Trusted || s.Commit != old || s.Ref != "main" || s.Source != skillsURL+"#skills/"+name || s.Detail != "" {
			t.Errorf("%s = %+v", name, s)
		}
		if s.Latest != moved {
			t.Errorf("%s latest = %q, want %q", name, s.Latest, moved)
		}
		if (name == "beh") != (s.Diff != "") {
			t.Errorf("%s diff = %q", name, s.Diff)
		}
	}
	if d := a.skill("fleet", "", "beh").Diff; !strings.Contains(d, "-v1") || !strings.Contains(d, "+v2") {
		t.Errorf("beh diff = %q", d)
	}
	if s := a.skill("fleet", "", "secondhand"); s.Managed || s.Source != "hand init" || s.State != "" || s.Trusted {
		t.Errorf("an unmanaged skill = %+v", s)
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPINarrowsAReadToOneScope(t *testing.T) {
	f := newProposeFixture(t, "")
	h := f.h
	repo := gitRepo(t)
	h.ok("project", "add", "app", repo)
	h.ok("project", "add", "lib", gitRepo(t))
	a := newSkillsAPI(t, h)
	if got := a.list("?scope=fleet"); len(got.Scopes) != 1 || got.Scopes[0].Scope != "fleet" {
		t.Fatalf("fleet = %+v", got)
	}
	if got := a.list("?scope=user"); len(got.Scopes) != 1 || got.Scopes[0].Scope != "user" || len(got.Scopes[0].Skills) != 0 {
		t.Fatalf("user = %+v", got)
	}
	if got := a.list("?scope=project"); len(got.Scopes) != 2 {
		t.Fatalf("projects = %+v", got)
	}
	if got := a.list("?scope=project&project=lib"); len(got.Scopes) != 1 || got.Scopes[0].Project != "lib" {
		t.Fatalf("one project = %+v", got)
	}
	rec := a.do(httptest.NewRequest("GET", "/api/v1/skills?scope=project&project=nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown project = %d %s", rec.Code, rec.Body)
	}
}

func TestSkillsAPISyncAppliesAnOwnRepoWithABackupAndTheCLIsRow(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	old := skillState(t, f.h.home)["triage"].Commit
	moved := f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	a := newSkillsAPI(t, f.h)
	code, run, receipt, raw := a.post("sync", map[string]string{"scope": "fleet"})
	if code != 200 || receipt != "Skills sync: 1 updated" || run.Refused != 0 || run.Scope != "fleet" {
		t.Fatalf("sync = %d %q %+v %s", code, receipt, run, raw)
	}
	if r := resultOf(run, "triage"); r.State != "updated" || r.Detail != old[:12]+" -> "+moved[:12] {
		t.Fatalf("row = %+v", r)
	}
	if got := readSkill(t, skillPath(f.h.home, ".claude", "triage")); !strings.Contains(got, "\nv2\n") {
		t.Fatalf("copy = %q", got)
	}
	if len(backups(t, filepath.Join(f.h.home, ".agents", "skills-backups"), "triage")) != 1 {
		t.Fatal("no backup of the previous copy")
	}
	if s := a.skill("fleet", "", "triage"); s.State != "current" || s.Commit != moved {
		t.Fatalf("after sync = %+v", s)
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPISyncMovesAThirdPartySkillOnlyWithTheWholeYes(t *testing.T) {
	h := skillsHarness(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	src := "file://" + filepath.ToSlash(tool.bare)
	h.ok("skills", "add", "--fleet", "--ref", "main", src)
	old := skillGit(t, tool.work, "rev-parse", "HEAD")
	moved := tool.commit(skillFiles("tool-skill", "v2"))
	a := newSkillsAPI(t, h)
	if s := a.skill("fleet", "", "tool-skill"); s.Trusted || s.State != "behind" || s.Latest != moved || !strings.Contains(s.Diff, "+v2") {
		t.Fatalf("third-party row = %+v", s)
	}
	copyHolds := func(want string) {
		t.Helper()
		if got := readSkill(t, skillPath(h.home, ".claude", "tool-skill")); !strings.Contains(got, "\n"+want+"\n") {
			t.Fatalf("copy = %q, want %s", got, want)
		}
	}
	run := a.ok("sync", map[string]string{"scope": "fleet"})
	if r := resultOf(run, "tool-skill"); r.State != "behind" || !strings.Contains(r.Detail, "tool-skill@"+moved) || run.Refused != 0 || len(run.Diffs) != 1 || run.Diffs[0].Name != "tool-skill" || !strings.Contains(run.Diffs[0].Diff, "+v2") {
		t.Fatalf("sync without yes = %+v", run)
	}
	copyHolds("v1")
	for _, yes := range []string{"tool-skill", "tool-skill@" + moved[:10], "other@" + moved} {
		run = a.ok("sync", map[string]string{"scope": "fleet", "yes": yes})
		if r := resultOf(run, "tool-skill"); r.State == "updated" {
			t.Fatalf("yes %q applied: %+v", yes, run)
		}
		copyHolds("v1")
	}
	tool.commit(skillFiles("tool-skill", "v3"))
	run = a.ok("sync", map[string]string{"scope": "fleet", "yes": "tool-skill@" + moved})
	if r := resultOf(run, "tool-skill"); r.State != "refused" || !strings.Contains(r.Detail, "moved since the diff you read") || run.Refused != 1 || len(run.Diffs) != 1 || !strings.Contains(run.Diffs[0].Diff, "+v3") {
		t.Fatalf("stale yes = %+v", run)
	}
	copyHolds("v1")
	latest := skillGit(t, tool.work, "rev-parse", "HEAD")
	code, run, receipt, raw := a.post("sync", map[string]string{"scope": "fleet", "name": "tool-skill", "yes": "tool-skill@" + latest})
	if code != 200 || receipt != "Skills sync: 1 updated" || resultOf(run, "tool-skill").Detail != old[:12]+" -> "+latest[:12] {
		t.Fatalf("sync with yes = %d %q %s", code, receipt, raw)
	}
	copyHolds("v3")
}

func TestSkillsAPILeavesAnEditedCopyAloneAndSendsAConflictToReconcile(t *testing.T) {
	f := newProposeFixture(t, "")
	f.hub.commit(map[string]string{"skills/edi/SKILL.md": skillFiles("edi", "v1")["SKILL.md"], "skills/clash/SKILL.md": skillFiles("clash", "v1")["SKILL.md"]})
	for _, name := range []string{"edi", "clash"} {
		f.h.ok("skills", "add", "--fleet", name)
	}
	editedText, clashText := f.edit("edi"), f.edit("clash")
	f.hub.commit(map[string]string{"skills/clash/SKILL.md": skillFiles("clash", "v2")["SKILL.md"]})
	a := newSkillsAPI(t, f.h)
	run := a.ok("sync", map[string]string{"scope": "fleet"})
	if r := resultOf(run, "edi"); r.State != "edited" || !strings.Contains(r.Detail, "skills propose --fleet edi") {
		t.Fatalf("edited row = %+v", r)
	}
	if r := resultOf(run, "clash"); r.State != "conflict" || !strings.Contains(r.Detail, "reconcile first") || run.Refused != 1 {
		t.Fatalf("conflict row = %+v (refused %d)", r, run.Refused)
	}
	if len(run.Reconcile) != 1 || !strings.Contains(run.Reconcile[0], "task add --goal \"Reconcile the skill clash of the fleet scope") {
		t.Fatalf("reconcile = %q", run.Reconcile)
	}
	run = a.ok("update", map[string]string{"scope": "fleet"})
	if r := resultOf(run, "edi"); r.State != "refused" || !strings.Contains(r.Detail, "skills propose --fleet edi") || run.Refused != 2 || len(run.Reconcile) != 1 {
		t.Fatalf("update = %+v", run)
	}
	if readSkill(t, skillPath(f.h.home, ".claude", "edi")) != editedText || readSkill(t, skillPath(f.h.home, ".claude", "clash")) != clashText {
		t.Fatal("an edited copy was overwritten")
	}
	if len(backups(t, filepath.Join(f.h.home, ".agents", "skills-backups"), "clash")) != 0 {
		t.Fatal("a backup without an apply")
	}
}

func TestSkillsAPIUpdateTakesOneNamedSkillAndRefusesAnUnknownOne(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	f.h.ok("skills", "add", "--fleet", "other")
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"], "skills/other/SKILL.md": skillFiles("other", "v2")["SKILL.md"]})
	a := newSkillsAPI(t, f.h)
	run := a.ok("update", map[string]string{"scope": "fleet", "name": "triage"})
	if len(run.Results) != 1 || run.Results[0].State != "updated" {
		t.Fatalf("update = %+v", run)
	}
	if got := readSkill(t, skillPath(f.h.home, ".claude", "other")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("an unnamed skill moved: %q", got)
	}
	run = a.ok("update", map[string]string{"scope": "fleet", "name": "nothing"})
	if r := resultOf(run, "nothing"); r.State != "refused" || !strings.Contains(r.Detail, "no recorded source") || run.Refused != 1 {
		t.Fatalf("unknown = %+v", run)
	}
	run = a.ok("update", map[string]string{"scope": "fleet"})
	if len(run.Results) != 2 || resultOf(run, "triage").State != "current" || resultOf(run, "other").State != "updated" {
		t.Fatalf("update all = %+v", run)
	}
}

func TestSkillsAPIProposeOpensThePullRequestAndRefusesLikeTheCLI(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	a := newSkillsAPI(t, f.h)
	code, _, _, raw := a.post("propose", map[string]string{"scope": "fleet", "name": "triage"})
	if code != http.StatusConflict || !strings.Contains(raw, "conflict") {
		t.Fatalf("propose of an unedited copy = %d %s", code, raw)
	}
	f.untouched("an unedited propose")
	f.edit("triage")
	code, run, receipt, raw := a.post("propose", map[string]string{"scope": "fleet", "name": "triage"})
	if code != 200 || receipt != "Skills propose: 1 proposed" || resultOf(run, "triage").Detail != prURL {
		t.Fatalf("propose = %d %q %s", code, receipt, raw)
	}
	var branch string
	for name := range f.refs() {
		if strings.HasPrefix(name, "refs/heads/hand-skills/triage-") {
			branch = strings.TrimPrefix(name, "refs/heads/")
		}
	}
	argv := readSkill(t, f.log)
	if branch == "" || !strings.Contains(argv, "--head\",\""+branch+"\"") || strings.Count(argv, "pr\",\"create") != 1 {
		t.Fatalf("branch %q, gh log %s", branch, argv)
	}
	code, _, _, raw = a.post("propose", map[string]string{"scope": "fleet", "name": "triage"})
	if code != http.StatusConflict || strings.Count(readSkill(t, f.log), "pr\",\"create") != 1 {
		t.Fatalf("a repeat = %d %s", code, raw)
	}
	code, _, _, raw = a.post("propose", map[string]string{"scope": "fleet", "name": "secondhand"})
	if code != http.StatusBadRequest {
		t.Fatalf("propose of an unmanaged skill = %d %s", code, raw)
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPISyncInAProjectOpensABumpPullRequestAndLeavesTheCheckout(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage")
	a := newSkillsAPI(t, f.ph)
	if s := a.skill("project", "app", "triage"); s.State != "current" || s.Trusted != true {
		t.Fatalf("project row = %+v", s)
	}
	moved := f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	lockBefore := readSkill(t, fleetLock(f.repo))
	if s := a.skill("project", "app", "triage"); s.State != "behind" || s.Latest != moved || !strings.Contains(s.Diff, "+v2") {
		t.Fatalf("project behind row = %+v", s)
	}
	code, run, receipt, raw := a.post("sync", map[string]string{"scope": "project", "project": "app"})
	if code != 200 || receipt != "Skills sync: 1 bumped" || run.Project != "app" || resultOf(run, "triage").Detail != prURL {
		t.Fatalf("sync = %d %q %s", code, receipt, raw)
	}
	branches := f.branches("triage")
	argv := f.ghArgs()
	if len(branches) != 1 || len(argv) != 1 || argv[0][3] != "acme/app" || argv[0][5] != branches[0] {
		t.Fatalf("branches %v, gh %q", branches, argv)
	}
	if !strings.Contains(f.skillText("triage"), "\nv1\n") || readSkill(t, fleetLock(f.repo)) != lockBefore || gitIn(t, f.repo, "status", "--porcelain") != "" {
		t.Fatal("the checkout changed")
	}
	run = a.ok("sync", map[string]string{"scope": "project", "project": "app"})
	if r := resultOf(run, "triage"); r.State != "pending" || len(f.ghArgs()) != 1 || len(f.branches("triage")) != 1 {
		t.Fatalf("a repeat = %+v", run)
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPIUpdateInAProjectEditsTheCheckoutAndOpensNoPullRequest(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage")
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	a := newSkillsAPI(t, f.ph)
	run := a.ok("update", map[string]string{"scope": "project", "project": "app", "name": "triage"})
	if resultOf(run, "triage").State != "updated" || !strings.Contains(f.skillText("triage"), "\nv2\n") || len(f.ghArgs()) != 0 {
		t.Fatalf("update = %+v", run)
	}
	writeFiles(t, filepath.Join(f.repo, ".agents", "skills", "triage"), map[string]string{"notes.md": "dirty\n"})
	run = a.ok("update", map[string]string{"scope": "project", "project": "app", "name": "triage"})
	if r := resultOf(run, "triage"); r.State != "refused" || run.Refused != 1 {
		t.Fatalf("a dirty folder = %+v", run)
	}
}

func TestSkillsAPIRefusesAnArgumentThatWouldBeAFlag(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	a := newSkillsAPI(t, f.h)
	for _, body := range []map[string]string{
		{"scope": "fleet", "name": "--user"},
		{"scope": "fleet", "name": "-x"},
		{"scope": "project", "project": "-x", "name": "triage"},
		{"scope": "fleet", "yes": "--ref=x"},
		{"scope": "fleet", "name": "../triage"},
		{"scope": "fleet", "name": "a/b"},
	} {
		code, _, _, raw := a.post("sync", body)
		if code != http.StatusBadRequest {
			t.Errorf("%v = %d %s", body, code, raw)
		}
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPIActsOnTheUserScope(t *testing.T) {
	h := skillsHarness(t)
	src := skillSource(t, "notes", "one")
	h.ok("skills", "add", "--user", src)
	a := newSkillsAPI(t, h)
	if s := a.skill("user", "", "notes"); !s.Managed || s.State != "current" || !s.Trusted {
		t.Fatalf("user row = %+v", s)
	}
	run := a.ok("sync", map[string]string{"scope": "user"})
	if r := resultOf(run, "notes"); r.State != "current" || run.Scope != "user" {
		t.Fatalf("user sync = %+v", run)
	}
	if got := readSkill(t, skillPath(h.vars["HOME"], ".agents", "notes")); !strings.Contains(got, "one") {
		t.Fatalf("user copy = %q", got)
	}
}

func TestSkillsAPIReadShowsAnUnreachableSourceAsAnErrorAndWritesNothing(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	lock := readSkill(t, fleetLock(f.h.home))
	a := newSkillsAPI(t, f.h)
	cli.SetSkillFetchLimits(t, time.Nanosecond, time.Nanosecond)
	s := a.skill("fleet", "", "triage")
	if s.State != "error" || s.Detail == "" || s.Latest != "" || s.Diff != "" {
		t.Fatalf("an unreachable source = %+v", s)
	}
	if strings.Contains(s.Detail, f.tmp) || strings.Contains(s.Detail, f.h.home) {
		t.Fatalf("the detail carries a path: %q", s.Detail)
	}
	if readSkill(t, fleetLock(f.h.home)) != lock {
		t.Fatal("a read wrote the lock")
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPIReadCutsAHugeDiff(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	var big strings.Builder
	for i := 0; i < 8000; i++ {
		big.WriteString("line of the new reference text number a long one\n")
	}
	f.hub.commit(map[string]string{"skills/triage/references/big.md": big.String()})
	a := newSkillsAPI(t, f.h)
	s := a.skill("fleet", "", "triage")
	if !s.DiffCut || len(s.Diff) > 64<<10 || !strings.HasSuffix(s.Diff, "\n") || len(s.Diff) < 60<<10 {
		t.Fatalf("diff len %d cut %v", len(s.Diff), s.DiffCut)
	}
}

func TestSkillsAPIPlantedLockEntryStaysUntrusted(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	editJSON(t, filepath.Join(f.h.home, "skills.json"), func(skills map[string]map[string]any) {
		skills["triage"]["source"] = "someone/else"
	})
	a := newSkillsAPI(t, f.h)
	if s := a.skill("fleet", "", "triage"); s.Trusted {
		t.Fatalf("a changed subscription is trusted: %+v", s)
	}
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	run := a.ok("sync", map[string]string{"scope": "fleet"})
	if r := resultOf(run, "triage"); r.State != "behind" || len(run.Diffs) != 1 {
		t.Fatalf("sync = %+v", run)
	}
	if got := readSkill(t, skillPath(f.h.home, ".claude", "triage")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("applied: %q", got)
	}
}

func TestSkillsAPIDropsALoginFromASourceAndFromAnErrorDetail(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	const dirty = "https://pat:s3@cret@127.0.0.1:1/acme/skills.git"
	editJSON(t, fleetLock(f.h.home), func(skills map[string]map[string]any) {
		skills["triage"]["sourceUrl"] = dirty
	})
	a := newSkillsAPI(t, f.h)
	s := a.skill("fleet", "", "triage")
	if s.State != "error" || !strings.Contains(s.Source, "127.0.0.1:1/acme/skills.git") || s.Detail == "" || strings.Contains(s.Source+s.Detail, "cret") || strings.Contains(s.Source+s.Detail, "pat:") {
		t.Fatalf("row = %+v", s)
	}
	run := a.ok("sync", map[string]string{"scope": "fleet"})
	if r := resultOf(run, "triage"); r.State != "refused" || r.Detail == "" || strings.Contains(r.Detail, "cret") || run.Refused != 1 {
		t.Fatalf("sync = %+v", run)
	}
}

func TestSkillsAPIDropsALoginFromARequestError(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	const dirty = "https://pat:s3@cret@127.0.0.1:1/acme/skills.git"
	editJSON(t, fleetLock(f.h.home), func(skills map[string]map[string]any) {
		skills["triage"]["sourceUrl"] = dirty
	})
	a := newSkillsAPI(t, f.h)
	code, _, _, raw := a.post("propose", map[string]string{"scope": "fleet", "name": "triage"})
	if code != http.StatusBadRequest || !strings.Contains(raw, "127.0.0.1:1/acme/skills.git") || strings.Contains(raw, "cret") || strings.Contains(raw, "pat:") {
		t.Fatalf("propose = %d %s", code, raw)
	}
}

func TestSkillsAPIDropsALoginAndAPathFromAFailingGh(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	fakebin.Install(t, f.bin, "gh", "gh", map[string]string{"log": f.log, "stderr": "could not read https://pat:s3@cret@github.com/acme/hub.git and /home/someone/secret/dir/x"})
	f.edit("triage")
	a := newSkillsAPI(t, f.h)
	code, _, _, raw := a.post("propose", map[string]string{"scope": "fleet", "name": "triage"})
	if code != http.StatusInternalServerError || !strings.Contains(raw, "github.com/acme/hub.git") || strings.Contains(raw, "cret") || strings.Contains(raw, "pat:") || strings.Contains(raw, "someone") {
		t.Fatalf("propose = %d %s", code, raw)
	}
}

func TestSkillsAPIDropsALoginFromAReconcileGoal(t *testing.T) {
	f := newProposeFixture(t, "")
	f.hub.commit(map[string]string{"skills/clash/SKILL.md": skillFiles("clash", "v1")["SKILL.md"]})
	f.h.ok("skills", "add", "--fleet", "clash")
	const dirty = "https://pat:s3@cret@example.test/acme/skills.git"
	editJSON(t, fleetLock(f.h.home), func(skills map[string]map[string]any) {
		skills["clash"]["sourceUrl"] = dirty
	})
	f.route(dirty, f.hub.bare)
	f.edit("clash")
	f.hub.commit(map[string]string{"skills/clash/SKILL.md": skillFiles("clash", "v2")["SKILL.md"]})
	a := newSkillsAPI(t, f.h)
	run := a.ok("sync", map[string]string{"scope": "fleet"})
	if len(run.Reconcile) != 1 || !strings.Contains(run.Reconcile[0], "example.test/acme/skills.git") || strings.Contains(run.Reconcile[0], "cret") || strings.Contains(run.Reconcile[0], "pat:") {
		t.Fatalf("reconcile = %q", run.Reconcile)
	}
}

func TestSkillsLoginKeepsAnAtInAPathOrQuery(t *testing.T) {
	for _, keep := range []string{"https://host/a@b", "https://host/a?u=x@y", "see https://host/p and mail a@b.test"} {
		if got := cli.SkillsLogin(keep); got != keep {
			t.Fatalf("login(%q) = %q", keep, got)
		}
	}
	if got := cli.SkillsLogin("https://pat:s3@cret@host/a@b"); got != "https://host/a@b" {
		t.Fatalf("login = %q", got)
	}
}

func TestSkillsAPIReadDropsADirectoryFromASourceAndADetail(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	dir := filepath.Join(t.TempDir(), "private-parent", "gone")
	editJSON(t, fleetLock(f.h.home), func(skills map[string]map[string]any) {
		skills["triage"]["sourceUrl"] = dir
	})
	a := newSkillsAPI(t, f.h)
	s := a.skill("fleet", "", "triage")
	if !strings.Contains(s.Detail, " gone ") || strings.Contains(s.Source+s.Detail, "private-parent") {
		t.Fatalf("row = %+v", s)
	}
}

func TestBoardStopsASkillsActionWhenItShutsDown(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	editJSON(t, fleetLock(f.h.home), func(skills map[string]map[string]any) {
		skills["triage"]["sourceUrl"] = "git://" + ln.Addr().String() + "/acme/skills.git"
	})
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
		req, _ := http.NewRequest("POST", fleetURL+"/api/v1/skills/sync", strings.NewReader(`{"scope":"fleet"}`))
		req.Header.Set("X-Hand-CSRF", sess.Data.CSRF)
		if res, err := client.Do(req); err == nil {
			_ = res.Body.Close()
		}
	}()
	time.Sleep(500 * time.Millisecond)
	start := time.Now()
	stop()
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("board took %v to stop with a skills action running", d)
	}
	select {
	case <-answered:
	case <-time.After(3 * time.Second):
		t.Fatal("the skills request was still running after the board stopped")
	}
}
