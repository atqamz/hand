package toolchain

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
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

	"github.com/atqamz/hand/internal/atomicfile"
	"github.com/atqamz/hand/internal/filelock"
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
	Reason           string
}

func NewStore(root string, lock Lock) (*Store, error) {
	if root == "" {
		var err error
		root, err = secondhandHome()
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
	root, err := secondhandHome()
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
	target, err := s.Lock.Target(goos, goarch)
	if err != nil {
		return Runtime{}, err
	}
	targetName := goos + "/" + goarch
	if goos == "" || goarch == "" {
		targetName = currentTargetName(goos, goarch)
	}
	if selected, err := s.Selected(goos, goarch); err == nil && selected.ID == s.Lock.RuntimeID {
		return selected, nil
	}
	if s.HTTPClient == nil {
		s.HTTPClient = http.DefaultClient
	}
	if s.MaxArtifact <= 0 {
		s.MaxArtifact = maxArtifact
	}
	if err := os.MkdirAll(filepath.Join(s.Root, "runtime", "bundles"), 0o700); err != nil {
		return Runtime{}, fmt.Errorf("create runtime bundle store: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(s.Root, "runtime", "locks"), 0o700); err != nil {
		return Runtime{}, fmt.Errorf("create runtime lock store: %w", err)
	}
	lockFile, err := os.OpenFile(filepath.Join(s.Root, "runtime", "locks", "selection.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Runtime{}, fmt.Errorf("open runtime selection lock: %w", err)
	}
	defer func() { _ = lockFile.Close() }()
	if err := filelock.Lock(lockFile, true); err != nil {
		return Runtime{}, fmt.Errorf("lock runtime selection: %w", err)
	}
	defer func() { _ = filelock.Unlock(lockFile) }()
	if selected, err := s.Selected(goos, goarch); err == nil && selected.ID == s.Lock.RuntimeID {
		return selected, nil
	}

	stage, err := os.MkdirTemp(filepath.Join(s.Root, "runtime"), ".staging-")
	if err != nil {
		return Runtime{}, fmt.Errorf("create runtime staging directory: %w", err)
	}
	defer func() {
		if stage != "" {
			_ = os.RemoveAll(stage)
		}
	}()
	for name, component := range target.Components {
		if err := s.installComponent(ctx, stage, name, component); err != nil {
			return Runtime{}, fmt.Errorf("install %s: %w", name, err)
		}
	}
	bundleName := filepath.Join("bundles", fmt.Sprintf("%s-%d", s.Lock.RuntimeID, time.Now().UnixNano()))
	bundle := filepath.Join(s.Root, "runtime", bundleName)
	if _, err := os.Stat(bundle); err == nil {
		return Runtime{}, fmt.Errorf("runtime bundle path already exists: %s", bundle)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Runtime{}, fmt.Errorf("inspect runtime bundle: %w", err)
	}
	if err := os.Rename(stage, bundle); err != nil {
		return Runtime{}, fmt.Errorf("publish runtime bundle: %w", err)
	}
	stage = ""
	installedTarget, err := targetWithFileDigests(bundle, target)
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
	if err := atomicfile.Write(filepath.Join(bundle, manifestName), ".manifest-", append(manifestData, '\n'), 0o600); err != nil {
		return Runtime{}, fmt.Errorf("publish runtime manifest: %w", err)
	}
	current := Current{Schema: s.Lock.Schema, RuntimeID: s.Lock.RuntimeID, Target: targetName, Bundle: filepath.ToSlash(bundleName), ManifestSHA256: manifestDigest, SelectedAt: time.Now().UTC()}
	validated, err := s.runtimeFromCurrent(current, targetName, target)
	if err != nil {
		return Runtime{}, err
	}
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return Runtime{}, fmt.Errorf("encode selected runtime: %w", err)
	}
	if err := atomicfile.Write(filepath.Join(s.Root, "runtime", currentName), ".current-", append(data, '\n'), 0o600); err != nil {
		return Runtime{}, fmt.Errorf("publish selected runtime: %w", err)
	}
	return validated, nil
}

func (s *Store) installComponent(ctx context.Context, stage, name string, component Component) error {
	dir := filepath.Join(stage, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	artifact, err := os.CreateTemp(stage, ".artifact-")
	if err != nil {
		return err
	}
	artifactPath := artifact.Name()
	defer func() { _ = os.Remove(artifactPath) }()
	if err := download(ctx, s.HTTPClient, component.URL, artifact, s.MaxArtifact); err != nil {
		_ = artifact.Close()
		return err
	}
	if err := artifact.Close(); err != nil {
		return fmt.Errorf("close downloaded artifact: %w", err)
	}
	digest, err := fileDigest(artifactPath)
	if err != nil {
		return err
	}
	if digest != component.SHA256 {
		return fmt.Errorf("SHA-256 mismatch: got %s, want %s", digest, component.SHA256)
	}
	if err := extract(artifactPath, dir, component); err != nil {
		return err
	}
	if err := verifyComponent(dir, component); err != nil {
		return err
	}
	artifactDir := filepath.Join(stage, "artifacts")
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		return fmt.Errorf("create runtime artifact store: %w", err)
	}
	if err := os.Rename(artifactPath, filepath.Join(artifactDir, name)); err != nil {
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
		data, err := os.ReadFile(artifact)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o700); err != nil {
			return err
		}
		return nil
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
	data, err := os.ReadFile(filepath.Join(s.Root, "runtime", currentName))
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
	manifestPath := filepath.Join(bundle, manifestName)
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: selected runtime manifest is missing: %v", ErrRuntimeNotReady, err)
	}
	if !manifestInfo.Mode().IsRegular() {
		return Runtime{}, fmt.Errorf("%w: selected runtime manifest is not a regular file", ErrRuntimeNotReady)
	}
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return Runtime{}, fmt.Errorf("%w: read selected runtime manifest: %v", ErrRuntimeNotReady, err)
	}
	var installed Target
	if err := json.Unmarshal(manifestData, &installed); err != nil {
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
		if err := verifyInstalledComponentAgainstArtifact(bundle, name, component); err != nil {
			return Runtime{}, fmt.Errorf("%w: selected runtime component %s failed immutable artifact verification: %v", ErrRuntimeNotReady, name, err)
		}
		if err := verifyComponent(filepath.Join(bundle, name), component); err != nil {
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
		for index, file := range component.Files {
			path, err := safeJoin(componentDir, file.Path)
			if err != nil {
				return Runtime{}, err
			}
			installedFile := installedComponent.Files[index]
			if installedFile.Path != file.Path || installedFile.SHA256 == "" {
				return Runtime{}, fmt.Errorf("%w: selected runtime file digest is missing for %s", ErrRuntimeNotReady, file.Path)
			}
			got, err := fileDigest(path)
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
	}, nil
}

func verifyInstalledComponentAgainstArtifact(bundle, name string, component Component) error {
	if runtimeFixtureAllowed {
		return nil
	}
	artifact := filepath.Join(bundle, "artifacts", name)
	info, err := os.Lstat(artifact)
	if err != nil {
		return fmt.Errorf("read retained artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("retained artifact is not a regular file")
	}
	digest, err := fileDigest(artifact)
	if err != nil {
		return err
	}
	if digest != component.SHA256 {
		return fmt.Errorf("retained artifact digest mismatch: got %s, want %s", digest, component.SHA256)
	}
	temporary, err := os.MkdirTemp(filepath.Dir(bundle), ".runtime-verify-")
	if err != nil {
		return fmt.Errorf("create artifact verification directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	extracted := filepath.Join(temporary, name)
	if err := extract(artifact, extracted, component); err != nil {
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
		installedDigest, err := fileDigest(installedPath)
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

func currentTargetName(goos, goarch string) string {
	if goos == "" {
		goos, _ = targetPlatform()
	}
	if goarch == "" {
		_, goarch = targetPlatform()
	}
	return goos + "/" + goarch
}
