package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/fleet"
)

const appURL = "https://github.com/acme/app.git"

type bumpFixture struct {
	*proposeFixture
	ph     *harness
	repo   string
	origin string
	main   string
}

func newBumpFixture(t *testing.T) *bumpFixture {
	t.Helper()
	f := &bumpFixture{proposeFixture: newProposeFixture(t, "")}
	f.ph, f.repo = projectHarness(t)
	f.ph.vars["PATH"] = f.bin
	f.origin = gitIn(t, f.repo, "config", "--get", "remote.origin.url")
	gitIn(t, f.repo, "remote", "set-url", "origin", appURL)
	f.route(appURL, f.origin)
	return f
}

func (f *bumpFixture) install(names ...string) {
	f.t.Helper()
	for _, n := range names {
		f.ph.ok("skills", "add", n)
	}
	f.publish("skills")
}

func (f *bumpFixture) publish(msg string) {
	f.t.Helper()
	gitIn(f.t, f.repo, "add", "-A")
	gitIn(f.t, f.repo, "commit", "-q", "-m", msg)
	gitIn(f.t, f.repo, "push", "-q", "origin", "HEAD:main")
	f.main = f.originRefs()["refs/heads/main"]
}

func (f *bumpFixture) originRefs() map[string]string {
	f.t.Helper()
	got := map[string]string{}
	for line := range strings.Lines(skillGit(f.t, f.origin, "for-each-ref", "--format=%(refname) %(objectname)")) {
		name, id, _ := strings.Cut(strings.TrimSpace(line), " ")
		got[name] = id
	}
	return got
}

func (f *bumpFixture) branches(skill string) []string {
	var out []string
	for name := range f.originRefs() {
		if strings.HasPrefix(name, "refs/heads/hand-skills/bump-"+skill+"-") {
			out = append(out, strings.TrimPrefix(name, "refs/heads/"))
		}
	}
	slices.Sort(out)
	return out
}

func (f *bumpFixture) ghArgs() [][]string {
	f.t.Helper()
	b, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var all [][]string
	for line := range strings.Lines(string(b)) {
		var argv []string
		if err := json.Unmarshal([]byte(line), &argv); err != nil {
			f.t.Fatalf("gh log %q: %v", line, err)
		}
		all = append(all, argv)
	}
	return all
}

func (f *bumpFixture) quiet(why string) {
	f.t.Helper()
	if refs := f.originRefs(); len(refs) != 1 || refs["refs/heads/main"] != f.main {
		f.t.Fatalf("%s: the origin moved: %v", why, refs)
	}
	if len(f.ghArgs()) != 0 {
		f.t.Fatalf("%s: gh ran: %v", why, f.ghArgs())
	}
	if left := tempLeft(f.t, f.tmp); len(left) != 0 {
		f.t.Fatalf("%s: temp folders left: %q", why, left)
	}
}

func (f *bumpFixture) skillText(name string) string {
	return readSkill(f.t, skillPath(f.repo, ".agents", name))
}

