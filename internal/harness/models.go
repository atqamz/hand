package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/flock"
	"github.com/atqamz/hand/internal/fsx"
	"github.com/atqamz/hand/internal/state"
)

type Source string

const (
	SourceBuiltin    Source = "built-in"
	SourceCustom     Source = "custom"
	SourceDiscovered Source = "discovered"
)

type Model struct {
	Name       string   `json:"name"`
	Label      string   `json:"label,omitempty"`
	Efforts    []string `json:"efforts,omitempty"`
	Source     Source   `json:"source"`
	Hidden     bool     `json:"hidden,omitempty"`
	Unverified bool     `json:"unverified,omitempty"`
}

type Failure struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

type Listing struct {
	Format  int       `json:"format"`
	Harness string    `json:"harness"`
	Version string    `json:"version,omitempty"`
	Key     string    `json:"key,omitempty"`
	At      time.Time `json:"at"`
	Models  []Model   `json:"models,omitempty"`
	Gone    []string  `json:"gone,omitempty"`
	Failed  *Failure  `json:"failed,omitempty"`
}

type lister struct {
	ttl      time.Duration
	prefixes []string
	files    func(Env) []string
	list     func(ctx context.Context, x runner, env Env) ([]Model, bool, error)
}

var listers = map[string]lister{
	"claude":   claudeLister,
	"codex":    codexLister,
	"opencode": opencodeLister,
	"agy":      agyLister,
	"copilot":  copilotLister,
}

const (
	cacheFormat = 1
	failRetry   = 15 * time.Minute
	maxCache    = 8 << 20
	maxModels   = 5000
	maxGone     = 200
)

func cacheDir(env Env) string {
	root := env.get("SECONDHAND_HOME")
	if root == "" {
		home := env.get("HOME")
		if home == "" {
			return ""
		}
		root = filepath.Join(home, ".secondhand")
	}
	return filepath.Join(root, "models")
}

func load(name string, env Env) (Listing, bool) {
	dir := cacheDir(env)
	if _, ok := listers[name]; !ok || dir == "" {
		return Listing{}, false
	}
	f, err := os.Open(filepath.Join(dir, name+".json"))
	if err != nil {
		return Listing{}, false
	}
	defer f.Close()
	var l Listing
	if json.NewDecoder(io.LimitReader(f, maxCache)).Decode(&l) != nil || l.Format != cacheFormat || l.Harness != name {
		return Listing{}, false
	}
	return l, true
}

func store(dir string, l Listing) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, l.Harness+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = fsx.Rename(f.Name(), filepath.Join(dir, l.Harness+".json"))
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}

