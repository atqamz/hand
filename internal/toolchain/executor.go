package toolchain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atqamz/hand/internal/pathdisplay"
)

type ProcessSpec struct {
	Path   string
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func NewProcessSpec(path string, args []string, env []string) (ProcessSpec, error) {
	if err := requireExecutable(path); err != nil {
		return ProcessSpec{}, err
	}
	return ProcessSpec{Path: path, Args: append([]string(nil), args...), Env: append([]string(nil), env...)}, nil
}

func (s ProcessSpec) Command(ctx context.Context) (*exec.Cmd, error) {
	if err := requireExecutable(s.Path); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, s.Path, s.Args...)
	cmd.Dir = s.Dir
	if s.Env != nil {
		cmd.Env = append([]string(nil), s.Env...)
	}
	cmd.Stdin = s.Stdin
	cmd.Stdout = s.Stdout
	cmd.Stderr = s.Stderr
	return cmd, nil
}

func (s ProcessSpec) Run(ctx context.Context) error {
	cmd, err := s.Command(ctx)
	if err != nil {
		return err
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run managed command %s: %w", filepath.Base(s.Path), err)
	}
	return nil
}

func (s ProcessSpec) Output(ctx context.Context) ([]byte, error) {
	cmd, err := s.Command(ctx)
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	if runErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return nil, fmt.Errorf("run managed command %s: %w: %s", filepath.Base(s.Path), runErr, message)
		}
		return nil, fmt.Errorf("run managed command %s: %w", filepath.Base(s.Path), runErr)
	}
	return out, nil
}

func RunLegacyForTests(ctx context.Context, name, dir string, args ...string) ([]byte, []byte, error) {
	if !legacyFallbackAllowed {
		return nil, nil, errors.New("legacy PATH execution is available only to test binaries")
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func ManagedEnvironment(parent []string, gitBin string) ([]string, error) {
	if gitBin == "" {
		return append([]string(nil), parent...), nil
	}
	if err := requireAbsolute(gitBin); err != nil {
		return nil, fmt.Errorf("managed Git directory: %w", err)
	}
	result := make([]string, 0, len(parent)+1)
	pathIndex := -1
	for _, item := range parent {
		key, value, hasValue := strings.Cut(item, "=")
		if hasValue && strings.EqualFold(key, "PATH") {
			if pathIndex == -1 {
				pathIndex = len(result)
				result = append(result, key+"="+gitBin+string(filepath.ListSeparator)+value)
			}
			continue
		}
		result = append(result, item)
	}
	if pathIndex == -1 {
		result = append(result, "PATH="+gitBin)
	}
	return result, nil
}

type Runtime struct {
	ID               string
	Target           string
	BundleDir        string
	GitPath          string
	GitVersion       string
	TreehousePath    string
	TreehouseVersion string
	HerdrPath        string
	HerdrVersion     string
	GitBin           string
	// GitTemplateDir is an empty directory hand ensures exists, since the bundle ships no
	// share/git-core/templates of its own (hand#464). Empty when hand could not create it -
	// callers treat that as "no template arg to add", not a readiness failure.
	GitTemplateDir string
}

func (r Runtime) Process(path string, args ...string) (ProcessSpec, error) {
	if !filepath.IsAbs(path) {
		return ProcessSpec{}, errors.New("managed core commands require an absolute executable path")
	}
	env, err := ManagedEnvironment(os.Environ(), r.GitBin)
	if err != nil {
		return ProcessSpec{}, err
	}
	return NewProcessSpec(path, args, env)
}

// GitArgsWithTemplate points a managed git invocation at an existing, empty template directory
// (hand#464), the one answer every dispatch site shares for what a managed git run carries. A
// blank templateDir means hand could not create one, so args pass through unchanged.
func GitArgsWithTemplate(templateDir string, args []string) []string {
	if templateDir == "" {
		return args
	}
	return append([]string{"-c", "init.templateDir=" + templateDir}, args...)
}

// SupportsGitTransport reports whether the runtime's Git carries the external remote helper a
// URL scheme needs, observed as git-remote-<scheme> next to the managed git binary rather than
// assumed from the runtime id or version (hand#440); ssh, git, and file need no helper.
func (r Runtime) SupportsGitTransport(scheme string) bool {
	if r.GitBin == "" {
		return false
	}
	path := filepath.Join(r.GitBin, executableName("git-remote-"+scheme))
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if info.Mode()&0o111 == 0 && !strings.HasSuffix(strings.ToLower(path), ".exe") {
		return false
	}
	return true
}

func requireExecutable(path string) error {
	if err := requireAbsolute(path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" && !strings.ContainsAny(filepath.Base(path), ".") {
		return fmt.Errorf("managed Windows executable %s must include its suffix", pathdisplay.Context(path))
	}
	return nil
}

func requireAbsolute(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("managed core executable %s must be an absolute path", pathdisplay.Context(path))
	}
	return nil
}
