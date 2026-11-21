package cli_test

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

const prURL = "https://github.com/atqamz/skills/pull/7"

func ghMain(args []string) int {
	p := fakebin.Params()
	b, _ := json.Marshal(args)
	fakebin.Append(p["log"], string(b))
	if p["stderr"] != "" {
		fmt.Fprint(os.Stderr, p["stderr"])
		return 1
	}
	fmt.Println(prURL)
	return 0
}

type proposeFixture struct {
	t    *testing.T
	h    *harness
	hub  *skillRemote
	tmp  string
	log  string
	bin  string
	head string
}

func newProposeFixture(t *testing.T, gitConfig string) *proposeFixture {
	t.Helper()
	f := &proposeFixture{t: t, tmp: isolateTemp(t)}
	f.h = skillsHarness(t)
	f.hub = newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{
		"skills/triage/SKILL.md":        skillFiles("triage", "v1")["SKILL.md"],
		"skills/triage/references/a.md": "a\n",
		"skills/other/SKILL.md":         skillFiles("other", "v1")["SKILL.md"],
	})
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	text := "[url \"file://" + filepath.ToSlash(f.hub.bare) + "\"]\n\tinsteadOf = " + skillsURL + "\n[user]\n\tname = Pat Author\n\temail = pat@example.com\n" + gitConfig
	if err := os.WriteFile(cfg, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	f.log = filepath.Join(t.TempDir(), "gh.log")
	f.bin = t.TempDir()
	fakebin.Install(t, f.bin, "gh", "gh", map[string]string{"log": f.log})
	f.h.vars["PATH"] = f.bin
	f.head = f.refs()["refs/heads/main"]
	return f
}

func (f *proposeFixture) refs() map[string]string {
	f.t.Helper()
	got := map[string]string{}
	for line := range strings.Lines(skillGit(f.t, f.hub.bare, "for-each-ref", "--format=%(refname) %(objectname)")) {
		name, id, _ := strings.Cut(strings.TrimSpace(line), " ")
		got[name] = id
	}
	return got
}

func (f *proposeFixture) edit(name string) string {
	f.t.Helper()
	path := skillPath(f.h.home, ".claude", name)
	text := readSkill(f.t, path) + "\nmy own line\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return text
}

func (f *proposeFixture) untouched(why string) {
	f.t.Helper()
	if refs := f.refs(); len(refs) != 1 || refs["refs/heads/main"] != f.head {
		f.t.Fatalf("%s: the source moved: %v", why, refs)
	}
	if _, err := os.Stat(f.log); err == nil {
		f.t.Fatalf("%s: gh ran", why)
	}
	if left := tempLeft(f.t, f.tmp); len(left) != 0 {
		f.t.Fatalf("%s: temp folders left: %q", why, left)
	}
}

