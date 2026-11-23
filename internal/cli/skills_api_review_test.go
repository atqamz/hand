package cli_test

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fleet"
)

const toolSource = "https://example.com/acme/tool-skill"

type toolFixture struct {
	*proposeFixture
	tool *skillRemote
	api  *skillsAPI
}

func newToolFixture(t *testing.T) *toolFixture {
	t.Helper()
	f := &toolFixture{proposeFixture: newProposeFixture(t, "")}
	f.tool = newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	f.route(toolSource, f.tool.bare)
	f.api = newSkillsAPI(t, f.h)
	return f
}

func (f *toolFixture) tip() string { return skillGit(f.t, f.tool.work, "rev-parse", "HEAD") }

func (f *toolFixture) body(extra ...string) map[string]string {
	b := map[string]string{"scope": "fleet", "source": toolSource}
	for i := 0; i+1 < len(extra); i += 2 {
		b[extra[i]] = extra[i+1]
	}
	return b
}

func (f *toolFixture) nothingInstalled(why string) {
	f.t.Helper()
	skillGone(f.t, f.h.home, "tool-skill")
	if _, err := os.Stat(fleetLock(f.h.home)); !os.IsNotExist(err) {
		f.t.Fatalf("%s: a lock was written (%v)", why, err)
	}
	if _, err := os.Stat(filepath.Join(f.h.home, "skills.json")); !os.IsNotExist(err) {
		f.t.Fatalf("%s: a subscription was written (%v)", why, err)
	}
	if left := tempLeft(f.t, f.tmp); len(left) != 0 {
		f.t.Fatalf("%s: temp folders left: %q", why, left)
	}
}

func (f *toolFixture) review(extra ...string) board.APISkillsRun {
	f.t.Helper()
	code, run, receipt, raw := f.api.post("add", f.body(extra...))
	if code != 200 || receipt != "Skills add: 1 review" || run.Refused != 0 || len(run.Results) != 1 || len(run.Diffs) != 1 {
		f.t.Fatalf("review = %d %q %s", code, receipt, raw)
	}
	return run
}

type gitWrap struct{ log, subst string }

func wrapGit(t *testing.T) *gitWrap {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a shell wrapper around git")
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	w := &gitWrap{log: filepath.Join(t.TempDir(), "git.log")}
	w.subst = filepath.Join(t.TempDir(), "subst")
	bin := t.TempDir()
	script := "#!/bin/bash\nprintf '%s\\n' \"$*\" >> '" + w.log + "'\nlast=\"${@: -1}\"\nif [ -f '" + w.subst + "' ]; then\n  from=$(cut -d' ' -f1 '" + w.subst + "'); to=$(cut -d' ' -f2 '" + w.subst + "')\n  if [ \"$last\" = \"$from\" ]; then set -- \"${@:1:$#-1}\" \"$to\"; fi\nfi\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return w
}

