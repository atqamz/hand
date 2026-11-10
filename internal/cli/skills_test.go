package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
)

func skillFiles(name, text string) map[string]string {
	return map[string]string{"SKILL.md": strings.NewReplacer("%s", name).Replace("---\nname: %s\ndescription: a test skill\n---\n\n# %s\n\n") + text + "\n"}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func skillGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type skillRemote struct {
	t    *testing.T
	work string
	bare string
}

func newSkillRemote(t *testing.T, bare string, files map[string]string) *skillRemote {
	t.Helper()
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	skillGit(t, bare, "init", "-q", "--bare", "-b", "main")
	work := t.TempDir()
	skillGit(t, work, "init", "-q", "-b", "main")
	skillGit(t, work, "remote", "add", "origin", bare)
	r := &skillRemote{t: t, work: work, bare: bare}
	r.commit(files)
	return r
}

func (r *skillRemote) commit(files map[string]string) string {
	r.t.Helper()
	writeFiles(r.t, r.work, files)
	skillGit(r.t, r.work, "add", "-A")
	skillGit(r.t, r.work, "commit", "-q", "-m", "c")
	skillGit(r.t, r.work, "push", "-q", "origin", "HEAD:main")
	return skillGit(r.t, r.work, "rev-parse", "HEAD")
}

func isolateGit(t *testing.T, remote string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url.file://"+filepath.ToSlash(remote)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/atqamz/hand-skills.git")
}

func skillsHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.vars["HOME"] = t.TempDir()
	h.ok("init")
	return h
}

func toonCell(s string) string {
	if strings.ContainsAny(s, "\\") {
		return strconv.Quote(s)
	}
	return s
}

func skillPath(base, root, name string) string {
	return filepath.Join(base, root, "skills", name, "SKILL.md")
}

