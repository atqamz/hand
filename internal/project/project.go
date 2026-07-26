// Package project manages the registry of git projects tracked in data/projects.md.
package project

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/atqamz/secondhand/internal/atomicfile"
)

const (
	ModeNoMistakes = "no-mistakes"
	ModeDirectPR   = "direct-pr"
	ModeLocalOnly  = "local-only"
)

// ErrNotFound is wrapped into the error Remove returns when name isn't
// registered, rendering as `project "<name>" not registered` to match the
// wording the cmd layer uses for the same condition.
var ErrNotFound = errors.New("not registered")

type Project struct {
	Name string
	URL  string
	Mode string
}

func RegistryPath(homeDir string) string {
	return homeDir + "/data/projects.md"
}

// DeriveName extracts a project name from a git URL: the last path segment minus ".git".
func DeriveName(url string) string {
	name := url
	if idx := strings.LastIndexAny(name, "/:"); idx != -1 {
		name = name[idx+1:]
	}
	name = strings.TrimSuffix(name, ".git")
	return name
}

// List reads and parses the registry file. Returns an empty slice if the file doesn't exist.
func List(homeDir string) ([]Project, error) {
	path := RegistryPath(homeDir)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read project registry: %w", err)
	}
	defer func() { _ = f.Close() }()

	var projects []Project
	scanner := bufio.NewScanner(f)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, ok := parseLine(line)
		if !ok {
			return nil, fmt.Errorf("invalid project registry line %d", lineNumber)
		}
		projects = append(projects, p)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read project registry: %w", err)
	}
	return projects, nil
}

func parseLine(line string) (Project, bool) {
	if !strings.HasPrefix(line, "- ") {
		return Project{}, false
	}
	line = strings.TrimPrefix(line, "- ")

	nameRest := strings.SplitN(line, ":", 2)
	if len(nameRest) != 2 {
		return Project{}, false
	}
	name := strings.TrimSpace(nameRest[0])
	rest := strings.TrimSpace(nameRest[1])

	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return Project{}, false
	}
	url := fields[0]
	mode := ""
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "mode=") {
			mode = strings.TrimPrefix(f, "mode=")
		}
	}
	if name == "" || url == "" || mode == "" {
		return Project{}, false
	}
	if !validMode(mode) {
		return Project{}, false
	}
	return Project{Name: name, URL: url, Mode: mode}, true
}

// Find returns the project with the given name, or false if not registered.
func Find(homeDir, name string) (Project, bool, error) {
	projects, err := List(homeDir)
	if err != nil {
		return Project{}, false, err
	}
	for _, p := range projects {
		if p.Name == name {
			return p, true, nil
		}
	}
	return Project{}, false, nil
}

// Add appends a project line to the registry. Returns an error if the name is already registered.
func Add(homeDir string, p Project) error {
	if !validMode(p.Mode) {
		return fmt.Errorf("invalid project mode %q", p.Mode)
	}

	unlock, err := lockRegistry(homeDir)
	if err != nil {
		return err
	}
	defer unlock()

	_, exists, err := Find(homeDir, p.Name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("project %q already registered", p.Name)
	}

	path := RegistryPath(homeDir)
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read project registry: %w", err)
	}

	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	line := fmt.Sprintf("- %s: %s mode=%s\n", p.Name, p.URL, p.Mode)
	if err := atomicfile.Write(path, ".projects.md-", append(content, line...), 0o644); err != nil {
		return fmt.Errorf("write project registry: %w", err)
	}
	return nil
}

func validMode(mode string) bool {
	return mode == ModeNoMistakes || mode == ModeDirectPR || mode == ModeLocalOnly
}

// Remove deletes the project line matching name from the registry.
func Remove(homeDir, name string) error {
	unlock, err := lockRegistry(homeDir)
	if err != nil {
		return err
	}
	defer unlock()

	path := RegistryPath(homeDir)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return fmt.Errorf("project %q %w", name, ErrNotFound)
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	found := false
	var kept []string
	scanner := bufio.NewScanner(f)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			kept = append(kept, line)
			continue
		}
		p, ok := parseLine(trimmed)
		if !ok {
			return fmt.Errorf("invalid project registry line %d", lineNumber)
		}
		if p.Name == name {
			found = true
			continue
		}
		kept = append(kept, line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read project registry: %w", err)
	}
	if !found {
		return fmt.Errorf("project %q %w", name, ErrNotFound)
	}

	content := strings.Join(kept, "\n") + "\n"
	if err := atomicfile.Write(path, ".projects.md-", []byte(content), 0o644); err != nil {
		return fmt.Errorf("write project registry: %w", err)
	}
	return nil
}

func lockRegistry(homeDir string) (func(), error) {
	lock, err := os.OpenFile(RegistryPath(homeDir)+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock project registry: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock project registry: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}, nil
}
