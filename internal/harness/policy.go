package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

const PolicyFile = "routing.json"

type Policy struct {
	Profiles map[string]Spec `json:"profiles"`
}

var StarterPolicy = Policy{Profiles: map[string]Spec{
	"quick":   {Harness: "codex", Model: "gpt-6-luna", Effort: "low"},
	"default": {Harness: "claude", Model: "sonnet", Effort: "medium"},
	"deep":    {Harness: "claude", Model: "opus", Effort: "xhigh"},
}}

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func LoadPolicy(home string) (Policy, error) {
	path := filepath.Join(home, PolicyFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Policy{}, fmt.Errorf("%w: no routing policy at %s; run `hand init` to write a starter", state.ErrInvalid, path)
	}
	if err != nil {
		return Policy{}, err
	}
	var p Policy
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("%w: %s: %v", state.ErrInvalid, path, err)
	}
	if len(p.Profiles) == 0 {
		return Policy{}, fmt.Errorf("%w: %s defines no profiles", state.ErrInvalid, path)
	}
	for name := range p.Profiles {
		if !profileName.MatchString(name) {
			return Policy{}, fmt.Errorf("%w: %s: profile name %q must match %s", state.ErrInvalid, path, name, profileName)
		}
	}
	return p, nil
}

func (p Policy) Names() []string {
	names := make([]string, 0, len(p.Profiles))
	for name := range p.Profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (p Policy) Profile(name string) (Spec, error) {
	s, ok := p.Profiles[name]
	if !ok {
		return Spec{}, fmt.Errorf("%w: unknown profile %q; known: %s", state.ErrInvalid, name, strings.Join(p.Names(), ", "))
	}
	return s, nil
}

func WriteStarterPolicy(home string) (bool, error) {
	b, err := json.MarshalIndent(StarterPolicy, "", "  ")
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(filepath.Join(home, PolicyFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, werr := f.Write(append(b, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr == nil, werr
}
