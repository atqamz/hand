package toolchain

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

//go:embed runtime.lock.json
var lockBytes []byte

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

var ErrRuntimeNotReady = errors.New("private Secondhand runtime is not ready")

type Lock struct {
	Schema      int               `json:"schema"`
	RuntimeID   string            `json:"runtime_id"`
	GeneratedBy string            `json:"generated_by"`
	Targets     map[string]Target `json:"targets"`
}

type Target struct {
	Unsupported string               `json:"unsupported,omitempty"`
	Components  map[string]Component `json:"components"`
}

type Component struct {
	Name     string         `json:"name"`
	Version  string         `json:"version"`
	Revision string         `json:"revision"`
	URL      string         `json:"url"`
	SHA256   string         `json:"sha256"`
	Format   string         `json:"format"`
	Root     string         `json:"root,omitempty"`
	Files    []ExpectedFile `json:"files"`
}

type ExpectedFile struct {
	Path       string `json:"path"`
	Executable bool   `json:"executable"`
	Regular    bool   `json:"regular"`
	SHA256     string `json:"sha256,omitempty"`
}

func LoadLock() (Lock, error) {
	var lock Lock
	if err := json.Unmarshal(lockBytes, &lock); err != nil {
		return Lock{}, fmt.Errorf("decode embedded runtime lock: %w", err)
	}
	if err := lock.Validate(); err != nil {
		return Lock{}, fmt.Errorf("validate embedded runtime lock: %w", err)
	}
	return lock, nil
}

func (l Lock) Validate() error {
	if l.Schema != 1 {
		return fmt.Errorf("runtime lock schema %d is unsupported", l.Schema)
	}
	if l.GeneratedBy == "" {
		return errors.New("runtime lock has no generator identity")
	}
	if !strings.HasPrefix(l.RuntimeID, "r") || len(l.RuntimeID) != 17 {
		return fmt.Errorf("runtime lock ID %q is not a 16-hex runtime ID", l.RuntimeID)
	}
	expectedID, err := l.DeterministicID()
	if err != nil {
		return err
	}
	if l.RuntimeID != expectedID {
		return fmt.Errorf("runtime lock ID %q does not match deterministic ID %q", l.RuntimeID, expectedID)
	}
	if len(l.Targets) == 0 {
		return errors.New("runtime lock has no targets")
	}
	for targetName, target := range l.Targets {
		if _, _, err := splitTarget(targetName); err != nil {
			return err
		}
		if target.Unsupported != "" {
			if len(target.Components) != 0 {
				return fmt.Errorf("unsupported target %s also defines components", targetName)
			}
			continue
		}
		if len(target.Components) != 3 {
			return fmt.Errorf("target %s must define exactly git, treehouse, and herdr", targetName)
		}
		for _, name := range []string{"git", "treehouse", "herdr"} {
			component, ok := target.Components[name]
			if !ok {
				return fmt.Errorf("target %s is missing %s", targetName, name)
			}
			if component.Name != name {
				return fmt.Errorf("target %s component key %s has name %q", targetName, name, component.Name)
			}
			if component.Version == "" || component.Revision == "" || component.URL == "" || component.Format == "" || component.Root == "" {
				return fmt.Errorf("target %s component %s has incomplete immutable metadata", targetName, name)
			}
			if !strings.HasPrefix(component.URL, "https://") {
				return fmt.Errorf("target %s component %s URL is not HTTPS", targetName, name)
			}
			if !sha256Pattern.MatchString(component.SHA256) {
				return fmt.Errorf("target %s component %s has invalid SHA-256", targetName, name)
			}
			if len(component.Files) == 0 {
				return fmt.Errorf("target %s component %s has no expected files", targetName, name)
			}
			switch component.Format {
			case "binary":
				if len(component.Files) != 1 {
					return fmt.Errorf("target %s component %s binary format requires exactly one expected file", targetName, name)
				}
			case "tar.gz", "zip":
			default:
				return fmt.Errorf("target %s component %s has unsupported format %q", targetName, name, component.Format)
			}
			if err := validateRelativePath(component.Root, "component root"); err != nil {
				return fmt.Errorf("target %s component %s: %w", targetName, name, err)
			}
			if err := validateExpectedFiles(component.Files); err != nil {
				return fmt.Errorf("target %s component %s: %w", targetName, name, err)
			}
		}
	}
	return nil
}

func validateExpectedFiles(files []ExpectedFile) error {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if err := validateRelativePath(file.Path, "expected file path"); err != nil {
			return fmt.Errorf("%w escapes component", err)
		}
		clean := filepath.Clean(filepath.FromSlash(file.Path))
		if clean == "." {
			return fmt.Errorf("expected file path %q is invalid", file.Path)
		}
		if _, ok := seen[clean]; ok {
			return fmt.Errorf("expected file %q is duplicated", file.Path)
		}
		seen[clean] = struct{}{}
		if !file.Regular && !file.Executable {
			return fmt.Errorf("expected file %q must declare regular or executable", file.Path)
		}
	}
	return nil
}

func validateRelativePath(path, label string) error {
	if path == "" || filepath.IsAbs(filepath.FromSlash(path)) || strings.ContainsRune(path, 0) {
		return fmt.Errorf("%s %q is invalid", label, path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q escapes component", label, path)
	}
	return nil
}

func (l Lock) DeterministicID() (string, error) {
	copyLock := l
	copyLock.RuntimeID = ""
	data, err := json.Marshal(copyLock)
	if err != nil {
		return "", fmt.Errorf("canonicalize runtime lock: %w", err)
	}
	digest := sha256.Sum256(data)
	return "r" + hex.EncodeToString(digest[:])[:16], nil
}

func (l Lock) Target(goos, goarch string) (Target, error) {
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	key := goos + "/" + goarch
	target, ok := l.Targets[key]
	if !ok {
		return Target{}, fmt.Errorf("private Secondhand runtime does not support %s; run `hand runtime ensure` after installing a supported Hand build", key)
	}
	if target.Unsupported != "" {
		return Target{}, fmt.Errorf("private Secondhand runtime does not support %s: %s", key, target.Unsupported)
	}
	return target, nil
}

func splitTarget(target string) (string, string, error) {
	parts := strings.Split(target, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("runtime lock target %q is not GOOS/GOARCH", target)
	}
	return parts[0], parts[1], nil
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func secondhandHome() (string, error) {
	if configured := os.Getenv("SECONDHAND_HOME"); configured != "" {
		path, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("resolve SECONDHAND_HOME: %w", err)
		}
		return filepath.Clean(path), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for Secondhand runtime: %w", err)
	}
	return filepath.Join(home, ".secondhand"), nil
}
