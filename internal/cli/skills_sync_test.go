package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if got := set["triage"]; got.Source != "atqamz/skills" || got.Ref != "main" {
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
	if out, errOut, code := h.run("skills", "sync", "--fleet", "--yes"); code != 3 || !strings.Contains(out, "--yes applies only to a skill you name") {
		t.Fatalf("--yes without a name: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "tool-skill")); !strings.Contains(got, "\nv1\n") {
		t.Fatalf("--yes without a name applied: %q", got)
	}
	out = h.ok("skills", "sync", "--fleet", "--yes", "tool-skill")
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
	if out := h.ok("skills", "sync", "--fleet", "--yes", "tool-skill"); !strings.Contains(out, "tool-skill,updated,") {
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
	if got := subscribedSet(t, filepath.Join(repo, ".agents", "skills", "skills.json"))["triage"]; got.Source != "atqamz/skills" || got.Ref != "main" {
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