func TestSkillsProposeCommitsTheEditedFolderAndOpensAPullRequest(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	mine := f.edit("triage")
	dir := filepath.Dir(skillPath(f.h.home, ".claude", "triage"))
	if err := os.Remove(filepath.Join(dir, "references", "a.md")); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{"scripts/new.sh": "echo new\n"})
	lockBefore := readSkill(t, fleetLock(f.h.home))
	out := f.h.ok("skills", "propose", "--fleet", "triage")
	if !strings.Contains(out, "triage,proposed,\""+prURL+"\"") {
		t.Fatalf("propose = %q", out)
	}
	refs := f.refs()
	var branch string
	for name := range refs {
		if rest, ok := strings.CutPrefix(name, "refs/heads/hand-skills/triage-"); ok && len(rest) == 8 {
			branch = strings.TrimPrefix(name, "refs/heads/")
		}
	}
	if len(refs) != 2 || branch == "" || refs["refs/heads/main"] != f.head {
		t.Fatalf("refs = %v", refs)
	}
	if got := skillGit(t, f.hub.bare, "log", "-1", "--format=%an|%ae|%cn|%ce|%P", branch); got != "Pat Author|pat@example.com|Pat Author|pat@example.com|"+f.head {
		t.Fatalf("commit = %q", got)
	}
	if got := skillGit(t, f.hub.bare, "log", "-1", "--format=%B", branch); got != "skills: update triage" {
		t.Fatalf("message = %q", got)
	}
	if got := skillGit(t, f.hub.bare, "diff", "--name-only", "main", branch); got != "skills/triage/SKILL.md\nskills/triage/references/a.md\nskills/triage/scripts/new.sh" {
		t.Fatalf("changed files = %q", got)
	}
	shown := skillGit(t, f.hub.bare, "show", branch+":skills/triage/SKILL.md")
	if !strings.Contains(shown, "my own line") || strings.Contains(shown, "managed-by") || shown != strings.TrimSuffix(strings.ReplaceAll(mine, "metadata:\n  managed-by: hand\n", ""), "\n") {
		t.Fatalf("the proposed file = %q", shown)
	}
	var argv []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(readSkill(t, f.log))), &argv); err != nil {
		t.Fatalf("gh log: %v", err)
	}
	if len(argv) != 10 || strings.Join(argv[:7], " ") != "pr create --repo atqamz/skills --head "+branch+" --title" || argv[7] != "skills: update triage" || argv[8] != "--body" {
		t.Fatalf("gh argv = %q", argv)
	}
	for _, want := range []string{"hand skills propose", "skills/triage/SKILL.md", "3 files changed", "skills/triage/references/a.md", "skills/triage/scripts/new.sh"} {
		if !strings.Contains(argv[9], want) {
			t.Errorf("the body lacks %q: %q", want, argv[9])
		}
	}
	for _, private := range []string{f.h.home, f.h.vars["HOME"], f.h.vars["SECONDHAND_HOME"], f.tmp} {
		if strings.Contains(argv[9], private) || strings.Contains(out, private) {
			t.Errorf("the output carries the private path %q", private)
		}
	}
	if readSkill(t, skillPath(f.h.home, ".claude", "triage")) != mine || readSkill(t, fleetLock(f.h.home)) != lockBefore {
		t.Fatal("propose changed the installed copy or the lock")
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
	if r := row(f.h.ok("skills", "outdated", "--fleet"), "triage"); !strings.HasPrefix(r, "triage,edited,") {
		t.Fatalf("after propose = %q", r)
	}
}