func TestSkillsSyncInAProjectOpensABumpPullRequestAndLeavesTheCheckoutAlone(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage")
	old := lockEntries(t, fleetLock(f.repo))["triage"]
	moved := f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	writeFiles(t, f.repo, map[string]string{"notes.txt": "mine\n"})
	lockBefore := readSkill(t, fleetLock(f.repo))
	out := f.ph.ok("skills", "sync")
	if !strings.Contains(out, "triage,bumped,\""+prURL+"\"") {
		t.Fatalf("sync = %q", out)
	}
	branches := f.branches("triage")
	if len(branches) != 1 || !regexp.MustCompile(`^hand-skills/bump-triage-[0-9a-f]{8}$`).MatchString(branches[0]) {
		t.Fatalf("branches = %v", branches)
	}
	branch := branches[0]
	if refs := f.originRefs(); len(refs) != 2 || refs["refs/heads/main"] != f.main {
		t.Fatalf("refs = %v", refs)
	}
	title := "skills: triage " + old.Commit[:12] + " -> " + moved[:12]
	if got := skillGit(t, f.origin, "log", "-1", "--format=%an|%ae|%P|%B", branch); got != "Pat Author|pat@example.com|"+f.main+"|"+title {
		t.Fatalf("commit = %q", got)
	}
	if got := skillGit(t, f.origin, "diff", "--name-only", "main", branch); got != ".agents/skills/triage/SKILL.md\nskills-lock.json" {
		t.Fatalf("changed files = %q", got)
	}
	shown := skillGit(t, f.origin, "show", branch+":.agents/skills/triage/SKILL.md")
	if !strings.Contains(shown, "\nv2") || !strings.Contains(shown, "managed-by: hand") {
		t.Fatalf("the branch's copy = %q", shown)
	}
	var lock struct{ Skills map[string]fleet.LockEntry }
	if err := json.Unmarshal([]byte(skillGit(t, f.origin, "show", branch+":skills-lock.json")), &lock); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	skillGit(t, dir, "init", "-q")
	skillGit(t, dir, "fetch", "-q", f.origin, "refs/heads/"+branch)
	skillGit(t, dir, "checkout", "-q", "FETCH_HEAD")
	sum, err := fleet.ComputedHash(filepath.Join(dir, ".agents", "skills", "triage"))
	if err != nil || lock.Skills["triage"].Commit != moved || lock.Skills["triage"].ComputedHash != sum {
		t.Fatalf("the branch's lock = %+v, copy sum %s, %v", lock.Skills["triage"], sum, err)
	}
	argv := f.ghArgs()
	if len(argv) != 1 || len(argv[0]) != 12 || strings.Join(argv[0][:9], " ") != "pr create --repo acme/app --head "+branch+" --base main --title" || argv[0][9] != title || argv[0][10] != "--body-file" || argv[0][11] != "-" {
		t.Fatalf("gh argv = %q", argv)
	}
	bodies := ghBodies(t, f.log)
	if len(bodies) != 1 {
		t.Fatalf("bodies = %q", bodies)
	}
	body := bodies[0]
	for _, want := range []string{title + "\n", "Source: " + skillsURL + "#skills/triage, ref main", "```diff\n", "-v1\n", "+v2\n", "skills-lock.json", "normal gate"} {
		if !strings.Contains(body, want) {
			t.Errorf("the body lacks %q: %q", want, body)
		}
	}
	for _, private := range []string{f.ph.home, f.ph.vars["HOME"], f.tmp, f.origin} {
		if strings.Contains(body, private) || strings.Contains(out, private) {
			t.Errorf("the output carries the private path %q", private)
		}
	}
	if !strings.Contains(f.skillText("triage"), "\nv1\n") || readSkill(t, fleetLock(f.repo)) != lockBefore {
		t.Fatal("the checkout's copy or lock changed")
	}
	if got := gitIn(t, f.repo, "status", "--porcelain"); got != "?? notes.txt" {
		t.Fatalf("status = %q", got)
	}
	if len(backups(t, filepath.Join(f.repo, ".git", "hand-skills", "backups"), "triage")) != 0 {
		t.Fatal("a backup without an apply")
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsSyncInAProjectSendsTheSameUpdateOnce(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage")
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	f.ph.ok("skills", "sync")
	branch := f.branches("triage")[0]
	out, errOut, code := f.ph.run("skills", "sync")
	if code != 0 || !strings.Contains(out, "triage,pending,") || !strings.Contains(out, branch) || strings.Contains(out, "bumped") {
		t.Fatalf("repeat: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if len(f.ghArgs()) != 1 || len(f.branches("triage")) != 1 {
		t.Fatalf("a repeat opened another pull request: %v %v", f.ghArgs(), f.branches("triage"))
	}
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v3")["SKILL.md"]})
	if out := f.ph.ok("skills", "sync"); !strings.Contains(out, "triage,bumped,") {
		t.Fatalf("a newer update: %q", out)
	}
	if len(f.branches("triage")) != 2 || len(f.ghArgs()) != 2 {
		t.Fatalf("branches %v, gh %v", f.branches("triage"), f.ghArgs())
	}
}

func TestSkillsSyncInAProjectKnowsWhenTheUpdateIsMergedAndNotPulled(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage")
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	f.ph.ok("skills", "sync")
	branch := f.branches("triage")[0]
	skillGit(t, f.origin, "update-ref", "refs/heads/main", "refs/heads/"+branch)
	skillGit(t, f.origin, "update-ref", "-d", "refs/heads/"+branch)
	out, errOut, code := f.ph.run("skills", "sync")
	if code != 0 || !strings.Contains(out, "triage,pending,") || !strings.Contains(out, "already holds this update; pull the checkout") {
		t.Fatalf("merged: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if len(f.ghArgs()) != 1 || len(f.branches("triage")) != 0 {
		t.Fatalf("gh %v, branches %v", f.ghArgs(), f.branches("triage"))
	}
	gitIn(t, f.repo, "pull", "-q", "--ff-only", "origin", "main")
	if out := f.ph.ok("skills", "outdated"); !strings.HasPrefix(row(out, "triage"), "triage,current,") {
		t.Fatalf("after the pull = %q", out)
	}
}

func TestSkillsSyncInAProjectOpensOneFreshPullRequestPerSkill(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage", "other")
	f.hub.commit(map[string]string{
		"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"],
		"skills/other/SKILL.md":  skillFiles("other", "v2")["SKILL.md"],
	})
	out := f.ph.ok("skills", "sync")
	if !strings.Contains(out, "other,bumped,") || !strings.Contains(out, "triage,bumped,") {
		t.Fatalf("sync = %q", out)
	}
	for skill, other := range map[string]string{"triage": "other", "other": "triage"} {
		bs := f.branches(skill)
		if len(bs) != 1 {
			t.Fatalf("%s: branches = %v", skill, bs)
		}
		if parent := skillGit(t, f.origin, "log", "-1", "--format=%P", bs[0]); parent != f.main {
			t.Errorf("%s: the branch is not cut from the default branch: %s", skill, parent)
		}
		files := skillGit(t, f.origin, "diff", "--name-only", "main", bs[0])
		if strings.Contains(files, "/"+other+"/") || !strings.Contains(files, "/"+skill+"/SKILL.md") {
			t.Errorf("%s: changed files = %q", skill, files)
		}
		var lock struct{ Skills map[string]fleet.LockEntry }
		if err := json.Unmarshal([]byte(skillGit(t, f.origin, "show", bs[0]+":skills-lock.json")), &lock); err != nil {
			t.Fatal(err)
		}
		if lock.Skills[other].Commit != f.head || lock.Skills[skill].Commit == f.head {
			t.Errorf("%s: lock = %+v", skill, lock.Skills)
		}
	}
	if len(f.ghArgs()) != 2 {
		t.Fatalf("gh = %v", f.ghArgs())
	}
}

func TestSkillsSyncInAProjectKeepsTheThirdPartyYes(t *testing.T) {
	f := newBumpFixture(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	f.ph.ok("skills", "add", "--ref", "main", "file://"+filepath.ToSlash(tool.bare))
	f.publish("skills")
	moved := tool.commit(skillFiles("tool-skill", "v2"))
	out := f.ph.ok("skills", "sync")
	if !strings.Contains(out, "tool-skill,behind,") || !strings.Contains(out, "+v2") || !strings.Contains(out, "sync --yes tool-skill@"+moved) {
		t.Fatalf("sync = %q", out)
	}
	f.quiet("third party without a yes")
	out = f.ph.ok("skills", "sync", "--yes", "tool-skill@"+moved)
	if !strings.Contains(out, "tool-skill,bumped,") {
		t.Fatalf("sync --yes = %q", out)
	}
	if len(f.branches("tool-skill")) != 1 || len(f.ghArgs()) != 1 {
		t.Fatalf("branches %v, gh %v", f.branches("tool-skill"), f.ghArgs())
	}
	if !strings.Contains(f.skillText("tool-skill"), "\nv1\n") {
		t.Fatal("the checkout moved")
	}
}

func TestSkillsSyncInAProjectLeavesAnEditedOrConflictingCopyOutOfAPullRequest(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage", "other")
	for _, name := range []string{"triage", "other"} {
		path := skillPath(f.repo, ".agents", name)
		if err := os.WriteFile(path, []byte(readSkill(t, path)+"\nmine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.hub.commit(map[string]string{"skills/other/SKILL.md": skillFiles("other", "v2")["SKILL.md"]})
	out, _, code := f.ph.run("skills", "sync")
	if code != 3 || !strings.Contains(out, "triage,edited,") || !strings.Contains(out, "other,conflict,") {
		t.Fatalf("sync: code=%d out=%q", code, out)
	}
	f.quiet("edited and conflict")
}

func TestSkillsSyncInAProjectRefusals(t *testing.T) {
	step := func(t *testing.T, f *bumpFixture, want string) {
		t.Helper()
		out, errOut, code := f.ph.run("skills", "sync")
		if code != 3 || !strings.Contains(out, "triage,refused,") || !strings.Contains(out+errOut, want) {
			t.Fatalf("code=%d stdout=%q stderr=%q, want %q", code, out, errOut, want)
		}
		f.quiet(want)
		if !strings.Contains(f.skillText("triage"), "\nv1\n") {
			t.Fatal("the checkout changed")
		}
	}
	moveHub := func(f *bumpFixture) {
		f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	}
	t.Run("origin is not GitHub", func(t *testing.T) {
		f := newBumpFixture(t)
		f.install("triage")
		moveHub(f)
		gitIn(t, f.repo, "remote", "set-url", "origin", f.origin)
		step(t, f, "not a GitHub repository")
	})
	t.Run("no origin", func(t *testing.T) {
		f := newBumpFixture(t)
		f.install("triage")
		moveHub(f)
		gitIn(t, f.repo, "remote", "remove", "origin")
		step(t, f, "not a GitHub repository")
	})
	t.Run("no gh", func(t *testing.T) {
		f := newBumpFixture(t)
		f.install("triage")
		moveHub(f)
		f.ph.vars["PATH"] = t.TempDir()
		step(t, f, "gh pr create")
	})
	t.Run("the default branch lacks the skill", func(t *testing.T) {
		f := newBumpFixture(t)
		f.ph.ok("skills", "add", "triage")
		f.main = f.originRefs()["refs/heads/main"]
		moveHub(f)
		step(t, f, "does not hold the copy of triage this checkout has")
	})
	t.Run("the default branch holds another copy", func(t *testing.T) {
		f := newBumpFixture(t)
		f.install("triage")
		f.hub.commit(map[string]string{"skills/triage/references/a.md": "b\n"})
		f.ph.ok("skills", "update", "triage")
		gitIn(t, f.repo, "add", "-A")
		gitIn(t, f.repo, "commit", "-q", "-m", "local update")
		moveHub(f)
		out, errOut, code := f.ph.run("skills", "sync")
		if code != 3 || !strings.Contains(out+errOut, "does not hold the copy of triage this checkout has") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
		}
		f.quiet("another copy")
	})
	t.Run("the default branch's copy was edited", func(t *testing.T) {
		f := newBumpFixture(t)
		f.install("triage")
		moveHub(f)
		path := skillPath(f.repo, ".agents", "triage")
		edited := readSkill(t, path) + "\nmine\n"
		if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, f.repo, "add", "-A")
		gitIn(t, f.repo, "commit", "-q", "-m", "edit")
		gitIn(t, f.repo, "push", "-q", "origin", "HEAD:main")
		f.main = f.originRefs()["refs/heads/main"]
		if err := os.WriteFile(path, []byte(strings.TrimSuffix(edited, "\nmine\n")), 0o644); err != nil {
			t.Fatal(err)
		}
		out, errOut, code := f.ph.run("skills", "sync")
		if code != 3 || !strings.Contains(out+errOut, "is not clean") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
		}
		f.quiet("edited on the default branch")
	})
	t.Run("the source's ignore rules drop a file", func(t *testing.T) {
		f := newBumpFixture(t)
		writeFiles(t, f.repo, map[string]string{".gitignore": "extra.txt\n"})
		f.install("triage")
		f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"], "skills/triage/extra.txt": "x\n"})
		step(t, f, ".gitignore")
		out, errOut, _ := f.ph.run("skills", "sync")
		if text := out + errOut; !strings.Contains(text, "the project's .gitignore") || !strings.Contains(text, "nothing sent") || strings.Contains(text, "proposed") || strings.Contains(text, "in the source") {
			t.Errorf("the refusal = %q", text)
		}
	})
}

func TestSkillsSyncInAProjectKeepsASourceCredentialOutOfThePullRequest(t *testing.T) {
	f := newBumpFixture(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	const source = "https://tok:sekrit@example.com/o/tool-skill.git"
	f.route(source, tool.bare)
	f.ph.ok("skills", "add", "--ref", "main", source)
	f.publish("skills")
	moved := tool.commit(skillFiles("tool-skill", "v2"))
	out, errOut, code := f.ph.run("skills", "sync", "--yes", "tool-skill@"+moved)
	if code != 0 || !strings.Contains(out, "tool-skill,bumped,") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	argv, bodies := f.ghArgs(), ghBodies(t, f.log)
	if len(argv) != 1 || len(bodies) != 1 {
		t.Fatalf("gh argv %q, bodies %q", argv, bodies)
	}
	if !strings.Contains(bodies[0], "Source: https://example.com/o/tool-skill.git, ref main.") {
		t.Errorf("body = %q", bodies[0])
	}
	branch := f.branches("tool-skill")[0]
	for what, text := range map[string]string{
		"body":           bodies[0],
		"argv":           strings.Join(argv[0], "\x00"),
		"commit message": skillGit(t, f.origin, "log", "-1", "--format=%B", branch),
		"stdout":         out,
		"stderr":         errOut,
	} {
		if strings.Contains(text, "sekrit") || strings.Contains(text, "tok:") {
			t.Errorf("the %s carries the credential: %q", what, text)
		}
	}
}

func TestSkillsSyncInAProjectSendsALargeBodyOnStdin(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage")
	var big strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&big, "line %05d of a long reference file\n", i)
	}
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"], "skills/triage/references/big.md": big.String()})
	out := f.ph.ok("skills", "sync")
	if !strings.Contains(out, "triage,bumped,") {
		t.Fatalf("sync = %q", out)
	}
	argv, bodies := f.ghArgs(), ghBodies(t, f.log)
	if len(argv) != 1 || len(bodies) != 1 {
		t.Fatalf("gh argv %q, bodies %q", argv, bodies)
	}
	for _, a := range argv[0] {
		if len(a) > 4000 {
			t.Errorf("an argv entry is %d bytes", len(a))
		}
	}
	body := bodies[0]
	if len(body) < 40000 || len(body) > 65536 || !strings.Contains(body, "+line 00000 of a long reference file\n") || !strings.HasSuffix(body, "The diff is cut at 60000 bytes; Files changed has all of it.\n\n") {
		t.Errorf("the body is %d bytes, ends %q", len(body), body[max(0, len(body)-80):])
	}
}

func TestSkillsSyncInAProjectSaysWhenThePullRequestFailsAfterThePush(t *testing.T) {
	f := newBumpFixture(t)
	fakebin.Install(t, f.bin, "gh", "gh", map[string]string{"log": f.log, "stderr": "GraphQL: no permission"})
	f.install("triage")
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	out, _, code := f.ph.run("skills", "sync")
	if code != 3 || !strings.Contains(out, "pushed the branch hand-skills/bump-triage-") || !strings.Contains(out, "no permission") {
		t.Fatalf("code=%d stdout=%q", code, out)
	}
	if refs := f.originRefs(); len(refs) != 2 || refs["refs/heads/main"] != f.main {
		t.Fatalf("refs = %v", refs)
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsSyncInAProjectAppliesALocalSourceInPlace(t *testing.T) {
	f := newBumpFixture(t)
	src := skillSource(t, "mine", "v1")
	f.ph.ok("skills", "add", src)
	f.publish("skills")
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(skillFiles("mine", "v2")["SKILL.md"]), 0o644); err != nil {
		t.Fatal(err)
	}
	out := f.ph.ok("skills", "sync")
	m := regexp.MustCompile(`--yes mine@([0-9a-f]+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("sync = %q", out)
	}
	if out := f.ph.ok("skills", "sync", "--yes", "mine@"+m[1]); !strings.Contains(out, "mine,updated,") {
		t.Fatalf("sync --yes = %q", out)
	}
	if !strings.Contains(f.skillText("mine"), "\nv2\n") {
		t.Fatal("not applied")
	}
	f.quiet("a local source")
}

func TestSkillsUpdateInAProjectStillEditsTheCheckout(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage")
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	if out := f.ph.ok("skills", "update", "triage"); !strings.Contains(out, "triage,updated,") {
		t.Fatalf("update = %q", out)
	}
	if !strings.Contains(f.skillText("triage"), "\nv2\n") {
		t.Fatal("not applied")
	}
	f.quiet("update")
}

func TestSkillsSyncInAProjectOpensTheBumpAgainstTheDefaultBranch(t *testing.T) {
	f := newBumpFixture(t)
	f.install("triage")
	tree := skillGit(t, f.origin, "rev-parse", "main^{tree}")
	trunk := skillGit(t, f.origin, "commit-tree", tree, "-p", "main", "-m", "trunk")
	skillGit(t, f.origin, "update-ref", "refs/heads/trunk", trunk)
	skillGit(t, f.origin, "symbolic-ref", "HEAD", "refs/heads/trunk")
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	f.ph.ok("skills", "sync")
	bs := f.branches("triage")
	argv := f.ghArgs()
	if len(bs) != 1 || len(argv) != 1 || argv[0][6] != "--base" || argv[0][7] != "trunk" {
		t.Fatalf("branches %v, gh %v", bs, argv)
	}
	if parent := skillGit(t, f.origin, "log", "-1", "--format=%P", bs[0]); parent != trunk {
		t.Fatalf("parent = %s, trunk = %s", parent, trunk)
	}
	if refs := f.originRefs(); refs["refs/heads/main"] != f.main || refs["refs/heads/trunk"] != trunk {
		t.Fatalf("refs = %v", refs)
	}
}
