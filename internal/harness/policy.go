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

	"github.com/atqamz/hand/internal/inbox"
	"github.com/atqamz/hand/internal/state"
)

const PolicyFile = "routing.json"

type Profile struct {
	Spec
	Approve string `json:"approve,omitempty"`
	Access  string `json:"access,omitempty"`
}

type Policy struct {
	Profiles       map[string]Profile `json:"profiles"`
	Notify         []string           `json:"notify,omitzero"`
	Stall          Stall              `json:"stall,omitzero"`
	Inbox          Inbox              `json:"inbox,omitzero"`
	ApproveTimeout ApproveTimeout     `json:"approve_timeout,omitzero"`
	Supervisor     struct {
		Autoresume bool `json:"autoresume"`
	} `json:"supervisor,omitzero"`
}

var StarterPolicy = Policy{Profiles: map[string]Profile{
	"quick":   {Spec: Spec{Harness: "claude", Model: "sonnet", Effort: "low"}},
	"default": {Spec: Spec{Harness: "claude", Model: "sonnet", Effort: "medium"}},
	"high":    {Spec: Spec{Harness: "claude", Model: "sonnet", Effort: "high"}},
	"deep":    {Spec: Spec{Harness: "claude", Model: "sonnet", Effort: "xhigh"}},
	"max":     {Spec: Spec{Harness: "claude", Model: "sonnet", Effort: "max"}},
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

type ApproveTimeout time.Duration

const DefaultApproveTimeout = 10 * time.Minute

func (t *ApproveTimeout) UnmarshalJSON(b []byte) error {
	var text string
	if json.Unmarshal(b, &text) != nil {
		return errors.New(`approve_timeout must be a duration from 1m to 24h, like "10m"`)
	}
	d, err := time.ParseDuration(text)
	if err != nil || d < time.Minute || d > 24*time.Hour {
		return fmt.Errorf("approve_timeout is %q; give a duration from 1m to 24h, like \"10m\"", text)
	}
	*t = ApproveTimeout(d)
	return nil
}

func (p Policy) ApproveWait() time.Duration {
	if p.ApproveTimeout == 0 {
		return DefaultApproveTimeout
	}
	return time.Duration(p.ApproveTimeout)
}

type Inbox struct{ Keep time.Duration }

const inboxForm = `inbox must be "off" or {"keep": "30d"}`

func (i *Inbox) UnmarshalJSON(b []byte) error {
	var word string
	if json.Unmarshal(b, &word) == nil {
		if word != "off" {
			return errors.New(inboxForm)
		}
		*i = Inbox{}
		return nil
	}
	var raw struct {
		Keep *string `json:"keep"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("%s: %w", inboxForm, err)
	}
	*i = Inbox{}
	if raw.Keep == nil || *raw.Keep == "off" {
		return nil
	}
	keep, ok := inbox.ParseAge(*raw.Keep)
	if !ok {
		return fmt.Errorf("inbox.keep is %q; give \"off\" or days from 1d to 9999d, like \"30d\"", *raw.Keep)
	}
	i.Keep = keep
	return nil
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
		err := checkApprove(name, p.Profiles[name])
		if err == nil {
			err = checkAccess(name, p.Profiles[name])
		}
		if err != nil {
			return Policy{}, fmt.Errorf("%w: %s: %v", state.ErrInvalid, path, err)
		}
	}
	return p, nil
}

func checkApprove(name string, pr Profile) error {
	switch pr.Approve {
	case "", state.ApproveNever:
		return nil
	case state.ApproveAsk:
		if pr.Harness != "claude" {
			return fmt.Errorf("profile %q: approve %q is only available for claude, not %s", name, state.ApproveAsk, pr.Harness)
		}
		return nil
	}
	return fmt.Errorf("profile %q: approve is %q; give %q or %q", name, pr.Approve, state.ApproveNever, state.ApproveAsk)
}

func checkAccess(name string, pr Profile) error {
	if err := CheckAccess(pr.Harness, pr.Access); err != nil {
		return fmt.Errorf("profile %q: %s", name, strings.TrimPrefix(err.Error(), state.ErrInvalid.Error()+": "))
	}
	if pr.Access == state.AccessReadonly && pr.Approve == state.ApproveAsk {
		return fmt.Errorf("profile %q: access %q cannot ask for approval; leave approve at %q", name, state.AccessReadonly, state.ApproveNever)
	}
	return nil
}

func (p Policy) Names() []string {
	names := make([]string, 0, len(p.Profiles))
	for name := range p.Profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (p Policy) Profile(name string) (Profile, error) {
	s, ok := p.Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("%w: unknown profile %q; known: %s", state.ErrInvalid, name, strings.Join(p.Names(), ", "))
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
