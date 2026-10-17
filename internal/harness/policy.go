package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/state"
)

const PolicyFile = "routing.json"

type Policy struct {
	Profiles   map[string]Spec `json:"profiles"`
	Notify     []string        `json:"notify,omitzero"`
	Stall      Stall           `json:"stall,omitzero"`
	Supervisor struct {
		Autoresume bool `json:"autoresume"`
	} `json:"supervisor,omitzero"`
}

var StarterPolicy = Policy{Profiles: map[string]Spec{
	"quick":   {Harness: "claude", Model: "sonnet", Effort: "low"},
	"default": {Harness: "claude", Model: "sonnet", Effort: "medium"},
	"high":    {Harness: "claude", Model: "sonnet", Effort: "high"},
	"deep":    {Harness: "claude", Model: "sonnet", Effort: "xhigh"},
	"max":     {Harness: "claude", Model: "sonnet", Effort: "max"},
}}

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type Stall struct {
	Off          bool
	After, Every time.Duration
}

var DefaultStall = Stall{After: 20 * time.Minute, Every: time.Hour}

const stallForm = `stall must be "off" or {"after": "20m", "every": "60m"}`

func (s *Stall) UnmarshalJSON(b []byte) error {
	var word string
	if json.Unmarshal(b, &word) == nil {
		if word != "off" {
			return errors.New(stallForm)
		}
		*s = Stall{Off: true}
		return nil
	}
	var raw struct {
		After *string `json:"after"`
		Every *string `json:"every"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("%s: %w", stallForm, err)
	}
	*s = DefaultStall
	if err := stallDelay(&s.After, "after", raw.After); err != nil {
		return err
	}
	return stallDelay(&s.Every, "every", raw.Every)
}

func stallDelay(to *time.Duration, name string, text *string) error {
	if text == nil {
		return nil
	}
	d, err := time.ParseDuration(*text)
	if err != nil || d < time.Minute || d > 24*time.Hour {
		return fmt.Errorf("stall.%s is %q; give a duration from 1m to 24h, like \"20m\"", name, *text)
	}
	*to = d
	return nil
}

func (s Stall) Delay(n int) time.Duration {
	d := s.After
	for range n {
		d = min(2*d, s.Every)
	}
	return d
}

func LoadPolicy(home string) (Policy, error) {
	path := filepath.Join(home, PolicyFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Policy{}, fmt.Errorf("%w: no routing policy at %s; run `hand init` to write a starter", state.ErrInvalid, path)
	}
	if err != nil {
		return Policy{}, err
	}
	p := Policy{Stall: DefaultStall}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("%w: %s: %v", state.ErrInvalid, path, err)
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return Policy{}, fmt.Errorf("%w: %s holds data after the policy", state.ErrInvalid, path)
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
	path := filepath.Join(home, PolicyFile)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
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
	if werr != nil {
		return false, errors.Join(werr, os.Remove(path))
	}
	return true, nil
}
