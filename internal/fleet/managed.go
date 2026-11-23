package fleet

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/fsx"
	"github.com/atqamz/hand/internal/state"
)

const MaxSkillBytes = 32 << 20

var Rename = fsx.Rename

var SkillName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const (
	Marker      = "managed-by: hand"
	lockFileMu  = ".hand-skills.lock"
	pendingFile = ".hand-skills-next.json"
)

type Skills struct{ Base, Scope, Meta string }

func (s Skills) Roots() []string {
	if s.Scope == "project" {
		return SkillRoots[1:2]
	}
	return SkillRoots
}

func (s Skills) Dir(root, name string) string {
	return filepath.Join(s.Base, root, "skills", name)
}

func (s Skills) metaDir() string {
	if s.Meta != "" {
		return s.Meta
	}
	return filepath.Join(s.Base, SkillRoots[1], "skills")
}

func (s Skills) pendingPath() string { return filepath.Join(s.metaDir(), pendingFile) }

func (s Skills) owned(name string) bool {
	for _, root := range s.Roots() {
		if Owned(s.Dir(root, name)) {
			return true
		}
	}
	return false
}

func (s Skills) Load() (map[string]Managed, error) {
	if err := s.migrate(); err != nil {
		return nil, err
	}
	l, err := s.readLock()
	if err != nil {
		return nil, err
	}
	pending, err := s.readPending()
	if err != nil {
		return nil, err
	}
	sub, err := s.readSubscribed()
	if err != nil {
		return nil, err
	}
	out := map[string]Managed{}
	for name, raw := range l.Skills {
		if !SkillName.MatchString(name) || !s.owned(name) {
			continue
		}
		e := Managed{Next: pending[name]}
		if err := json.Unmarshal(raw, &e.LockEntry); err != nil {
			return nil, fmt.Errorf("read %s: %s: %w", s.lockPath(), name, err)
		}
		if want, ok := sub.Skills[name]; ok {
			e.Ref, e.Subscribed = want.Ref, want.Source
		}
		out[name] = e
	}
	return out, nil
}

func (s Skills) readPending() (map[string]string, error) {
	pending := map[string]string{}
	b, err := os.ReadFile(s.pendingPath())
	if errors.Is(err, fs.ErrNotExist) {
		return pending, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &pending); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.pendingPath(), err)
	}
	return pending, nil
}

func (s Skills) Lock() (func(), error) {
	dir := s.metaDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFileMu), os.O_RDWR|os.O_CREATE, 0o644)
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

func (s Skills) Save(managed map[string]Managed) error {
	l, err := s.readLock()
	if err != nil {
		return err
	}
	sub, err := s.readSubscribed()
	if err != nil {
		return err
	}
	pending := map[string]string{}
	for name, e := range managed {
		if s.Scope == "project" && e.Local() {
			e.Source = s.portableSource(e.Source)
		}
		b, err := json.Marshal(e.LockEntry)
		if err != nil {
			return err
		}
		l.Skills[name] = b
		sub.Skills[name] = subscription{Source: cmp.Or(e.Subscribed, e.Source), Ref: e.Ref}
		if e.Next != "" {
			pending[name] = e.Next
		}
	}
	if err := s.writeLock(l); err != nil {
		return err
	}
	if err := s.writeSubscribed(sub); err != nil {
		return err
	}
	return s.writePending(pending)
}