func readSkill(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func skillState(t *testing.T, base string) map[string]fleet.Managed {
	t.Helper()
	var state map[string]fleet.Managed
	if err := json.Unmarshal([]byte(readSkill(t, filepath.Join(base, ".agents", "skills", ".hand-skills.json"))), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestSkillsAddByPathCopiesMarksListsAndRemoves(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "notes")
	writeFiles(t, src, skillFiles("notes", "one"))
	writeFiles(t, src, map[string]string{"scripts/run.sh": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(src, "scripts", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := h.ok("skills", "add", src)
	if !strings.Contains(out, "notes,added") {
		t.Fatalf("add = %q", out)
	}
	for _, root := range fleet.SkillRoots {
		path := skillPath(h.home, root, "notes")
		info, err := os.Lstat(filepath.Dir(path))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s: %v %v", root, info, err)
		}
		got := readSkill(t, path)
		if !strings.Contains(got, "metadata:\n  managed-by: hand-skills\n---\n") || !strings.Contains(got, "\none\n") {
			t.Fatalf("%s: SKILL.md = %q", root, got)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), "scripts", "run.sh")); err != nil {
			t.Fatal(err)
		}
	}
	if got := readSkill(t, filepath.Join(src, "SKILL.md")); strings.Contains(got, "managed-by") {
		t.Fatalf("the source was marked: %q", got)
	}
	e := skillState(t, h.home)["notes"]
	if e.Source != src || e.Scope != "fleet" || e.Sum == "" || e.Commit != "" {
		t.Fatalf("state = %+v", e)
	}
	list := h.ok("skills", "list")
	for _, want := range []string{"secondhand,hand init,\"\",fleet,no", "secondhand-cleanup,hand init,\"\",fleet,no", "notes," + toonCell(src) + ",\"\",fleet,yes"} {
		if !strings.Contains(list, want) {
			t.Fatalf("list missing %q:\n%s", want, list)
		}
	}
	if _, errOut, code := h.run("skills", "add", src); code != 3 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("second add: code=%d stderr=%q", code, errOut)
	}
	out = h.ok("skills", "remove", "notes")
	if !strings.Contains(out, "notes,removed") {
		t.Fatalf("remove = %q", out)
	}
	for _, root := range fleet.SkillRoots {
		if _, err := os.Stat(filepath.Dir(skillPath(h.home, root, "notes"))); !os.IsNotExist(err) {
			t.Fatalf("%s still has the skill: %v", root, err)
		}
	}
	if len(skillState(t, h.home)) != 0 {
		t.Fatal("state still records notes")
	}
}

func TestSkillsAddByNameAndURLUseALocalRepo(t *testing.T) {
	h := skillsHarness(t)
	remotes := t.TempDir()
	hub := newSkillRemote(t, filepath.Join(remotes, "hub.git"), map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v1")["SKILL.md"], "skills/triage/ref/a.md": "a\n"})
	isolateGit(t, hub.bare)
	out := h.ok("skills", "add", "triage")
	commit := skillGit(t, hub.work, "rev-parse", "HEAD")
	if !strings.Contains(out, "triage,added,"+commit) {
		t.Fatalf("add by name = %q", out)
	}
	e := skillState(t, h.home)["triage"]
	if e.Source != "https://github.com/atqamz/hand-skills.git" || e.Path != "skills/triage" || e.Commit != commit {
		t.Fatalf("state = %+v", e)
	}
	if _, err := os.Stat(filepath.Join(h.home, ".claude", "skills", "triage", "ref", "a.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.home, ".claude", "skills", "triage", ".git")); err == nil {
		t.Fatal("copied the .git folder")
	}
	tool := newSkillRemote(t, filepath.Join(remotes, "tool-skill.git"), skillFiles("tool-skill", "root"))
	url := "file://" + filepath.ToSlash(tool.bare)
	if !strings.Contains(h.ok("skills", "add", url), "tool-skill,added,"+skillGit(t, tool.work, "rev-parse", "HEAD")) {
		t.Fatal("add by url failed")
	}
	if e := skillState(t, h.home)["tool-skill"]; e.Source != url || e.Path != "" {
		t.Fatalf("state = %+v", e)
	}
	if _, errOut, code := h.run("skills", "add", "missing-skill"); code == 0 || !strings.Contains(errOut, "missing-skill") {
		t.Fatalf("unknown name: code=%d stderr=%q", code, errOut)
	}
}

func TestSkillsUpdateFollowsTheCommit(t *testing.T) {
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v1")["SKILL.md"]})
	isolateGit(t, hub.bare)
	h.ok("skills", "add", "triage")
	if out := h.ok("skills", "update"); !strings.Contains(out, "triage,current,") {
		t.Fatalf("unmoved update = %q", out)
	}
	moved := hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	if out := h.ok("skills", "update", "triage"); !strings.Contains(out, "triage,updated,"+moved) {
		t.Fatalf("update = %q", out)
	}
	for _, root := range fleet.SkillRoots {
		if got := readSkill(t, skillPath(h.home, root, "triage")); !strings.Contains(got, "\nv2\n") || !strings.Contains(got, "managed-by: hand-skills") {
			t.Fatalf("%s: %q", root, got)
		}
	}
	if e := skillState(t, h.home)["triage"]; e.Commit != moved {
		t.Fatalf("state = %+v", e)
	}
}

func TestSkillsUpdateOfAPathFollowsItsContent(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "notes")
	writeFiles(t, src, skillFiles("notes", "one"))
	h.ok("skills", "add", src)
	if out := h.ok("skills", "update"); !strings.Contains(out, "notes,current") {
		t.Fatalf("update = %q", out)
	}
	writeFiles(t, src, skillFiles("notes", "two"))
	if out := h.ok("skills", "update"); !strings.Contains(out, "notes,updated") {
		t.Fatalf("update = %q", out)
	}
	if got := readSkill(t, skillPath(h.home, ".grok", "notes")); !strings.Contains(got, "\ntwo\n") {
		t.Fatalf("got %q", got)
	}
}

func TestSkillsUpdateRefusesALocalChange(t *testing.T) {
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v1")["SKILL.md"]})
	isolateGit(t, hub.bare)
	h.ok("skills", "add", "triage")
	edited := skillPath(h.home, ".pi", "triage")
	mine := readSkill(t, edited) + "my note\n"
	if err := os.WriteFile(edited, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	out, errOut, code := h.run("skills", "update", "triage")
	if code != 3 || !strings.Contains(out, "triage,refused") || !strings.Contains(out, "changed locally in .pi") || !strings.Contains(errOut, "1 skill(s) not updated") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if readSkill(t, edited) != mine || strings.Contains(readSkill(t, skillPath(h.home, ".agents", "triage")), "v2") {
		t.Fatal("an update overwrote a changed skill")
	}
	if _, _, code := h.run("skills", "remove", "triage"); code != 3 || readSkill(t, edited) != mine {
		t.Fatalf("remove of a changed skill: code=%d", code)
	}
}

func TestSkillsLeaveUnmanagedSkillsAlone(t *testing.T) {
	h := skillsHarness(t)
	mine := filepath.Join(h.home, ".agents", "skills", "mine")
	writeFiles(t, mine, skillFiles("mine", "hand-written"))
	for _, name := range []string{"secondhand", "secondhand-cleanup", "mine"} {
		out, _, code := h.run("skills", "remove", name)
		if code != 3 || !strings.Contains(out, name+",refused") {
			t.Fatalf("remove %s: code=%d out=%q", name, code, out)
		}
	}
	if out, _, code := h.run("skills", "update", "secondhand"); code != 3 || !strings.Contains(out, "secondhand,refused") {
		t.Fatalf("update: code=%d out=%q", code, out)
	}
	src := filepath.Join(t.TempDir(), "secondhand")
	writeFiles(t, src, skillFiles("secondhand", "mine"))
	if _, errOut, code := h.run("skills", "add", src); code != 3 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("add over a shipped skill: code=%d stderr=%q", code, errOut)
	}
	for _, path := range []string{skillPath(h.home, ".claude", "secondhand"), skillPath(h.home, ".agents", "mine")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	h.ok("init")
	if !strings.Contains(readSkill(t, skillPath(h.home, ".claude", "secondhand")), "managed-by: hand\n") {
		t.Fatal("init no longer owns its skill")
	}
}

func TestSkillsUserScopeUsesHomeAndTouchesNoFleet(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "notes")
	writeFiles(t, src, skillFiles("notes", "one"))
	if out := h.ok("skills", "add", "--user", src); !strings.Contains(out, "notes,added") {
		t.Fatalf("add = %q", out)
	}
	for _, root := range fleet.SkillRoots {
		if got := readSkill(t, skillPath(h.vars["HOME"], root, "notes")); !strings.Contains(got, "managed-by: hand-skills") {
			t.Fatalf("%s: %q", root, got)
		}
		if _, err := os.Stat(filepath.Dir(skillPath(h.home, root, "notes"))); !os.IsNotExist(err) {
			t.Fatalf("the fleet got the user skill: %v", err)
		}
	}
	if e := skillState(t, h.vars["HOME"])["notes"]; e.Scope != "user" {
		t.Fatalf("state = %+v", e)
	}
	if list := h.ok("skills", "list", "--user"); !strings.Contains(list, "notes,"+toonCell(src)+",\"\",user,yes") || strings.Contains(list, "secondhand") {
		t.Fatalf("list = %q", list)
	}
	if list := h.ok("skills", "list"); strings.Contains(list, "notes") {
		t.Fatalf("fleet list shows a user skill: %q", list)
	}
	writeFiles(t, src, skillFiles("notes", "two"))
	if out := h.ok("skills", "update", "--user", "notes"); !strings.Contains(out, "notes,updated") {
		t.Fatalf("update = %q", out)
	}
	if out := h.ok("skills", "remove", "--user"); !strings.Contains(out, "notes,removed") {
		t.Fatalf("remove = %q", out)
	}
	if _, err := os.Stat(filepath.Dir(skillPath(h.vars["HOME"], ".agents", "notes"))); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}
}

func TestSkillsRefuseASourceThatIsNotASkill(t *testing.T) {
	h := skillsHarness(t)
	empty := filepath.Join(t.TempDir(), "empty")
	writeFiles(t, empty, map[string]string{"README.md": "x"})
	plain := filepath.Join(t.TempDir(), "plain")
	writeFiles(t, plain, map[string]string{"SKILL.md": "no frontmatter\n"})
	for _, src := range []string{empty, plain} {
		if _, _, code := h.run("skills", "add", src); code == 0 {
			t.Fatalf("add %s succeeded", src)
		}
		if _, err := os.Stat(filepath.Dir(skillPath(h.home, ".claude", filepath.Base(src)))); !os.IsNotExist(err) {
			t.Fatalf("%s left a folder: %v", src, err)
		}
	}
	if _, _, code := h.run("skills", "add", "--user", "bad name!"); code != 2 {
		t.Fatalf("code = %d", code)
	}
}

func TestDoctorRowForManagedSkills(t *testing.T) {
	fx := newDoctorFixture(t)
	h := fx.h
	src := filepath.Join(t.TempDir(), "notes")
	writeFiles(t, src, skillFiles("notes", "one"))
	if row := checkRows(t, h.ok("doctor"))["skills"]; row != [2]string{"ok", `""`} {
		t.Fatalf("empty = %q", row)
	}
	h.ok("skills", "add", src)
	if row := checkRows(t, h.ok("doctor"))["skills"]; row != [2]string{"ok", `""`} {
		t.Fatalf("clean = %q", row)
	}
	edited := skillPath(h.home, ".claude", "notes")
	orig := readSkill(t, edited)
	if err := os.WriteFile(edited, []byte(orig+"x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if row := checkRows(t, h.ok("doctor"))["skills"]; row[0] != "warn" || !strings.Contains(row[1], "fleet notes drifted") {
		t.Fatalf("drifted = %q", row)
	}
	if err := os.WriteFile(edited, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	if row := checkRows(t, h.ok("doctor"))["skills"]; row[0] != "warn" || !strings.Contains(row[1], "fleet notes source missing") {
		t.Fatalf("source missing = %q", row)
	}
}

func skipUnlessPermsHold(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop this user")
	}
}

func TestSkillsRefuseANameThatLeavesTheSkillsFolder(t *testing.T) {
	h := skillsHarness(t)
	home := h.vars["HOME"]
	src := filepath.Join(t.TempDir(), "bar")
	writeFiles(t, src, skillFiles("bar", "one"))
	h.ok("skills", "add", "--user", src)
	decoy := filepath.Join(home, ".claude", "decoy")
	writeFiles(t, decoy, map[string]string{"SKILL.md": "---\nname: decoy\nmetadata:\n  managed-by: hand-skills\n---\n"})
	for _, cmd := range []string{"remove", "update"} {
		if _, errOut, code := h.run("skills", cmd, "--user", "../decoy"); code != 2 || !strings.Contains(errOut, "not a skill name") {
			t.Fatalf("%s: code=%d stderr=%q", cmd, code, errOut)
		}
	}
	if _, err := os.Stat(filepath.Join(decoy, "SKILL.md")); err != nil {
		t.Fatalf("a folder outside skills/ was touched: %v", err)
	}
	state := filepath.Join(home, ".agents", "skills", ".hand-skills.json")
	if err := os.WriteFile(state, []byte(`{"../decoy":{"source":"x","scope":"user","sum":"x"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("skills", "remove", "--user"); code == 0 || !strings.Contains(errOut, "not a skill name") {
		t.Fatalf("state key: code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(decoy, "SKILL.md")); err != nil {
		t.Fatalf("a state key walked out: %v", err)
	}
}

func TestSkillsUpdateThatFailsLeavesEveryRootOldAndARerunCompletes(t *testing.T) {
	skipUnlessPermsHold(t)
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "foo")
	writeFiles(t, src, skillFiles("foo", "one"))
	h.ok("skills", "add", src)
	writeFiles(t, src, skillFiles("foo", "two"))
	locked := filepath.Join(h.home, ".pi", "skills")
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, _, code := h.run("skills", "update", "foo"); code == 0 {
		t.Fatal("update into an unwritable root succeeded")
	}
	for _, root := range fleet.SkillRoots {
		if got := readSkill(t, skillPath(h.home, root, "foo")); !strings.Contains(got, "\none\n") {
			t.Fatalf("%s is half-new: %q", root, got)
		}
	}
	if err := os.Chmod(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if out := h.ok("skills", "update", "foo"); !strings.Contains(out, "foo,updated") {
		t.Fatalf("rerun = %q", out)
	}
	for _, root := range fleet.SkillRoots {
		if got := readSkill(t, skillPath(h.home, root, "foo")); !strings.Contains(got, "\ntwo\n") {
			t.Fatalf("%s: %q", root, got)
		}
	}
	entries, err := os.ReadDir(filepath.Join(h.home, ".claude", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".hand-foo-") {
			t.Fatalf("left %s behind", e.Name())
		}
	}
}

func TestSkillsRerunFinishesAnInterruptedSwap(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "foo")
	writeFiles(t, src, skillFiles("foo", "one"))
	h.ok("skills", "add", src)
	old := readSkill(t, skillPath(h.home, ".pi", "foo"))
	writeFiles(t, src, skillFiles("foo", "two"))
	h.ok("skills", "update", "foo")
	newSum := skillState(t, h.home)["foo"].Sum
	if err := os.WriteFile(skillPath(h.home, ".pi", "foo"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	state := skillState(t, h.home)
	oldSum := state["foo"]
	oldSum.Sum, oldSum.Next = fleetSum(t, filepath.Dir(skillPath(h.home, ".pi", "foo"))), newSum
	state["foo"] = oldSum
	b, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(h.home, ".agents", "skills", ".hand-skills.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if out := h.ok("skills", "update", "foo"); !strings.Contains(out, "foo,updated") {
		t.Fatalf("rerun = %q", out)
	}
	if got := readSkill(t, skillPath(h.home, ".pi", "foo")); !strings.Contains(got, "\ntwo\n") {
		t.Fatalf("pi = %q", got)
	}
	if e := skillState(t, h.home)["foo"]; e.Sum != newSum || e.Next != "" {
		t.Fatalf("state = %+v", e)
	}
}

func fleetSum(t *testing.T, dir string) string {
	t.Helper()
	sum, err := fleet.Sum(dir)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func TestSkillsMarkerCountsOnlyInTheFrontmatter(t *testing.T) {
	h := skillsHarness(t)
	quote := filepath.Join(h.home, ".agents", "skills", "quote")
	writeFiles(t, quote, map[string]string{"SKILL.md": "---\nname: quote\n---\n\nmetadata:\n  managed-by: hand-skills\n"})
	if out, _, code := h.run("skills", "remove", "quote"); code != 3 || !strings.Contains(out, "quote,refused") {
		t.Fatalf("remove: code=%d out=%q", code, out)
	}
	if _, err := os.Stat(filepath.Join(quote, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsRemoveRefusesAMarkedFolderWithNoRecord(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "notes")
	writeFiles(t, src, skillFiles("notes", "one"))
	h.ok("skills", "add", src)
	if err := os.WriteFile(filepath.Join(h.home, ".agents", "skills", ".hand-skills.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _, code := h.run("skills", "remove", "notes"); code != 3 || !strings.Contains(out, "no recorded copy") {
		t.Fatalf("remove: code=%d out=%q", code, out)
	}
	for _, root := range fleet.SkillRoots {
		if _, err := os.Stat(skillPath(h.home, root, "notes")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSkillsAcceptCRLFFrontmatter(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "crlf")
	writeFiles(t, src, map[string]string{"SKILL.md": "---\r\nname: crlf\r\ndescription: x\r\n---\r\n\r\nbody\r\n"})
	if out := h.ok("skills", "add", src); !strings.Contains(out, "crlf,added") {
		t.Fatalf("add = %q", out)
	}
	got := readSkill(t, skillPath(h.home, ".claude", "crlf"))
	if !strings.HasPrefix(got, "---\nname: crlf\ndescription: x\nmetadata:\n  managed-by: hand-skills\n---\n") {
		t.Fatalf("got %q", got)
	}
	if out := h.ok("skills", "remove", "crlf"); !strings.Contains(out, "crlf,removed") {
		t.Fatalf("remove = %q", out)
	}
}

func TestSkillsMarkerFollowsTheMetadataIndent(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "wide")
	writeFiles(t, src, map[string]string{"SKILL.md": "---\nname: wide\nmetadata:\n    version: 1\n---\n"})
	h.ok("skills", "add", src)
	if got := readSkill(t, skillPath(h.home, ".claude", "wide")); got != "---\nname: wide\nmetadata:\n    managed-by: hand-skills\n    version: 1\n---\n" {
		t.Fatalf("got %q", got)
	}
	h.ok("skills", "remove", "wide")
}

func TestSkillsRefuseATreeOverTheCap(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "big")
	writeFiles(t, src, skillFiles("big", "one"))
	f, err := os.Create(filepath.Join(src, "blob"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(fleet.MaxSkillBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, errOut, code := h.run("skills", "add", src); code == 0 || !strings.Contains(errOut, "32 MiB") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Dir(skillPath(h.home, ".claude", "big"))); !os.IsNotExist(err) {
		t.Fatalf("left a folder: %v", err)
	}
}

func TestSkillsConcurrentAddsKeepEveryRecord(t *testing.T) {
	h := skillsHarness(t)
	names := []string{"a1", "a2", "a3", "a4", "a5", "a6"}
	var wg sync.WaitGroup
	for _, name := range names {
		src := filepath.Join(t.TempDir(), name)
		writeFiles(t, src, skillFiles(name, "one"))
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ok("skills", "add", src)
		}()
	}
	wg.Wait()
	if got := skillState(t, h.home); len(got) != len(names) {
		t.Fatalf("state records %d of %d skills", len(got), len(names))
	}
}

func TestDoctorShowsTheSkillsRowOutsideAFleet(t *testing.T) {
	fx := newDoctorFixture(t)
	h := fx.h
	src := filepath.Join(t.TempDir(), "notes")
	writeFiles(t, src, skillFiles("notes", "one"))
	h.ok("skills", "add", "--user", src)
	h.vars["HAND_HOME"] = ""
	out, _, _ := h.run("doctor")
	if row := checkRows(t, out)["skills"]; row != [2]string{"ok", `""`} {
		t.Fatalf("skills row = %q in %q", row, out)
	}
	if err := os.WriteFile(skillPath(h.vars["HOME"], ".claude", "notes"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _ = h.run("doctor")
	if row := checkRows(t, out)["skills"]; row[0] != "warn" || !strings.Contains(row[1], "user notes") {
		t.Fatalf("drifted user skill = %q", row)
	}
}
