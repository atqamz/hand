package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/atqamz/hand/internal/state"
)

const subscribedFile = "skills.json"

type subscription struct {
	Source string `json:"source"`
	Ref    string `json:"ref,omitempty"`
}

type subscribed struct {
	Skills map[string]subscription `json:"skills"`
}

func (s Skills) subscribedPath() string {
	switch s.Scope {
	case "project":
		return filepath.Join(s.Base, SkillRoots[1], "skills", subscribedFile)
	case "user":
		return filepath.Join(s.Base, SkillRoots[1], subscribedFile)
	}
	return filepath.Join(s.Base, subscribedFile)
}

func (s Skills) readSubscribed() (subscribed, error) {
	sub := subscribed{Skills: map[string]subscription{}}
	b, err := os.ReadFile(s.subscribedPath())
	if errors.Is(err, fs.ErrNotExist) {
		return sub, nil
	}
	if err != nil {
		return sub, err
	}
	if err := json.Unmarshal(b, &sub); err != nil || sub.Skills == nil {
		return sub, fmt.Errorf("%w: %s is not a skills subscription file: %v", state.ErrInvalid, s.subscribedPath(), err)
	}
	return sub, nil
}

func (s Skills) writeSubscribed(sub subscribed) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sub); err != nil {
		return err
	}
	return writeAtomic(s.subscribedPath(), b.Bytes())
}