func (w *gitWrap) fetches() []string {
	b, _ := os.ReadFile(w.log)
	var out []string
	for line := range strings.Lines(string(b)) {
		if strings.Contains(line, " fetch ") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func TestSkillsAPIThirdPartyAddWithoutYesInstallsNothingAndReturnsTheReview(t *testing.T) {
	f := newToolFixture(t)
	head := f.tip()
	run := f.review()
	r := resultOf(run, "tool-skill")
	if r.State != "review" || r.Detail != head || len(head) != 40 {
		t.Fatalf("row = %+v, want the whole commit %s", r, head)
	}
	d := run.Diffs[0]
	if d.Name != "tool-skill" || d.Cut || !strings.Contains(d.Diff, "new file mode") || !strings.Contains(d.Diff, "+name: tool-skill") || !strings.Contains(d.Diff, "+v1") {
		t.Fatalf("diff = %+v", d)
	}
	f.nothingInstalled("a review")
	again := f.review()
	if resultOf(again, "tool-skill").Detail != head {
		t.Fatalf("a second review = %+v", again)
	}
	f.nothingInstalled("a second review")
}

func TestSkillsAPIThirdPartyAddWithTheRightYesInstallsThatCommitUntrusted(t *testing.T) {
	f := newToolFixture(t)
	wrap := wrapGit(t)
	head := f.tip()
	review := f.review()
	yes := "tool-skill@" + resultOf(review, "tool-skill").Detail
	code, run, receipt, raw := f.api.post("add", f.body("yes", yes))
	if code != 200 || receipt != "Skills add: 1 added" || run.Refused != 0 || len(run.Diffs) != 0 {
		t.Fatalf("add = %d %q %s", code, receipt, raw)
	}
	if r := resultOf(run, "tool-skill"); r.State != "added" || r.Detail != head {
		t.Fatalf("row = %+v", r)
	}
	skillThere(t, f.h.home, "tool-skill")
	e := skillState(t, f.h.home)["tool-skill"]
	if e.Commit != head || e.Ref != head || e.ComputedHash == "" {
		t.Fatalf("lock = %+v, want pinned to %s", e, head)
	}
	if !strings.Contains(subscribed(t, f.h.home), `"tool-skill"`) {
		t.Fatalf("skills.json = %s", subscribed(t, f.h.home))
	}
	fetched := wrap.fetches()
	if s := f.api.skill("fleet", "", "tool-skill"); !s.Managed || s.Trusted || s.State != "current" || s.Commit != head || s.Ref != head {
		t.Fatalf("list = %+v", s)
	}
	byID := 0
	for _, line := range fetched {
		if strings.HasSuffix(line, " "+head) {
			byID++
		}
	}
	if byID == 0 {
		t.Fatalf("no fetch asked for the whole commit %s: %q", head, fetched)
	}
	for _, line := range fetched {
		if !strings.Contains(line, "protocol.http.allow=never") {
			t.Errorf("a fetch may follow a redirect to plain http: %s", line)
		}
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
	code, _, _, raw = f.api.post("add", f.body("yes", yes))
	if code != http.StatusConflict || !strings.Contains(raw, "already exists") {
		t.Fatalf("a second add = %d %s", code, raw)
	}
}

func TestSkillsAPIThirdPartyAddAtARefReviewsThatRefsCommitAndPinsIt(t *testing.T) {
	f := newToolFixture(t)
	old := f.tip()
	skillGit(t, f.tool.bare, "update-ref", "refs/tags/v1", old)
	f.tool.commit(skillFiles("tool-skill", "v2"))
	run := f.review("ref", "v1")
	if r := resultOf(run, "tool-skill"); r.Detail != old || !strings.Contains(run.Diffs[0].Diff, "+v1") || strings.Contains(run.Diffs[0].Diff, "+v2") {
		t.Fatalf("review at v1 = %+v", run)
	}
	code, run, _, raw := f.api.post("add", f.body("ref", "v1", "yes", "tool-skill@"+old))
	if code != 200 || resultOf(run, "tool-skill").State != "added" {
		t.Fatalf("add = %d %s", code, raw)
	}
	if e := skillState(t, f.h.home)["tool-skill"]; e.Commit != old || e.Ref != old {
		t.Fatalf("lock = %+v", e)
	}
	if got := readSkill(t, skillPath(f.h.home, ".claude", "tool-skill")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("copy = %q", got)
	}
}

func TestSkillsAPIThirdPartyAddRefusesAYesThatIsNotTheCommitItFetched(t *testing.T) {
	f := newToolFixture(t)
	first := f.tip()
	moved := f.tool.commit(skillFiles("tool-skill", "v2"))
	for _, yes := range []string{
		"tool-skill",
		"tool-skill@",
		"@" + moved,
		moved,
		"tool-skill@" + moved[:12],
		"tool-skill@" + moved[:39],
		"tool-skill@" + moved + "0",
		"tool-skill@" + strings.ToUpper(moved),
		"other@" + moved,
		"tool-skill@" + first,
		"tool-skill@" + strings.Repeat("0", 40),
		"tool-skill @" + moved,
	} {
		code, run, receipt, raw := f.api.post("add", f.body("yes", yes))
		if code != 200 || receipt != "Skills add: 1 refused" || run.Refused != 1 || len(run.Results) != 1 || len(run.Diffs) != 1 {
			t.Fatalf("yes %q = %d %q %s", yes, code, receipt, raw)
		}
		if r := resultOf(run, "tool-skill"); r.State != "refused" || strings.Contains(r.Detail, "\n") || strings.Contains(r.Detail, "NAME@COMMIT") != (!strings.Contains(yes, "@") || strings.HasSuffix(yes, "@") || strings.HasPrefix(yes, "@")) {
			t.Fatalf("yes %q row = %+v", yes, r)
		}
		if !strings.Contains(run.Diffs[0].Diff, "+v2") {
			t.Fatalf("yes %q: the new diff is %q", yes, run.Diffs[0].Diff)
		}
		f.nothingInstalled("yes " + yes)
	}
}

func TestSkillsAPIThirdPartyAddRefusesAYesWhoseCommitMovedBetweenTheCalls(t *testing.T) {
	f := newToolFixture(t)
	reviewed := resultOf(f.review(), "tool-skill").Detail
	moved := f.tool.commit(skillFiles("tool-skill", "v2"))
	code, run, _, raw := f.api.post("add", f.body("yes", "tool-skill@"+reviewed))
	if code != 200 || run.Refused != 1 || resultOf(run, "tool-skill").State != "refused" || !strings.Contains(run.Diffs[0].Diff, "+v2") || !strings.Contains(resultOf(run, "tool-skill").Detail, moved) {
		t.Fatalf("a stale yes = %d %s", code, raw)
	}
	f.nothingInstalled("a stale yes")
	run = f.api.ok("add", f.body("yes", "tool-skill@"+moved))
	if r := resultOf(run, "tool-skill"); r.State != "added" || r.Detail != moved {
		t.Fatalf("the fresh yes = %+v", run)
	}
	if got := readSkill(t, skillPath(f.h.home, ".claude", "tool-skill")); !strings.Contains(got, "\nv2\n") {
		t.Fatalf("copy = %q", got)
	}
}

func TestSkillsAPIThirdPartyAddCatchesASourceThatAnswersAnotherCommit(t *testing.T) {
	f := newToolFixture(t)
	wrap := wrapGit(t)
	old := f.tip()
	moved := f.tool.commit(skillFiles("tool-skill", "v2"))
	if err := os.WriteFile(wrap.subst, []byte(moved+" "+old), 0o644); err != nil {
		t.Fatal(err)
	}
	code, run, _, raw := f.api.post("add", f.body("yes", "tool-skill@"+moved))
	if code != http.StatusConflict || !strings.Contains(raw, "not the reviewed "+moved) || len(run.Results) != 0 {
		t.Fatalf("a source answering another commit = %d %s", code, raw)
	}
	f.nothingInstalled("a mismatching commit")
}

func TestSkillsAPIThirdPartyAddNeverTakesAnOwnRepositoryURLForTheBareName(t *testing.T) {
	f := newToolFixture(t)
	wrap := wrapGit(t)
	own := newSkillRemote(t, filepath.Join(t.TempDir(), "own.git"), skillFiles("skills", "v1"))
	f.route("https://github.com/atqamz/skills", own.bare)
	for _, src := range []string{"https://github.com/atqamz/skills", "https://github.com/atqamz/skills/", "https://github.com/atqamz/skills.git"} {
		for _, yes := range []string{"", "skills@" + strings.Repeat("a", 40)} {
			code, _, _, raw := f.api.post("add", map[string]string{"scope": "fleet", "source": src, "yes": yes})
			if code != http.StatusBadRequest || !strings.Contains(raw, "bare name") || strings.Contains(raw, "atqamz") {
				t.Fatalf("source %q yes %q = %d %s", src, yes, code, raw)
			}
		}
	}
	if got := wrap.fetches(); len(got) != 0 {
		t.Fatalf("an own repository URL was fetched: %q", got)
	}
	skillGone(t, f.h.home, "skills")
	if _, err := os.Stat(fleetLock(f.h.home)); !os.IsNotExist(err) {
		t.Fatalf("an own repository URL wrote a lock: %v", err)
	}
	code, _, _, raw := f.api.post("add", map[string]string{"scope": "fleet", "source": "triage", "yes": "triage@" + f.proposeFixture.head})
	if code != http.StatusBadRequest || !strings.Contains(raw, "only with a third-party source") {
		t.Fatalf("a yes on a bare name = %d %s", code, raw)
	}
	skillGone(t, f.h.home, "triage")
}

func TestSkillsAPIThirdPartyAddRefusesEverySourceButAPlainHTTPSRepositoryBeforeAnyFetch(t *testing.T) {
	f := newToolFixture(t)
	wrap := wrapGit(t)
	long := "https://example.com/" + strings.Repeat("a", 520)
	for _, src := range []string{
		"https://pat:s3cret@github.com/acme/tool.git",
		"https://pat@github.com/acme/tool",
		"https://@github.com/acme/tool",
		"https://github.com/acme/tool?x=1",
		"https://github.com/acme/tool?",
		"https://github.com/acme/tool#frag",
		"https://github.com/acme/tool#",
		"https://github.com/acme/tool@v1",
		"http://github.com/acme/tool",
		"HTTPS://github.com/acme/tool",
		"https:github.com/acme/tool",
		"https:///acme/tool",
		"https://github.com",
		"https://github.com/",
		"https://github.com//tool",
		"https://github.com/acme//tool",
		"https://github.com/acme/../tool",
		"https://github.com/acme/./tool",
		"https://github.com/acme/tool/..",
		"https://github.com:/acme/tool",
		"https://github.com:abc/acme/tool",
		"https://github.com:123456/acme/tool",
		"https://[::1]/acme/tool",
		"https://-github.com/acme/tool",
		"https://git hub.com/acme/tool",
		"https://github.com/acme/to ol",
		"https://github.com/acme/to%6Fl",
		`https://github.com\acme\tool`,
		`https://github.com/acme\tool`,
		"https://github.com/acme/to\tol",
		"https://github.com/acme/to\nol",
		"https://github.com/acme/tool\x00",
		"https://github.com/acme/toöl",
		"file://" + filepath.ToSlash(f.tool.bare),
		"git@github.com:acme/tool.git",
		"ssh://git@github.com/acme/tool.git",
		"git://github.com/acme/tool",
		"ftp://github.com/acme/tool",
		"ext::sh -c id",
		"fd::17/acme/tool",
		"./triage",
		"../triage",
		"/abs/triage",
		"skills/triage",
		`a\b`,
		"tri:age",
		"-triage",
		"--upload-pack=x",
		".hidden",
		"triage.git",
		"a b",
		"https://github.com/acme/.git",
		long,
	} {
		code, _, _, raw := f.api.post("add", map[string]string{"scope": "fleet", "source": src})
		if code != http.StatusBadRequest {
			t.Errorf("source %q = %d %s", src, code, raw)
		}
		for _, leak := range []string{"s3cret", "ext::", "acme", "fd::", "pat:"} {
			if strings.Contains(raw, leak) {
				t.Errorf("source %q: %q is echoed: %s", src, leak, raw)
			}
		}
	}
	if got := wrap.fetches(); len(got) != 0 {
		t.Fatalf("a refused source was fetched: %q", got)
	}
	f.nothingInstalled("a refused source")
}

func TestSkillsAPIThirdPartyAddInAProjectEditsTheCheckoutAndCommitsNothing(t *testing.T) {
	f := newBumpFixture(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	f.route(toolSource, tool.bare)
	before := gitIn(t, f.repo, "rev-list", "--count", "HEAD")
	a := newSkillsAPI(t, f.ph)
	body := map[string]string{"scope": "project", "project": "app", "source": toolSource}
	run := a.ok("add", body)
	head := skillGit(t, tool.work, "rev-parse", "HEAD")
	if r := resultOf(run, "tool-skill"); r.State != "review" || r.Detail != head {
		t.Fatalf("review = %+v", run)
	}
	if _, err := os.Stat(filepath.Join(f.repo, ".agents", "skills", "tool-skill")); !os.IsNotExist(err) {
		t.Fatalf("a review wrote into the checkout: %v", err)
	}
	if st := gitIn(t, f.repo, "status", "--porcelain"); st != "" {
		t.Fatalf("a review left %q", st)
	}
	body["yes"] = "tool-skill@" + head
	if r := resultOf(a.ok("add", body), "tool-skill"); r.State != "added" || r.Detail != head {
		t.Fatalf("add = %+v", r)
	}
	if !strings.Contains(f.skillText("tool-skill"), "tool-skill") {
		t.Fatal("the checkout holds no copy")
	}
	if got := gitIn(t, f.repo, "rev-list", "--count", "HEAD"); got != before {
		t.Fatalf("commits %s -> %s", before, got)
	}
	if s := a.skill("project", "app", "tool-skill"); s.Trusted || s.Commit != head {
		t.Fatalf("list = %+v", s)
	}
	other := newSkillRemote(t, filepath.Join(t.TempDir(), "other-tool.git"), skillFiles("other-tool", "v1"))
	f.route("https://example.com/acme/other-tool", other.bare)
	writeFiles(t, filepath.Join(f.repo, ".agents", "skills", "other-tool"), map[string]string{"notes.md": "dirty\n"})
	code, _, _, raw := a.post("add", map[string]string{"scope": "project", "project": "app", "source": "https://example.com/acme/other-tool"})
	if code != http.StatusConflict || !strings.Contains(raw, "uncommitted changes") {
		t.Fatalf("a dirty folder = %d %s", code, raw)
	}
}

func TestSkillsAPIThirdPartyAddInTheUserScope(t *testing.T) {
	f := newToolFixture(t)
	home := f.h.vars["HOME"]
	run := f.api.ok("add", f.body("scope", "user"))
	head := f.tip()
	if r := resultOf(run, "tool-skill"); r.State != "review" || r.Detail != head || run.Scope != "user" {
		t.Fatalf("review = %+v", run)
	}
	skillGone(t, home, "tool-skill")
	run = f.api.ok("add", f.body("scope", "user", "yes", "tool-skill@"+head))
	if resultOf(run, "tool-skill").State != "added" {
		t.Fatalf("add = %+v", run)
	}
	skillThere(t, home, "tool-skill")
	if e := lockEntries(t, userLock(home))["tool-skill"]; e.Commit != head {
		t.Fatalf("user lock = %+v", e)
	}
	skillGone(t, f.h.home, "tool-skill")
}

func tlsServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	s := httptest.NewUnstartedServer(h)
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func TestSkillsAPIThirdPartyAddDoesNotHangOnACredentialPrompt(t *testing.T) {
	f := newToolFixture(t)
	var hits atomic.Int32
	srv := tlsServer(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("WWW-Authenticate", `Basic realm="private"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	src := srv.URL + "/acme/private-tool"
	start := time.Now()
	code, _, _, raw := f.api.post("add", map[string]string{"scope": "fleet", "source": src})
	if d := time.Since(start); d > 30*time.Second {
		t.Fatalf("the add waited %v", d)
	}
	if code != http.StatusInternalServerError || hits.Load() == 0 || !strings.Contains(raw, "terminal prompts disabled") {
		t.Fatalf("a private source = %d (%d hits) %s", code, hits.Load(), raw)
	}
	skillGone(t, f.h.home, "private-tool")
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPIThirdPartyAddFollowsAnHTTPSRedirectButNotOneToPlainHTTP(t *testing.T) {
	f := newToolFixture(t)
	var plain, secure atomic.Int32
	insecure := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plain.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(insecure.Close)
	target := tlsServer(t, func(w http.ResponseWriter, _ *http.Request) {
		secure.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	to := &atomic.Pointer[string]{}
	origin := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, *to.Load()+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(origin.URL, "https://"))
	src := "https://127.0.0.1:" + port + "/acme/moved-tool"
	for _, tc := range []struct {
		to   string
		hits *atomic.Int32
		want bool
	}{{target.URL, &secure, true}, {insecure.URL, &plain, false}} {
		to.Store(&tc.to)
		code, _, _, raw := f.api.post("add", map[string]string{"scope": "fleet", "source": src})
		if code != http.StatusInternalServerError {
			t.Fatalf("a redirect to %s = %d %s", tc.to, code, raw)
		}
		if got := tc.hits.Load() > 0; got != tc.want {
			t.Fatalf("a redirect to %s reached its target = %v, want %v (%s)", tc.to, got, tc.want, raw)
		}
	}
	skillGone(t, f.h.home, "moved-tool")
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsAPIThirdPartyAddStopsAtTheFetchBudgetAndLeavesNothing(t *testing.T) {
	cli.SetSkillFetchLimits(t, 400*time.Millisecond, 600*time.Millisecond)
	f := newToolFixture(t)
	const hung = "https://example.com/acme/hung-tool"
	f.routeTo(hung, hungRemote(t))
	for _, yes := range []string{"", "hung-tool@" + strings.Repeat("a", 40)} {
		start := time.Now()
		code, _, _, raw := f.api.post("add", map[string]string{"scope": "fleet", "source": hung, "yes": yes})
		if d := time.Since(start); d > 20*time.Second {
			t.Fatalf("the add took %v", d)
		}
		if code != http.StatusInternalServerError || !strings.Contains(raw, "limit") {
			t.Fatalf("a hung source (yes %q) = %d %s", yes, code, raw)
		}
		skillGone(t, f.h.home, "hung-tool")
		if left := tempLeft(t, f.tmp); runtime.GOOS != "windows" && len(left) != 0 {
			t.Fatalf("temp folders left: %q", left)
		}
	}
}

func (f *proposeFixture) routeTo(from, to string) {
	f.t.Helper()
	text, err := os.ReadFile(f.cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	text = append(text, "[url \""+to+"\"]\n\tinsteadOf = "+from+"\n"...)
	if err := os.WriteFile(f.cfg, text, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *toolFixture) commitModes(files map[string]string, exec ...string) {
	f.t.Helper()
	writeFiles(f.t, f.tool.work, files)
	for _, rel := range exec {
		if err := os.Chmod(filepath.Join(f.tool.work, rel), 0o755); err != nil {
			f.t.Fatal(err)
		}
	}
	f.tool.commit(nil)
}

func TestSkillsAPIThirdPartyAddListsEveryFileAheadOfTheDiff(t *testing.T) {
	f := newToolFixture(t)
	f.commitModes(map[string]string{"run.sh": "#!/bin/sh\necho hi\n", "data/blob.bin": "a\x00b\n"}, "run.sh")
	d := f.review().Diffs[0]
	if d.Cut || !strings.HasPrefix(d.Diff, "files of this skill") {
		t.Fatalf("diff = %q", d.Diff)
	}
	head, _, _ := strings.Cut(d.Diff, "diff --git")
	runMode := "100755"
	if runtime.GOOS == "windows" {
		runMode = "100644"
	}
	for _, want := range []string{runMode + " 18 run.sh\n", "100644 4 data/blob.bin Binary file\n", " SKILL.md\n"} {
		if !strings.Contains(head, want) {
			t.Errorf("file list lacks %q: %q", want, head)
		}
	}
	if strings.Contains(head, "run.sh Binary") || strings.Contains(head, "SKILL.md Binary") {
		t.Errorf("a text file is marked binary: %q", head)
	}
}

func TestSkillsAPIThirdPartyAddRefusesYesWhenTheReviewDiffIsCut(t *testing.T) {
	f := newToolFixture(t)
	f.commitModes(map[string]string{
		"a/huge.txt": strings.Repeat("x", 70<<10) + "\n",
		"blob.bin":   "\x00\x01\x02 binary\n",
		"run.sh":     "#!/bin/sh\n",
		"z/late.md":  "late\n",
	}, "run.sh")
	review := f.review()
	d := review.Diffs[0]
	if !d.Cut || len(d.Diff) > 64<<10 {
		t.Fatalf("the review diff is cut = %v at %d bytes, want cut at no more than 64 KiB", d.Cut, len(d.Diff))
	}
	if strings.Contains(d.Diff, "late.md\n+late") {
		t.Fatalf("the text past the cut is shown")
	}
	runMode := "100755"
	if runtime.GOOS == "windows" {
		runMode = "100644"
	}
	for _, want := range []string{"100644 ", " blob.bin Binary file\n", runMode + " 10 run.sh\n", " z/late.md\n", " a/huge.txt\n"} {
		if !strings.Contains(d.Diff, want) {
			t.Errorf("file list lacks %q", want)
		}
	}
	yes := "tool-skill@" + resultOf(review, "tool-skill").Detail
	code, run, receipt, raw := f.api.post("add", f.body("yes", yes))
	if code != 200 || receipt != "Skills add: 1 refused" || run.Refused != 1 || len(run.Diffs) != 1 || !run.Diffs[0].Cut {
		t.Fatalf("add = %d %q %s", code, receipt, raw)
	}
	r := resultOf(run, "tool-skill")
	if r.State != "refused" || !strings.Contains(r.Detail, "the diff is cut, so the whole skill cannot be reviewed here; install it by hand after reading the repository") {
		t.Fatalf("row = %+v", r)
	}
	f.nothingInstalled("a cut review with the matching yes")
}

func TestSkillsAPIThirdPartyAddWithASymlinkAnswersAClientErrorAndLeavesNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git does not check out a symlink on Windows")
	}
	f := newToolFixture(t)
	if err := os.MkdirAll(filepath.Join(f.tool.work, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(f.tool.work, "docs", "link")); err != nil {
		t.Skip(err)
	}
	f.tool.commit(nil)
	for _, yes := range []string{"", "tool-skill@" + f.tip()} {
		code, _, _, raw := f.api.post("add", f.body("yes", yes))
		if code < 400 || code >= 500 || !strings.Contains(raw, "docs/link is not a regular file") || strings.Contains(raw, f.tmp) {
			t.Fatalf("a skill with a symlink (yes %q) = %d %s", yes, code, raw)
		}
		f.nothingInstalled("a symlink")
	}
}

func TestSkillsAPIThirdPartyAddOverTheSizeLimitAnswersAClientErrorNamingTheFile(t *testing.T) {
	f := newToolFixture(t)
	big, err := os.Create(filepath.Join(f.tool.work, "blob.dat"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(fleet.MaxSkillBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = big.Close()
	f.tool.commit(nil)
	code, _, _, raw := f.api.post("add", f.body())
	if code < 400 || code >= 500 || !strings.Contains(raw, "blob.dat takes a skill over 32 MiB") || strings.Contains(raw, f.tmp) {
		t.Fatalf("an oversize skill = %d %s", code, raw)
	}
	f.nothingInstalled("an oversize skill")
}