func TestSkillsProposeFromAProjectCheckout(t *testing.T) {
	f := newProposeFixture(t, "")
	h, repo := projectHarness(t)
	h.vars["PATH"] = f.bin
	h.ok("skills", "add", "triage")
	path := skillPath(repo, ".agents", "triage")
	if err := os.WriteFile(path, []byte(readSkill(t, path)+"\nproject edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := h.ok("skills", "propose", "triage"); !strings.Contains(out, "triage,proposed,\""+prURL+"\"") {
		t.Fatalf("propose = %q", out)
	}
	if refs := f.refs(); len(refs) != 2 || refs["refs/heads/main"] != f.head {
		t.Fatalf("refs = %v", refs)
	}
}

func TestSkillsProposeRefusals(t *testing.T) {
	type refusal struct {
		name string
		code int
		want string
		prep func(f *proposeFixture)
	}
	toolURL := func(t *testing.T) string {
		tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
		return "file://" + filepath.ToSlash(tool.bare)
	}
	target := map[string]string{"a skill Hand does not manage": "nothing", "a local source": "notes", "a git source that is not on GitHub": "tool-skill"}
	for _, tc := range []refusal{
		{"a clean copy", 3, "triage is current, not edited", func(f *proposeFixture) {
			f.h.ok("skills", "add", "--fleet", "triage")
		}},
		{"a clean copy that is behind", 3, "triage is behind, not edited", func(f *proposeFixture) {
			f.h.ok("skills", "add", "--fleet", "triage")
			f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
			f.head = f.refs()["refs/heads/main"]
		}},
		{"a conflict", 3, "triage is a conflict, changed here and moved at its source; nothing proposed; reconcile first", func(f *proposeFixture) {
			f.h.ok("skills", "add", "--fleet", "triage")
			f.edit("triage")
			f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
			f.head = f.refs()["refs/heads/main"]
		}},
		{"a copy that differs only by the marker", 3, "differs from its source only by Hand's marker", func(f *proposeFixture) {
			f.h.ok("skills", "add", "--fleet", "triage")
			path := skillPath(f.h.home, ".claude", "triage")
			if err := os.WriteFile(path, []byte(strings.Replace(readSkill(t, path), "metadata:\n  managed-by: hand\n", "", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"a skill Hand does not manage", 2, "nothing is not a skill Hand manages in this scope", func(f *proposeFixture) {}},
		{"a local source", 2, "which is not a GitHub repository", func(f *proposeFixture) {
			f.h.ok("skills", "add", "--fleet", skillSource(t, "notes", "v1"))
			f.edit("notes")
		}},
		{"a git source that is not on GitHub", 2, "which is not a GitHub repository", func(f *proposeFixture) {
			f.h.ok("skills", "add", "--fleet", toolURL(t))
			f.edit("tool-skill")
		}},
		{"no gh on PATH", 2, "propose opens its pull request with `gh pr create`", func(f *proposeFixture) {
			f.h.ok("skills", "add", "--fleet", "triage")
			f.edit("triage")
			f.h.vars["PATH"] = t.TempDir()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProposeFixture(t, "")
			tc.prep(f)
			out, errOut, code := f.h.run("skills", "propose", "--fleet", cmp.Or(target[tc.name], "triage"))
			if code != tc.code || !strings.Contains(errOut, tc.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
			}
			f.untouched(tc.name)
		})
	}
}

func TestSkillsProposeUsageNeedsOneName(t *testing.T) {
	h := skillsHarness(t)
	for _, args := range [][]string{{"skills", "propose", "--fleet"}, {"skills", "propose", "--fleet", "a", "b"}, {"skills", "propose", "--fleet", "--user", "a"}} {
		if _, errOut, code := h.run(args...); code != 2 || !strings.Contains(errOut, "usage: hand skills propose") {
			t.Errorf("%v: code=%d stderr=%q", args, code, errOut)
		}
	}
}

func TestSkillsProposeKeepsYourHooksAndSigning(t *testing.T) {
	t.Run("a commit hook that refuses", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the hook is a shell script")
		}
		hooks := t.TempDir()
		if err := os.WriteFile(filepath.Join(hooks, "commit-msg"), []byte("#!/bin/sh\necho hook-says-no >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		f := newProposeFixture(t, "[core]\n\thooksPath = "+filepath.ToSlash(hooks)+"\n")
		f.h.ok("skills", "add", "--fleet", "triage")
		f.edit("triage")
		if out, errOut, code := f.h.run("skills", "propose", "--fleet", "triage"); code == 0 || !strings.Contains(errOut, "hook-says-no") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
		}
		f.untouched("hook")
	})
	t.Run("a signing program that fails", func(t *testing.T) {
		gpg := t.TempDir()
		path := fakebin.Install(t, gpg, "gpgfake", "quiet", nil)
		f := newProposeFixture(t, "[commit]\n\tgpgsign = true\n[gpg]\n\tprogram = "+filepath.ToSlash(path)+"\n")
		f.h.ok("skills", "add", "--fleet", "triage")
		f.edit("triage")
		if out, errOut, code := f.h.run("skills", "propose", "--fleet", "triage"); code == 0 || !strings.Contains(errOut, "failed to sign") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
		}
		f.untouched("signing")
	})
}

func TestSkillsProposeSaysWhenThePullRequestFailsAfterThePush(t *testing.T) {
	f := newProposeFixture(t, "")
	fakebin.Install(t, f.bin, "gh", "gh", map[string]string{"log": f.log, "stderr": "GraphQL: no permission"})
	f.h.ok("skills", "add", "--fleet", "triage")
	f.edit("triage")
	_, errOut, code := f.h.run("skills", "propose", "--fleet", "triage")
	if code == 0 || !strings.Contains(errOut, "pushed the branch hand-skills/triage-") || !strings.Contains(errOut, "no permission") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if refs := f.refs(); len(refs) != 2 || refs["refs/heads/main"] != f.head {
		t.Fatalf("refs = %v", refs)
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsDriftStatesInListUpdateAndSync(t *testing.T) {
	f := newProposeFixture(t, "")
	f.hub.commit(map[string]string{
		"skills/cur/SKILL.md": skillFiles("cur", "v1")["SKILL.md"], "skills/beh/SKILL.md": skillFiles("beh", "v1")["SKILL.md"],
		"skills/edi/SKILL.md": skillFiles("edi", "v1")["SKILL.md"], "skills/con/SKILL.md": skillFiles("con", "v1")["SKILL.md"],
	})
	h := f.h
	for _, name := range []string{"cur", "beh", "edi", "con"} {
		h.ok("skills", "add", "--fleet", name)
	}
	f.edit("edi")
	f.edit("con")
	moved := f.hub.commit(map[string]string{"skills/beh/SKILL.md": skillFiles("beh", "v2")["SKILL.md"], "skills/con/SKILL.md": skillFiles("con", "v2")["SKILL.md"]})
	old := skillState(t, h.home)["cur"].Commit
	list := h.ok("skills", "list", "--fleet")
	for name, state := range map[string]string{"cur": "current", "beh": "behind", "edi": "edited", "con": "conflict"} {
		want := fmt.Sprintf("%s,\"%s#skills/%s\",%s,fleet,yes,main,%s,", name, skillsURL, name, old, state)
		if r := row(list, name); !strings.HasPrefix(r, want) {
			t.Errorf("list %s = %q, want prefix %q", name, r, want)
		}
	}
	if r := row(list, "secondhand"); !strings.HasSuffix(r, ",no,\"\",\"\",\"\"") {
		t.Errorf("an unmanaged row = %q", r)
	}
	if r := row(h.ok("skills", "outdated", "--fleet"), "con"); !strings.Contains(r, ",main,"+old+","+moved) {
		t.Errorf("outdated shows the commits wrongly: %q", r)
	}
	out, errOut, code := h.run("skills", "sync", "--fleet", "con")
	goal := ""
	for line := range strings.Lines(out) {
		if strings.Contains(line, "task add --goal") {
			goal = strings.TrimSpace(line)
		}
	}
	if code != 3 || row(out, "con") == "" || !strings.HasPrefix(row(out, "con"), "con,conflict,") || !strings.Contains(row(out, "con"), "reconcile first") || !strings.Contains(errOut, "1 skill(s) not synced") {
		t.Fatalf("sync of a conflict: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	for _, want := range []string{"`hand task add --goal \"Reconcile the skill con of the fleet scope", old[:12] + " to " + moved[:12], "hand skills update --fleet con", "hand skills propose --fleet con", "\" PROJECT \"Reconcile skill con\"`"} {
		if !strings.Contains(goal, want) {
			t.Errorf("the task line lacks %q: %q", want, goal)
		}
	}
	if strings.ContainsAny(strings.TrimSuffix(strings.TrimPrefix(goal[strings.Index(goal, "--goal ")+7:], "\""), "`"), "$`\\") {
		t.Errorf("the goal carries shell text: %q", goal)
	}
	if out, errOut, code := h.run("skills", "update", "--fleet", "con"); code != 3 || !strings.HasPrefix(row(out, "con"), "con,conflict,") || !strings.Contains(out, "task add --goal") || !strings.Contains(errOut, "1 skill(s) not updated") {
		t.Fatalf("update of a conflict: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if out, errOut, code := h.run("skills", "update", "--fleet", "edi"); code != 3 || !strings.HasPrefix(row(out, "edi"), "edi,refused,") || !strings.Contains(row(out, "edi"), "skills propose --fleet edi") || strings.Contains(out, "task add") {
		t.Fatalf("update of an edited copy: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	out, _, code = h.run("skills", "sync", "--fleet", "edi")
	if code != 0 || !strings.HasPrefix(row(out, "edi"), "edi,edited,") || !strings.Contains(row(out, "edi"), "skills propose --fleet edi") || strings.Contains(out, "task add") {
		t.Fatalf("sync of an edited copy: code=%d stdout=%q", code, out)
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}
