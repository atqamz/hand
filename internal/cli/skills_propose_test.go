package cli_test

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/fleet"
)

const prURL = "https://github.com/acme/hub/pull/7"

var ownRepoName = strings.TrimSuffix(strings.TrimPrefix(skillsURL, "https://github.com/"), ".git")

func ghMain(args []string) int {
	p := fakebin.Params()
	b, _ := json.Marshal(args)
	fakebin.Append(p["log"], string(b))
	if slices.Contains(args, "--body-file") {
		body, _ := io.ReadAll(os.Stdin)
		b, _ := json.Marshal(string(body))
		fakebin.Append(p["log"]+".body", string(b))
	}
	if p["stderr"] != "" {
		fmt.Fprint(os.Stderr, p["stderr"])
		return 1
	}
	fmt.Println(prURL)
	return 0
}

func ghBodies(t *testing.T, log string) []string {
	t.Helper()
	var all []string
	if b, err := os.ReadFile(log + ".body"); err == nil {
		for line := range strings.Lines(string(b)) {
			var body string
			if err := json.Unmarshal([]byte(line), &body); err != nil {
				t.Fatalf("gh body log: %v", err)
			}
			all = append(all, body)
		}
	}
	return all
}

type proposeFixture struct {
	t    *testing.T
	h    *harness
	hub  *skillRemote
	tmp  string
	log  string
	bin  string
	cfg  string
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
	f.cfg = cfg
	text := "[url \"file://" + filepath.ToSlash(f.hub.bare) + "\"]\n\tinsteadOf = " + skillsURL + "\n[url \"file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "no-such-host")) + "/\"]\n\tinsteadOf = https://github.com/\n[user]\n\tname = Pat Author\n\temail = pat@example.com\n" + gitConfig
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

func (f *proposeFixture) route(from string, bare string) {
	f.t.Helper()
	text, err := os.ReadFile(f.cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	text = append(text, "[url \"file://"+filepath.ToSlash(bare)+"\"]\n\tinsteadOf = "+from+"\n"...)
	if err := os.WriteFile(f.cfg, text, 0o644); err != nil {
		f.t.Fatal(err)
	}
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
	if len(argv) != 12 || strings.Join(argv[:9], " ") != "pr create --repo "+ownRepoName+" --head "+branch+" --base main --title" || argv[9] != "skills: update triage" || argv[10] != "--body-file" || argv[11] != "-" {
		t.Fatalf("gh argv = %q", argv)
	}
	bodies := ghBodies(t, f.log)
	if len(bodies) != 1 {
		t.Fatalf("bodies = %q", bodies)
	}
	for _, want := range []string{"hand skills propose", "skills/triage/SKILL.md", "3 files changed", "skills/triage/references/a.md", "skills/triage/scripts/new.sh"} {
		if !strings.Contains(bodies[0], want) {
			t.Errorf("the body lacks %q: %q", want, bodies[0])
		}
	}
	for _, private := range []string{f.h.home, f.h.vars["HOME"], f.h.vars["SECONDHAND_HOME"], f.tmp} {
		if strings.Contains(bodies[0], private) || strings.Contains(out, private) {
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
	routed := func(f *proposeFixture, from string) {
		tool := newSkillRemote(f.t, filepath.Join(f.t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
		f.route(from, tool.bare)
		f.h.ok("skills", "add", "--fleet", from)
		f.edit("tool-skill")
	}
	target := map[string]string{"a skill Hand does not manage": "nothing", "a local source": "notes", "a git source that is not on GitHub": "tool-skill", "a GitHub-shaped URL on another host": "tool-skill", "a host that only starts with github.com": "tool-skill", "an ssh URL": "tool-skill", "a URL that holds a GitHub URL after another prefix": "tool-skill", "a host one character off github.com": "tool-skill"}
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
		{"a GitHub-shaped URL on another host", 2, "which is not a GitHub repository", func(f *proposeFixture) {
			routed(f, "https://git.example.test/acme/tool-skill.git")
		}},
		{"a host that only starts with github.com", 2, "which is not a GitHub repository", func(f *proposeFixture) {
			routed(f, "https://github.com.example.test/acme/tool-skill.git")
		}},
		{"a URL that holds a GitHub URL after another prefix", 2, "which is not a GitHub repository", func(f *proposeFixture) {
			routed(f, "https://mirror.example.test/https://github.com/acme/tool-skill.git")
		}},
		{"a host one character off github.com", 2, "which is not a GitHub repository", func(f *proposeFixture) {
			routed(f, "https://githubxcom/acme/tool-skill.git")
		}},
		{"an ssh URL", 2, "which is not a GitHub repository", func(f *proposeFixture) {
			routed(f, "git@github.com:acme/tool-skill.git")
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
		"skills/edi/SKILL.md": skillFiles("edi", "v1")["SKILL.md"], "skills/clash/SKILL.md": skillFiles("clash", "v1")["SKILL.md"],
	})
	h := f.h
	for _, name := range []string{"cur", "beh", "edi", "clash"} {
		h.ok("skills", "add", "--fleet", name)
	}
	f.edit("edi")
	f.edit("clash")
	moved := f.hub.commit(map[string]string{"skills/beh/SKILL.md": skillFiles("beh", "v2")["SKILL.md"], "skills/clash/SKILL.md": skillFiles("clash", "v2")["SKILL.md"]})
	old := skillState(t, h.home)["cur"].Commit
	list := h.ok("skills", "list", "--fleet")
	for name, state := range map[string]string{"cur": "current", "beh": "behind", "edi": "edited", "clash": "conflict"} {
		want := fmt.Sprintf("%s,\"%s#skills/%s\",%s,fleet,yes,main,%s,", name, skillsURL, name, old, state)
		if r := row(list, name); !strings.HasPrefix(r, want) {
			t.Errorf("list %s = %q, want prefix %q", name, r, want)
		}
	}
	if r := row(list, "secondhand"); !strings.HasSuffix(r, ",no,\"\",\"\",\"\"") {
		t.Errorf("an unmanaged row = %q", r)
	}
	if r := row(h.ok("skills", "outdated", "--fleet"), "clash"); !strings.Contains(r, ",main,"+old+","+moved) {
		t.Errorf("outdated shows the commits wrongly: %q", r)
	}
	out, errOut, code := h.run("skills", "sync", "--fleet", "clash")
	goal := ""
	for line := range strings.Lines(out) {
		if strings.Contains(line, "task add --goal") {
			goal = strings.TrimSpace(line)
		}
	}
	if code != 3 || row(out, "clash") == "" || !strings.HasPrefix(row(out, "clash"), "clash,conflict,") || !strings.Contains(row(out, "clash"), "reconcile first") || !strings.Contains(errOut, "1 skill(s) not synced") {
		t.Fatalf("sync of a conflict: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	for _, want := range []string{"`hand task add --goal \"Reconcile the skill clash of the fleet scope", old[:12] + " to " + moved[:12], "hand skills add --fleet --ref main clash", "hand skills propose --fleet clash", "\" PROJECT \"Reconcile skill clash\"`"} {
		if !strings.Contains(goal, want) {
			t.Errorf("the task line lacks %q: %q", want, goal)
		}
	}
	if strings.ContainsAny(strings.TrimSuffix(strings.TrimPrefix(goal[strings.Index(goal, "--goal ")+7:], "\""), "`"), "$`\\") {
		t.Errorf("the goal carries shell text: %q", goal)
	}
	if out, errOut, code := h.run("skills", "update", "--fleet", "clash"); code != 3 || !strings.HasPrefix(row(out, "clash"), "clash,conflict,") || !strings.Contains(out, "task add --goal") || !strings.Contains(errOut, "1 skill(s) not updated") {
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

func (f *proposeFixture) proposeAfterEdit(name string) (string, string) {
	f.t.Helper()
	f.h.ok("skills", "add", "--fleet", name)
	f.edit(name)
	out := f.h.ok("skills", "propose", "--fleet", name)
	if !strings.Contains(out, name+",proposed,") {
		f.t.Fatalf("propose = %q", out)
	}
	for ref := range f.refs() {
		if rest, ok := strings.CutPrefix(ref, "refs/heads/hand-skills/"); ok {
			return strings.TrimPrefix(ref, "refs/heads/"), rest
		}
	}
	f.t.Fatalf("no proposal branch: %v", f.refs())
	return "", ""
}

func TestSkillsProposeRefusesARepeatBeforeItPushesOrOpensAnotherPullRequest(t *testing.T) {
	f := newProposeFixture(t, "")
	branch, _ := f.proposeAfterEdit("triage")
	refs := f.refs()
	logBefore := readSkill(t, f.log)
	_, errOut, code := f.h.run("skills", "propose", "--fleet", "triage")
	if code != 3 || !strings.Contains(errOut, "already proposed") || !strings.Contains(errOut, branch) || strings.Contains(errOut, "hint:") {
		t.Fatalf("repeat: code=%d stderr=%q", code, errOut)
	}
	if got := f.refs(); !maps.Equal(got, refs) {
		t.Fatalf("a repeat changed the source: %v, was %v", got, refs)
	}
	if readSkill(t, f.log) != logBefore {
		t.Fatal("a repeat ran gh again")
	}
	if left := tempLeft(t, f.tmp); len(left) != 0 {
		t.Fatalf("temp folders left: %q", left)
	}
}

func TestSkillsProposeNeverForcesAPush(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the git wrapper is a shell script")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	f := newProposeFixture(t, "")
	branch, _ := f.proposeAfterEdit("triage")
	planted := skillGit(t, f.hub.bare, "commit-tree", "-m", "planted", "main^{tree}")
	skillGit(t, f.hub.bare, "update-ref", "refs/heads/"+branch, planted)
	wrap, hide := t.TempDir(), filepath.Join(t.TempDir(), "hide-ls-remote")
	script := "#!/bin/sh\ncase \"$*\" in *ls-remote*) [ -e '" + hide + "' ] && exit 0;; esac\nexec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrap, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hide, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrap+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, errOut, code := f.h.run("skills", "propose", "--fleet", "triage"); code == 0 {
		t.Fatalf("a push over a diverged branch succeeded: %q", out)
	} else if !strings.Contains(errOut, "push") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := f.refs()["refs/heads/"+branch]; got != planted {
		t.Fatalf("the branch was overwritten: %s, was %s", got, planted)
	}
}

func TestSkillsProposeCutsTheBranchFromThePinnedCommit(t *testing.T) {
	f := newProposeFixture(t, "")
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	url := "https://github.com/acme/tool-skill.git"
	f.route(url, tool.bare)
	f.h.ok("skills", "add", "--fleet", url)
	pinned := skillState(t, f.h.home)["tool-skill"].Commit
	moved := tool.commit(skillFiles("tool-skill", "v2"))
	if pinned == "" || pinned == moved {
		t.Fatalf("pinned %q, moved %q", pinned, moved)
	}
	f.edit("tool-skill")
	out := f.h.ok("skills", "propose", "--fleet", "tool-skill")
	if !strings.Contains(out, "tool-skill,proposed,") {
		t.Fatalf("propose = %q", out)
	}
	var branch string
	for line := range strings.Lines(skillGit(t, tool.bare, "for-each-ref", "--format=%(refname)", "refs/heads/hand-skills/")) {
		branch = strings.TrimSpace(line)
	}
	if branch == "" {
		t.Fatal("no proposal branch on the source")
	}
	if got := skillGit(t, tool.bare, "log", "-1", "--format=%P", branch); got != pinned {
		t.Fatalf("the branch is cut from %s, want the pinned commit %s", got, pinned)
	}
	if got := skillGit(t, tool.bare, "rev-parse", "main"); got != moved {
		t.Fatalf("main = %s, want %s", got, moved)
	}
	var argv []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(readSkill(t, f.log))), &argv); err != nil || strings.Join(argv[:3], " ") != "pr create --repo" || argv[3] != "acme/tool-skill" {
		t.Fatalf("gh argv = %q, %v", argv, err)
	}
}

func followReconcile(t *testing.T, h *harness, goalLine string, before func(add []string)) (propose []string) {
	t.Helper()
	add := regexp.MustCompile(`run (\S+ skills add [^,]+),`).FindStringSubmatch(goalLine)
	prop := regexp.MustCompile(`then run (\S+ skills propose .+?) and stop`).FindStringSubmatch(goalLine)
	if add == nil || prop == nil {
		t.Fatalf("the pasted goal has no add and propose step: %q", goalLine)
	}
	if before != nil {
		before(strings.Fields(add[1])[1:])
	}
	if out := h.ok(strings.Fields(add[1])[1:]...); !strings.Contains(out, "triage,added,") {
		t.Fatalf("the add step = %q", out)
	}
	return strings.Fields(prop[1])[1:]
}

func goalOf(out string) string {
	for line := range strings.Lines(out) {
		if strings.Contains(line, "task add --goal") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func TestSkillsReconcileGoalWorksStepByStep(t *testing.T) {
	for _, scope := range []string{"fleet", "project"} {
		t.Run(scope, func(t *testing.T) {
			f := newProposeFixture(t, "")
			h, base, root, flags := f.h, f.h.home, ".claude", []string{"--fleet"}
			if scope == "project" {
				h, base = projectHarness(t)
				h.vars["PATH"] = f.bin
				root, flags = ".agents", []string{"--project", "app"}
			}
			h.ok(append([]string{"skills", "add"}, append(flags, "triage")...)...)
			if scope == "project" {
				gitIn(t, base, "add", "-A")
				gitIn(t, base, "commit", "-q", "-m", "track the skill")
			}
			path := skillPath(base, root, "triage")
			mine := readSkill(t, path) + "\nmy own line\n"
			if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
				t.Fatal(err)
			}
			moved := f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
			out, _, code := h.run(append([]string{"skills", "sync"}, append(flags, "triage")...)...)
			if code != 3 {
				t.Fatalf("sync of a conflict: code=%d stdout=%q", code, out)
			}
			saved := t.TempDir()
			if err := os.WriteFile(filepath.Join(saved, "SKILL.md"), []byte(mine), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, r := range fleet.SkillRoots {
				if err := os.RemoveAll(filepath.Dir(skillPath(base, r, "triage"))); err != nil {
					t.Fatal(err)
				}
			}
			goal := goalOf(out)
			if has := strings.Contains(goal, "commit the deletion in git"); has != (scope == "project") {
				t.Fatalf("the commit step is %v in the %s scope: %q", has, scope, goal)
			}
			var before func([]string)
			if scope == "project" {
				before = func(add []string) {
					if _, errOut, code := h.run(add...); code != 3 || !strings.Contains(errOut, "uncommitted changes in git") {
						t.Fatalf("add of a tracked skill that was deleted: code=%d stderr=%q", code, errOut)
					}
					gitIn(t, base, "add", "-A")
					gitIn(t, base, "commit", "-q", "-m", "drop the skill")
				}
			}
			propose := followReconcile(t, h, goal, before)
			fresh := readSkill(t, path)
			if !strings.Contains(fresh, "v2") || strings.Contains(fresh, "my own line") {
				t.Fatalf("the add did not restore the new copy: %q", fresh)
			}
			if r := row(h.ok(append([]string{"skills", "outdated"}, flags...)...), "triage"); !strings.HasPrefix(r, "triage,current,") {
				t.Fatalf("after the add = %q", r)
			}
			if err := os.WriteFile(path, []byte(fresh+"\nmy own line\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if out := h.ok(propose...); !strings.Contains(out, "triage,proposed,") {
				t.Fatalf("the propose step = %q", out)
			}
			for ref, id := range f.refs() {
				if strings.HasPrefix(ref, "refs/heads/hand-skills/triage-") {
					if got := skillGit(t, f.hub.bare, "log", "-1", "--format=%P", id); got != moved {
						t.Fatalf("the proposal is cut from %s, want %s", got, moved)
					}
					return
				}
			}
			t.Fatalf("no proposal branch: %v", f.refs())
		})
	}
}

func TestSkillsProposeConflictHelpNamesTheSyncCommandWithoutAScopeFlag(t *testing.T) {
	f := newProposeFixture(t, "")
	h, repo := projectHarness(t)
	h.vars["PATH"] = f.bin
	h.ok("skills", "add", "triage")
	path := skillPath(repo, ".agents", "triage")
	if err := os.WriteFile(path, []byte(readSkill(t, path)+"\nproject edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	_, errOut, code := h.run("skills", "propose", "triage")
	if code != 3 || !strings.Contains(errOut, "reconcile first: `hand skills sync` prints") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestSkillsProposeOpensThePullRequestAgainstTheBranchItTracks(t *testing.T) {
	f := newProposeFixture(t, "")
	skillGit(t, f.hub.work, "checkout", "-q", "-b", "next")
	writeFiles(t, f.hub.work, map[string]string{"skills/triage/references/b.md": "b\n"})
	skillGit(t, f.hub.work, "add", "-A")
	skillGit(t, f.hub.work, "commit", "-q", "-m", "next")
	skillGit(t, f.hub.work, "push", "-q", "origin", "HEAD:next")
	next := skillGit(t, f.hub.work, "rev-parse", "HEAD")
	f.h.ok("skills", "add", "--fleet", "--ref", "next", "triage")
	f.edit("triage")
	f.h.ok("skills", "propose", "--fleet", "triage")
	var argv []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(readSkill(t, f.log))), &argv); err != nil || len(argv) != 12 || argv[6] != "--base" || argv[7] != "next" {
		t.Fatalf("gh argv = %q, %v", argv, err)
	}
	for ref := range f.refs() {
		if strings.HasPrefix(ref, "refs/heads/hand-skills/") {
			if got := skillGit(t, f.hub.bare, "log", "-1", "--format=%P", ref); got != next {
				t.Fatalf("the branch is cut from %s, want %s", got, next)
			}
			return
		}
	}
	t.Fatal("no proposal branch")
}

func TestSkillsProposePassesABaseOnlyForATrackedBranch(t *testing.T) {
	for _, kind := range []string{"branch", "tag", "commit"} {
		t.Run(kind, func(t *testing.T) {
			f := newProposeFixture(t, "")
			args := []string{"skills", "add", "--fleet"}
			switch kind {
			case "tag":
				skillGit(t, f.hub.work, "tag", "v1")
				skillGit(t, f.hub.work, "push", "-q", "origin", "v1")
				args = append(args, "--ref", "v1")
			case "commit":
				args = append(args, "--ref", f.head)
			}
			f.h.ok(append(args, "triage")...)
			f.edit("triage")
			f.h.ok("skills", "propose", "--fleet", "triage")
			var argv []string
			if err := json.Unmarshal([]byte(strings.TrimSpace(readSkill(t, f.log))), &argv); err != nil {
				t.Fatalf("gh argv = %q, %v", argv, err)
			}
			at := slices.Index(argv, "--base")
			if kind == "branch" && (at < 0 || argv[at+1] != "main") || kind != "branch" && at >= 0 {
				t.Fatalf("gh argv for a %s = %q", kind, argv)
			}
		})
	}
}

func TestSkillsProposeRefusesFilesTheSourceIgnores(t *testing.T) {
	f := newProposeFixture(t, "")
	f.hub.commit(map[string]string{".gitignore": "*.log\n"})
	f.head = f.refs()["refs/heads/main"]
	f.h.ok("skills", "add", "--fleet", "triage")
	f.edit("triage")
	writeFiles(t, filepath.Dir(skillPath(f.h.home, ".claude", "triage")), map[string]string{"references/run.log": "x\n", "references/ok.md": "y\n"})
	_, errOut, code := f.h.run("skills", "propose", "--fleet", "triage")
	if code != 2 || !strings.Contains(errOut, "references/run.log") || strings.Contains(errOut, "ok.md") || !strings.Contains(errOut, "nothing proposed") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	f.untouched("an ignored file")
}

func TestSkillsUpdateSendsAConflictOfAnUnmarkedCopyToReconcile(t *testing.T) {
	f := newProposeFixture(t, "")
	f.h.ok("skills", "add", "--fleet", "triage")
	path := skillPath(f.h.home, ".agents", "triage")
	if err := os.WriteFile(path, []byte(strings.Replace(readSkill(t, path), "metadata:\n  managed-by: hand\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	f.hub.commit(map[string]string{"skills/triage/SKILL.md": skillFiles("triage", "v2")["SKILL.md"]})
	out, errOut, code := f.h.run("skills", "update", "--fleet", "triage")
	if code != 3 || !strings.HasPrefix(row(out, "triage"), "triage,conflict,") || !strings.Contains(out, "task add --goal") {
		t.Fatalf("update: code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestSkillsReconcileGoalOfASourceItCannotWriteInFull(t *testing.T) {
	h := skillsHarness(t)
	dir := filepath.Join(t.TempDir(), "team skills", "notes")
	writeFiles(t, dir, skillFiles("notes", "v1"))
	h.ok("skills", "add", "--fleet", dir)
	path := skillPath(h.home, ".claude", "notes")
	if err := os.WriteFile(path, []byte(readSkill(t, path)+"\nmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, skillFiles("notes", "v2"))
	out, _, code := h.run("skills", "sync", "--fleet", "notes")
	goal := goalOf(out)
	if code != 3 || goal == "" {
		t.Fatalf("code=%d stdout=%q", code, out)
	}
	for _, want := range []string{"skills add --fleet SOURCE,", "SOURCE in the add step is the source column of notes in hand skills list --fleet.", "then stop: propose takes only a GitHub source"} {
		if !strings.Contains(goal, want) {
			t.Errorf("the goal lacks %q: %q", want, goal)
		}
	}
	if strings.Contains(goal, "skills propose") || strings.Contains(goal, "team") {
		t.Errorf("the goal names propose or the path: %q", goal)
	}
}

func TestSkillsReconcileGoalResolvesAProjectRelativeSource(t *testing.T) {
	h, repo := projectHarness(t)
	src := skillSource(t, "notes", "v1")
	h.ok("skills", "add", "--project", "app", src)
	if lock := readSkill(t, filepath.Join(repo, "skills-lock.json")); strings.Contains(lock, filepath.ToSlash(src)) {
		t.Skipf("the lock holds %s as it is", src)
	}
	path := skillPath(repo, ".agents", "notes")
	if err := os.WriteFile(path, []byte(readSkill(t, path)+"\nmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, src, skillFiles("notes", "v2"))
	other := t.TempDir()
	h.cwd = other
	out, _, code := h.run("skills", "sync", "--project", "app", "notes")
	goal := goalOf(out)
	want := "skills add --project app " + src + ","
	if runtime.GOOS == "windows" {
		want = "skills add --project app SOURCE,"
	}
	if code != 3 || !strings.Contains(goal, want) {
		t.Fatalf("code=%d goal=%q, want %q", code, goal, want)
	}
}

func TestSkillsReconcileGoalOfAThirdPartySource(t *testing.T) {
	const github = "https://github.com/acme/tool-skill.git"
	for _, tc := range []struct {
		name, url string
		plant     func(skills map[string]map[string]any)
		want      []string
		not       []string
	}{
		{"a GitHub source with a ref", github, nil, []string{"skills add --fleet --ref main " + github + ",", "then run hand skills propose --fleet tool-skill and stop"}, nil},
		{"a source that is not on GitHub", "file://", nil, []string{"then stop: propose takes only a GitHub source"}, []string{"skills propose"}},
		{"a subscription with no ref", github, func(skills map[string]map[string]any) { skills["tool-skill"]["ref"] = "" }, []string{"skills add --fleet " + github + ","}, []string{"--ref"}},
		{"a source that starts with a dash", "--upload-pack=x", nil, []string{"skills add --fleet --ref main SOURCE,"}, []string{"--upload-pack"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProposeFixture(t, "")
			tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
			url := cmp.Or(strings.TrimPrefix(tc.url, "file://"), "file://"+filepath.ToSlash(tool.bare))
			if strings.HasPrefix(tc.url, "file://") {
				f.h.ok("skills", "add", "--fleet", "--ref", "main", url)
			} else {
				f.route(github, tool.bare)
				f.h.ok("skills", "add", "--fleet", "--ref", "main", github)
			}
			if tc.url == "--upload-pack=x" {
				f.route(tc.url, tool.bare)
				editJSON(t, fleetLock(f.h.home), func(skills map[string]map[string]any) { skills["tool-skill"]["sourceUrl"] = tc.url })
			}
			if tc.plant != nil {
				editJSON(t, filepath.Join(f.h.home, "skills.json"), tc.plant)
			}
			f.edit("tool-skill")
			tool.commit(skillFiles("tool-skill", "v2"))
			out, _, code := f.h.run("skills", "sync", "--fleet", "tool-skill")
			goal := goalOf(out)
			if code != 3 || goal == "" {
				t.Fatalf("code=%d stdout=%q", code, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(goal, want) {
					t.Errorf("the goal lacks %q: %q", want, goal)
				}
			}
			for _, not := range tc.not {
				if strings.Contains(goal, not) {
					t.Errorf("the goal has %q: %q", not, goal)
				}
			}
		})
	}
}
