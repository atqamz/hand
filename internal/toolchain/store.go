package toolchain

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/filelock"
	"github.com/atqamz/hand/internal/secondhand"
)

const (
	currentName  = "current.json"
	manifestName = "manifest.json"
	maxArtifact  = 2 << 30
)

type Store struct {
	Root        string
	Lock        Lock
	HTTPClient  *http.Client
	MaxArtifact int64
}

type Current struct {
	Schema         int       `json:"schema"`
	RuntimeID      string    `json:"runtime_id"`
	Target         string    `json:"target"`
	Bundle         string    `json:"bundle,omitempty"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	SelectedAt     time.Time `json:"selected_at"`
}

type Status struct {
	Ready            bool
	Target           string
	RuntimeID        string
	BundleDir        string
	GitPath          string
	GitVersion       string
	TreehousePath    string
	TreehouseVersion string
	HerdrPath        string
	HerdrVersion     string
	// GitHTTPSReady is observed independently of Ready: whether the installed Git carries a
	// git-remote-https helper, not whether the bundle as a whole is intact (hand#440).
	GitHTTPSReady bool
	Reason        string
}

func NewStore(root string, lock Lock) (*Store, error) {
	if root == "" {
		var err error
		root, err = secondhand.Home()
		if err != nil {
			return nil, err
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve runtime store: %w", err)
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	return &Store{Root: filepath.Clean(root), Lock: lock, HTTPClient: http.DefaultClient, MaxArtifact: maxArtifact}, nil
}

func DefaultStore() (*Store, error) {
	lock, err := LoadLock()
	if err != nil {
		return nil, err
	}
	root, err := secondhand.Home()
	if err != nil {
		return nil, err
	}
	return NewStore(root, lock)
}

func Resolve() (Runtime, error) {
	store, err := DefaultStore()
	if err != nil {
		return Runtime{}, err
	}
	return store.Selected("", "")
}

func (s *Store) Status(goos, goarch string) (Status, error) {
	targetName := currentTargetName(goos, goarch)
	target, err := s.Lock.Target(goos, goarch)
	if err != nil {
		return Status{Target: targetName, Reason: err.Error()}, nil
	}
	current, err := s.readCurrent()
	if errors.Is(err, os.ErrNotExist) {
		return Status{Target: targetName, RuntimeID: s.Lock.RuntimeID, Reason: "no selected runtime; run `hand runtime ensure`"}, nil
	}
	if err != nil {
		return Status{Target: targetName, RuntimeID: s.Lock.RuntimeID, Reason: fmt.Sprintf("read selected runtime: %v", err)}, nil
	}
	runtime, err := s.runtimeFromCurrent(current, targetName, target)
	if err != nil {
		return Status{Target: targetName, RuntimeID: current.RuntimeID, Reason: err.Error()}, nil
	}
	return Status{
		Ready:            true,
		Target:           targetName,
		RuntimeID:        runtime.ID,
		BundleDir:        runtime.BundleDir,
		GitPath:          runtime.GitPath,
		GitVersion:       runtime.GitVersion,
		TreehousePath:    runtime.TreehousePath,
		TreehouseVersion: runtime.TreehouseVersion,
		HerdrPath:        runtime.HerdrPath,
		HerdrVersion:     runtime.HerdrVersion,
		GitHTTPSReady:    runtime.SupportsGitTransport("https"),
	}, nil
}

func (s *Store) Selected(goos, goarch string) (Runtime, error) {
	target, err := s.Lock.Target(goos, goarch)
	if err != nil {
		return Runtime{}, err
	}
	targetName := goos + "/" + goarch
	if goos == "" || goarch == "" {
		targetName = currentTargetName(goos, goarch)
	}
	current, err := s.readCurrent()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Runtime{}, fmt.Errorf("%w: no selected runtime; run `hand runtime ensure`", ErrRuntimeNotReady)
		}
		return Runtime{}, fmt.Errorf("%w: read selected runtime: %v", ErrRuntimeNotReady, err)
	}
	return s.runtimeFromCurrent(current, targetName, target)
}

func (s *Store) Ensure(ctx context.Context, goos, goarch string) (Runtime, error) {
	if s.HTTPClient == nil {
		s.HTTPClient = http.DefaultClient
	}
	if s.MaxArtifact <= 0 {
		s.MaxArtifact = maxArtifact
	}
	if err := ensureRuntimeDirectory(s.Root, filepath.Join(s.Root, "runtime", "bundles"), 0o700); err != nil {
		return Runtime{}, fmt.Errorf("create runtime bundle store: %w", err)
	}
	if err := ensureRuntimeDirectory(s.Root, filepath.Join(s.Root, "runtime", "locks"), 0o700); err != nil {
		return Runtime{}, fmt.Errorf("create runtime lock store: %w", err)
	}
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return Runtime{}, fmt.Errorf("open runtime store: %w", err)
	}
	defer func() { _ = rootHandle.Close() }()
	lockFile, _, err := openRuntimeFile(rootHandle, s.Root, filepath.Join(s.Root, "runtime", "locks", "selection.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Runtime{}, fmt.Errorf("open runtime selection lock: %w", err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := filelock.Lock(lockFile, true); err != nil {
		return Runtime{}, fmt.Errorf("lock runtime selection: %w", err)
	}
	defer func() { _ = filelock.Unlock(lockFile) }()

	if err := s.Lock.Validate(); err != nil {
		return Runtime{}, fmt.Errorf("revalidate runtime lock: %w", err)
	}
	target, err := s.Lock.Target(goos, goarch)
	if err != nil {
		return Runtime{}, err
	}
	targetName := currentTargetName(goos, goarch)
	bundleName, err := generationBundleName(s.Lock.RuntimeID, targetName, target)
	if err != nil {
		return Runtime{}, err
	}
	if selected, current, err := s.generation(bundleName, targetName, target); err == nil {
		if err := s.selectGeneration(current); err != nil {
			return Runtime{}, err
		}
		return selected, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Runtime{}, fmt.Errorf("%w: exact runtime generation %s is invalid and will not be rewritten: %v", ErrRuntimeNotReady, bundleName, err)
	}

	stage, err := mkdirTempRuntime(rootHandle, s.Root, filepath.Join(s.Root, "runtime"), ".staging-")
	if err != nil {
		return Runtime{}, fmt.Errorf("create runtime staging directory: %w", err)
	}
	defer func() {
		if stage != "" {
			_ = removeAllRuntimePath(rootHandle, s.Root, stage)
		}
	}()
	for name, component := range target.Components {
		if err := s.installComponent(ctx, rootHandle, stage, name, component); err != nil {
			return Runtime{}, fmt.Errorf("install %s: %w", name, err)
		}
	}
	installedTarget, err := targetWithFileDigestsRooted(rootHandle, s.Root, stage, target)
	if err != nil {
		return Runtime{}, fmt.Errorf("digest installed runtime files: %w", err)
	}
	manifestDigest, err := targetDigest(installedTarget)
	if err != nil {
		return Runtime{}, err
	}
	manifestData, err := json.MarshalIndent(installedTarget, "", "  ")
	if err != nil {
		return Runtime{}, fmt.Errorf("encode installed runtime manifest: %w", err)
	}
	if err := atomicWriteRuntimeFile(rootHandle, s.Root, filepath.Join(stage, manifestName), ".manifest-", append(manifestData, '\n'), 0o600); err != nil {
		return Runtime{}, fmt.Errorf("publish staged runtime manifest: %w", err)
	}
	current := Current{Schema: s.Lock.Schema, RuntimeID: s.Lock.RuntimeID, Target: targetName, Bundle: filepath.ToSlash(bundleName), ManifestSHA256: manifestDigest, SelectedAt: time.Now().UTC()}
	if _, err := s.runtimeFromBundle(current, stage, targetName, target); err != nil {
		return Runtime{}, fmt.Errorf("verify staged runtime generation: %w", err)
	}

	bundle := filepath.Join(s.Root, "runtime", bundleName)
	bundleRelative, err := runtimeRelativePath(s.Root, bundle)
	if err != nil {
		return Runtime{}, err
	}
	if _, err := rootHandle.Lstat(bundleRelative); err == nil {
		winner, winnerCurrent, validationErr := s.generation(bundleName, targetName, target)
		if validationErr != nil {
			return Runtime{}, fmt.Errorf("%w: exact runtime generation %s already exists but is invalid and will not be rewritten: %v", ErrRuntimeNotReady, bundleName, validationErr)
		}
		if err := s.selectGeneration(winnerCurrent); err != nil {
			return Runtime{}, err
		}
		return winner, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Runtime{}, fmt.Errorf("inspect runtime bundle: %w", err)
	}
	if err := renameRuntimePath(rootHandle, s.Root, stage, bundle); err != nil {
		if winner, winnerCurrent, validationErr := s.generation(bundleName, targetName, target); validationErr == nil {
			if err := s.selectGeneration(winnerCurrent); err != nil {
				return Runtime{}, err
			}
			return winner, nil
		}
		return Runtime{}, fmt.Errorf("publish runtime bundle: %w", err)
	}
	stage = ""
	validated, current, err := s.generation(bundleName, targetName, target)
	if err != nil {
		return Runtime{}, fmt.Errorf("verify published runtime generation: %w", err)
	}
	if err := s.selectGeneration(current); err != nil {
		return Runtime{}, err
	}
	return validated, nil
}

func generationBundleName(runtimeID, targetName string, target Target) (string, error) {
	digest, err := targetDigest(target)
	if err != nil {
		return "", err
	}
	generation := sha256.Sum256([]byte(runtimeID + "\x00" + targetName + "\x00" + digest))
	return filepath.Join("bundles", runtimeID+"-"+hex.EncodeToString(generation[:])), nil
}

func (s *Store) generation(bundleName, targetName string, target Target) (Runtime, Current, error) {
	bundle, err := safeJoin(filepath.Join(s.Root, "runtime"), bundleName)
	if err != nil {
		return Runtime{}, Current{}, err
	}
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return Runtime{}, Current{}, err
	}
	defer func() { _ = rootHandle.Close() }()
	manifestData, err := readRuntimeFile(rootHandle, s.Root, filepath.Join(bundle, manifestName))
	if err != nil {
		return Runtime{}, Current{}, err
	}
	var installed Target
	if err := decodeTargetManifest(manifestData, &installed); err != nil {
		return Runtime{}, Current{}, fmt.Errorf("decode runtime generation manifest: %w", err)
	}
	manifestDigest, err := targetDigest(installed)
	if err != nil {
		return Runtime{}, Current{}, err
	}
	current := Current{
		Schema: s.Lock.Schema, RuntimeID: s.Lock.RuntimeID, Target: targetName,
		Bundle: filepath.ToSlash(bundleName), ManifestSHA256: manifestDigest, SelectedAt: time.Now().UTC(),
	}
	runtime, err := s.runtimeFromBundle(current, bundle, targetName, target)
	return runtime, current, err
}

func (s *Store) selectGeneration(current Current) error {
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return fmt.Errorf("encode selected runtime: %w", err)
	}
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return fmt.Errorf("open runtime store: %w", err)
	}
	defer func() { _ = rootHandle.Close() }()
	if err := atomicWriteRuntimeFile(rootHandle, s.Root, filepath.Join(s.Root, "runtime", currentName), ".current-", append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("publish selected runtime: %w", err)
	}
	return nil
}

func (s *Store) installComponent(ctx context.Context, rootHandle *os.Root, stage, name string, component Component) error {
	dir := filepath.Join(stage, name)
	if err := ensureRuntimeDirectory(s.Root, dir, 0o700); err != nil {
		return err
	}
	artifact, artifactPath, err := createTempRuntimeFile(rootHandle, s.Root, stage, ".artifact-", 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = removeRuntimeFile(rootHandle, s.Root, artifactPath) }()
	hash := sha256.New()
	if err := download(ctx, s.HTTPClient, component.URL, io.MultiWriter(artifact, hash), s.MaxArtifact); err != nil {
		_ = artifact.Close()
		return err
	}
	if err := errors.Join(artifact.Sync(), artifact.Close()); err != nil {
		return fmt.Errorf("close downloaded artifact: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if digest != component.SHA256 {
		return fmt.Errorf("SHA-256 mismatch: got %s, want %s", digest, component.SHA256)
	}
	if err := extractRuntime(rootHandle, s.Root, artifactPath, dir, component); err != nil {
		return err
	}
	if err := verifyRuntimeComponent(rootHandle, s.Root, dir, component); err != nil {
		return err
	}
	artifactDir := filepath.Join(stage, "artifacts")
	if err := ensureRuntimeDirectory(s.Root, artifactDir, 0o700); err != nil {
		return fmt.Errorf("create runtime artifact store: %w", err)
	}
	if err := renameRuntimePath(rootHandle, s.Root, artifactPath, filepath.Join(artifactDir, name)); err != nil {
		return fmt.Errorf("retain verified runtime artifact: %w", err)
	}
	artifactPath = ""
	return nil
}

func download(ctx context.Context, client *http.Client, rawURL string, dst io.Writer, max int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download artifact: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download artifact: HTTP %s", response.Status)
	}
	if response.ContentLength > max {
		return fmt.Errorf("download artifact has invalid size %d", response.ContentLength)
	}
	limited := io.LimitReader(response.Body, max+1)
	n, err := io.Copy(dst, limited)
	if err != nil {
		return fmt.Errorf("write downloaded artifact: %w", err)
	}
	if n == 0 || n > max {
		return fmt.Errorf("downloaded artifact has invalid size %d", n)
	}
	return nil
}

func extractRuntime(rootHandle *os.Root, root, artifact, destination string, component Component) error {
	destination, err := componentRootPath(destination, component.Root)
	if err != nil {
		return err
	}
	if err := ensureRuntimeDirectory(root, destination, 0o700); err != nil {
		return err
	}
	input, info, err := openRuntimeFile(rootHandle, root, artifact, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	switch component.Format {
	case "binary":
		file := component.Files[0]
		path, err := safeJoin(destination, file.Path)
		if err != nil {
			return err
		}
		if err := ensureRuntimeDirectory(root, filepath.Dir(path), 0o700); err != nil {
			return err
		}
		output, _, err := openRuntimeFile(rootHandle, root, path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		return errors.Join(copyErr, closeErr)
	case "tar.gz":
		gz, err := gzip.NewReader(input)
		if err != nil {
			return fmt.Errorf("open gzip archive: %w", err)
		}
		defer func() { _ = gz.Close() }()
		return extractRuntimeTar(rootHandle, root, gz, destination)
	case "zip":
		archive, err := zip.NewReader(input, info.Size())
		if err != nil {
			return fmt.Errorf("open zip archive: %w", err)
		}
		seen := map[string]struct{}{}
		for _, entry := range archive.File {
			path, err := safeJoin(destination, entry.Name)
			if err != nil {
				return err
			}
			if _, ok := seen[path]; ok {
				return fmt.Errorf("archive contains duplicate destination %q", entry.Name)
			}
			seen[path] = struct{}{}
			if entry.FileInfo().IsDir() {
				if err := ensureRuntimeDirectory(root, path, 0o700); err != nil {
					return err
				}
				continue
			}
			if entry.Mode()&os.ModeSymlink != 0 || entry.Mode()&os.ModeIrregular != 0 {
				return fmt.Errorf("archive entry %q is not a regular file", entry.Name)
			}
			if err := ensureRuntimeDirectory(root, filepath.Dir(path), 0o700); err != nil {
				return err
			}
			entryInput, err := entry.Open()
			if err != nil {
				return err
			}
			output, _, err := openRuntimeFile(rootHandle, root, path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
			if err == nil {
				_, err = io.Copy(output, entryInput)
				closeErr := output.Close()
				if err == nil {
					err = closeErr
				}
			}
			_ = entryInput.Close()
			if err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported runtime artifact format %q", component.Format)
	}
}

func extractRuntimeTar(rootHandle *os.Root, root string, input io.Reader, destination string) error {
	seen := map[string]struct{}{}
	reader := tar.NewReader(input)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		path, err := safeJoin(destination, header.Name)
		if err != nil {
			return err
		}
		if _, ok := seen[path]; ok {
			return fmt.Errorf("archive contains duplicate destination %q", header.Name)
		}
		seen[path] = struct{}{}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := ensureRuntimeDirectory(root, path, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := ensureRuntimeDirectory(root, filepath.Dir(path), 0o700); err != nil {
				return err
			}
			output, _, err := openRuntimeFile(rootHandle, root, path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, reader)
			closeErr := output.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive entry %q is not a regular file or directory", header.Name)
		}
	}
}

func extract(artifact, destination string, component Component) error {
	destination, err := componentRoot(destination, component.Root)
	if err != nil {
		return err
	}
	switch component.Format {
	case "binary":
		file := component.Files[0]
		path, err := safeJoin(destination, file.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		input, err := os.Open(artifact)
		if err != nil {
			return err
		}
		defer func() { _ = input.Close() }()
		output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	case "tar.gz":
		file, err := os.Open(artifact)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		gz, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("open gzip archive: %w", err)
		}
		defer func() { _ = gz.Close() }()
		return extractTar(gz, destination)
	case "zip":
		archive, err := zip.OpenReader(artifact)
		if err != nil {
			return fmt.Errorf("open zip archive: %w", err)
		}
		defer func() { _ = archive.Close() }()
		seen := map[string]struct{}{}
		for _, entry := range archive.File {
			path, err := safeJoin(destination, entry.Name)
			if err != nil {
				return err
			}
			if _, ok := seen[path]; ok {
				return fmt.Errorf("archive contains duplicate destination %q", entry.Name)
			}
			seen[path] = struct{}{}
			if entry.FileInfo().IsDir() {
				if err := os.MkdirAll(path, 0o700); err != nil {
					return err
				}
				continue
			}
			if entry.Mode()&os.ModeSymlink != 0 || entry.Mode()&os.ModeIrregular != 0 {
				return fmt.Errorf("archive entry %q is not a regular file", entry.Name)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			input, err := entry.Open()
			if err != nil {
				return err
			}
			output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
			if err == nil {
				_, err = io.Copy(output, input)
				closeErr := output.Close()
				if err == nil {
					err = closeErr
				}
			}
			_ = input.Close()
			if err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported runtime artifact format %q", component.Format)
	}
}

func extractTar(input io.Reader, destination string) error {
	seen := map[string]struct{}{}
	reader := tar.NewReader(input)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}
		path, err := safeJoin(destination, header.Name)
		if err != nil {
			return err
		}
		if _, ok := seen[path]; ok {
			return fmt.Errorf("archive contains duplicate destination %q", header.Name)
		}
		seen[path] = struct{}{}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, reader)
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("archive entry %q is not a regular file or directory", header.Name)
		}
	}
}

func verifyComponent(root string, component Component) error {
	root, err := componentRoot(root, component.Root)
	if err != nil {
		return err
	}
	for _, expected := range component.Files {
		path, err := safeJoin(root, expected.Path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("required file %s is missing: %w", expected.Path, err)
		}
		if expected.Regular && !info.Mode().IsRegular() {
			return fmt.Errorf("required file %s is not regular", expected.Path)
		}
		if expected.Executable && info.Mode()&0111 == 0 && !strings.HasSuffix(strings.ToLower(path), ".exe") {
			return fmt.Errorf("required executable %s is not executable", expected.Path)
		}
	}
	return nil
}

func verifyRuntimeComponent(rootHandle *os.Root, storeRoot, root string, component Component) error {
	root, err := componentRootPath(root, component.Root)
	if err != nil {
		return err
	}
	for _, expected := range component.Files {
		path, err := safeJoin(root, expected.Path)
		if err != nil {
			return err
		}
		file, info, err := openRuntimeFile(rootHandle, storeRoot, path, os.O_RDONLY, 0)
		if err != nil {
			return fmt.Errorf("required file %s is missing: %w", expected.Path, err)
		}
		_ = file.Close()
		if expected.Regular && !info.Mode().IsRegular() {
			return fmt.Errorf("required file %s is not regular", expected.Path)
		}
		if expected.Executable && info.Mode()&0111 == 0 && !strings.HasSuffix(strings.ToLower(path), ".exe") {
			return fmt.Errorf("required executable %s is not executable", expected.Path)
		}
	}
	return nil
}

func targetWithFileDigests(bundle string, target Target) (Target, error) {
	installed := target
	installed.Components = make(map[string]Component, len(target.Components))
	for name, component := range target.Components {
		installedComponent := component
		installedComponent.Files = append([]ExpectedFile(nil), component.Files...)
		componentDir, err := componentRootPath(filepath.Join(bundle, name), component.Root)
		if err != nil {
			return Target{}, err
		}
		for index, expected := range component.Files {
			path, err := safeJoin(componentDir, expected.Path)
			if err != nil {
				return Target{}, err
			}
			digest, err := fileDigest(path)
			if err != nil {
				return Target{}, fmt.Errorf("digest %s: %w", expected.Path, err)
			}
			installedComponent.Files[index].SHA256 = digest
		}
		installed.Components[name] = installedComponent
	}
	return installed, nil
}

func targetWithFileDigestsRooted(rootHandle *os.Root, root, bundle string, target Target) (Target, error) {
	installed := target
	installed.Components = make(map[string]Component, len(target.Components))
	for name, component := range target.Components {
		installedComponent := component
		installedComponent.Files = append([]ExpectedFile(nil), component.Files...)
		componentDir, err := componentRootPath(filepath.Join(bundle, name), component.Root)
		if err != nil {
			return Target{}, err
		}
		for index, expected := range component.Files {
			path, err := safeJoin(componentDir, expected.Path)
			if err != nil {
				return Target{}, err
			}
			digest, err := digestRuntimeFile(rootHandle, root, path)
			if err != nil {
				return Target{}, fmt.Errorf("digest %s: %w", expected.Path, err)
			}
			installedComponent.Files[index].SHA256 = digest
		}
		installed.Components[name] = installedComponent
	}
	return installed, nil
}

func componentRoot(destination, root string) (string, error) {
	path, err := componentRootPath(destination, root)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("create runtime component root: %w", err)
	}
	return path, nil
}

func componentRootPath(destination, root string) (string, error) {
	if root == "" {
		return "", errors.New("runtime component has no extraction root")
	}
	path, err := safeJoin(destination, root)
	if err != nil {
		return "", fmt.Errorf("runtime component extraction root: %w", err)
	}
	return path, nil
}

func (s *Store) readCurrent() (Current, error) {
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return Current{}, err
	}
	defer func() { _ = rootHandle.Close() }()
	data, err := readRuntimeFile(rootHandle, s.Root, filepath.Join(s.Root, "runtime", currentName))
	if err != nil {
		return Current{}, err
	}
	var current Current
	if err := json.Unmarshal(data, &current); err != nil {
		return Current{}, fmt.Errorf("decode selected runtime: %w", err)
	}
	return current, nil
}

func (s *Store) runtimeFromCurrent(current Current, targetName string, target Target) (Runtime, error) {
	if current.Schema != s.Lock.Schema {
		return Runtime{}, fmt.Errorf("%w: selected runtime metadata schema %d is not supported; run `hand runtime ensure`", ErrRuntimeNotReady, current.Schema)
	}
	if current.RuntimeID != s.Lock.RuntimeID || current.Target != targetName {
		return Runtime{}, fmt.Errorf("%w: selected runtime %q does not match required %q; run `hand runtime ensure`", ErrRuntimeNotReady, current.RuntimeID, s.Lock.RuntimeID)
	}
	bundleName := current.Bundle
	if bundleName == "" {
		bundleName = filepath.ToSlash(filepath.Join("bundles", current.RuntimeID))
	}
	if filepath.IsAbs(filepath.FromSlash(bundleName)) {
		return Runtime{}, fmt.Errorf("%w: selected runtime bundle path is absolute", ErrRuntimeNotReady)
	}
	bundle, err := safeJoin(filepath.Join(s.Root, "runtime"), bundleName)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: selected runtime bundle path is invalid: %v", ErrRuntimeNotReady, err)
	}
	return s.runtimeFromBundle(current, bundle, targetName, target)
}

func (s *Store) runtimeFromBundle(current Current, bundle, targetName string, target Target) (Runtime, error) {
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: open runtime store: %v", ErrRuntimeNotReady, err)
	}
	defer func() { _ = rootHandle.Close() }()
	manifestPath := filepath.Join(bundle, manifestName)
	manifestFile, manifestInfo, err := openRuntimeFile(rootHandle, s.Root, manifestPath, os.O_RDONLY, 0)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: selected runtime manifest is missing: %v", ErrRuntimeNotReady, err)
	}
	defer func() { _ = manifestFile.Close() }()
	if !manifestInfo.Mode().IsRegular() {
		return Runtime{}, fmt.Errorf("%w: selected runtime manifest is not a regular file", ErrRuntimeNotReady)
	}
	manifestData, err := io.ReadAll(manifestFile)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: read selected runtime manifest: %v", ErrRuntimeNotReady, err)
	}
	var installed Target
	if err := decodeTargetManifest(manifestData, &installed); err != nil {
		return Runtime{}, fmt.Errorf("%w: decode selected runtime manifest: %v", ErrRuntimeNotReady, err)
	}
	installedDigest, err := targetDigest(installed)
	if err != nil {
		return Runtime{}, err
	}
	digest, err := targetDigest(target)
	if err != nil {
		return Runtime{}, err
	}
	if current.ManifestSHA256 != installedDigest || targetDigestWithoutFileDigests(installed) != digest {
		return Runtime{}, fmt.Errorf("%w: selected runtime manifest digest is invalid; run `hand runtime ensure`", ErrRuntimeNotReady)
	}
	paths := map[string]*string{}
	for name, component := range target.Components {
		if err := verifyInstalledComponentAgainstArtifact(rootHandle, s.Root, bundle, name, component); err != nil {
			return Runtime{}, fmt.Errorf("%w: selected runtime component %s failed immutable artifact verification: %v", ErrRuntimeNotReady, name, err)
		}
		if err := verifyRuntimeComponent(rootHandle, s.Root, filepath.Join(bundle, name), component); err != nil {
			return Runtime{}, fmt.Errorf("%w: selected runtime component %s is incomplete: %v", ErrRuntimeNotReady, name, err)
		}
		installedComponent, ok := installed.Components[name]
		if !ok || len(installedComponent.Files) != len(component.Files) {
			return Runtime{}, fmt.Errorf("%w: selected runtime component %s manifest is incomplete", ErrRuntimeNotReady, name)
		}
		componentDir, err := componentRootPath(filepath.Join(bundle, name), component.Root)
		if err != nil {
			return Runtime{}, err
		}
		if err := validateRuntimePath(bundle, componentDir); err != nil {
			return Runtime{}, fmt.Errorf("%w: selected runtime component %s path is not direct: %v", ErrRuntimeNotReady, name, err)
		}
		for index, file := range component.Files {
			path, err := safeJoin(componentDir, file.Path)
			if err != nil {
				return Runtime{}, err
			}
			if err := validateRuntimePath(bundle, path); err != nil {
				return Runtime{}, fmt.Errorf("%w: selected runtime file %s path is not direct: %v", ErrRuntimeNotReady, file.Path, err)
			}
			installedFile := installedComponent.Files[index]
			if installedFile.Path != file.Path || installedFile.SHA256 == "" {
				return Runtime{}, fmt.Errorf("%w: selected runtime file digest is missing for %s", ErrRuntimeNotReady, file.Path)
			}
			got, err := digestRuntimeFile(rootHandle, s.Root, path)
			if err != nil {
				return Runtime{}, fmt.Errorf("%w: digest selected runtime file %s: %v", ErrRuntimeNotReady, file.Path, err)
			}
			if got != installedFile.SHA256 {
				return Runtime{}, fmt.Errorf("%w: selected runtime file %s digest mismatch", ErrRuntimeNotReady, file.Path)
			}
		}
		for _, file := range component.Files {
			if !file.Executable {
				continue
			}
			componentDir, err := componentRootPath(filepath.Join(bundle, name), component.Root)
			if err != nil {
				return Runtime{}, err
			}
			path, err := safeJoin(componentDir, file.Path)
			if err != nil {
				return Runtime{}, err
			}
			paths[name] = &path
			break
		}
	}
	gitPath, ok := paths["git"]
	if !ok {
		return Runtime{}, fmt.Errorf("%w: selected runtime has no Git executable", ErrRuntimeNotReady)
	}
	treehousePath, ok := paths["treehouse"]
	if !ok {
		return Runtime{}, fmt.Errorf("%w: selected runtime has no Treehouse executable", ErrRuntimeNotReady)
	}
	herdrPath, ok := paths["herdr"]
	if !ok {
		return Runtime{}, fmt.Errorf("%w: selected runtime has no Herdr executable", ErrRuntimeNotReady)
	}
	templateDir := filepath.Join(s.Root, "runtime", "git-templates")
	if err := ensureRuntimeDirectory(s.Root, templateDir, 0o700); err != nil {
		templateDir = ""
	}
	return Runtime{
		ID:               current.RuntimeID,
		Target:           current.Target,
		BundleDir:        bundle,
		GitPath:          *gitPath,
		GitVersion:       target.Components["git"].Version,
		TreehousePath:    *treehousePath,
		TreehouseVersion: target.Components["treehouse"].Version,
		HerdrPath:        *herdrPath,
		HerdrVersion:     target.Components["herdr"].Version,
		GitBin:           filepath.Dir(*gitPath),
		GitTemplateDir:   templateDir,
	}, nil
}

func verifyInstalledComponentAgainstArtifact(rootHandle *os.Root, root, bundle, name string, component Component) error {
	if runtimeFixtureAllowed {
		return nil
	}
	artifact := filepath.Join(bundle, "artifacts", name)
	input, info, err := openRuntimeFile(rootHandle, root, artifact, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("read retained artifact: %w", err)
	}
	defer func() { _ = input.Close() }()
	if !info.Mode().IsRegular() {
		return errors.New("retained artifact is not a regular file")
	}
	temporary, err := os.MkdirTemp("", "hand-runtime-verify-")
	if err != nil {
		return fmt.Errorf("create artifact verification directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	retainedCopy, err := os.OpenFile(filepath.Join(temporary, "artifact"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create retained artifact verification copy: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(retainedCopy, hash), input)
	closeErr := retainedCopy.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return fmt.Errorf("copy retained artifact for verification: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if digest != component.SHA256 {
		return fmt.Errorf("retained artifact digest mismatch: got %s, want %s", digest, component.SHA256)
	}
	extracted := filepath.Join(temporary, name)
	if err := extract(filepath.Join(temporary, "artifact"), extracted, component); err != nil {
		return fmt.Errorf("extract retained artifact: %w", err)
	}
	if err := verifyComponent(extracted, component); err != nil {
		return fmt.Errorf("verify retained artifact files: %w", err)
	}
	for _, expected := range component.Files {
		installedRoot, err := componentRootPath(filepath.Join(bundle, name), component.Root)
		if err != nil {
			return err
		}
		installedPath, err := safeJoin(installedRoot, expected.Path)
		if err != nil {
			return err
		}
		extractedRoot, err := componentRootPath(extracted, component.Root)
		if err != nil {
			return err
		}
		extractedPath, err := safeJoin(extractedRoot, expected.Path)
		if err != nil {
			return err
		}
		installedDigest, err := digestRuntimeFile(rootHandle, root, installedPath)
		if err != nil {
			return err
		}
		extractedDigest, err := fileDigest(extractedPath)
		if err != nil {
			return err
		}
		if installedDigest != extractedDigest {
			return fmt.Errorf("installed file %s differs from verified artifact", expected.Path)
		}
	}
	return nil
}

func targetDigest(target Target) (string, error) {
	data, err := json.Marshal(target)
	if err != nil {
		return "", fmt.Errorf("canonicalize installed runtime manifest: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func decodeTargetManifest(data []byte, target *Target) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("runtime generation manifest has trailing data")
	}
	return nil
}

func targetDigestWithoutFileDigests(target Target) string {
	copyTarget := target
	copyTarget.Components = make(map[string]Component, len(target.Components))
	for name, component := range target.Components {
		copyComponent := component
		copyComponent.Files = append([]ExpectedFile(nil), component.Files...)
		for index := range copyComponent.Files {
			copyComponent.Files[index].SHA256 = ""
		}
		copyTarget.Components[name] = copyComponent
	}
	digest, _ := targetDigest(copyTarget)
	return digest
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open downloaded artifact: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash downloaded artifact: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func safeJoin(root, name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("archive path %q is invalid", name)
	}
	portable := strings.ReplaceAll(name, "\\", "/")
	if isPortableAbsolutePath(portable) {
		return "", fmt.Errorf("archive path %q is absolute", name)
	}
	cleanPortable := pathpkg.Clean(portable)
	if cleanPortable == ".." || strings.HasPrefix(cleanPortable, "../") {
		return "", fmt.Errorf("archive path %q escapes staging directory", name)
	}
	clean := filepath.FromSlash(cleanPortable)
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, clean)
	if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path %q escapes staging directory", name)
	}
	return path, nil
}

func ensureRuntimeDirectory(root, path string, perm os.FileMode) error {
	relative, err := runtimeRelativePath(root, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, perm); err != nil {
		return err
	}
	rootHandle, err := openDirectRuntimeRoot(root)
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
		if runtimePathIsIndirect(info) || !info.IsDir() {
			return fmt.Errorf("runtime directory %s is not a direct directory", filepath.Join(root, relative))
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
			return fmt.Errorf("runtime directory %s changed while opening it", filepath.Join(root, relative))
		}
		if current != rootHandle {
			_ = current.Close()
		}
		current = next
	}
	return nil
}

func validateRuntimePath(root, path string) error {
	relative, err := runtimeRelativePath(root, path)
	if err != nil {
		return err
	}
	rootHandle, err := openDirectRuntimeRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = rootHandle.Close() }()
	if relative == "." {
		return nil
	}
	parts := strings.Split(relative, string(filepath.Separator))
	current := rootHandle
	defer func() {
		if current != rootHandle {
			_ = current.Close()
		}
	}()
	for index, part := range parts {
		info, err := current.Lstat(part)
		if err != nil {
			return err
		}
		if runtimePathIsIndirect(info) {
			return fmt.Errorf("runtime path component %s is indirect", filepath.Join(root, filepath.Join(parts[:index+1]...)))
		}
		if index == len(parts)-1 {
			return nil
		}
		if !info.IsDir() {
			return fmt.Errorf("runtime path component %s is not a directory", filepath.Join(root, filepath.Join(parts[:index+1]...)))
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
			return fmt.Errorf("runtime path component %s changed while opening it", filepath.Join(root, filepath.Join(parts[:index+1]...)))
		}
		if current != rootHandle {
			_ = current.Close()
		}
		current = next
	}
	return nil
}

func runtimeRelativePath(root, path string) (string, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("runtime path %q escapes store %q", path, root)
	}
	return relative, nil
}

func openDirectRuntimeRoot(root string) (*os.Root, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if runtimePathIsIndirect(info) || !info.IsDir() {
		return nil, fmt.Errorf("runtime store %s is not a direct directory", root)
	}
	handle, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	opened, err := handle.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = handle.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("runtime store %s changed while opening it", root)
	}
	return handle, nil
}

func openDirectRuntimeSubroot(rootHandle *os.Root, relative string) (*os.Root, bool, error) {
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
		if runtimePathIsIndirect(info) || !info.IsDir() {
			if owned {
				_ = current.Close()
			}
			return nil, false, fmt.Errorf("runtime path component %s is not a direct directory", part)
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
			return nil, false, fmt.Errorf("runtime path component %s changed while opening it", part)
		}
		if owned {
			_ = current.Close()
		}
		current = next
		owned = true
	}
	return current, owned, nil
}

func openRuntimeFile(rootHandle *os.Root, root, path string, flag int, perm os.FileMode) (*os.File, os.FileInfo, error) {
	relative, err := runtimeRelativePath(root, path)
	if err != nil {
		return nil, nil, err
	}
	if relative == "." {
		return nil, nil, fmt.Errorf("runtime file path names the store root")
	}
	parent, owned, err := openDirectRuntimeSubroot(rootHandle, filepath.Dir(relative))
	if err != nil {
		return nil, nil, err
	}
	if owned {
		defer func() { _ = parent.Close() }()
	}
	leaf := filepath.Base(relative)
	expected, inspectErr := parent.Lstat(leaf)
	if inspectErr == nil && runtimePathIsIndirect(expected) {
		return nil, nil, fmt.Errorf("runtime path component %s is indirect", path)
	}
	if inspectErr != nil && !errors.Is(inspectErr, os.ErrNotExist) {
		return nil, nil, inspectErr
	}
	file, err := parent.OpenFile(leaf, flag, perm)
	if err != nil {
		return nil, nil, err
	}
	opened, err := file.Stat()
	if err != nil || runtimePathIsIndirect(opened) || expected != nil && !os.SameFile(expected, opened) {
		_ = file.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("runtime path component %s changed while opening it", path)
	}
	return file, opened, nil
}

func readRuntimeFile(rootHandle *os.Root, root, path string) ([]byte, error) {
	file, _, err := openRuntimeFile(rootHandle, root, path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

func digestRuntimeFile(rootHandle *os.Root, root, path string) (string, error) {
	file, _, err := openRuntimeFile(rootHandle, root, path, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func atomicWriteRuntimeFile(rootHandle *os.Root, root, path, prefix string, data []byte, perm os.FileMode) error {
	relative, err := runtimeRelativePath(root, path)
	if err != nil {
		return err
	}
	if relative == "." {
		return fmt.Errorf("runtime file path names the store root")
	}
	parent, owned, err := openDirectRuntimeSubroot(rootHandle, filepath.Dir(relative))
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
	return parent.Rename(temporary, filepath.Base(relative))
}

func mkdirTempRuntime(rootHandle *os.Root, root, parentPath, prefix string) (string, error) {
	relative, err := runtimeRelativePath(root, parentPath)
	if err != nil {
		return "", err
	}
	parent, owned, err := openDirectRuntimeSubroot(rootHandle, relative)
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
	return "", errors.New("allocate unique runtime staging directory")
}

func createTempRuntimeFile(rootHandle *os.Root, root, parentPath, prefix string, perm os.FileMode) (*os.File, string, error) {
	for range 100 {
		var nonce [16]byte
		if _, err := cryptorand.Read(nonce[:]); err != nil {
			return nil, "", err
		}
		path := filepath.Join(parentPath, prefix+hex.EncodeToString(nonce[:]))
		file, _, err := openRuntimeFile(rootHandle, root, path, os.O_CREATE|os.O_EXCL|os.O_RDWR, perm)
		if err == nil {
			return file, path, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("allocate unique runtime staging file")
}

func removeAllRuntimePath(rootHandle *os.Root, root, path string) error {
	relative, err := runtimeRelativePath(root, path)
	if err != nil {
		return err
	}
	if relative == "." {
		return errors.New("refusing to remove runtime store root")
	}
	return rootHandle.RemoveAll(relative)
}

func removeRuntimeFile(rootHandle *os.Root, root, path string) error {
	relative, err := runtimeRelativePath(root, path)
	if err != nil {
		return err
	}
	if relative == "." {
		return errors.New("refusing to remove runtime store root")
	}
	parent, owned, err := openDirectRuntimeSubroot(rootHandle, filepath.Dir(relative))
	if err != nil {
		return err
	}
	if owned {
		defer func() { _ = parent.Close() }()
	}
	return parent.Remove(filepath.Base(relative))
}

func renameRuntimePath(rootHandle *os.Root, root, oldPath, newPath string) error {
	oldRelative, err := runtimeRelativePath(root, oldPath)
	if err != nil {
		return err
	}
	if oldRelative == "." {
		return errors.New("refusing to rename runtime store root")
	}
	newRelative, err := runtimeRelativePath(root, newPath)
	if err != nil {
		return err
	}
	if newRelative == "." {
		return errors.New("refusing to replace runtime store root")
	}
	return rootHandle.Rename(oldRelative, newRelative)
}

func currentTargetName(goos, goarch string) string {
	if goos == "" {
		goos, _ = targetPlatform()
	}
	if goarch == "" {
		_, goarch = targetPlatform()
	}
	return goos + "/" + goarch
}
