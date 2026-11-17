package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/atqamz/hand/internal/state"
)

const (
	lockFile    = "skills-lock.json"
	lockVersion = 2
)

type LockEntry struct {
	Source       string `json:"source"`
	SourceURL    string `json:"sourceUrl,omitempty"`
	Ref          string `json:"ref,omitempty"`
	Commit       string `json:"commit,omitempty"`
	SourceType   string `json:"sourceType"`
	SkillPath    string `json:"skillPath,omitempty"`
	ComputedHash string `json:"computedHash"`
}

type Managed struct {
	LockEntry
	Next string
}

func (e Managed) Local() bool { return e.SourceType == "local" }

func (e Managed) URL() string {
	if e.SourceURL != "" {
		return e.SourceURL
	}
	return e.Source
}

func (e Managed) Path() string {
	if dir := path.Dir(e.SkillPath); dir != "." {
		return dir
	}
	return ""
}

type lock struct {
	Version int                        `json:"version"`
	Skills  map[string]json.RawMessage `json:"skills"`
}

func (s Skills) lockPath() string {
	if s.Scope == "user" {
		return filepath.Join(s.Base, SkillRoots[1], lockFile)
	}
	return filepath.Join(s.Base, lockFile)
}

func (s Skills) readLock() (lock, error) {
	l := lock{Version: lockVersion, Skills: map[string]json.RawMessage{}}
	b, err := os.ReadFile(s.lockPath())
	if errors.Is(err, fs.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	var read lock
	if err := json.Unmarshal(b, &read); err != nil || read.Skills == nil {
		return l, fmt.Errorf("%w: %s is not a skills lock: %v", state.ErrInvalid, s.lockPath(), err)
	}
	if read.Version != lockVersion {
		return l, fmt.Errorf("%w: %s is lock version %d; Hand reads version %d (`npx skills check` upgrades it)", state.ErrInvalid, s.lockPath(), read.Version, lockVersion)
	}
	return read, nil
}

func (s Skills) writeLock(l lock) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(l); err != nil {
		return err
	}
	return writeAtomic(s.lockPath(), b.Bytes())
}
