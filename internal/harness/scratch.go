package harness

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var scratchKey = regexp.MustCompile(`^(-.*worktrees)-(f[0-9a-f]{12})-(t[0-9]+-a[0-9]+)$`)

func claudeScratchRoot(getenv func(string) string, uid int) string {
	if uid < 0 {
		return ""
	}
	tmp := cmp.Or(getenv("CLAUDE_CODE_TMPDIR"), getenv("TMPDIR"), "/tmp")
	return filepath.Join(tmp, "claude-"+strconv.Itoa(uid))
}

func claudeKey(dir string) string {
	return strings.Map(func(r rune) rune {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, dir)
}

func realBeside(dir string) []string {
	paths := []string{dir}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(dir)); err == nil {
		if real := filepath.Join(parent, filepath.Base(dir)); real != dir {
			paths = append(paths, real)
		}
	}
	return paths
}

func isDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

func Leftovers(name, worktree string, uid int, getenv func(string) string) []string {
	root := claudeScratchRoot(getenv, uid)
	if name != "claude" || root == "" || !filepath.IsAbs(worktree) {
		return nil
	}
	var out []string
	for _, dir := range realBeside(worktree) {
		key := claudeKey(dir)
		if path := filepath.Join(root, key); scratchKey.MatchString(key) && isDir(path) {
			out = append(out, path)
		}
	}
	return out
}

func Orphans(name, worktrees string, uid int, getenv func(string) string) []string {
	root := claudeScratchRoot(getenv, uid)
	if name != "claude" || root == "" || !filepath.IsAbs(worktrees) {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		m := scratchKey.FindStringSubmatch(e.Name())
		if m == nil || !e.IsDir() {
			continue
		}
		for _, dir := range realBeside(worktrees) {
			if m[1] != claudeKey(dir) {
				continue
			}
			if _, err := os.Lstat(filepath.Join(dir, m[2], m[3])); errors.Is(err, fs.ErrNotExist) {
				out = append(out, filepath.Join(root, e.Name()))
			}
			break
		}
	}
	return out
}
