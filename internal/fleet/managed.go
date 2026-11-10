package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/atqamz/hand/internal/fsx"
)

const (
	Marker    = "managed-by: hand-skills"
	stateFile = ".hand-skills.json"
)

type Managed struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	Commit string `json:"commit,omitempty"`
	Scope  string `json:"scope"`
	Sum    string `json:"sum"`
}

type Skills struct{ Base, Scope string }

func (s Skills) Dir(root, name string) string {
	return filepath.Join(s.Base, root, "skills", name)
}

func (s Skills) statePath() string {
	return filepath.Join(s.Base, SkillRoots[1], "skills", stateFile)
}

func (s Skills) Load() (map[string]Managed, error) {
	state := map[string]Managed{}
	b, err := os.ReadFile(s.statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.statePath(), err)
	}
	return state, nil
}

func (s Skills) Save(state map[string]Managed) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.statePath()), 0o755); err != nil {
		return err
	}
	tmp := s.statePath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return fsx.Rename(tmp, s.statePath())
}

func Owned(dir string) bool {
	have, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	return err == nil && strings.Contains(string(have), "\n  "+Marker+"\n")
}

func Sum(dir string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), len(b))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}

func Copy(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir() && d.Name() == ".git" && rel != ".":
			return filepath.SkipDir
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file; a skill is copied, never linked", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644|info.Mode().Perm()&0o111)
	})
}

func mark(dir string) error {
	path := filepath.Join(dir, "SKILL.md")
	have, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("a skill folder holds a SKILL.md: %w", err)
	}
	head, rest, ok := strings.Cut(strings.TrimPrefix(string(have), "---\n"), "\n---\n")
	if !ok || !strings.HasPrefix(string(have), "---\n") {
		return fmt.Errorf("%s has no frontmatter", path)
	}
	lines := strings.Split(head, "\n")
	at := len(lines)
	for i, l := range lines {
		if l == "metadata:" {
			at = i + 1
			break
		}
		if strings.HasPrefix(l, "metadata:") {
			return fmt.Errorf("%s: metadata is not a block; add `  %s` to it by hand", path, Marker)
		}
	}
	if at == len(lines) {
		lines = append(lines, "metadata:")
		at++
	}
	lines = append(lines[:at], append([]string{"  " + Marker}, lines[at:]...)...)
	return os.WriteFile(path, []byte("---\n"+strings.Join(lines, "\n")+"\n---\n"+rest), 0o644)
}

func Stage(src string) (dir, sum string, err error) {
	if dir, err = os.MkdirTemp("", "hand-skill-"); err != nil {
		return "", "", err
	}
	if err = Copy(src, dir); err == nil {
		err = mark(dir)
	}
	if err == nil {
		sum, err = Sum(dir)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", err
	}
	return dir, sum, nil
}

func (s Skills) Install(name, staged string) error {
	for _, root := range SkillRoots {
		dst := s.Dir(root, name)
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
		if err := Copy(staged, dst); err != nil {
			return err
		}
	}
	return nil
}

func (s Skills) Remove(name string) error {
	for _, root := range SkillRoots {
		if err := os.RemoveAll(s.Dir(root, name)); err != nil {
			return err
		}
	}
	return nil
}

func (s Skills) Check(name string, e Managed) (kind, root string, err error) {
	for _, r := range SkillRoots {
		dir := s.Dir(r, name)
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			if kind == "" {
				kind, root = "missing", r
			}
			continue
		}
		if !Owned(dir) {
			return "unmanaged", r, nil
		}
		sum, err := Sum(dir)
		if err != nil {
			return "", "", err
		}
		if e.Sum != "" && sum != e.Sum && kind != "changed" {
			kind, root = "changed", r
		}
	}
	return kind, root, nil
}
