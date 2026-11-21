package cli_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/fsx"
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
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/atqamz/skills.git")
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

const skillsURL = "https://github.com/atqamz/skills.git"

var ownName = strings.TrimSuffix(strings.TrimPrefix(skillsURL, "https://github.com/"), ".git")

func fleetLock(base string) string { return filepath.Join(base, "skills-lock.json") }

func userLock(base string) string { return filepath.Join(base, ".agents", "skills-lock.json") }

func lockEntries(t *testing.T, path string) map[string]fleet.LockEntry {
	t.Helper()
	var lock struct {
		Version int
		Skills  map[string]fleet.LockEntry
	}
	if err := json.Unmarshal([]byte(readSkill(t, path)), &lock); err != nil || lock.Version != 2 {
		t.Fatalf("%s: version %d: %v", path, lock.Version, err)
	}
	return lock.Skills
}

func skillState(t *testing.T, base string) map[string]fleet.LockEntry {
	t.Helper()
	return lockEntries(t, fleetLock(base))
}

func pendingSwaps(t *testing.T, base string) map[string]string {
	t.Helper()
	pending := map[string]string{}
	b, err := os.ReadFile(filepath.Join(base, ".agents", "skills", ".hand-skills-next.json"))
	if os.IsNotExist(err) {
		return pending
	}
	if err != nil || json.Unmarshal(b, &pending) != nil {
		t.Fatalf("pending swaps: %v", err)
	}
	return pending
}

