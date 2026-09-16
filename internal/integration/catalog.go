package integration

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
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

	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/pathdisplay"
	"github.com/atqamz/hand/internal/state"
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
	// Test builds must use the caller's PATH so per-test fakes cannot be bypassed by a private
	// integration installed in the developer's SECONDHAND_HOME.
	if legacyCapabilityFallback {
		return runExecutable(ctx, capability.Executable, dir, args...)
	}
	store := DefaultStore()
	path, err := store.Resolve(id)
	if err != nil {
		return nil, nil, err
	}
	fleetID, err := payloadFleetID()
	if err != nil {
		return nil, nil, fmt.Errorf("identify Fleet for optional capability %q reference: %w", id, err)
	}
	referenceID, err := newPayloadReferenceID()
	if err != nil {
		return nil, nil, fmt.Errorf("create optional capability %q reference identity: %w", id, err)
	}
	role := os.Getenv("HAND_ROLE")
	if role == "" {
		role = "operator"
	}
	reference, err := acquireRunReference(store, id, path, referenceID, fleetID, role)
	if err != nil {
		return nil, nil, err
	}
	stdout, stderr, runErr := runReferencedExecutable(ctx, reference, path, dir, args...)
	return stdout, stderr, errors.Join(runErr, reference.Close())
}

func acquireRunReference(store *Store, id, path, referenceID, fleetID, role string) (*PayloadReference, error) {
	return store.AcquireReference(id, path, PayloadReferenceRequest{
		ReferenceID: referenceID,
		LockScope:   payloadReferenceLockScope(id),
		FleetID:     fleetID,
		Consumer:    "integration-process",
		Evidence:    "role=" + role + ";capability=" + id,
	})
}

func payloadReferenceLockScope(id string) string {
	return "integration-process:" + id
}

