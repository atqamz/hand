package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/atqamz/hand/internal/state"
)

type Spec struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

type Env struct {
	CodexHome, Path string
}

func EnvOf(getenv func(string) string) Env {
	return Env{CodexHome: CodexHome(getenv), Path: getenv("PATH")}
}

const MaxPromptBytes = 16384

var (
	claudeModel   = regexp.MustCompile(`^(default|opus|sonnet|haiku|fable|claude-[a-z0-9.-]+)(\[1m\])?$`)
	claudeEfforts = []string{"low", "medium", "high", "xhigh", "max"}
)

func Validate(s Spec, env Env) error {
	switch s.Harness {
	case "claude":
		if !claudeModel.MatchString(s.Model) {
			return fmt.Errorf("%w: claude model %q is not an alias (opus, sonnet, haiku, fable) or a claude-* name", state.ErrInvalid, s.Model)
		}
		if !slices.Contains(claudeEfforts, s.Effort) {
			return fmt.Errorf("%w: claude effort %q must be one of %s", state.ErrInvalid, s.Effort, strings.Join(claudeEfforts, ", "))
		}
		return nil
	case "codex":
		return validateCodex(s, filepath.Join(env.CodexHome, "models_cache.json"))
	case "agy":
		if s.Effort != "" {
			return fmt.Errorf("%w: agy names the effort in its model id; leave effort empty", state.ErrInvalid)
		}
		models, err := agyModels(env)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(models, func(m Model) bool { return m.Name == s.Model }) {
			return fmt.Errorf("%w: agy model %q is not in `agy models`", state.ErrInvalid, s.Model)
		}
		return nil
	case "opencode":
		if s.Model != "" || s.Effort != "" {
			return fmt.Errorf("%w: opencode picks its model from its own configuration; leave model and effort empty", state.ErrInvalid)
		}
		return nil
	}
	return fmt.Errorf("%w: harness %q must be one of %s", state.ErrInvalid, s.Harness, strings.Join(state.Harnesses, ", "))
}

func Prefills(name string) bool { return name == "opencode" }

type Model struct {
	Name    string
	Efforts []string
}

var claudeAliases = []string{"opus", "sonnet", "haiku", "fable"}

func Models(name string, env Env) ([]Model, error) {
	switch name {
	case "claude":
		out := make([]Model, 0, len(claudeAliases))
		for _, m := range claudeAliases {
			out = append(out, Model{Name: m, Efforts: slices.Clone(claudeEfforts)})
		}
		return out, nil
	case "codex":
		return codexModels(filepath.Join(env.CodexHome, "models_cache.json"))
	case "agy":
		return agyModels(env)
	case "opencode":
		return nil, nil
	}
	return nil, fmt.Errorf("%w: harness %q must be one of %s", state.ErrInvalid, name, strings.Join(state.Harnesses, ", "))
}

type agyListing struct {
	models []Model
	err    error
	at     time.Time
}

var agyRetry = 60 * time.Second

var agyLists = struct {
	sync.Mutex
	byBin map[string]agyListing
}{byBin: map[string]agyListing{}}

func agyModels(env Env) ([]Model, error) {
	bin, err := LookPath("agy", env.Path)
	if err != nil {
		return nil, err
	}
	agyLists.Lock()
	defer agyLists.Unlock()
	if l, ok := agyLists.byBin[bin]; ok && (l.err == nil || time.Since(l.at) < agyRetry) {
		return slices.Clone(l.models), l.err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "models")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	var models []Model
	for _, line := range strings.Split(string(out), "\n") {
		if id, _, ok := strings.Cut(line, "\t"); ok && strings.TrimSpace(id) != "" {
			models = append(models, Model{Name: strings.TrimSpace(id)})
		}
	}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && len(bytes.TrimSpace(exit.Stderr)) > 0:
		err = fmt.Errorf("%v: %s", err, bytes.TrimSpace(exit.Stderr))
	case err == nil && len(models) == 0:
		err = fmt.Errorf("it listed no models")
	}
	if err != nil {
		err = fmt.Errorf("%w: could not list agy models: %v; check that agy is logged in", state.ErrInvalid, err)
		models = nil
	}
	agyLists.byBin[bin] = agyListing{models, err, time.Now()}
	return slices.Clone(models), err
}

func codexModels(cache string) ([]Model, error) {
	b, err := os.ReadFile(cache)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read codex model cache %s (run codex once): %v", state.ErrInvalid, cache, err)
	}
	var c struct {
		Models []struct {
			Slug   string `json:"slug"`
			Levels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%w: codex model cache %s: %v", state.ErrInvalid, cache, err)
	}
	out := make([]Model, 0, len(c.Models))
	for _, m := range c.Models {
		model := Model{Name: m.Slug}
		for _, l := range m.Levels {
			model.Efforts = append(model.Efforts, l.Effort)
		}
		out = append(out, model)
	}
	return out, nil
}

func validateCodex(s Spec, cache string) error {
	models, err := codexModels(cache)
	if err != nil {
		return err
	}
	var slugs []string
	for _, m := range models {
		slugs = append(slugs, m.Name)
		if m.Name != s.Model {
			continue
		}
		if !slices.Contains(m.Efforts, s.Effort) {
			return fmt.Errorf("%w: codex model %s supports effort %s, not %q", state.ErrInvalid, s.Model, strings.Join(m.Efforts, ", "), s.Effort)
		}
		return nil
	}
	return fmt.Errorf("%w: codex model %q is not in %s (known: %s)", state.ErrInvalid, s.Model, cache, strings.Join(slugs, ", "))
}

func CodexHome(getenv func(string) string) string {
	if h := getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(getenv("HOME"), ".codex")
}

func checkPrompt(prompt string) error {
	switch {
	case strings.TrimSpace(prompt) == "":
		return fmt.Errorf("%w: prompt must not be empty", state.ErrInvalid)
	case len(prompt) > MaxPromptBytes:
		return fmt.Errorf("%w: prompt is %d bytes; luvus accepts at most %d per argument", state.ErrInvalid, len(prompt), MaxPromptBytes)
	case strings.HasPrefix(prompt, "-"):
		return fmt.Errorf("%w: prompt must not start with '-', or the harness reads it as a flag", state.ErrInvalid)
	case strings.ContainsRune(prompt, 0):
		return fmt.Errorf("%w: prompt must not contain NUL", state.ErrInvalid)
	}
	return nil
}

func Argv(bin string, s Spec, prompt string) ([]string, error) {
	if err := checkPrompt(prompt); err != nil {
		return nil, err
	}
	switch s.Harness {
	case "opencode":
		return []string{bin, "--standalone", "--auto", "--prompt", prompt}, nil
	case "claude":
		return []string{bin, "--dangerously-skip-permissions", "--model", s.Model, "--effort", s.Effort, prompt}, nil
	case "agy":
		return []string{bin, "--model", s.Model, "--dangerously-skip-permissions", "-i", prompt}, nil
	case "codex":
		return []string{bin, "--dangerously-bypass-approvals-and-sandbox", "-m", s.Model, "-c", "model_reasoning_effort=" + s.Effort, prompt}, nil
	}
	return nil, fmt.Errorf("%w: harness %q has no launch command", state.ErrInvalid, s.Harness)
}
