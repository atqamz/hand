package cli_test

import (
	"crypto/sha1"
	"fmt"
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

func isolateTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, dir)
	}
	return dir
}

func tempLeft(t *testing.T, dir string) []string {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(dir, "hand-skills-*"))
	if err != nil {
		t.Fatal(err)
	}
	return left
}

func hungRemote(t *testing.T) string {
	t.Helper()
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
	return "git://" + ln.Addr().String() + "/tool-skill"
}

func TestSkillsCommandsLeaveNoTempFolder(t *testing.T) {
	cli.SetSkillFetchLimits(t, 400*time.Millisecond, time.Minute)
	tmp := isolateTemp(t)
	h := skillsHarness(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	h.ok("skills", "add", "--fleet", "--ref", "main", "file://"+filepath.ToSlash(tool.bare))
	tool.commit(skillFiles("tool-skill", "v2"))
	scenario := func(name string) {
		for _, args := range [][]string{
			{"skills", "outdated", "--fleet"},
			{"skills", "outdated", "--all"},
			{"skills", "sync", "--fleet"},
			{"skills", "update", "--fleet"},
		} {
			h.run(args...)
			if left := tempLeft(t, tmp); len(left) != 0 {
				t.Fatalf("%s: hand %v left %q", name, args, left)
			}
		}
	}
	scenario("success")
	editJSON(t, fleetLock(h.home), func(skills map[string]map[string]any) {
		skills["tool-skill"]["sourceUrl"] = "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "gone.git"))
	})
	scenario("error")
	if runtime.GOOS == "windows" {
		t.Log("timeout scenario skipped: a git helper outlives the kill there and holds the folder")
		return
	}
	editJSON(t, fleetLock(h.home), func(skills map[string]map[string]any) {
		skills["tool-skill"]["sourceUrl"] = hungRemote(t)
	})
	scenario("timeout")
}

func TestSkillsFetchStopsAtTheCommandBudget(t *testing.T) {
	cli.SetSkillFetchLimits(t, time.Minute, 500*time.Millisecond)
	h := skillsHarness(t)
	start := time.Now()
	_, errOut, code := h.run("skills", "add", "--fleet", hungRemote(t))
	if code == 0 || time.Since(start) > 20*time.Second {
		t.Fatalf("a hung remote: code=%d after %s: %q", code, time.Since(start), errOut)
	}
}

func TestSkillsFetchDoesNotRetryAFailedSourceInACommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the counting git is a shell script")
	}
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{
		"skills/a/SKILL.md": skillFiles("a", "v1")["SKILL.md"], "skills/b/SKILL.md": skillFiles("b", "v1")["SKILL.md"],
	})
	isolateGit(t, hub.bare)
	for _, name := range []string{"a", "b"} {
		h.ok("skills", "add", "--fleet", name)
	}
	if err := os.RemoveAll(hub.bare); err != nil {
		t.Fatal(err)
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
	out := h.ok("skills", "outdated", "--fleet")
	for _, name := range []string{"a", "b"} {
		if r := row(out, name); !strings.HasPrefix(r, name+",error,") {
			t.Fatalf("%s = %q", name, r)
		}
	}
	if n := strings.Count(readFile(t, log), " fetch "); n != 1 {
		t.Fatalf("%d fetches for one failing source", n)
	}
}

func TestSkillsFetchKeepsTwoRefsOfOneSourceApart(t *testing.T) {
	h := skillsHarness(t)
	hub := newSkillRemote(t, filepath.Join(t.TempDir(), "hub.git"), map[string]string{"skills/a/SKILL.md": skillFiles("a", "v1")["SKILL.md"]})
	isolateGit(t, hub.bare)
	skillGit(t, hub.work, "checkout", "-q", "-b", "other")
	writeFiles(t, hub.work, map[string]string{"skills/b/SKILL.md": skillFiles("b", "v1")["SKILL.md"]})
	skillGit(t, hub.work, "add", "-A")
	skillGit(t, hub.work, "commit", "-q", "-m", "b")
	skillGit(t, hub.work, "push", "-q", "origin", "other")
	h.ok("skills", "add", "--fleet", "a")
	h.ok("skills", "add", "--fleet", "--ref", "other", "b")
	out := h.ok("skills", "outdated", "--fleet")
	for _, name := range []string{"a", "b"} {
		if r := row(out, name); !strings.HasPrefix(r, name+",current,") {
			t.Fatalf("%s = %q", name, r)
		}
	}
}

func commitID(body string) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("commit %d\x00%s", len(body), body))))
}

