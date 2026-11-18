package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fleet"
)

func skillSource(t *testing.T, name, text string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	writeFiles(t, src, skillFiles(name, text))
	return src
}

func projectHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := skillsHarness(t)
	repo := gitRepo(t)
	h.ok("project", "add", "app", repo)
	h.cwd = repo
	return h, repo
}

func untracked(t *testing.T, repo string) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(gitIn(t, repo, "status", "--porcelain", "-uall"), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}

func TestSkillsAddInACheckoutInstallsOneRootAndTheRepoLock(t *testing.T) {
	h, repo := projectHarness(t)
	src := skillSource(t, "notes", "one")
	if out := h.ok("skills", "add", src); !strings.Contains(out, "notes,added") {
		t.Fatalf("add = %q", out)
	}
	for _, root := range fleet.SkillRoots {
		_, err := os.Stat(filepath.Join(repo, root, "skills", "notes", "SKILL.md"))
		if (root == ".agents") != (err == nil) {
			t.Fatalf("%s: %v", root, err)
		}
	}
	if got := readSkill(t, skillPath(repo, ".agents", "notes")); !strings.Contains(got, "metadata:\n  managed-by: hand\n---\n") {
		t.Fatalf("SKILL.md = %q", got)
	}
	e := lockEntries(t, fleetLock(repo))["notes"]
	want, err := fleet.ComputedHash(filepath.Join(repo, ".agents", "skills", "notes"))
	if err != nil || e.ComputedHash != want || e.SourceType != "local" {
		t.Fatalf("lock entry = %+v, want hash %s (%v)", e, want, err)
	}
	if filepath.IsAbs(e.Source) || filepath.ToSlash(e.Source) != e.Source || !strings.HasPrefix(e.Source, "../") || filepath.Clean(filepath.Join(repo, filepath.FromSlash(e.Source))) != filepath.Clean(src) {
		t.Fatalf("lock source %q is not the portable path to %s", e.Source, src)
	}
	if got := untracked(t, repo); !slices.Equal(got, []string{"?? .agents/skills/notes/SKILL.md", "?? .agents/skills/skills.json", "?? skills-lock.json"}) {
		t.Fatalf("the checkout holds more than the skill, its subscription and its lock: %q", got)
	}
	if _, err := os.Stat(filepath.Join(h.home, "skills-lock.json")); !os.IsNotExist(err) {
		t.Fatalf("the fleet got the project's lock: %v", err)
	}
	list := h.ok("skills", "list")
	if !strings.Contains(list, "notes,"+toonCell(e.Source)+",\"\",project,yes") {
		t.Fatalf("list = %q", list)
	}
}

func TestSkillsInTheFleetHomeNeedAProjectAFleetOrAUser(t *testing.T) {
	h, repo := projectHarness(t)
	h.cwd = ""
	src := skillSource(t, "notes", "one")
	for _, args := range [][]string{{"add", src}, {"list"}, {"update"}, {"remove", "notes"}} {
		_, errOut, code := h.run(append([]string{"skills"}, args...)...)
		if code != 2 || !strings.Contains(errOut, "pass --project NAME (app), --fleet or --user") {
			t.Fatalf("%v: code=%d stderr=%q", args, code, errOut)
		}
	}
	if _, errOut, code := h.run("skills", "add", "--project", "nope", src); code != 3 || !strings.Contains(errOut, "nope") {
		t.Fatalf("unknown project: code=%d stderr=%q", code, errOut)
	}
	if _, _, code := h.run("skills", "add", "--user", "--fleet", src); code != 2 {
		t.Fatalf("two scopes: code=%d", code)
	}
	if out := h.ok("skills", "add", "--project", "app", src); !strings.Contains(out, "notes,added") {
		t.Fatalf("add = %q", out)
	}
	if _, err := os.Stat(skillPath(repo, ".agents", "notes")); err != nil {
		t.Fatal(err)
	}
	source := lockEntries(t, fleetLock(repo))["notes"].Source
	if filepath.IsAbs(source) {
		t.Fatalf("lock source %q is absolute", source)
	}
	if list := h.ok("skills", "list", "--project", "app"); !strings.Contains(list, "notes,"+toonCell(source)+",\"\",project,yes") {
		t.Fatalf("list --project = %q", list)
	}
	if _, err := os.Stat(filepath.Join(h.home, ".agents", "skills", "notes")); !os.IsNotExist(err) {
		t.Fatalf("the fleet home got a project skill: %v", err)
	}
}

