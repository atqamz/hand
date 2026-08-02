// Package project manages the registry of git projects: a table in hand's
// sqlite database, with data/projects.md kept in step as the human-readable
// projection. SPECS.md's "Which one to believe" covers a disagreement.
package project

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/atqamz/secondhand/internal/atomicfile"
	"github.com/atqamz/secondhand/internal/store"
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

type Project = store.Project

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

func List(homeDir string) ([]Project, error) {
	db, err := openRegistry(homeDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return db.ListProjects()
}

// data/projects.md survives its own import as the projection, so its absence
// cannot serve as the done marker the way a task's JSON file does.
const legacyRegistryKey = "projects.md"

func openRegistry(homeDir string) (*store.DB, error) {
	db, err := store.Open(homeDir)
	if err != nil {
		return nil, err
	}
	if err := importLegacyRegistry(db, homeDir); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func importLegacyRegistry(db *store.DB, homeDir string) error {
	done, err := db.Migrated(legacyRegistryKey)
	if err != nil || done {
		return err
	}
	projects, err := parseRegistryFile(homeDir)
	if err != nil {
		return err
	}
	for _, p := range projects {
		// A name listed twice was already unreachable past the first match, so
		// dropping the later one imports exactly what the file resolved to.
		if err := db.AddProject(p); err != nil && !errors.Is(err, store.ErrProjectExists) {
			return err
		}
	}
	return db.MarkMigrated(legacyRegistryKey)
}

// Reading the file without going through the database is what keeps importing
// an existing fleet independent of the binary that wrote it.
func parseRegistryFile(homeDir string) ([]Project, error) {
	f, err := os.Open(RegistryPath(homeDir))
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

// GateState is the result of asking the no-mistakes binary whether a repo's gate is initialized.
type GateState int

const (
	GateReady GateState = iota
	GateNotInitialized
)

const gateNotInitializedMarker = "repo not initialized"

// GateInitCommand is the exact remedy for GateNotInitialized. no-mistakes init is idempotent and
// repairs a stale working_path in place, so callers should print this verbatim rather than describe it.
func GateInitCommand(clonePath string) string {
	return fmt.Sprintf("cd %s && no-mistakes init", clonePath)
}

// GateStatus asks the no-mistakes binary whether clonePath's gate is initialized, rather than
// reading ~/.no-mistakes/state.sqlite directly, which is another tool's private schema.
// no-mistakes status exits 0 whether or not the repo is initialized, so the outcome is read from
// its output text, not its exit code. Any failure to run the binary at all (missing, unexecutable,
// unexpected nonzero exit) is returned as an error distinct from GateNotInitialized: the remedy for
// a missing binary is not `no-mistakes init`.
func GateStatus(clonePath string) (GateState, error) {
	cmd := exec.Command("no-mistakes", "status")
	cmd.Dir = clonePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return GateReady, fmt.Errorf("no-mistakes binary not found or not runnable: %w", err)
	}
	if strings.Contains(string(out), gateNotInitializedMarker) {
		return GateNotInitialized, nil
	}
	return GateReady, nil
}

func Add(homeDir string, p Project) error {
	if !validMode(p.Mode) {
		return fmt.Errorf("invalid project mode %q", p.Mode)
	}
	db, err := openRegistry(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if err := db.AddProject(p); err != nil {
		return err
	}
	return writeProjection(db, homeDir)
}

func validMode(mode string) bool {
	return mode == ModeNoMistakes || mode == ModeDirectPR || mode == ModeLocalOnly
}

func Remove(homeDir, name string) error {
	db, err := openRegistry(homeDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	removed, err := db.RemoveProject(name)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("project %q %w", name, ErrNotFound)
	}
	return writeProjection(db, homeDir)
}

// Each project line is rewritten in place rather than regrouped: the live file
// interleaves hand-written `# profile=` comments with the entries they
// describe, and moving entries would rebind a comment to the wrong repo.
func writeProjection(db *store.DB, homeDir string) error {
	projects, err := db.ListProjects()
	if err != nil {
		return err
	}
	registered := make(map[string]Project, len(projects))
	for _, p := range projects {
		registered[p.Name] = p
	}

	existing, err := os.ReadFile(RegistryPath(homeDir))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read project registry: %w", err)
	}

	var rendered []string
	placed := make(map[string]bool, len(projects))
	for _, line := range strings.Split(strings.TrimSuffix(string(existing), "\n"), "\n") {
		p, isProject := parseLine(strings.TrimSpace(line))
		if !isProject {
			rendered = append(rendered, line)
			continue
		}
		current, ok := registered[p.Name]
		if !ok || placed[p.Name] {
			continue
		}
		placed[p.Name] = true
		rendered = append(rendered, renderProjectLine(current))
	}

	rendered = trimTrailingBlanks(rendered)
	for _, p := range projects {
		if !placed[p.Name] {
			rendered = append(rendered, renderProjectLine(p))
		}
	}

	content := strings.Join(rendered, "\n")
	if err := atomicfile.Write(RegistryPath(homeDir), ".projects.md-", []byte(content+"\n"), 0o644); err != nil {
		return fmt.Errorf("write project registry: %w", err)
	}
	return nil
}

func renderProjectLine(p Project) string {
	return fmt.Sprintf("- %s: %s mode=%s", p.Name, p.URL, p.Mode)
}

func trimTrailingBlanks(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
