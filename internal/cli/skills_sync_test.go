package cli_test

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
)

func row(out, name string) string {
	for line := range strings.Lines(out) {
		if strings.HasPrefix(line, "  "+name+",") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func subscribedSet(t *testing.T, path string) map[string]struct{ Source, Ref string } {
	t.Helper()
	var f struct {
		Skills map[string]struct{ Source, Ref string }
	}
	if err := json.Unmarshal([]byte(readSkill(t, path)), &f); err != nil {
		t.Fatal(err)
	}
	return f.Skills
}

func backups(t *testing.T, dir, name string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, name, "*", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestSkillsAddSubscribesAndRemoveForgets(t *testing.T) {
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v1")["SKILL.md"]})
	isolateGit(t, hub.bare)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	url := "file://" + filepath.ToSlash(tool.bare)
	h.ok("skills", "add", "--fleet", "triage")
	h.ok("skills", "add", "--fleet", url)
	commit := skillGit(t, tool.work, "rev-parse", "HEAD")
	set := subscribedSet(t, filepath.Join(h.home, "skills.json"))
	if got := set["triage"]; got.Source != ownName || got.Ref != "main" {
		t.Fatalf("own repo = %+v", got)
	}
	if got := set["tool-skill"]; got.Source != url || got.Ref != commit {
		t.Fatalf("third party without --ref pins its commit: %+v", got)
	}
	h.ok("skills", "remove", "--fleet", "triage")
	if set := subscribedSet(t, filepath.Join(h.home, "skills.json")); set["triage"].Source != "" || set["tool-skill"].Source == "" {
		t.Fatalf("remove left %+v", set)
	}
}

func TestSkillsSyncAppliesAnOwnRepoWithABackup(t *testing.T) {
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v1")["SKILL.md"]})
	isolateGit(t, hub.bare)
	h.ok("skills", "add", "--fleet", "triage")
	old := skillGit(t, hub.work, "rev-parse", "HEAD")
	if out := h.ok("skills", "sync", "--fleet"); !strings.Contains(out, "triage,current,"+old) {
		t.Fatalf("unmoved sync = %q", out)
	}
	moved := hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	out := h.ok("skills", "outdated", "--fleet")
	if r := row(out, "triage"); !strings.HasPrefix(r, "triage,behind,fleet,") || !strings.Contains(r, ",main,"+old+","+moved) {
		t.Fatalf("outdated = %q", out)
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "triage")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("outdated wrote the copy: %q", got)
	}
	out = h.ok("skills", "sync", "--fleet")
	if !strings.Contains(out, "triage,updated,"+old[:12]+" -> "+moved[:12]) {
		t.Fatalf("sync = %q", out)
	}
	for _, root := range []string{".claude", ".agents", ".grok", ".pi"} {
		if got := readSkill(t, skillPath(h.home, root, "triage")); !strings.Contains(got, "\nv2\n") {
			t.Fatalf("%s: %q", root, got)
		}
	}
	saved := backups(t, filepath.Join(h.home, ".agents", "skills-backups"), "triage")
	if len(saved) != 1 || !strings.Contains(readSkill(t, saved[0]), "\nv1\n") {
		t.Fatalf("backups = %q", saved)
	}
	if e := skillState(t, h.home)["triage"]; e.Commit != moved || e.Ref != "main" {
		t.Fatalf("lock = %+v", e)
	}
	if r := row(h.ok("skills", "outdated", "--fleet"), "triage"); !strings.HasPrefix(r, "triage,current,") {
		t.Fatalf("after sync = %q", r)
	}
}

func TestSkillsSyncShowsAThirdPartyDiffAndAppliesOnlyWithYes(t *testing.T) {
	h := skillsHarness(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	url := "file://" + filepath.ToSlash(tool.bare)
	h.ok("skills", "add", "--fleet", "--ref", "main", url)
	old := skillGit(t, tool.work, "rev-parse", "HEAD")
	moved := tool.commit(skillFiles("tool-skill", "v2"))
	out := h.ok("skills", "sync", "--fleet")
	if !strings.Contains(out, "tool-skill,behind,") || !strings.Contains(out, "-v1") || !strings.Contains(out, "+v2") || !strings.Contains(out, "sync --yes tool-skill") {
		t.Fatalf("sync = %q", out)
	}
	for _, root := range []string{".claude", ".agents", ".grok", ".pi"} {
		if got := readSkill(t, skillPath(h.home, root, "tool-skill")); !strings.Contains(got, "\nv1\n") {
			t.Fatalf("%s moved without a yes: %q", root, got)
		}
	}
	if e := skillState(t, h.home)["tool-skill"]; e.Commit != old {
		t.Fatalf("lock moved without a yes: %+v", e)
	}
	if len(backups(t, filepath.Join(h.home, ".agents", "skills-backups"), "tool-skill")) != 0 {
		t.Fatal("a backup without an apply")
	}
	if !strings.Contains(out, "sync --yes tool-skill@"+moved) {
		t.Fatalf("the row does not name the commit: %q", out)
	}
	if out, errOut, code := h.run("skills", "sync", "--fleet", "--yes", "tool-skill"); code != 3 || !strings.Contains(out, "--yes takes NAME@COMMIT") {
		t.Fatalf("--yes without a commit: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "tool-skill")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("--yes without a commit applied: %q", got)
	}
	out = h.ok("skills", "sync", "--fleet", "--yes", "tool-skill@"+moved)
	if !strings.Contains(out, "tool-skill,updated,"+old[:12]+" -> "+moved[:12]) {
		t.Fatalf("sync --yes = %q", out)
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "tool-skill")); !strings.Contains(got, "\nv2\n") {
		t.Fatalf("not applied: %q", got)
	}
	if len(backups(t, filepath.Join(h.home, ".agents", "skills-backups"), "tool-skill")) != 1 {
		t.Fatal("no backup of the previous copy")
	}
}

func TestSkillsSyncMovesAThirdPartyPinOnlyAfterYes(t *testing.T) {
	h := skillsHarness(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	url := "file://" + filepath.ToSlash(tool.bare)
	h.ok("skills", "add", "--fleet", url)
	moved := tool.commit(skillFiles("tool-skill", "v2"))
	if out := h.ok("skills", "sync", "--fleet"); !strings.Contains(out, "tool-skill,current,") {
		t.Fatalf("a pinned commit moved or errored: %q", out)
	}
	path := filepath.Join(h.home, "skills.json")
	b := readSkill(t, path)
	pinned := subscribedSet(t, path)["tool-skill"].Ref
	if err := os.WriteFile(path, []byte(strings.Replace(b, pinned, moved, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := h.ok("skills", "sync", "--fleet"); !strings.Contains(out, "tool-skill,behind,") {
		t.Fatalf("a moved pin applied or was missed: %q", out)
	}
	if out := h.ok("skills", "sync", "--fleet", "--yes", "tool-skill@"+moved); !strings.Contains(out, "tool-skill,updated,") {
		t.Fatalf("sync --yes = %q", out)
	}
	if e := skillState(t, h.home)["tool-skill"]; e.Commit != moved || e.Ref != moved {
		t.Fatalf("lock = %+v", e)
	}
}

func TestSkillsSyncLeavesAnEditedCopyAlone(t *testing.T) {
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{
		"skills/edit/SKILL.md": skillFiles("edit", "v1")["SKILL.md"], "skills/both/SKILL.md": skillFiles("both", "v1")["SKILL.md"], "skills/other/SKILL.md": skillFiles("other", "v1")["SKILL.md"],
	})
	isolateGit(t, hub.bare)
	for _, name := range []string{"edit", "both", "other"} {
		h.ok("skills", "add", "--fleet", name)
	}
	mine := map[string]string{}
	for _, name := range []string{"edit", "both"} {
		path := skillPath(h.home, ".claude", name)
		mine[name] = readSkill(t, path) + "\nmy own line\n"
		if err := os.WriteFile(path, []byte(mine[name]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hub.commit(map[string]string{"skills/both/SKILL.md": skillFiles("both", "v2")["SKILL.md"], "skills/other/SKILL.md": skillFiles("other", "v2")["SKILL.md"]})
	out := h.ok("skills", "outdated", "--fleet")
	for name, want := range map[string]string{"edit": "edit,edited,", "both": "both,conflict,", "other": "other,behind,"} {
		if r := row(out, name); !strings.HasPrefix(r, want) {
			t.Fatalf("outdated %s = %q", name, r)
		}
	}
	out = h.ok("skills", "sync", "--fleet")
	for name, want := range map[string]string{"edit": "edit,edited,", "both": "both,conflict,", "other": "other,updated,"} {
		if r := row(out, name); !strings.HasPrefix(r, want) {
			t.Fatalf("sync %s = %q", name, r)
		}
	}
	for name, text := range mine {
		if got := readSkill(t, skillPath(h.home, ".claude", name)); got != text {
			t.Fatalf("%s was overwritten: %q", name, got)
		}
		if len(backups(t, filepath.Join(h.home, ".agents", "skills-backups"), name)) != 0 {
			t.Fatalf("%s: sync touched an edited copy", name)
		}
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "other")); !strings.Contains(got, "\nv2\n") {
		t.Fatalf("the clean skill did not move with the others skipped: %q", got)
	}
}

func TestSkillsOutdatedStatesOfALocalSource(t *testing.T) {
	h := skillsHarness(t)
	sources := map[string]string{}
	for _, name := range []string{"same", "behind", "edited", "conflict"} {
		sources[name] = skillSource(t, name, "v1")
		h.ok("skills", "add", "--fleet", sources[name])
	}
	for _, name := range []string{"edited", "conflict"} {
		path := skillPath(h.home, ".claude", name)
		if err := os.WriteFile(path, []byte(readSkill(t, path)+"\nmine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"behind", "conflict"} {
		writeFiles(t, sources[name], skillFiles(name, "v2"))
	}
	out := h.ok("skills", "outdated", "--fleet")
	for name, want := range map[string]string{"same": "current", "behind": "behind", "edited": "edited", "conflict": "conflict"} {
		if r := row(out, name); !strings.HasPrefix(r, name+","+want+",fleet,") {
			t.Fatalf("%s = %q", name, r)
		}
	}
	if _, errOut, code := h.run("skills", "outdated", "--fleet", "extra"); code != 2 {
		t.Fatalf("an argument: code=%d stderr=%q", code, errOut)
	}
}

func TestSkillsSyncInAProjectKeepsTheSubscriptionInTheCheckout(t *testing.T) {
	h, repo := projectHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v1")["SKILL.md"]})
	isolateGit(t, hub.bare)
	h.ok("skills", "add", "triage")
	if got := subscribedSet(t, filepath.Join(repo, ".agents", "skills", "skills.json"))["triage"]; got.Source != ownName || got.Ref != "main" {
		t.Fatalf("subscription = %+v", got)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "skills")
	moved := hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	if out := h.ok("skills", "outdated"); !strings.HasPrefix(row(out, "triage"), "triage,behind,project,") {
		t.Fatalf("outdated = %q", out)
	}
	h.cwd = h.home
	if out := h.ok("skills", "outdated", "--all"); !strings.HasPrefix(row(out, "triage"), "triage,behind,project/app,") {
		t.Fatalf("outdated --all = %q", out)
	}
	h.cwd = repo
	if out := h.ok("skills", "sync"); !strings.Contains(out, "triage,updated,") {
		t.Fatalf("sync = %q", out)
	}
	if got := readSkill(t, filepath.Join(repo, ".agents", "skills", "triage", "SKILL.md")); !strings.Contains(got, "\nv2\n") {
		t.Fatalf("project copy = %q", got)
	}
	if e := lockEntries(t, fleetLock(repo))["triage"]; e.Commit != moved {
		t.Fatalf("project lock = %+v", e)
	}
	if saved := backups(t, filepath.Join(repo, ".git", "hand-skills", "backups"), "triage"); len(saved) != 1 {
		t.Fatalf("project backups = %q", saved)
	}
	if got := gitIn(t, repo, "status", "--porcelain"); !strings.Contains(got, "M .agents/skills/triage/SKILL.md") || !strings.Contains(got, "M skills-lock.json") {
		t.Fatalf("status = %q", got)
	}
	hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v3")["SKILL.md"]})
	if out, _, code := h.run("skills", "sync"); code != 3 || !strings.Contains(out, "uncommitted") || strings.Contains(readSkill(t, filepath.Join(repo, ".agents", "skills", "triage", "SKILL.md")), "\nv3\n") {
		t.Fatalf("a dirty project copy: code=%d stdout=%q", code, out)
	}
}

func editJSON(t *testing.T, path string, edit func(skills map[string]map[string]any)) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(readSkill(t, path)), &doc); err != nil {
		t.Fatal(err)
	}
	skills := map[string]map[string]any{}
	for name, v := range doc["skills"].(map[string]any) {
		skills[name] = v.(map[string]any)
	}
	edit(skills)
	doc["skills"] = skills
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsSyncDoesNotTrustAPlantedLockEntry(t *testing.T) {
	h := skillsHarness(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	url := "file://" + filepath.ToSlash(tool.bare)
	h.ok("skills", "add", "--fleet", "--ref", "main", url)
	old := skillGit(t, tool.work, "rev-parse", "HEAD")
	editJSON(t, fleetLock(h.home), func(skills map[string]map[string]any) {
		skills["tool-skill"]["source"], skills["tool-skill"]["sourceType"], skills["tool-skill"]["sourceUrl"] = ownName, "github", url
	})
	editJSON(t, filepath.Join(h.home, "skills.json"), func(skills map[string]map[string]any) {
		skills["tool-skill"]["source"] = ownName
	})
	tool.commit(skillFiles("tool-skill", "v2"))
	out := h.ok("skills", "sync", "--fleet")
	if !strings.Contains(out, "tool-skill,behind,") || !strings.Contains(out, "+v2") {
		t.Fatalf("sync = %q", out)
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "tool-skill")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("a planted lock entry applied third-party content: %q", got)
	}
	if e := skillState(t, h.home)["tool-skill"]; e.Commit != old {
		t.Fatalf("lock = %+v", e)
	}
}

func TestSkillsSyncAppliesAnOwnRepoOnlyWhenTheSubscriptionNamesIt(t *testing.T) {
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v1")["SKILL.md"]})
	isolateGit(t, hub.bare)
	h.ok("skills", "add", "--fleet", "triage")
	editJSON(t, filepath.Join(h.home, "skills.json"), func(skills map[string]map[string]any) {
		skills["triage"]["source"] = "someone/else"
	})
	hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	if out := h.ok("skills", "sync", "--fleet"); !strings.Contains(out, "triage,behind,") {
		t.Fatalf("sync = %q", out)
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "triage")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("applied: %q", got)
	}
}

func TestSkillsAddRefusesAPullRequestRef(t *testing.T) {
	h := skillsHarness(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	url := "file://" + filepath.ToSlash(tool.bare)
	for _, ref := range []string{"refs/pull/1/head", "pull/1/head", "Refs/heads/main", "merge-requests/2/head", "a..b"} {
		if _, errOut, code := h.run("skills", "add", "--fleet", "--ref", ref, url); code != 2 || !strings.Contains(errOut, "is not a branch, tag or commit name") {
			t.Fatalf("--ref %s: code=%d stderr=%q", ref, code, errOut)
		}
	}
	h.ok("skills", "add", "--fleet", "--ref", "main", url)
}

func TestSkillsSyncYesAppliesOnlyTheCommitThatWasDiffed(t *testing.T) {
	h := skillsHarness(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	url := "file://" + filepath.ToSlash(tool.bare)
	h.ok("skills", "add", "--fleet", "--ref", "main", url)
	shown := tool.commit(skillFiles("tool-skill", "v2"))
	if out := h.ok("skills", "sync", "--fleet"); !strings.Contains(out, "sync --yes tool-skill@"+shown) || !strings.Contains(out, "+v2") {
		t.Fatalf("sync = %q", out)
	}
	latest := tool.commit(skillFiles("tool-skill", "v3 NEVER SHOWN"))
	out, errOut, code := h.run("skills", "sync", "--fleet", "--yes", "tool-skill@"+shown)
	if code != 3 || !strings.Contains(out, "moved since the diff you read") || !strings.Contains(out, "+v3 NEVER SHOWN") || !strings.Contains(out, "tool-skill@"+latest) {
		t.Fatalf("a moved branch: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "tool-skill")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("applied a commit nobody read: %q", got)
	}
	if out := h.ok("skills", "sync", "--fleet", "--yes", "tool-skill@"+latest[:8]); !strings.Contains(out, "tool-skill,updated,") {
		t.Fatalf("sync --yes = %q", out)
	}
	if e := skillState(t, h.home)["tool-skill"]; e.Commit != latest {
		t.Fatalf("lock = %+v", e)
	}
}

func TestSkillsSyncYesIgnoresAnotherSkillsCommit(t *testing.T) {
	h := skillsHarness(t)
	a := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-a.git"), skillFiles("tool-a", "v1"))
	b := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-b.git"), skillFiles("tool-b", "v1"))
	for _, r := range []*skillRemote{a, b} {
		h.ok("skills", "add", "--fleet", "--ref", "main", "file://"+filepath.ToSlash(r.bare))
	}
	movedA := a.commit(skillFiles("tool-a", "v2"))
	b.commit(skillFiles("tool-b", "v2"))
	out := h.ok("skills", "sync", "--fleet", "--yes", "tool-a@"+movedA)
	if !strings.Contains(out, "tool-a,updated,") || !strings.Contains(out, "tool-b,behind,") {
		t.Fatalf("sync = %q", out)
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "tool-b")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("a yes for one skill moved another: %q", got)
	}
}

func TestSkillsSyncYesNeverOverwritesAnEditedOrConflictingCopy(t *testing.T) {
	h := skillsHarness(t)
	remotes := map[string]*skillRemote{}
	for _, name := range []string{"tool-edit", "tool-both"} {
		remotes[name] = newSkillRemote(t, filepath.Join(t.TempDir(), name+".git"), skillFiles(name, "v1"))
		h.ok("skills", "add", "--fleet", "--ref", "main", "file://"+filepath.ToSlash(remotes[name].bare))
	}
	mine := map[string]string{}
	for name := range remotes {
		path := skillPath(h.home, ".claude", name)
		mine[name] = readSkill(t, path) + "\nmy own line\n"
		if err := os.WriteFile(path, []byte(mine[name]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	moved := remotes["tool-both"].commit(skillFiles("tool-both", "v2"))
	for name, want := range map[string]string{"tool-edit": "edited", "tool-both": "conflict"} {
		out := h.ok("skills", "sync", "--fleet", "--yes", name+"@"+moved)
		if r := row(out, name); !strings.HasPrefix(r, name+","+want+",") {
			t.Fatalf("%s = %q", name, r)
		}
		if got := readSkill(t, skillPath(h.home, ".claude", name)); got != mine[name] {
			t.Fatalf("--yes overwrote the %s copy %s: %q", want, name, got)
		}
	}
}

func TestSkillsUpdateTakesABackupOfThePreviousCopy(t *testing.T) {
	h := skillsHarness(t)
	src := skillSource(t, "notes", "v1")
	h.ok("skills", "add", "--fleet", src)
	writeFiles(t, src, skillFiles("notes", "v2"))
	if out := h.ok("skills", "update", "--fleet"); !strings.Contains(out, "notes,updated,") {
		t.Fatalf("update = %q", out)
	}
	saved := backups(t, filepath.Join(h.home, ".agents", "skills-backups"), "notes")
	if len(saved) != 1 || !strings.Contains(readSkill(t, saved[0]), "\nv1\n") {
		t.Fatalf("backups = %q", saved)
	}
}

func TestSkillsFetchStopsAtTheGitLimit(t *testing.T) {
	cli.SetSkillFetchLimits(t, 400*time.Millisecond, time.Minute)
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
	h := skillsHarness(t)
	start := time.Now()
	_, errOut, code := h.run("skills", "add", "--fleet", "git://"+ln.Addr().String()+"/tool-skill")
	if code == 0 || !strings.Contains(errOut, "limit") || time.Since(start) > 20*time.Second {
		t.Fatalf("a hung remote: code=%d after %s: %q", code, time.Since(start), errOut)
	}
}

func TestSkillsFetchOncePerSourceInACommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the counting git is a shell script")
	}
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{
		"skills/a/SKILL.md": skillFiles("a", "v1")["SKILL.md"], "skills/b/SKILL.md": skillFiles("b", "v1")["SKILL.md"], "skills/c/SKILL.md": skillFiles("c", "v1")["SKILL.md"],
	})
	isolateGit(t, hub.bare)
	for _, name := range []string{"a", "b", "c"} {
		h.ok("skills", "add", "--fleet", name)
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "calls")
	writeFiles(t, bin, map[string]string{"git": "#!/bin/sh\necho \"$@\" >> '" + log + "'\nexec '" + real + "' \"$@\"\n"})
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, cmd := range []string{"outdated", "sync"} {
		_ = os.Remove(log)
		out := h.ok("skills", cmd, "--fleet")
		for _, name := range []string{"a", "b", "c"} {
			if r := row(out, name); !strings.Contains(r, ","+name+",") && !strings.HasPrefix(r, name+",current,") {
				t.Fatalf("%s %s = %q", cmd, name, r)
			}
		}
		if n := strings.Count(readFile(t, log), " fetch "); n != 1 {
			t.Fatalf("%s ran %d fetches for one source", cmd, n)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
