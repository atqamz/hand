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
)

const legacyStateFile = ".hand-skills.json"

var legacyMarker = regexp.MustCompile(`(?m)^([ \t]+)managed-by: hand-skills$`)

type legacyEntry struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Commit string `json:"commit"`
	Sum    string `json:"sum"`
}

func (s Skills) migrate() error {
	path := filepath.Join(s.metaDir(), legacyStateFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var old map[string]legacyEntry
	if err := json.Unmarshal(b, &old); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	l, err := s.readLock()
	if err != nil {
		return err
	}
	for name, e := range old {
		if !SkillName.MatchString(name) {
			return fmt.Errorf("read %s: %q is not a skill name", path, name)
		}
		if _, taken := l.Skills[name]; taken || !s.legacyClean(name, e.Sum) {
			continue
		}
		for _, root := range s.Roots() {
			file := filepath.Join(s.Dir(root, name), "SKILL.md")
			have, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			if err := os.WriteFile(file, legacyMarker.ReplaceAll(have, []byte("${1}"+Marker)), 0o644); err != nil {
				return err
			}
		}
		sum, err := ComputedHash(s.Dir(s.Roots()[0], name))
		if err != nil {
			return err
		}
		entry := LockEntry{Source: e.Source, SourceType: "git", Commit: e.Commit, ComputedHash: sum}
		if !strings.Contains(e.Source, "://") && !strings.HasPrefix(e.Source, "git@") {
			entry.SourceType = "local"
		} else if e.Path != "" {
			entry.SkillPath = e.Path + "/SKILL.md"
		}
		if l.Skills[name], err = json.Marshal(entry); err != nil {
			return err
		}
	}
	if err := s.writeLock(l); err != nil {
		return err
	}
	return os.Remove(path)
}

func (s Skills) legacyClean(name, want string) bool {
	for _, root := range s.Roots() {
		sum, err := legacySum(s.Dir(root, name))
		if err != nil || sum != want {
			return false
		}
	}
	return true
}

func legacySum(dir string) (string, error) {
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
