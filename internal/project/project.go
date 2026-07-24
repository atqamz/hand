// Package project manages the registry of git projects tracked in data/projects.md.
package project

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const (
	ModeNoMistakes = "no-mistakes"
	ModeDirectPR   = "direct-pr"
	ModeLocalOnly  = "local-only"
)

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
	defer f.Close()

	var projects []Project
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		p, ok := parseLine(line)
		if ok {
			projects = append(projects, p)
		}
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
	_, exists, err := Find(homeDir, p.Name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("project %q already registered", p.Name)
	}

	path := RegistryPath(homeDir)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("write project registry: %w", err)
	}
	defer f.Close()

	line := fmt.Sprintf("- %s: %s mode=%s\n", p.Name, p.URL, p.Mode)
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("write project registry: %w", err)
	}
	return nil
}

// Remove deletes the project line matching name from the registry.
func Remove(homeDir, name string) error {
	projects, err := List(homeDir)
	if err != nil {
		return err
	}

	found := false
	var kept []Project
	for _, p := range projects {
		if p.Name == name {
			found = true
			continue
		}
		kept = append(kept, p)
	}
	if !found {
		return fmt.Errorf("project %q not found", name)
	}

	var sb strings.Builder
	sb.WriteString("# Projects\n\n")
	for _, p := range kept {
		sb.WriteString(fmt.Sprintf("- %s: %s mode=%s\n", p.Name, p.URL, p.Mode))
	}

	path := RegistryPath(homeDir)
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write project registry: %w", err)
	}
	return nil
}