func (s Skills) writePending(pending map[string]string) error {
	if len(pending) == 0 {
		if err := os.Remove(s.pendingPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.pendingPath(), append(b, '\n'))
}

func (s Skills) Forget(name string) error {
	l, err := s.readLock()
	if err != nil {
		return err
	}
	delete(l.Skills, name)
	if err := s.writeLock(l); err != nil {
		return err
	}
	sub, err := s.readSubscribed()
	if err != nil {
		return err
	}
	delete(sub.Skills, name)
	return s.writeSubscribed(sub)
}

func writeAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
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
		err = fsx.Rename(tmp, path)
		if err == nil {
			_ = syncDir(dir)
			return nil
		}
	}
	_ = os.Remove(tmp)
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
			return fmt.Errorf("%w: %s is not a regular file; a skill is copied, never linked", state.ErrInvalid, rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if total += info.Size(); total > MaxSkillBytes {
			return fmt.Errorf("%w: a skill is over %d MiB; a skill is a small folder", state.ErrInvalid, MaxSkillBytes>>20)
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
	if _, _, err := parseFrontmatter(head); err != nil {
		return fmt.Errorf("%w: SKILL.md: %v", state.ErrInvalid, err)
	}
	lines := strings.Split(head, "\n")
	at, indent := len(lines), "  "
	for i, l := range lines {
		if strings.TrimRight(l, " \t") == "metadata:" {
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
	}
	if at == len(lines) {
		lines = append(lines, "metadata:")
		at++
	}
	lines = append(lines[:at], append([]string{indent + Marker}, lines[at:]...)...)
	return os.WriteFile(path, []byte("---\n"+strings.Join(lines, "\n")+"\n---\n"+rest), 0o644)
}

func Unmark(dir string) error {
	path := filepath.Join(dir, "SKILL.md")
	have, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	head, rest, ok := frontmatter(string(have))
	if !ok {
		return nil
	}
	lines := strings.Split(head, "\n")
	meta := slices.IndexFunc(lines, func(l string) bool { return strings.TrimRight(l, " \t") == "metadata:" })
	at := -1
	for i := meta + 1; meta >= 0 && i < len(lines) && at < 0; i++ {
		switch l := lines[i]; {
		case strings.TrimSpace(l) == "":
		case !strings.HasPrefix(l, " "):
			i = len(lines)
		case strings.TrimSpace(l) == Marker:
			at = i
		}
	}
	if at < 0 {
		return nil
	}
	lines = slices.Delete(lines, at, at+1)
	child := false
	for _, l := range lines[meta+1:] {
		if strings.TrimSpace(l) != "" {
			child = strings.HasPrefix(l, " ")
			break
		}
	}
	if !child {
		lines = slices.Delete(lines, meta, meta+1)
	}
	return os.WriteFile(path, []byte("---\n"+strings.Join(lines, "\n")+"\n---\n"+rest), 0o644)
}

func Stage(src, name string) (dir, sum string, err error) {
	if dir, err = os.MkdirTemp("", "hand-skill-"); err != nil {
		return "", "", err
	}
	if err = Copy(src, dir); err == nil {
		err = ValidateSpec(dir, name)
	}
	if err == nil {
		err = mark(dir)
	}
	if err == nil {
		sum, err = ComputedHash(dir)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", err
	}
	return dir, sum, nil
}

func (s Skills) Install(name, staged string, beforeSwap func() error) error {
	roots := s.Roots()
	n := len(roots)
	dst, fresh, old := make([]string, n), make([]string, n), make([]string, n)
	defer func() {
		for i := range fresh {
			if fresh[i] != "" {
				_ = os.RemoveAll(fresh[i])
			}
		}
	}()
	for i, root := range roots {
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

func (s Skills) backupDir() string {
	if s.Meta != "" {
		return filepath.Join(s.Meta, "backups")
	}
	return filepath.Join(s.Base, SkillRoots[1], "skills-backups")
}

func (s Skills) Backup(name, stamp string) (string, error) {
	for _, root := range s.Roots() {
		if _, err := os.Lstat(s.Dir(root, name)); err != nil {
			continue
		}
		parent := filepath.Join(s.backupDir(), name)
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", err
		}
		dst, err := os.MkdirTemp(parent, stamp+"-")
		if err != nil {
			return "", err
		}
		if err := Copy(s.Dir(root, name), dst); err != nil {
			_ = os.RemoveAll(dst)
			return "", err
		}
		return dst, nil
	}
	return "", nil
}

func (s Skills) Remove(name string) error {
	for _, root := range s.Roots() {
		if err := os.RemoveAll(s.Dir(root, name)); err != nil {
			return err
		}
	}
	return nil
}

func (s Skills) Check(name string, e Managed) (kind, root string, err error) {
	for _, r := range s.Roots() {
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
		sum, err := ComputedHash(dir)
		if err != nil {
			return "", "", err
		}
		if e.ComputedHash != "" && sum != e.ComputedHash && sum != e.Next && kind != "changed" {
			kind, root = "changed", r
		}
	}
	return kind, root, nil
}
