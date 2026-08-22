package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/atqamz/hand/internal/atomicfile"
)

type Capability struct {
	ID         string
	Executable string
	Owner      string
	External   bool
}

type State string

const (
	StateMissing   State = "missing"
	StateInstalled State = "installed"
)

type Status struct {
	Capability Capability
	State      State
	Path       string
}

type selection struct {
	Path string `json:"path"`
}

type MissingError struct {
	ID      string
	Command string
}

func (e *MissingError) Error() string {
	return fmt.Sprintf("optional capability %q is not installed; run `%s`", e.ID, e.Command)
}

func Catalog() []Capability {
	return []Capability{
		{ID: "github/gh", Executable: executable("gh"), Owner: "GitHub", External: true},
		{ID: "gitlab/glab", Executable: executable("glab"), Owner: "GitLab", External: true},
		{ID: "delivery/no-mistakes", Executable: executable("no-mistakes"), Owner: "no-mistakes", External: true},
		{ID: "delivery/witness", Executable: executable("witness"), Owner: "Witness", External: true},
	}
}

type Store struct {
	Root string
}

func Run(ctx context.Context, id, dir string, args ...string) ([]byte, []byte, error) {
	capability, ok := find(id)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported optional capability %q", id)
	}
	path, err := DefaultStore().Resolve(id)
	if err != nil {
		if legacyCapabilityFallback {
			cmd := exec.CommandContext(ctx, capability.Executable, args...)
			cmd.Dir = dir
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			runErr := cmd.Run()
			return stdout.Bytes(), stderr.Bytes(), runErr
		}
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), runErr
}

func NewStore(root string) *Store {
	if root == "" {
		root = os.Getenv("SECONDHAND_HOME")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			root = filepath.Join(home, ".secondhand")
		}
	}
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	return &Store{Root: root}
}

func DefaultStore() *Store { return NewStore("") }

func (s *Store) List() ([]Status, error) {
	capabilities := Catalog()
	result := make([]Status, 0, len(capabilities))
	for _, capability := range capabilities {
		path, err := s.Resolve(capability.ID)
		if err != nil {
			var missing *MissingError
			if errors.As(err, &missing) {
				result = append(result, Status{Capability: capability, State: StateMissing})
				continue
			}
			return nil, err
		}
		result = append(result, Status{Capability: capability, State: StateInstalled, Path: path})
	}
	return result, nil
}

func (s *Store) Resolve(id string) (string, error) {
	_, ok := find(id)
	if !ok {
		return "", fmt.Errorf("unsupported optional capability %q", id)
	}
	data, err := os.ReadFile(filepath.Join(s.Root, "integrations", id, "current.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &MissingError{ID: id, Command: "hand integration install " + id}
		}
		return "", fmt.Errorf("read optional capability %q selection: %w", id, err)
	}
	var selected selection
	if err := json.Unmarshal(data, &selected); err != nil {
		return "", fmt.Errorf("decode optional capability %q selection: %w", id, err)
	}
	path := selected.Path
	if !filepath.IsAbs(filepath.FromSlash(path)) {
		path = filepath.Join(s.Root, "integrations", id, filepath.FromSlash(path))
	} else {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve optional capability %q path: %w", id, err)
		}
	}
	root, err := filepath.Abs(filepath.Join(s.Root, "integrations", id))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("optional capability %q selection escapes its private store", id)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("optional capability %q is incomplete: %w", id, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("optional capability %q path is not a regular file", id)
	}
	if info.Mode()&0111 == 0 && runtime.GOOS != "windows" {
		return "", fmt.Errorf("optional capability %q path is not executable", id)
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 3 || parts[0] != "payloads" || len(parts[1]) != sha256.Size*2 {
		return "", fmt.Errorf("optional capability %q selection has no integrity-bound payload", id)
	}
	want, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("optional capability %q selection has invalid payload digest", id)
	}
	input, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open optional capability %q payload: %w", id, err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, input)
	closeErr := input.Close()
	if copyErr != nil {
		return "", fmt.Errorf("hash optional capability %q payload: %w", id, copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close optional capability %q payload: %w", id, closeErr)
	}
	if !bytes.Equal(hash.Sum(nil), want) {
		return "", fmt.Errorf("optional capability %q payload digest mismatch", id)
	}
	return path, nil
}

func (s *Store) Install(id, source string) (string, error) {
	capability, ok := find(id)
	if !ok {
		return "", fmt.Errorf("unsupported optional capability %q", id)
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve optional capability source: %w", err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", fmt.Errorf("inspect optional capability source: %w", err)
	}
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return "", fmt.Errorf("optional capability source %q is not an executable regular file", source)
	}

	input, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open optional capability source: %w", err)
	}
	defer func() { _ = input.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return "", fmt.Errorf("hash optional capability source: %w", err)
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	bundle := filepath.Join(s.Root, "integrations", id, "payloads", digest)
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		return "", fmt.Errorf("create optional capability bundle: %w", err)
	}
	destination := filepath.Join(bundle, capability.Executable)
	if _, err := os.Stat(destination); errors.Is(err, os.ErrNotExist) {
		if _, err := input.Seek(0, io.SeekStart); err != nil {
			return "", fmt.Errorf("rewind optional capability source: %w", err)
		}
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return "", fmt.Errorf("create optional capability bundle: %w", err)
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return "", fmt.Errorf("copy optional capability source: %w", copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close optional capability bundle: %w", closeErr)
		}
	} else if err != nil {
		return "", fmt.Errorf("inspect optional capability bundle: %w", err)
	}
	destinationDigest, err := digestFile(destination)
	if err != nil {
		return "", fmt.Errorf("hash optional capability bundle: %w", err)
	}
	if destinationDigest != digest {
		return "", fmt.Errorf("optional capability source changed while installing %q", id)
	}
	selected := selection{Path: filepath.ToSlash(filepath.Join("payloads", digest, capability.Executable))}
	data, err := json.MarshalIndent(selected, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode optional capability selection: %w", err)
	}
	selectionPath := filepath.Join(s.Root, "integrations", id, "current.json")
	if err := atomicfile.Write(selectionPath, ".current-", append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("publish optional capability selection: %w", err)
	}
	return destination, nil
}

func (s *Store) Remove(id string) error {
	if _, ok := find(id); !ok {
		return fmt.Errorf("unsupported optional capability %q", id)
	}
	if err := os.Remove(filepath.Join(s.Root, "integrations", id, "current.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove optional capability %q selection: %w", id, err)
	}
	return nil
}

func find(id string) (Capability, bool) {
	for _, capability := range Catalog() {
		if capability.ID == id {
			return capability, true
		}
	}
	return Capability{}, false
}

func executable(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func digestFile(path string) (string, error) {
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = input.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