func collidingCommits(t *testing.T, r *skillRemote, benign, evil map[string]string) (string, string) {
	t.Helper()
	parent := skillGit(t, r.work, "rev-parse", "HEAD")
	tree := func(files map[string]string) string {
		writeFiles(t, r.work, files)
		skillGit(t, r.work, "add", "-A")
		return skillGit(t, r.work, "write-tree")
	}
	benignTree, evilTree := tree(benign), tree(evil)
	skillGit(t, r.work, "reset", "-q", "--hard", parent)
	body := func(tree string, n int) string {
		return fmt.Sprintf("tree %s\nparent %s\nauthor t <t@t> 0 +0000\ncommitter t <t@t> 0 +0000\n\n%d\n", tree, parent, n)
	}
	benignSeen, evilSeen := map[string]int{}, map[string]int{}
	for n := 0; n < 1<<24; n++ {
		b, e := commitID(body(benignTree, n))[:7], commitID(body(evilTree, n))[:7]
		benignSeen[b], evilSeen[e] = n, n
		bn, en := n, n
		if m, ok := benignSeen[e]; ok {
			bn = m
		} else if m, ok := evilSeen[b]; ok {
			en = m
		} else {
			continue
		}
		write := func(body string) string {
			cmd := exec.Command("git", "-C", r.work, "hash-object", "-t", "commit", "-w", "--stdin")
			cmd.Stdin = strings.NewReader(body)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("hash-object: %v: %s", err, out)
			}
			return strings.TrimSpace(string(out))
		}
		bid, eid := write(body(benignTree, bn)), write(body(evilTree, en))
		if bid[:7] != eid[:7] || bid == eid || bid != commitID(body(benignTree, bn)) {
			t.Fatalf("collision %s %s: the repository does not hash as SHA-1", bid, eid)
		}
		return bid, eid
	}
	t.Fatal("no collision found")
	return "", ""
}

func TestSkillsSyncYesNeedsTheWholeCommitNotAPrefix(t *testing.T) {
	h := skillsHarness(t)
	tool := newSkillRemote(t, filepath.Join(t.TempDir(), "tool-skill.git"), skillFiles("tool-skill", "v1"))
	h.ok("skills", "add", "--fleet", "--ref", "main", "file://"+filepath.ToSlash(tool.bare))
	benign, evil := collidingCommits(t, tool, skillFiles("tool-skill", "v2 benign"), skillFiles("tool-skill", "v2 EVIL"))
	push := func(id string) {
		skillGit(t, tool.work, "push", "-q", "--force", "origin", id+":refs/heads/main")
	}
	push(benign)
	if out := h.ok("skills", "sync", "--fleet"); !strings.Contains(out, "sync --yes tool-skill@"+benign) || !strings.Contains(out, "+v2 benign") {
		t.Fatalf("sync = %q", out)
	}
	push(evil)
	installed := func() string { return readSkill(t, skillPath(h.home, ".claude", "tool-skill")) }
	for _, yes := range []string{benign[:7], benign[:12], benign[:39], evil[:39], benign} {
		out, errOut, code := h.run("skills", "sync", "--fleet", "--yes", "tool-skill@"+yes)
		if code != 3 || strings.Contains(installed(), "EVIL") || strings.Contains(installed(), "benign") {
			t.Fatalf("--yes tool-skill@%s: code=%d stdout=%q stderr=%q, copy %q", yes, code, out, errOut, installed())
		}
	}
	out, _, _ := h.run("skills", "sync", "--fleet", "--yes", "tool-skill@"+benign[:7])
	if !strings.Contains(out, "takes the whole NAME@COMMIT") || !strings.Contains(out, "tool-skill@"+evil) {
		t.Fatalf("a prefix = %q", out)
	}
	if out := h.ok("skills", "sync", "--fleet", "--yes", "tool-skill@"+evil); !strings.Contains(out, "tool-skill,updated,") || !strings.Contains(installed(), "EVIL") {
		t.Fatalf("the whole commit: %q", out)
	}
}

func TestSkillsSyncInAProjectNeverTrustsAPathAndNamesItsHash(t *testing.T) {
	h, repo := projectHarness(t)
	src := skillSource(t, "notes", "v1")
	h.ok("skills", "add", src)
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "skills")
	writeFiles(t, src, skillFiles("notes", "v2"))
	installed := filepath.Join(repo, ".agents", "skills", "notes", "SKILL.md")
	out := h.ok("skills", "sync")
	r := row(out, "notes")
	if !strings.HasPrefix(r, "notes,behind,") || !strings.Contains(out, "+v2") || strings.Contains(readSkill(t, installed), "\nv2\n") {
		t.Fatalf("a project path source applied without a yes: %q", out)
	}
	_, after, ok := strings.Cut(r, "sync --yes notes@")
	hash, _, _ := strings.Cut(after, "`")
	if !ok || len(hash) != 64 {
		t.Fatalf("the row does not name the content hash: %q", r)
	}
	if out, _, code := h.run("skills", "sync", "--yes", "notes@"+strings.Repeat("0", 64)); code != 3 || strings.Contains(readSkill(t, installed), "\nv2\n") {
		t.Fatalf("a wrong hash: code=%d %q", code, out)
	}
	if out := h.ok("skills", "sync", "--yes", "notes@"+hash); !strings.Contains(out, "notes,updated,") || !strings.Contains(readSkill(t, installed), "\nv2\n") {
		t.Fatalf("the hash: %q", out)
	}
}