func payloadFleetID() (string, error) {
	fleetHome, err := home.Resolve()
	if errors.Is(err, home.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return state.FleetIDReadOnly(fleetHome)
}

func newPayloadReferenceID() (string, error) {
	var identity [16]byte
	if _, err := cryptorand.Read(identity[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(identity[:]), nil
}

func runExecutable(ctx context.Context, path, dir string, args ...string) ([]byte, []byte, error) {
	return runReferencedExecutable(ctx, nil, path, dir, args...)
}

func runReferencedExecutable(ctx context.Context, reference *PayloadReference, path, dir string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	var runErr error
	if reference == nil {
		runErr = cmd.Run()
	} else if runErr = reference.StartChild(cmd); runErr == nil {
		runErr = cmd.Wait()
	}
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
	rootHandle, err := openDirectIntegrationRoot(s.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &MissingError{ID: id, Command: "hand integration install " + id}
		}
		return "", fmt.Errorf("open optional capability store: %w", err)
	}
	defer func() { _ = rootHandle.Close() }()
	data, err := readIntegrationFile(rootHandle, s.Root, filepath.Join(s.Root, "integrations", id, "current.json"))
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
	input, info, err := openIntegrationFile(rootHandle, s.Root, path, os.O_RDONLY, 0)
	if err != nil {
		return "", fmt.Errorf("optional capability %q is incomplete: %w", id, err)
	}
	defer func() { _ = input.Close() }()
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
	hash := sha256.New()
	_, copyErr := io.Copy(hash, input)
	if copyErr != nil {
		return "", fmt.Errorf("hash optional capability %q payload: %w", id, copyErr)
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
		return "", fmt.Errorf("optional capability source %s is not an executable regular file", pathdisplay.Context(source))
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
	payloadRoot := filepath.Join(s.Root, "integrations", id, "payloads")
	if err := ensureIntegrationDirectory(s.Root, payloadRoot, 0o700); err != nil {
		return "", fmt.Errorf("create optional capability payload store: %w", err)
	}
	rootHandle, err := openDirectIntegrationRoot(s.Root)
	if err != nil {
		return "", fmt.Errorf("open optional capability store: %w", err)
	}
	defer func() { _ = rootHandle.Close() }()
	bundle := filepath.Join(payloadRoot, digest)
	destination := filepath.Join(bundle, capability.Executable)
	bundleRelative, err := integrationRelativePath(s.Root, bundle)
	if err != nil {
		return "", err
	}
	if _, err := rootHandle.Lstat(bundleRelative); err == nil {
		if err := verifyIntegrationPayload(rootHandle, s.Root, destination, digest); err != nil {
			return "", fmt.Errorf("existing exact optional capability payload is invalid and will not be rewritten: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect optional capability bundle: %w", err)
	} else {
		stage, err := mkdirTempIntegration(rootHandle, s.Root, payloadRoot, ".staging-")
		if err != nil {
			return "", fmt.Errorf("create optional capability staging directory: %w", err)
		}
		defer func() {
			if stage != "" {
				_ = removeAllIntegrationPath(rootHandle, s.Root, stage)
			}
		}()
		stagedDestination := filepath.Join(stage, capability.Executable)
		if _, err := input.Seek(0, io.SeekStart); err != nil {
			return "", fmt.Errorf("rewind optional capability source: %w", err)
		}
		output, _, err := openIntegrationFile(rootHandle, s.Root, stagedDestination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return "", fmt.Errorf("create staged optional capability payload: %w", err)
		}
		_, copyErr := io.Copy(output, input)
		syncErr := output.Sync()
		closeErr := output.Close()
		if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
			return "", fmt.Errorf("copy optional capability source: %w", err)
		}
		if err := verifyIntegrationPayload(rootHandle, s.Root, stagedDestination, digest); err != nil {
			return "", fmt.Errorf("verify staged optional capability payload: %w", err)
		}
		if err := renameIntegrationPath(rootHandle, s.Root, stage, bundle); err != nil {
			if validationErr := verifyIntegrationPayload(rootHandle, s.Root, destination, digest); validationErr != nil {
				return "", fmt.Errorf("publish optional capability payload: %w", err)
			}
		} else {
			stage = ""
			if err := verifyIntegrationPayload(rootHandle, s.Root, destination, digest); err != nil {
				return "", fmt.Errorf("verify published optional capability payload: %w", err)
			}
		}
	}
	selected := selection{Path: filepath.ToSlash(filepath.Join("payloads", digest, capability.Executable))}
	data, err := json.MarshalIndent(selected, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode optional capability selection: %w", err)
	}
	selectionPath := filepath.Join(s.Root, "integrations", id, "current.json")
	if err := atomicWriteIntegrationFile(rootHandle, s.Root, selectionPath, ".current-", append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("publish optional capability selection: %w", err)
	}
	return destination, nil
}

func (s *Store) Remove(id string) error {
	if _, ok := find(id); !ok {
		return fmt.Errorf("unsupported optional capability %q", id)
	}
	rootHandle, err := openDirectIntegrationRoot(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open optional capability store: %w", err)
	}
	defer func() { _ = rootHandle.Close() }()
	if err := removeIntegrationFile(rootHandle, s.Root, filepath.Join(s.Root, "integrations", id, "current.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
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

func verifyIntegrationPayload(rootHandle *os.Root, root, path, digest string) error {
	input, info, err := openIntegrationFile(rootHandle, root, path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		return errors.New("optional capability payload is not an executable regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != digest {
		return fmt.Errorf("optional capability payload digest mismatch: got %s, want %s", got, digest)
	}
	return nil
}

func ensureIntegrationDirectory(root, path string, perm os.FileMode) error {
	relative, err := integrationRelativePath(root, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, perm); err != nil {
		return err
	}
	rootHandle, err := openDirectIntegrationRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = rootHandle.Close() }()
	parts := []string(nil)
	if relative != "." {
		parts = strings.Split(relative, string(filepath.Separator))
	}
	current := rootHandle
	defer func() {
		if current != rootHandle {
			_ = current.Close()
		}
	}()
	for _, part := range parts {
		info, inspectErr := current.Lstat(part)
		if errors.Is(inspectErr, os.ErrNotExist) {
			if mkdirErr := current.Mkdir(part, perm); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				return mkdirErr
			}
			info, inspectErr = current.Lstat(part)
		}
		if inspectErr != nil {
			return inspectErr
		}
		if integrationPathIsIndirect(info) || !info.IsDir() {
			return fmt.Errorf("integration directory %s is not a direct directory", filepath.Join(root, relative))
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			return err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = next.Close()
			if err != nil {
				return err
			}
			return fmt.Errorf("integration directory %s changed while opening it", filepath.Join(root, relative))
		}
		if current != rootHandle {
			_ = current.Close()
		}
		current = next
	}
	return nil
}

func integrationRelativePath(root, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("integration path %q escapes store %q", path, root)
	}
	return relative, nil
}

func openDirectIntegrationRoot(root string) (*os.Root, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if integrationPathIsIndirect(info) || !info.IsDir() {
		return nil, fmt.Errorf("integration store %s is not a direct directory", root)
	}
	handle, err := os.OpenRoot(resolved)
	if err != nil {
		return nil, err
	}
	opened, err := handle.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = handle.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("integration store %s changed while opening it", root)
	}
	return handle, nil
}

func openDirectIntegrationSubroot(rootHandle *os.Root, relative string) (*os.Root, bool, error) {
	if relative == "." || relative == "" {
		return rootHandle, false, nil
	}
	current := rootHandle
	owned := false
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		info, err := current.Lstat(part)
		if err != nil {
			if owned {
				_ = current.Close()
			}
			return nil, false, err
		}
		if integrationPathIsIndirect(info) || !info.IsDir() {
			if owned {
				_ = current.Close()
			}
			return nil, false, fmt.Errorf("integration path component %s is not a direct directory", part)
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			if owned {
				_ = current.Close()
			}
			return nil, false, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = next.Close()
			if owned {
				_ = current.Close()
			}
			if err != nil {
				return nil, false, err
			}
			return nil, false, fmt.Errorf("integration path component %s changed while opening it", part)
		}
		if owned {
			_ = current.Close()
		}
		current = next
		owned = true
	}
	return current, owned, nil
}

func openIntegrationFile(rootHandle *os.Root, root, path string, flag int, perm os.FileMode) (*os.File, os.FileInfo, error) {
	relative, err := integrationRelativePath(root, path)
	if err != nil {
		return nil, nil, err
	}
	if relative == "." {
		return nil, nil, fmt.Errorf("integration file path names the store root")
	}
	parent, owned, err := openDirectIntegrationSubroot(rootHandle, filepath.Dir(relative))
	if err != nil {
		return nil, nil, err
	}
	if owned {
		defer func() { _ = parent.Close() }()
	}
	leaf := filepath.Base(relative)
	expected, inspectErr := parent.Lstat(leaf)
	if inspectErr == nil && integrationPathIsIndirect(expected) {
		return nil, nil, fmt.Errorf("integration path component %s is indirect", path)
	}
	if inspectErr != nil && !errors.Is(inspectErr, os.ErrNotExist) {
		return nil, nil, inspectErr
	}
	file, err := parent.OpenFile(leaf, flag, perm)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil || integrationPathIsIndirect(opened) || expected != nil && !os.SameFile(expected, opened) {
		_ = file.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("integration path component %s changed while opening it", path)
	}
	return file, opened, nil
}

func readIntegrationFile(rootHandle *os.Root, root, path string) ([]byte, error) {
	file, _, err := openIntegrationFile(rootHandle, root, path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

func atomicWriteIntegrationFile(rootHandle *os.Root, root, path, prefix string, data []byte, perm os.FileMode) error {
	return atomicPublishIntegrationFile(rootHandle, root, path, prefix, data, perm, true)
}

func atomicCreateIntegrationFile(rootHandle *os.Root, root, path, prefix string, data []byte, perm os.FileMode) error {
	return atomicPublishIntegrationFile(rootHandle, root, path, prefix, data, perm, false)
}

func atomicPublishIntegrationFile(rootHandle *os.Root, root, path, prefix string, data []byte, perm os.FileMode, replace bool) error {
	relative, err := integrationRelativePath(root, path)
	if err != nil {
		return err
	}
	if relative == "." {
		return fmt.Errorf("integration file path names the store root")
	}
	parent, owned, err := openDirectIntegrationSubroot(rootHandle, filepath.Dir(relative))
	if err != nil {
		return err
	}
	if owned {
		defer func() { _ = parent.Close() }()
	}
	var nonce [16]byte
	if _, err := cryptorand.Read(nonce[:]); err != nil {
		return err
	}
	temporary := prefix + hex.EncodeToString(nonce[:])
	file, err := parent.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Remove(temporary) }()
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if replace {
		return renameIntegrationRoot(parent, temporary, filepath.Base(relative))
	}
	return parent.Link(temporary, filepath.Base(relative))
}

func removeIntegrationFile(rootHandle *os.Root, root, path string) error {
	relative, err := integrationRelativePath(root, path)
	if err != nil {
		return err
	}
	if relative == "." {
		return errors.New("refusing to remove integration store root")
	}
	parent, owned, err := openDirectIntegrationSubroot(rootHandle, filepath.Dir(relative))
	if err != nil {
		return err
	}
	if owned {
		defer func() { _ = parent.Close() }()
	}
	return parent.Remove(filepath.Base(relative))
}

func mkdirTempIntegration(rootHandle *os.Root, root, parentPath, prefix string) (string, error) {
	relative, err := integrationRelativePath(root, parentPath)
	if err != nil {
		return "", err
	}
	parent, owned, err := openDirectIntegrationSubroot(rootHandle, relative)
	if err != nil {
		return "", err
	}
	if owned {
		defer func() { _ = parent.Close() }()
	}
	for range 100 {
		var nonce [16]byte
		if _, err := cryptorand.Read(nonce[:]); err != nil {
			return "", err
		}
		name := prefix + hex.EncodeToString(nonce[:])
		if err := parent.Mkdir(name, 0o700); err == nil {
			return filepath.Join(parentPath, name), nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("allocate unique integration staging directory")
}

func removeAllIntegrationPath(rootHandle *os.Root, root, path string) error {
	relative, err := integrationRelativePath(root, path)
	if err != nil {
		return err
	}
	if relative == "." {
		return errors.New("refusing to remove integration store root")
	}
	return rootHandle.RemoveAll(relative)
}

func renameIntegrationPath(rootHandle *os.Root, root, oldPath, newPath string) error {
	oldRelative, err := integrationRelativePath(root, oldPath)
	if err != nil {
		return err
	}
	if oldRelative == "." {
		return errors.New("refusing to rename integration store root")
	}
	newRelative, err := integrationRelativePath(root, newPath)
	if err != nil {
		return err
	}
	if newRelative == "." {
		return errors.New("refusing to replace integration store root")
	}
	return renameIntegrationRoot(rootHandle, oldRelative, newRelative)
}