func TestSkillsAddByPathCopiesMarksListsAndRemoves(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "notes")
	writeFiles(t, src, skillFiles("notes", "one"))
	writeFiles(t, src, map[string]string{"scripts/run.sh": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(src, "scripts", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := h.ok("skills", "add", "--fleet", src)
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
		if !strings.Contains(got, "metadata:\n  managed-by: hand\n---\n") || !strings.Contains(got, "\none\n") {
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
	if e.Source != src || e.SourceType != "local" || e.ComputedHash == "" || e.Commit != "" {
		t.Fatalf("state = %+v", e)
	}
	list := h.ok("skills", "list", "--fleet")
	for _, want := range []string{"secondhand,hand init,\"\",fleet,no", "secondhand-cleanup,hand init,\"\",fleet,no", "notes," + toonCell(src) + ",\"\",fleet,yes"} {
		if !strings.Contains(list, want) {
			t.Fatalf("list missing %q:\n%s", want, list)
		}
	}
	if _, errOut, code := h.run("skills", "add", "--fleet", src); code != 3 || !strings.Contains(errOut, "already exists") {
		t.Fatalf("second add: code=%d stderr=%q", code, errOut)
	}
	out = h.ok("skills", "remove", "--fleet", "notes")
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
	out := h.ok("skills", "add", "--fleet", "triage")
	commit := skillGit(t, hub.work, "rev-parse", "HEAD")
	if !strings.Contains(out, "triage,added,"+commit) {
		t.Fatalf("add by name = %q", out)
	}
	e := skillState(t, h.home)["triage"]
	if e.Source != "atqamz/skills" || e.SourceURL != skillsURL || e.SourceType != "github" || e.SkillPath != "skills/triage/SKILL.md" || e.Commit != commit {
		t.Fatalf("state = %+v", e)
	}
	if list := h.ok("skills", "list", "--fleet"); !strings.Contains(list, `triage,"`+skillsURL+`#skills/triage",`+commit+",fleet,yes") {
		t.Fatalf("list = %q", list)
	}
	if _, err := os.Stat(filepath.Join(h.home, ".claude", "skills", "triage", "ref", "a.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.home, ".claude", "skills", "triage", ".git")); err == nil {
		t.Fatal("copied the .git folder")
	}
	tool := newSkillRemote(t, filepath.Join(remotes, "tool-skill.git"), skillFiles("tool-skill", "root"))
	url := "file://" + filepath.ToSlash(tool.bare)
	if !strings.Contains(h.ok("skills", "add", "--fleet", url), "tool-skill,added,"+skillGit(t, tool.work, "rev-parse", "HEAD")) {
		t.Fatal("add by url failed")
	}
	if e := skillState(t, h.home)["tool-skill"]; e.Source != url || e.SkillPath != "" || e.SourceType != "git" {
		t.Fatalf("state = %+v", e)
	}
	if _, errOut, code := h.run("skills", "add", "--fleet", "missing-skill"); code == 0 || !strings.Contains(errOut, "missing-skill") {
		t.Fatalf("unknown name: code=%d stderr=%q", code, errOut)
	}
}

func TestSkillsUpdateFollowsTheCommit(t *testing.T) {
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v1")["SKILL.md"]})
	isolateGit(t, hub.bare)
	h.ok("skills", "add", "--fleet", "triage")
	if out := h.ok("skills", "update", "--fleet"); !strings.Contains(out, "triage,current,") {
		t.Fatalf("unmoved update = %q", out)
	}
	moved := hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	if out := h.ok("skills", "update", "--fleet", "triage"); !strings.Contains(out, "triage,updated,"+moved) {
		t.Fatalf("update = %q", out)
	}
	for _, root := range fleet.SkillRoots {
		if got := readSkill(t, skillPath(h.home, root, "triage")); !strings.Contains(got, "\nv2\n") || !strings.Contains(got, "managed-by: hand") {
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
	h.ok("skills", "add", "--fleet", src)
	if out := h.ok("skills", "update", "--fleet"); !strings.Contains(out, "notes,current") {
		t.Fatalf("update = %q", out)
	}
	writeFiles(t, src, skillFiles("notes", "two"))
	if out := h.ok("skills", "update", "--fleet"); !strings.Contains(out, "notes,updated") {
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
	h.ok("skills", "add", "--fleet", "triage")
	edited := skillPath(h.home, ".pi", "triage")
	mine := readSkill(t, edited) + "my note\n"
	if err := os.WriteFile(edited, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	out, errOut, code := h.run("skills", "update", "--fleet", "triage")
	if code != 3 || !strings.Contains(out, "triage,conflict,") || !strings.Contains(out, "reconcile first") || !strings.Contains(out, "task add --goal") || !strings.Contains(errOut, "1 skill(s) not updated") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if readSkill(t, edited) != mine || strings.Contains(readSkill(t, skillPath(h.home, ".agents", "triage")), "v2") {
		t.Fatal("an update overwrote a changed skill")
	}
	if _, _, code := h.run("skills", "remove", "--fleet", "triage"); code != 3 || readSkill(t, edited) != mine {
		t.Fatalf("remove of a changed skill: code=%d", code)
	}
}

func TestSkillsLeaveUnmanagedSkillsAlone(t *testing.T) {
	h := skillsHarness(t)
	mine := filepath.Join(h.home, ".agents", "skills", "mine")
	writeFiles(t, mine, skillFiles("mine", "hand-written"))
	for _, name := range []string{"secondhand", "secondhand-cleanup", "mine"} {
		out, _, code := h.run("skills", "remove", "--fleet", name)
		if code != 3 || !strings.Contains(out, name+",refused") {
			t.Fatalf("remove %s: code=%d out=%q", name, code, out)
		}
	}
	if out, _, code := h.run("skills", "update", "--fleet", "secondhand"); code != 3 || !strings.Contains(out, "secondhand,refused") {
		t.Fatalf("update: code=%d out=%q", code, out)
	}
	src := filepath.Join(t.TempDir(), "secondhand")
	writeFiles(t, src, skillFiles("secondhand", "mine"))
	if _, errOut, code := h.run("skills", "add", "--fleet", src); code != 3 || !strings.Contains(errOut, "already exists") {
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
		if got := readSkill(t, skillPath(h.vars["HOME"], root, "notes")); !strings.Contains(got, "managed-by: hand") {
			t.Fatalf("%s: %q", root, got)
		}
		if _, err := os.Stat(filepath.Dir(skillPath(h.home, root, "notes"))); !os.IsNotExist(err) {
			t.Fatalf("the fleet got the user skill: %v", err)
		}
	}
	if e := lockEntries(t, userLock(h.vars["HOME"]))["notes"]; e.Source != src || e.ComputedHash == "" {
		t.Fatalf("state = %+v", e)
	}
	if _, err := os.Stat(fleetLock(h.home)); !os.IsNotExist(err) {
		t.Fatalf("the fleet got the user lock: %v", err)
	}
	if list := h.ok("skills", "list", "--user"); !strings.Contains(list, "notes,"+toonCell(src)+",\"\",user,yes") || strings.Contains(list, "secondhand") {
		t.Fatalf("list = %q", list)
	}
	if list := h.ok("skills", "list", "--fleet"); strings.Contains(list, "notes") {
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
		if _, _, code := h.run("skills", "add", "--fleet", src); code == 0 {
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
	h.ok("skills", "add", "--fleet", src)
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
	writeFiles(t, decoy, map[string]string{"SKILL.md": "---\nname: decoy\nmetadata:\n  managed-by: hand\n---\n"})
	for _, cmd := range []string{"remove", "update"} {
		if _, errOut, code := h.run("skills", cmd, "--user", "../decoy"); code != 2 || !strings.Contains(errOut, "not a skill name") {
			t.Fatalf("%s: code=%d stderr=%q", cmd, code, errOut)
		}
	}
	if _, err := os.Stat(filepath.Join(decoy, "SKILL.md")); err != nil {
		t.Fatalf("a folder outside skills/ was touched: %v", err)
	}
	if err := os.WriteFile(userLock(home), []byte(`{"version":2,"skills":{"../decoy":{"source":"x","sourceType":"local","computedHash":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := h.ok("skills", "remove", "--user"); strings.Contains(out, "decoy") {
		t.Fatalf("a lock key was acted on: %q", out)
	}
	if _, err := os.Stat(filepath.Join(decoy, "SKILL.md")); err != nil {
		t.Fatalf("a state key walked out: %v", err)
	}
}

func TestSkillsUpdateThatFailsLeavesEveryRootOldAndARerunCompletes(t *testing.T) {
	cases := []struct {
		name   string
		next   bool
		break_ func(t *testing.T, h *harness)
	}{
		{"staging", false, func(t *testing.T, h *harness) {
			skipUnlessPermsHold(t)
			locked := filepath.Join(h.home, ".pi", "skills")
			if err := os.Chmod(locked, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
		}},
		{"move aside", true, failRename(t, func(from, to string) bool {
			return strings.HasSuffix(from, filepath.Join(".grok", "skills", "foo"))
		})},
		{"swap in", true, failRename(t, func(from, to string) bool {
			return strings.HasSuffix(to, filepath.Join(".pi", "skills", "foo")) && !strings.HasSuffix(from, ".old")
		})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := skillsHarness(t)
			src := filepath.Join(t.TempDir(), "foo")
			writeFiles(t, src, skillFiles("foo", "one"))
			h.ok("skills", "add", "--fleet", src)
			before := skillState(t, h.home)["foo"]
			writeFiles(t, src, skillFiles("foo", "two"))
			c.break_(t, h)
			if _, _, code := h.run("skills", "update", "--fleet", "foo"); code == 0 {
				t.Fatal("update succeeded")
			}
			fleet.Rename = fsxRename
			for _, root := range fleet.SkillRoots {
				if got := readSkill(t, skillPath(h.home, root, "foo")); !strings.Contains(got, "\none\n") {
					t.Fatalf("%s is half-new: %q", root, got)
				}
				entries, err := os.ReadDir(filepath.Join(h.home, root, "skills"))
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range entries {
					if strings.HasPrefix(e.Name(), ".hand-foo-") {
						t.Fatalf("%s left %s behind", root, e.Name())
					}
				}
			}
			after, next := skillState(t, h.home)["foo"], pendingSwaps(t, h.home)["foo"]
			if c.next && (next == "" || next == before.ComputedHash || after != before) {
				t.Fatalf("state before the swap = %+v next %q, was %+v", after, next, before)
			}
			if !c.next && (after != before || next != "") {
				t.Fatalf("state = %+v next %q, was %+v", after, next, before)
			}
			if err := os.Chmod(filepath.Join(h.home, ".pi", "skills"), 0o755); err != nil && runtime.GOOS != "windows" {
				t.Fatal(err)
			}
			if out := h.ok("skills", "update", "--fleet", "foo"); !strings.Contains(out, "foo,updated") {
				t.Fatalf("rerun = %q", out)
			}
			for _, root := range fleet.SkillRoots {
				if got := readSkill(t, skillPath(h.home, root, "foo")); !strings.Contains(got, "\ntwo\n") {
					t.Fatalf("%s: %q", root, got)
				}
			}
			if e := skillState(t, h.home)["foo"]; e.ComputedHash == before.ComputedHash || len(pendingSwaps(t, h.home)) != 0 {
				t.Fatalf("state = %+v, pending %v", e, pendingSwaps(t, h.home))
			}
		})
	}
}

func fsxRename(from, to string) error { return fsx.Rename(from, to) }

func failRename(t *testing.T, hit func(from, to string) bool) func(*testing.T, *harness) {
	return func(t *testing.T, h *harness) {
		t.Cleanup(func() { fleet.Rename = fsxRename })
		fleet.Rename = func(from, to string) error {
			if hit(from, to) {
				return errors.New("injected rename failure")
			}
			return fsx.Rename(from, to)
		}
	}
}

func TestSkillsListSkipsDotFolders(t *testing.T) {
	h := skillsHarness(t)
	writeFiles(t, filepath.Join(h.home, ".agents", "skills", ".hand-foo-123.old"), skillFiles("foo", "one"))
	if out := h.ok("skills", "list", "--fleet"); strings.Contains(out, ".hand-foo") {
		t.Fatalf("list = %q", out)
	}
}

func TestSkillsRerunFinishesAnInterruptedSwap(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "foo")
	writeFiles(t, src, skillFiles("foo", "one"))
	h.ok("skills", "add", "--fleet", src)
	old := readSkill(t, skillPath(h.home, ".pi", "foo"))
	writeFiles(t, src, skillFiles("foo", "two"))
	h.ok("skills", "update", "--fleet", "foo")
	newSum := skillState(t, h.home)["foo"].ComputedHash
	if err := os.WriteFile(skillPath(h.home, ".pi", "foo"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := skillState(t, h.home)["foo"]
	entry.ComputedHash = fleetSum(t, filepath.Dir(skillPath(h.home, ".pi", "foo")))
	b, _ := json.Marshal(map[string]any{"version": 2, "skills": map[string]fleet.LockEntry{"foo": entry}})
	if err := os.WriteFile(fleetLock(h.home), b, 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(map[string]string{"foo": newSum})
	if err := os.WriteFile(filepath.Join(h.home, ".agents", "skills", ".hand-skills-next.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if out := h.ok("skills", "update", "--fleet", "foo"); !strings.Contains(out, "foo,updated") {
		t.Fatalf("rerun = %q", out)
	}
	if got := readSkill(t, skillPath(h.home, ".pi", "foo")); !strings.Contains(got, "\ntwo\n") {
		t.Fatalf("pi = %q", got)
	}
	if e := skillState(t, h.home)["foo"]; e.ComputedHash != newSum || len(pendingSwaps(t, h.home)) != 0 {
		t.Fatalf("state = %+v, pending %v", e, pendingSwaps(t, h.home))
	}
}

func fleetSum(t *testing.T, dir string) string {
	t.Helper()
	sum, err := fleet.ComputedHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func TestSkillsMarkerCountsOnlyInTheFrontmatter(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "quote")
	writeFiles(t, src, skillFiles("quote", "one"))
	h.ok("skills", "add", "--fleet", src)
	quote := filepath.Join(h.home, ".agents", "skills", "quote")
	writeFiles(t, quote, map[string]string{"SKILL.md": "---\nname: quote\n---\n\nmetadata:\n  managed-by: hand\n"})
	if out, _, code := h.run("skills", "remove", "--fleet", "quote"); code != 3 || !strings.Contains(out, "is not marked as managed") {
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
	h.ok("skills", "add", "--fleet", src)
	if err := os.WriteFile(fleetLock(h.home), []byte("{\"version\":2,\"skills\":{}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _, code := h.run("skills", "remove", "--fleet", "notes"); code != 3 || !strings.Contains(out, "no recorded copy") {
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
	if out := h.ok("skills", "add", "--fleet", src); !strings.Contains(out, "crlf,added") {
		t.Fatalf("add = %q", out)
	}
	got := readSkill(t, skillPath(h.home, ".claude", "crlf"))
	if !strings.HasPrefix(got, "---\nname: crlf\ndescription: x\nmetadata:\n  managed-by: hand\n---\n") {
		t.Fatalf("got %q", got)
	}
	if out := h.ok("skills", "remove", "--fleet", "crlf"); !strings.Contains(out, "crlf,removed") {
		t.Fatalf("remove = %q", out)
	}
}

func TestSkillsMarkerFollowsTheMetadataIndent(t *testing.T) {
	h := skillsHarness(t)
	src := filepath.Join(t.TempDir(), "wide")
	writeFiles(t, src, map[string]string{"SKILL.md": "---\nname: wide\ndescription: x\nmetadata:\n    version: 1\n---\n"})
	h.ok("skills", "add", "--fleet", src)
	if got := readSkill(t, skillPath(h.home, ".claude", "wide")); got != "---\nname: wide\ndescription: x\nmetadata:\n    managed-by: hand\n    version: 1\n---\n" {
		t.Fatalf("got %q", got)
	}
	h.ok("skills", "remove", "--fleet", "wide")
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
	if _, errOut, code := h.run("skills", "add", "--fleet", src); code == 0 || !strings.Contains(errOut, "32 MiB") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Dir(skillPath(h.home, ".claude", "big"))); !os.IsNotExist(err) {
		t.Fatalf("left a folder: %v", err)
	}
}

func TestSkillsConcurrentAddsKeepEveryRecord(t *testing.T) {
	h := skillsHarness(t)
	names := []string{"a1", "a2", "a3", "a4", "a5", "a6"}
	type result struct {
		name, errOut string
		code         int
	}
	results := make([]result, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		src := filepath.Join(t.TempDir(), name)
		writeFiles(t, src, skillFiles(name, "one"))
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errOut, code := h.run("skills", "add", "--fleet", src)
			results[i] = result{name, errOut, code}
		}()
	}
	wg.Wait()
	for _, r := range results {
		if r.code != 0 {
			t.Fatalf("hand skills add %s: exit %d: %s", r.name, r.code, r.errOut)
		}
	}
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
