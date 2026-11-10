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
	"regexp"
	"strings"

	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/fsx"
)

const MaxSkillBytes = 32 << 20

var Rename = fsx.Rename

var SkillName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

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
	Next   string `json:"next,omitempty"`
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
	for name := range state {
		if !SkillName.MatchString(name) {
			return nil, fmt.Errorf("read %s: %q is not a skill name", s.statePath(), name)
		}
	}
	return state, nil
}

func (s Skills) Lock() (func(), error) {
	dir := filepath.Dir(s.statePath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".hand-skills.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if ok, err := flock.Lock(f, true); err != nil || !ok {
		_ = f.Close()
		if err == nil {
			err = errors.New("could not lock the skills state")
		}
		return nil, err
	}
	return func() { _ = flock.Release(f) }, nil
}

func (s Skills) Save(state map[string]Managed) error {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.statePath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, stateFile+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = fsx.Rename(tmp, s.statePath())
		if err == nil {
			_ = syncDir(dir)
			return nil
		}
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

func frontmatter(text string) (head, rest string, ok bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	head, rest, ok = strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	return head, rest, ok && strings.HasPrefix(text, "---\n")
}

func Owned(dir string) bool {
	have, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return false
	}
	head, _, ok := frontmatter(string(have))
	if !ok {
		return false
	}
	for _, l := range strings.Split(head, "\n") {
		if strings.HasPrefix(l, " ") && strings.TrimSpace(l) == Marker {
			return true
		}
	}
	return false
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
	var total int64
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
		if total += info.Size(); total > MaxSkillBytes {
			return fmt.Errorf("%s is over %d MiB; a skill is a small folder", src, MaxSkillBytes>>20)
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
	head, rest, ok := frontmatter(string(have))
	if !ok {
		return fmt.Errorf("%s has no frontmatter", path)
	}
	lines := strings.Split(head, "\n")
	at, indent := len(lines), "  "
	for i, l := range lines {
		if l == "metadata:" {
			at = i + 1
			for _, next := range lines[at:] {
				if strings.TrimSpace(next) == "" {
					continue
				}
				if strings.HasPrefix(next, " ") {
					indent = next[:len(next)-len(strings.TrimLeft(next, " "))]
				}
				break
			}
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
	lines = append(lines[:at], append([]string{indent + Marker}, lines[at:]...)...)
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

func (s Skills) Install(name, staged string, beforeSwap func() error) error {
	n := len(SkillRoots)
	dst, fresh, old := make([]string, n), make([]string, n), make([]string, n)
	defer func() {
		for i := range fresh {
			if fresh[i] != "" {
				_ = os.RemoveAll(fresh[i])
			}
		}
	}()
	for i, root := range SkillRoots {
		dst[i] = s.Dir(root, name)
		parent := filepath.Dir(dst[i])
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp(parent, ".hand-"+name+"-")
		if err != nil {
			return err
		}
		fresh[i] = tmp
		if err := os.Chmod(tmp, 0o755); err != nil {
			return err
		}
		if err := Copy(staged, tmp); err != nil {
			return err
		}
	}
	if beforeSwap != nil {
		if err := beforeSwap(); err != nil {
			return err
		}
	}
	restore := func(from int) {
		for j := from; j < n; j++ {
			if old[j] != "" {
				_ = Rename(old[j], dst[j])
			}
		}
	}
	for i := range dst {
		if _, err := os.Lstat(dst[i]); err != nil {
			continue
		}
		old[i] = fresh[i] + ".old"
		if err := Rename(dst[i], old[i]); err != nil {
			old[i] = ""
			restore(0)
			return err
		}
	}
	for i := range dst {
		if err := Rename(fresh[i], dst[i]); err != nil {
			for j := range i {
				_ = os.RemoveAll(dst[j])
			}
			restore(0)
			return err
		}
		fresh[i] = ""
	}
	var first error
	for _, o := range old {
		if o == "" {
			continue
		}
		if err := os.RemoveAll(o); err != nil && first == nil {
			first = fmt.Errorf("installed, but could not delete the old copy: %w", err)
		}
	}
	return first
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
		if e.Sum != "" && sum != e.Sum && sum != e.Next && kind != "changed" {
			kind, root = "changed", r
		}
	}
	return kind, root, nil
}