func TestSkillsFleetAndUserFlagsLeaveTheCheckoutAlone(t *testing.T) {
	h, repo := projectHarness(t)
	src := skillSource(t, "notes", "one")
	h.ok("skills", "add", "--fleet", src)
	h.ok("skills", "add", "--user", skillSource(t, "other", "one"))
	if got := untracked(t, repo); len(got) != 0 {
		t.Fatalf("the checkout changed: %q", got)
	}
	for _, root := range fleet.SkillRoots {
		if _, err := os.Stat(skillPath(h.home, root, "notes")); err != nil {
			t.Fatal(err)
		}
	}
	if list := h.ok("skills", "list"); strings.Contains(list, "notes") {
		t.Fatalf("project list shows a fleet skill: %q", list)
	}
}

func TestSkillsRefuseAProjectPathThatIsDirtyInGit(t *testing.T) {
	h, repo := projectHarness(t)
	src := skillSource(t, "notes", "one")
	stray := filepath.Join(repo, ".agents", "skills", "notes")
	writeFiles(t, stray, map[string]string{"draft.md": "x"})
	if _, errOut, code := h.run("skills", "add", src); code != 3 || !strings.Contains(errOut, ".agents/skills/notes has uncommitted changes") {
		t.Fatalf("add over an untracked folder: code=%d stderr=%q", code, errOut)
	}
	if err := os.RemoveAll(stray); err != nil {
		t.Fatal(err)
	}
	h.ok("skills", "add", src)
	if out, _, code := h.run("skills", "update", "notes"); code != 3 || !strings.Contains(out, "notes,refused,") || !strings.Contains(out, "uncommitted") {
		t.Fatalf("update of an uncommitted skill: code=%d out=%q", code, out)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "skills: notes")
	writeFiles(t, filepath.Dir(src), map[string]string{"notes/SKILL.md": skillFiles("notes", "two")["SKILL.md"]})
	if out := h.ok("skills", "update", "notes"); !strings.Contains(out, "notes,updated") {
		t.Fatalf("update = %q", out)
	}
	if out, _, code := h.run("skills", "remove", "notes"); code != 3 || !strings.Contains(out, "uncommitted") {
		t.Fatalf("remove of a modified skill: code=%d out=%q", code, out)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "skills: notes two")
	if out := h.ok("skills", "remove", "notes"); !strings.Contains(out, "notes,removed") {
		t.Fatalf("remove = %q", out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents", "skills", "notes")); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}
	if _, ok := lockEntries(t, fleetLock(repo))["notes"]; ok {
		t.Fatal("the lock still has notes")
	}
}

func TestSkillsRecordAnInsideSourceRelativeToTheCheckout(t *testing.T) {
	h, repo := projectHarness(t)
	src := filepath.Join(repo, "sources", "notes")
	writeFiles(t, src, skillFiles("notes", "one"))
	h.ok("skills", "add", src)
	if e := lockEntries(t, fleetLock(repo))["notes"]; e.Source != "./sources/notes" {
		t.Fatalf("lock source = %q, want ./sources/notes", e.Source)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "skills: notes")
	writeFiles(t, src, skillFiles("notes", "two"))
	if out := h.ok("skills", "update", "notes"); !strings.Contains(out, "notes,updated") {
		t.Fatalf("update = %q", out)
	}
	e := lockEntries(t, fleetLock(repo))["notes"]
	if e.Source != "./sources/notes" || e.ComputedHash == "" {
		t.Fatalf("the update lost the portable source: %+v", e)
	}
}

func TestAWorkerAttemptStartedFromAProjectCheckoutHasTheInstalledSkill(t *testing.T) {
	fx := newAttemptFixture(t)
	src := skillSource(t, "notes", "one")
	fx.h.cwd = fx.repo
	fx.h.ok("skills", "add", src)
	gitIn(t, fx.repo, "add", "-A")
	gitIn(t, fx.repo, "commit", "-q", "-m", "skills: notes")
	gitIn(t, fx.repo, "push", "-q", "origin", "HEAD:main")
	fx.h.cwd = ""
	fx.start()
	wt := fx.h.worktree("t1-a1")
	if call := fx.rt.lastCreate(); call.CWD != wt {
		t.Fatalf("the worker runs in %s, not its worktree %s", call.CWD, wt)
	}
	if got := readSkill(t, skillPath(wt, ".agents", "notes")); !strings.Contains(got, "name: notes") {
		t.Fatalf("the worktree's skill = %q", got)
	}
	if lockEntries(t, fleetLock(wt))["notes"].ComputedHash == "" {
		t.Fatal("the worktree has no lock for the skill")
	}
	fx.h.cwd = wt
	source := lockEntries(t, fleetLock(wt))["notes"].Source
	if filepath.IsAbs(source) {
		t.Fatalf("lock source %q is absolute", source)
	}
	if list := fx.h.ok("skills", "list"); !strings.Contains(list, "notes,"+toonCell(source)+",\"\",project,yes") {
		t.Fatalf("the worker lists %q", list)
	}
}

func TestSkillsRefuseASpecBreakWithTheFieldNamedAndInstallNothing(t *testing.T) {
	h := skillsHarness(t)
	for name, c := range map[string]struct{ file, want string }{
		"nofront":  {"# nofront\n", "frontmatter missing"},
		"mismatch": {"---\nname: other\ndescription: x\n---\n", `name "other" must equal`},
		"nodesc":   {"---\nname: nodesc\n---\n", "description is 0 characters"},
		"toolong":  {"---\nname: toolong\ndescription: x\n---\n" + strings.Repeat("x\n", 500), "keep SKILL.md under 500"},
		"flowmeta": {"---\nname: flowmeta\ndescription: x\nmetadata: {version: \"1\"}\n---\n", "metadata must be a block map"},
	} {
		src := filepath.Join(t.TempDir(), name)
		writeFiles(t, src, map[string]string{"SKILL.md": c.file})
		_, errOut, code := h.run("skills", "add", "--fleet", src)
		if code != 2 || !strings.Contains(errOut, c.want) || strings.Contains(errOut, "hand-skill-") {
			t.Fatalf("%s: code=%d stderr=%q, want %q and no temp path", name, code, errOut, c.want)
		}
		for _, root := range fleet.SkillRoots {
			if _, err := os.Stat(filepath.Dir(skillPath(h.home, root, name))); !os.IsNotExist(err) {
				t.Fatalf("%s left a folder in %s: %v", name, root, err)
			}
		}
		if readSkill(t, filepath.Join(src, "SKILL.md")) != c.file {
			t.Fatalf("%s: the source was rewritten", name)
		}
	}
	if _, err := os.Stat(fleetLock(h.home)); !os.IsNotExist(err) {
		t.Fatalf("a refused add wrote a lock: %v", err)
	}
}

func TestSkillsUpdateRefusesASourceThatBrokeTheSpec(t *testing.T) {
	h := skillsHarness(t)
	src := skillSource(t, "notes", "one")
	h.ok("skills", "add", "--fleet", src)
	before := readSkill(t, skillPath(h.home, ".claude", "notes"))
	writeFiles(t, src, map[string]string{"SKILL.md": "---\nname: notes\n---\nno description\n"})
	if out, _, code := h.run("skills", "update", "--fleet", "notes"); code != 3 || !strings.Contains(out, "description is 0 characters") {
		t.Fatalf("update: code=%d out=%q", code, out)
	}
	if readSkill(t, skillPath(h.home, ".claude", "notes")) != before {
		t.Fatal("a refused update changed the copy")
	}
}

func TestSkillsLockKeepsTheEntriesOfOtherToolsAndRefusesAnotherVersion(t *testing.T) {
	h := skillsHarness(t)
	foreign := `{"source":"vercel-labs/agent-skills","sourceType":"github","skillPath":"skills/pdf/SKILL.md","computedHash":"abc","installMode":"copy","skillName":"pdf"}`
	if err := os.WriteFile(fleetLock(h.home), []byte(`{"version":2,"skills":{"pdf":`+foreign+`}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(h.home, ".agents", "skills", "pdf"), skillFiles("pdf", "theirs"))
	h.ok("skills", "add", "--fleet", skillSource(t, "notes", "one"))
	lock := readSkill(t, fleetLock(h.home))
	if !strings.Contains(lock, `"installMode": "copy"`) || !strings.Contains(lock, `"skillName": "pdf"`) || !strings.Contains(lock, `"computedHash": "abc"`) {
		t.Fatalf("the other tool's entry changed:\n%s", lock)
	}
	if strings.Index(lock, `"notes"`) > strings.Index(lock, `"pdf"`) {
		t.Fatalf("skills are not sorted by name:\n%s", lock)
	}
	if out := h.ok("skills", "update", "--fleet"); strings.Contains(out, "pdf") {
		t.Fatalf("update touched the other tool's skill: %q", out)
	}
	if out, _, code := h.run("skills", "remove", "--fleet", "pdf"); code != 3 || !strings.Contains(out, "pdf,refused") {
		t.Fatalf("remove of the other tool's skill: code=%d out=%q", code, out)
	}
	if err := os.WriteFile(fleetLock(h.home), []byte(`{"version":1,"skills":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := h.run("skills", "list", "--fleet"); code != 2 || !strings.Contains(errOut, "lock version 1") {
		t.Fatalf("version 1: code=%d stderr=%q", code, errOut)
	}
}

func legacyHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var names []string
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range names {
		rel, _ := filepath.Rel(dir, path)
		b, _ := os.ReadFile(path)
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestSkillsMigrateThePreV2StateOnceOnRead(t *testing.T) {
	h := skillsHarness(t)
	legacy := func(name, nl, body string) {
		front := strings.ReplaceAll("---\nname: "+name+"\ndescription: x\nmetadata:\n  managed-by: hand-skills\n---\n\nbody\n"+body, "\n", nl)
		for _, root := range fleet.SkillRoots {
			writeFiles(t, filepath.Join(h.home, root, "skills", name), map[string]string{"SKILL.md": front})
		}
	}
	legacy("kept", "\n", "    managed-by: hand-skills\n")
	legacy("crlf", "\r\n", "")
	legacy("edited", "\n", "")
	for _, root := range fleet.SkillRoots {
		writeFiles(t, filepath.Join(h.home, root, "skills", "bodyonly"), map[string]string{"SKILL.md": "---\nname: bodyonly\ndescription: x\n---\n\n  managed-by: hand-skills\n"})
	}
	sum := func(name string) string { return legacyHash(t, filepath.Join(h.home, ".agents", "skills", name)) }
	state := fmt.Sprintf(`{"kept":{"source":"https://example.com/kept.git","path":"skills/kept","commit":"c0ffee","scope":"fleet","sum":%q},"crlf":{"source":"/old/crlf","scope":"fleet","sum":%q},"bodyonly":{"source":"/old/bodyonly","scope":"fleet","sum":%q},"edited":{"source":"/old/edited","scope":"fleet","sum":"stale"}}`, sum("kept"), sum("crlf"), sum("bodyonly"))
	old := filepath.Join(h.home, ".agents", "skills", ".hand-skills.json")
	if err := os.WriteFile(old, []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	list := h.ok("skills", "list", "--fleet")
	for _, want := range []string{`kept,"https://example.com/kept.git#skills/kept",c0ffee,fleet,yes`, "crlf,/old/crlf,\"\",fleet,yes", "edited,\"\",\"\",fleet,no"} {
		if !strings.Contains(list, want) {
			t.Fatalf("list missing %q:\n%s", want, list)
		}
	}
	var rest map[string]json.RawMessage
	if b, err := os.ReadFile(old); err != nil || json.Unmarshal(b, &rest) != nil || len(rest) != 2 || rest["edited"] == nil || rest["bodyonly"] == nil {
		t.Fatalf("the old state file keeps exactly the entries that did not move: %q, %v", b, err)
	}
	e := skillState(t, h.home)["kept"]
	if want, _ := fleet.ComputedHash(filepath.Join(h.home, ".agents", "skills", "kept")); e.ComputedHash != want || e.Commit != "c0ffee" || e.SkillPath != "skills/kept/SKILL.md" {
		t.Fatalf("entry = %+v", e)
	}
	for _, root := range fleet.SkillRoots {
		if got := readSkill(t, skillPath(h.home, root, "kept")); !strings.Contains(got, "\n  managed-by: hand\n---\n") || !strings.HasSuffix(got, "\nbody\n    managed-by: hand-skills\n") {
			t.Fatalf("%s: %q", root, got)
		}
		if got := readSkill(t, skillPath(h.home, root, "crlf")); !strings.Contains(got, "\n  managed-by: hand\n---\n") {
			t.Fatalf("%s: %q", root, got)
		}
	}
	if got := readSkill(t, skillPath(h.home, ".claude", "edited")); !strings.Contains(got, "managed-by: hand-skills") {
		t.Fatalf("a skill that no longer matches its old sum was rewritten: %q", got)
	}
	for _, name := range []string{"edited", "bodyonly"} {
		if _, ok := skillState(t, h.home)[name]; ok {
			t.Fatalf("%s was adopted", name)
		}
	}
}

func TestSkillsKeepThePreV2StateWhenNothingMoves(t *testing.T) {
	h := skillsHarness(t)
	old := filepath.Join(h.home, ".agents", "skills", ".hand-skills.json")
	state := `{"gone":{"source":"/old/gone","scope":"fleet","sum":"stale"}}`
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	h.ok("skills", "list", "--fleet")
	if b, err := os.ReadFile(old); err != nil || string(b) != state {
		t.Fatalf("the pre-v2 file was changed or removed: %q, %v", b, err)
	}
	if _, err := os.Stat(fleetLock(h.home)); !os.IsNotExist(err) {
		t.Fatalf("a migration that moved nothing wrote a lock: %v", err)
	}
}

func TestSkillsUpdateRefusesALockedPathThatLeavesTheClone(t *testing.T) {
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v1")["SKILL.md"], "outside/triage/SKILL.md": skillFiles("triage", "elsewhere")["SKILL.md"]})
	isolateGit(t, hub.bare)
	h.ok("skills", "add", "--fleet", "triage")
	lock := readSkill(t, fleetLock(h.home))
	if err := os.WriteFile(fleetLock(h.home), []byte(strings.Replace(lock, "skills/triage/SKILL.md", "../repo/outside/triage/SKILL.md", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _, code := h.run("skills", "update", "--fleet", "triage"); code != 3 || !strings.Contains(out, "leaves the repository") {
		t.Fatalf("update: code=%d out=%q", code, out)
	}
	if strings.Contains(readSkill(t, skillPath(h.home, ".claude", "triage")), "elsewhere") {
		t.Fatal("update copied a folder from outside the skill's path in the clone")
	}
}

func TestSkillsUpdateRefusesALockedPathThroughASymlinkOutOfTheClone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	h := skillsHarness(t)
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{"triage/SKILL.md": skillFiles("triage", "outside")["SKILL.md"]})
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v1")["SKILL.md"]})
	if err := os.Symlink(outside, filepath.Join(hub.work, "link")); err != nil {
		t.Fatal(err)
	}
	hub.commit(map[string]string{"skills/triage/note.md": "x\n"})
	isolateGit(t, hub.bare)
	h.ok("skills", "add", "--fleet", "triage")
	lock := readSkill(t, fleetLock(h.home))
	if err := os.WriteFile(fleetLock(h.home), []byte(strings.Replace(lock, "skills/triage/SKILL.md", "link/triage/SKILL.md", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _, code := h.run("skills", "update", "--fleet", "triage"); code != 3 || !strings.Contains(out, "leaves the repository") {
		t.Fatalf("update: code=%d out=%q", code, out)
	}
	if strings.Contains(readSkill(t, skillPath(h.home, ".claude", "triage")), "outside") {
		t.Fatal("update copied a folder from outside the clone")
	}
}
