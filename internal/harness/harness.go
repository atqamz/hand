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

	"github.com/atqamz/hand/internal/proc"
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
	modelToken    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@\[\]#+-]{0,199}$`)
	effortToken   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	claudeEfforts = []string{"low", "medium", "high", "xhigh", "max"}
)

func Check(s Spec, env Env) (string, error) {
	switch s.Harness {
	case "claude", "codex", "agy":
	case "opencode":
		if s.Model != "" || s.Effort != "" {
			return "", fmt.Errorf("%w: opencode picks its model from its own configuration; leave model and effort empty", state.ErrInvalid)
		}
		return "", nil
	default:
		return "", fmt.Errorf("%w: harness %q must be one of %s", state.ErrInvalid, s.Harness, strings.Join(state.Harnesses, ", "))
	}
	if !modelToken.MatchString(s.Model) {
		return "", fmt.Errorf("%w: %s model %q must be 1 to 200 characters of letters, digits and . _ : / @ [ ] # + -, starting with a letter or digit", state.ErrInvalid, s.Harness, s.Model)
	}
	if s.Harness == "agy" {
		if s.Effort != "" {
			return "", fmt.Errorf("%w: agy names the effort in its model id; leave effort empty", state.ErrInvalid)
		}
	} else if !effortToken.MatchString(s.Effort) {
		return "", fmt.Errorf("%w: %s effort %q must be 1 to 32 lowercase letters, digits, _ or -, starting with a letter", state.ErrInvalid, s.Harness, s.Effort)
	}
	switch s.Harness {
	case "claude":
		return checkClaude(s), nil
	case "codex":
		return checkCodex(s, filepath.Join(env.CodexHome, "models_cache.json")), nil
	}
	return checkAgy(s, env), nil
}

func checkClaude(s Spec) string {
	var warns []string
	if !claudeModel.MatchString(s.Model) {
		warns = append(warns, fmt.Sprintf("claude model %s is not an alias (opus, sonnet, haiku, fable) or a claude-* name, and Claude Code lists no models", s.Model))
	}
	if !slices.Contains(claudeEfforts, s.Effort) {
		warns = append(warns, fmt.Sprintf("claude model %s: effort %s is not one of %s", s.Model, s.Effort, strings.Join(claudeEfforts, ", ")))
	}
	return unverified(warns)
}

func checkAgy(s Spec, env Env) string {
	models, err := agyModels(env)
	switch {
	case err != nil:
		return unverified([]string{fmt.Sprintf("agy model %s could not be checked: %s", s.Model, plainReason(err))})
	case !slices.ContainsFunc(models, func(m Model) bool { return m.Name == s.Model }):
		return unverified([]string{fmt.Sprintf("agy model %s is not in `agy models`", s.Model)})
	}
	return ""
}

func unverified(warns []string) string {
	if len(warns) == 0 {
		return ""
	}
	return strings.Join(warns, "; ") + "; starting anyway"
}

func plainReason(err error) string {
	return strings.Join(strings.Fields(strings.TrimPrefix(err.Error(), state.ErrInvalid.Error()+": ")), " ")
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

var (
	agyRetry   = 60 * time.Second
	agyTimeout = 30 * time.Second
)

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
	ctx, cancel := context.WithTimeout(context.Background(), agyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "models")
	cmd.WaitDelay = time.Second
	proc.NoWindow(cmd)
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
	const hint = "could not list agy models: %v; check that agy is logged in"
	switch {
	case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		err = fmt.Errorf("%w: agy models timed out after %v", state.ErrInvalid, agyTimeout)
	case errors.As(err, &exit) && len(bytes.TrimSpace(exit.Stderr)) > 0:
		err = fmt.Errorf("%w: "+hint, state.ErrInvalid, fmt.Sprintf("%v: %s", err, bytes.TrimSpace(exit.Stderr)))
	case err != nil:
		err = fmt.Errorf("%w: "+hint, state.ErrInvalid, err)
	case len(models) == 0:
		err = fmt.Errorf("%w: "+hint, state.ErrInvalid, "it listed no models")
	}
	if err != nil {
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

func checkCodex(s Spec, cache string) string {
	models, err := codexModels(cache)
	if err != nil {
		return unverified([]string{fmt.Sprintf("codex model %s could not be checked: no usable %s (run codex once)", s.Model, filepath.Base(cache))})
	}
	for _, m := range models {
		if m.Name != s.Model {
			continue
		}
		if !slices.Contains(m.Efforts, s.Effort) {
			return unverified([]string{fmt.Sprintf("codex model %s lists efforts %s, not %s", s.Model, strings.Join(m.Efforts, ", "), s.Effort)})
		}
		return ""
	}
	return unverified([]string{fmt.Sprintf("codex model %s is not in the model cache", s.Model)})
}

func CodexHome(getenv func(string) string) string {
	if h := getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(getenv("HOME"), ".codex")
}

func CheckPrompt(prompt string) error {
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

func checkDir(s Spec, dir string) error {
	if s.Harness == "codex" && !filepath.IsAbs(dir) {
		return fmt.Errorf("%w: codex needs an absolute directory for --cd, not %q", state.ErrInvalid, dir)
	}
	return nil
}

func Argv(bin string, s Spec, prompt, dir string) ([]string, error) {
	if err := CheckPrompt(prompt); err != nil {
		return nil, err
	}
	if err := checkDir(s, dir); err != nil {
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
		return []string{bin, "--dangerously-bypass-approvals-and-sandbox", "--disable", "hooks", "--cd", dir, "-m", s.Model, "-c", "model_reasoning_effort=" + s.Effort, prompt}, nil
	}
	return nil, fmt.Errorf("%w: harness %q has no launch command", state.ErrInvalid, s.Harness)
}