func listingKey(bin string, files []string) string {
	h := sha256.New()
	for _, p := range append([]string{bin}, files...) {
		fmt.Fprint(h, p, "\x00")
		if fi, err := os.Stat(p); err == nil {
			fmt.Fprint(h, fi.Size(), "\x00", fi.ModTime().UnixNano())
		} else {
			fmt.Fprint(h, "-")
		}
		fmt.Fprintln(h)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

type Status struct {
	Listing Listing
	Found   bool
	Missing string
	Stale   bool
	Old     bool
	Due     bool
}

func StatusOf(name string, env Env, now time.Time) Status {
	var s Status
	l, ok := listers[name]
	if !ok {
		return s
	}
	s.Listing, s.Found = load(name, env)
	bin, err := Find(name, env)
	s.Missing = missing(name, err)
	s.Old = s.Found && !s.Listing.At.IsZero() && now.Sub(s.Listing.At) >= l.ttl
	s.Stale = !s.Found || s.Listing.At.IsZero() || s.Old
	if s.Missing == "" && !s.Stale {
		s.Stale = s.Listing.Key != listingKey(bin, l.files(env))
	}
	s.Due = s.Missing == "" && s.Stale && (s.Listing.Failed == nil || now.Sub(s.Listing.Failed.At) >= failRetry)
	return s
}

func missing(name string, err error) string {
	var batch *BatchFileError
	switch {
	case errors.As(err, &batch):
		return name + " found only as a batch file"
	case err != nil:
		return name + " not found on PATH"
	}
	return ""
}

func catalog(name string, env Env) (Listing, error) {
	l, ok := load(name, env)
	if ok && len(l.Models) > 0 {
		return l, nil
	}
	switch name {
	case "claude":
		return Listing{Harness: name, Models: builtinClaude()}, nil
	case "codex":
		if models, err := readCodexCache(codexCache(env)); err == nil {
			return Listing{Harness: name, Models: models}, nil
		}
	}
	reason := "no model list yet; run `hand models --refresh " + name + "`"
	if ok && l.Failed != nil {
		reason = l.Failed.Reason
	}
	return Listing{}, fmt.Errorf("%w: %s", state.ErrInvalid, reason)
}

func builtinClaude() []Model {
	out := make([]Model, 0, len(claudeAliases))
	for _, m := range claudeAliases {
		out = append(out, Model{Name: m, Efforts: slices.Clone(claudeEfforts), Source: SourceBuiltin})
	}
	return out
}

func find(models []Model, name string) (Model, bool) {
	i := slices.IndexFunc(models, func(m Model) bool { return m.Name == name })
	if i < 0 {
		return Model{}, false
	}
	return models[i], true
}

func goneText(s Spec) string {
	return fmt.Sprintf("model %s is no longer listed by %s", s.Model, s.Harness)
}

func Vanished(s Spec, env Env, now time.Time) (string, bool) {
	return StatusOf(s.Harness, env, now).Vanished(s)
}

func (st Status) Vanished(s Spec) (string, bool) {
	if s.Model == "" || !slices.Contains(st.Listing.Gone, s.Model) {
		return "", false
	}
	return goneText(s), s.Harness != "claude" && st.Found && st.Missing == "" && !st.Stale
}

func Detected(name string, env Env) ([]Model, error) {
	if _, ok := listers[name]; !ok {
		return nil, fmt.Errorf("%w: harness %q must be one of %s", state.ErrInvalid, name, strings.Join(state.Harnesses, ", "))
	}
	l, err := catalog(name, env)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(slices.Clone(l.Models), func(m Model) bool { return m.Hidden }), nil
}

type Options struct {
	Environ []string
	Dir     string
	Now     func() time.Time
}

func Refresh(ctx context.Context, name string, env Env, o Options) (Listing, error) {
	l, ok := listers[name]
	if !ok {
		return Listing{}, fmt.Errorf("%w: harness %q must be one of %s", state.ErrInvalid, name, strings.Join(state.Harnesses, ", "))
	}
	dir := cacheDir(env)
	if dir == "" {
		return Listing{}, fmt.Errorf("%w: set HOME or SECONDHAND_HOME to keep the model cache", state.ErrInvalid)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	bin, err := Find(name, env)
	if err != nil {
		return Listing{Harness: name, Failed: &Failure{At: o.Now(), Reason: missing(name, err)}}, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Listing{}, err
	}
	_ = os.Chmod(dir, 0o700)
	started := time.Now()
	unlock, waited, err := lock(ctx, filepath.Join(dir, name+".lock"))
	if err != nil {
		return Listing{}, err
	}
	defer unlock()
	stale, _ := filepath.Glob(filepath.Join(dir, name+".*.tmp"))
	for _, tmp := range stale {
		_ = os.Remove(tmp)
	}
	if prev, ok := load(name, env); ok && waited {
		if fi, err := os.Stat(filepath.Join(dir, name+".json")); err == nil && !fi.ModTime().Before(started.Add(-time.Second)) {
			return prev, nil
		}
	}
	x := runner{bin: bin, environ: childEnv(o.Environ, l.prefixes), dir: o.Dir}
	key := listingKey(bin, l.files(env))
	version := x.version(ctx)
	models, complete, err := l.list(ctx, x, env)
	if ctx.Err() != nil {
		return Listing{}, ctx.Err()
	}
	if models = tidy(name, models); err == nil && len(models) == 0 {
		err = errors.New("it listed no models")
	}
	prev, had := load(name, env)
	next := Listing{Format: cacheFormat, Harness: name, Version: version, Key: key, At: o.Now(), Models: models, Gone: gone(prev, models, complete)}
	if err != nil {
		next = prev
		next.Format, next.Harness = cacheFormat, name
		if !had {
			next.Version = version
		}
		next.Failed = &Failure{At: o.Now(), Reason: clip(err.Error(), 200)}
	}
	return next, store(dir, next)
}

func lock(ctx context.Context, path string) (func(), bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	waited := false
	for {
		held, err := flock.Lock(f, false)
		if err != nil {
			_ = f.Close()
			return nil, false, err
		}
		if held {
			return func() { _ = flock.Release(f) }, waited, nil
		}
		waited = true
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, false, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func gone(prev Listing, models []Model, complete bool) []string {
	candidates := slices.Clone(prev.Gone)
	if complete {
		candidates = append(candidates, names(prev.Models)...)
	}
	var out []string
	for _, n := range candidates {
		if _, here := find(models, n); !here && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out[max(0, len(out)-maxGone):]
}

func names(models []Model) []string {
	out := make([]string, len(models))
	for i, m := range models {
		out[i] = m.Name
	}
	return out
}

func tidy(harness string, models []Model) []Model {
	var out []Model
	for _, m := range models {
		if !validModel(harness, m.Name) || len(out) >= maxModels {
			continue
		}
		if _, dup := find(out, m.Name); dup {
			continue
		}
		m.Label = clip(m.Label, 100)
		var efforts []string
		for _, e := range m.Efforts {
			if effortToken.MatchString(e) && !slices.Contains(efforts, e) {
				efforts = append(efforts, e)
			}
		}
		m.Efforts = efforts
		out = append(out, m)
	}
	return out
}
