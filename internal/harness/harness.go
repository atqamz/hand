package harness

import (
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/atqamz/hand/internal/state"
)

type Spec struct {
	Harness string `json:"harness"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

type Env struct {
	CodexHome, Path string
	Getenv          func(string) string
}

func EnvOf(getenv func(string) string) Env {
	return Env{CodexHome: CodexHome(getenv), Path: getenv("PATH"), Getenv: getenv}
}

func (e Env) get(key string) string {
	if e.Getenv == nil {
		return ""
	}
	return e.Getenv(key)
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
	case "claude", "codex", "agy", "copilot":
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
	switch {
	case s.Harness == "agy":
		if s.Effort != "" {
			return "", fmt.Errorf("%w: agy names the effort in its model id; leave effort empty", state.ErrInvalid)
		}
	case s.Harness == "copilot" && s.Effort == "":
	case !effortToken.MatchString(s.Effort):
		return "", fmt.Errorf("%w: %s effort %q must be 1 to 32 lowercase letters, digits, _ or -, starting with a letter", state.ErrInvalid, s.Harness, s.Effort)
	}
	switch s.Harness {
	case "claude":
		return checkClaude(s, env), nil
	case "codex":
		return checkCodex(s, env), nil
	case "copilot":
		return "", nil
	}
	return checkAgy(s, env), nil
}

func checkClaude(s Spec, env Env) string {
	l, _ := catalog("claude", env)
	m, listed := find(l.Models, s.Model)
	var warns []string
	switch {
	case slices.Contains(l.Gone, s.Model):
		warns = append(warns, goneText(s))
	case !listed && !claudeModel.MatchString(s.Model):
		warns = append(warns, fmt.Sprintf("claude model %s is not an alias (opus, sonnet, haiku, fable), a claude-* name or in Claude Code's model list", s.Model))
	}
	efforts := claudeEfforts
	if len(m.Efforts) > 0 {
		efforts = m.Efforts
	}
	if !slices.Contains(efforts, s.Effort) {
		warns = append(warns, fmt.Sprintf("claude model %s: effort %s is not one of %s", s.Model, s.Effort, strings.Join(efforts, ", ")))
	}
	return unverified(warns)
}

func checkAgy(s Spec, env Env) string {
	l, err := catalog("agy", env)
	switch {
	case err != nil:
		return unverified([]string{fmt.Sprintf("agy model %s could not be checked: %s", s.Model, plainReason(err))})
	case slices.Contains(l.Gone, s.Model):
		return unverified([]string{goneText(s)})
	}
	if _, ok := find(l.Models, s.Model); !ok {
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

func CheckSupervisor(s Spec) error {
	if s.Harness == "copilot" {
		return fmt.Errorf("%w: copilot starts workers only; run the supervisor on claude, codex, opencode or agy", state.ErrInvalid)
	}
	return nil
}

var claudeAliases = []string{"opus", "sonnet", "haiku", "fable"}

func checkCodex(s Spec, env Env) string {
	l, err := catalog("codex", env)
	if err != nil {
		return unverified([]string{fmt.Sprintf("codex model %s could not be checked: %s", s.Model, plainReason(err))})
	}
	m, ok := find(l.Models, s.Model)
	switch {
	case slices.Contains(l.Gone, s.Model):
		return unverified([]string{goneText(s)})
	case !ok:
		return unverified([]string{fmt.Sprintf("codex model %s is not in codex's model list", s.Model)})
	case !slices.Contains(m.Efforts, s.Effort):
		return unverified([]string{fmt.Sprintf("codex model %s lists efforts %s, not %s", s.Model, strings.Join(m.Efforts, ", "), s.Effort)})
	}
	return ""
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
		return fmt.Errorf("%w: prompt is %d bytes; luvus accepts at most %d per argument; shorten it, or write the long text to a file and make the prompt say to read that file", state.ErrInvalid, len(prompt), MaxPromptBytes)
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
	argv, err := workerArgv(bin, s, prompt, dir)
	if err != nil {
		return nil, err
	}
	if err := checkCommandLine(runtime.GOOS, argv); err != nil {
		return nil, err
	}
	return argv, nil
}

func workerArgv(bin string, s Spec, prompt, dir string) ([]string, error) {
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
	case "copilot":
		argv := []string{bin, "--no-auto-update", "--model", s.Model}
		if s.Effort != "" {
			argv = append(argv, "--reasoning-effort", s.Effort)
		}
		return append(argv, "--allow-all-tools", "--allow-all-paths", "--session-id", NewSessionID(), "-i", prompt), nil
	case "codex":
		return []string{bin, "--dangerously-bypass-approvals-and-sandbox", "--disable", "hooks", "--cd", dir, "-m", s.Model, "-c", "model_reasoning_effort=" + s.Effort, prompt}, nil
	}
	return nil, fmt.Errorf("%w: harness %q has no launch command", state.ErrInvalid, s.Harness)
}
