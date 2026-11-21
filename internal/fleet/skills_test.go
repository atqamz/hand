package fleet_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"errors"
	"github.com/atqamz/hand/internal/fleet"
	"github.com/atqamz/hand/internal/state"
)

func writeTree(t *testing.T, dir string, files map[string]string) string {
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
	return dir
}

func TestComputedHashMatchesTheVercelVectors(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"none", nil, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		{"one file", map[string]string{"SKILL.md": "# hi\n"}, "93c36952d8843b33e68ae970ebabcc03e1687c9bb0dd1ecaf357060318952f55"},
		{"two files", map[string]string{"SKILL.md": "a\n", "docs/b.md": "b\n"}, "b22c4d22aa319d2924312fa18fcbfedc88fa89c74dbd8f298c683e1caf7b7f24"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := fleet.ComputedHash(writeTree(t, t.TempDir(), c.files))
			if err != nil || got != c.want {
				t.Fatalf("hash = %s, %v; want %s", got, err, c.want)
			}
		})
	}
}

func TestComputedHashFollowsTheReadmeRules(t *testing.T) {
	base := map[string]string{"SKILL.md": "a\n", "docs/b.md": "b\n"}
	want, err := fleet.ComputedHash(writeTree(t, t.TempDir(), base))
	if err != nil {
		t.Fatal(err)
	}
	same := writeTree(t, t.TempDir(), base)
	writeTree(t, same, map[string]string{
		".git/HEAD": "x", "__pycache__/a.pyc": "x", "__pypackages__/b": "x", "metadata.json": "{}", "docs/metadata.json": "{}",
	})
	if got, err := fleet.ComputedHash(same); err != nil || got != want {
		t.Fatalf("excluded files changed the hash: %s, %v", got, err)
	}
	crlf := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "a\r\n", "docs/b.md": "b\r\n"})
	if got, err := fleet.ComputedHash(crlf); err != nil || got != want {
		t.Fatalf("CRLF changed the hash: %s, %v", got, err)
	}
	binary := func(content string) string {
		got, err := fleet.ComputedHash(writeTree(t, t.TempDir(), map[string]string{"a.bin": content}))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if binary("a\x00\r\nb") == binary("a\x00\nb") {
		t.Fatal("a file with a NUL byte had its CRLF replaced")
	}
	h := sha256.New()
	for _, f := range [][2]string{{"B.md", "1"}, {"a.md", "2"}, {"é.md", "3"}} {
		fmt.Fprintf(h, "%s\x00%d\x00%s", f[0], len(f[1]), f[1])
	}
	order := writeTree(t, t.TempDir(), map[string]string{"a.md": "2", "é.md": "3", "B.md": "1"})
	if got, err := fleet.ComputedHash(order); err != nil || got != hex.EncodeToString(h.Sum(nil)) {
		t.Fatalf("paths are sorted by their UTF-8 bytes, not by locale: %s, %v", got, err)
	}
}

func TestComputedHashFollowsASymlinkedFolderAndSkipsOneLeavingTheFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	linked := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "a\n", "real/b.md": "b\n"})
	outside := writeTree(t, t.TempDir(), map[string]string{"x.md": "secret"})
	for link, target := range map[string]string{"b.md": "real/b.md", "out.md": filepath.Join(outside, "x.md"), "loop.md": "loop.md", "gone.md": "missing.md", "dir": "real", "back": ".", "outdir": outside} {
		if err := os.Symlink(target, filepath.Join(linked, link)); err != nil {
			t.Fatal(err)
		}
	}
	want, err := fleet.ComputedHash(writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "a\n", "b.md": "b\n", "real/b.md": "b\n", "dir/b.md": "b\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := fleet.ComputedHash(linked); err != nil || got != want {
		t.Fatalf("hash = %s, %v; want %s", got, err, want)
	}
	only := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "a\n", "real/b.md": "b\n"})
	if err := os.Symlink("real", filepath.Join(only, "dir")); err != nil {
		t.Fatal(err)
	}
	if got, err := fleet.ComputedHash(only); err != nil || got != "56d7390de30a704c1e09025c23d759489030ea898f8de4b7641e846a3049d81e" {
		t.Fatalf("hash = %s, %v; want the vercel hash of SKILL.md, real/b.md and dir/b.md", got, err)
	}
}

func TestValidateSpecNamesTheBrokenField(t *testing.T) {
	skill := func(front string, body ...string) string {
		return "---\n" + front + "\n---\n" + strings.Join(body, "")
	}
	long := func(n int) string { return strings.Repeat("a", n) }
	lines := func(n int) string { return strings.Repeat("line\n", n) }
	for _, c := range []struct {
		name, folder, file, want string
	}{
		{"valid", "notes", skill("name: notes\ndescription: Do it when asked."), ""},
		{"quoted and folded", "notes", skill("name: \"notes\"\ndescription: >\n  one\n  two\nlicense: MIT\nmetadata:\n  version: \"1\"\n  author: x\nallowed-tools: Bash(git:*)"), ""},
		{"column zero sequence", "notes", skill("name: notes\ndescription: x\nallowed-tools:\n- Read\n- Bash"), ""},
		{"name 64", long(64), skill("name: " + long(64) + "\ndescription: x"), ""},
		{"description 1024", "notes", skill("name: notes\ndescription: " + long(1024)), ""},
		{"compatibility 500", "notes", skill("name: notes\ndescription: x\ncompatibility: " + long(500)), ""},
		{"499 lines", "notes", skill("name: notes\ndescription: x", lines(495)), ""},
		{"no frontmatter", "notes", "# notes\n", "frontmatter missing"},
		{"unclosed frontmatter", "notes", "---\nname: notes\ndescription: x\n", "frontmatter missing"},
		{"not key value", "notes", skill("name: notes\ndescription: x\njust words"), "not `key: value`"},
		{"no name", "notes", skill("description: x"), "name is required"},
		{"empty name", "notes", skill("name:\ndescription: x"), "name is required"},
		{"other folder", "notes", skill("name: other\ndescription: x"), `name "other" must equal the skill's folder name "notes"`},
		{"uppercase", "Notes", skill("name: Notes\ndescription: x"), `name "Notes" must be 1-64 lowercase`},
		{"underscore", "my_notes", skill("name: my_notes\ndescription: x"), "name \"my_notes\" must be 1-64"},
		{"double hyphen", "a--b", skill("name: a--b\ndescription: x"), `name "a--b"`},
		{"trailing hyphen", "ab-", skill("name: ab-\ndescription: x"), `name "ab-"`},
		{"name 65", long(65), skill("name: " + long(65) + "\ndescription: x"), "name \"" + long(65) + "\" must be 1-64"},
		{"no description", "notes", skill("name: notes"), "description is 0 characters"},
		{"blank description", "notes", skill("name: notes\ndescription: \"\""), "description is 0 characters"},
		{"description 1025", "notes", skill("name: notes\ndescription: " + long(1025)), "description is 1025 characters"},
		{"empty compatibility", "notes", skill("name: notes\ndescription: x\ncompatibility:"), "compatibility is 0 characters"},
		{"compatibility 501", "notes", skill("name: notes\ndescription: x\ncompatibility: " + long(501)), "compatibility is 501 characters"},
		{"metadata scalar", "notes", skill("name: notes\ndescription: x\nmetadata: nope"), "metadata must be a map"},
		{"metadata flow map", "notes", skill("name: notes\ndescription: x\nmetadata: {version: \"1\"}"), "metadata must be a block map"},
		{"500 lines", "notes", skill("name: notes\ndescription: x", lines(496)), "500 lines; keep SKILL.md under 500"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := writeTree(t, filepath.Join(t.TempDir(), c.folder), map[string]string{"SKILL.md": c.file})
			err := fleet.ValidateSpec(dir, c.folder)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("valid skill refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) || !errors.Is(err, state.ErrInvalid)):
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(got) != c.file {
				t.Fatal("validation rewrote the file")
			}
		})
	}
}

func TestUnmarkUndoesTheMarkOfStage(t *testing.T) {
	for name, head := range map[string]string{
		"no metadata":         "name: notes\ndescription: a test skill",
		"metadata with a key": "name: notes\ndescription: a test skill\nmetadata:\n  owner: me",
		"metadata first":      "metadata:\n    owner: me\nname: notes\ndescription: a test skill",
		"metadata is last":    "name: notes\ndescription: a test skill\nmetadata:\n  owner: me\n  tier: 2",
		"crlf":                "name: notes\r\ndescription: a test skill",
	} {
		src := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "---\n" + head + "\n---\n\n# notes\n", "scripts/run.sh": "echo\n"})
		want, err := fleet.ComputedHash(src)
		if err != nil {
			t.Fatal(err)
		}
		staged, _, err := fleet.Stage(src, "notes")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fleet.Owned(staged) == false {
			t.Fatalf("%s: stage did not mark", name)
		}
		if err := fleet.Unmark(staged); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got, err := fleet.ComputedHash(staged); err != nil || got != want || fleet.Owned(staged) {
			t.Errorf("%s: hash %s (%v) owned=%v, want %s", name, got, err, fleet.Owned(staged), want)
		}
		os.RemoveAll(staged)
	}
	plain := writeTree(t, t.TempDir(), map[string]string{"SKILL.md": "---\nname: notes\ndescription: x\n---\nbody\n"})
	before, _ := os.ReadFile(filepath.Join(plain, "SKILL.md"))
	if err := fleet.Unmark(plain); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(filepath.Join(plain, "SKILL.md")); string(after) != string(before) {
		t.Fatalf("an unmarked file changed: %q", after)
	}
}
