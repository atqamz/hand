package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// MaterializeHandExecutable retains the exact bytes opened at source behind a
// content-addressed path. Managed children can launch that immutable path even
// if an updater later replaces the canonical Hand pathname.
func (s *Store) MaterializeHandExecutable(source string) (string, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve managed Hand executable: %w", err)
	}
	input, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("open managed Hand executable: %w", err)
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return "", fmt.Errorf("stat managed Hand executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("managed Hand executable is not a regular file")
	}

	root := filepath.Join(s.Root, "runtime", "hand-generations")
	if err := ensureRuntimeDirectory(s.Root, root, 0o700); err != nil {
		return "", fmt.Errorf("create managed Hand generation store: %w", err)
	}
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return "", fmt.Errorf("open managed Hand generation store: %w", err)
	}
	defer func() { _ = rootHandle.Close() }()
	stage, err := mkdirTempRuntime(rootHandle, s.Root, root, ".staging-")
	if err != nil {
		return "", fmt.Errorf("create managed Hand generation staging directory: %w", err)
	}
	defer func() {
		if stage != "" {
			_ = removeAllRuntimePath(rootHandle, s.Root, stage)
		}
	}()
	name := executableName("hand")
	staged := filepath.Join(stage, name)
	output, _, err := openRuntimeFile(rootHandle, s.Root, staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", fmt.Errorf("create staged managed Hand executable: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(output, hash), input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return "", fmt.Errorf("stage managed Hand executable: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	generation := filepath.Join(root, digest)
	managed := filepath.Join(generation, name)
	generationRelative, err := runtimeRelativePath(s.Root, generation)
	if err != nil {
		return "", err
	}
	if _, err := rootHandle.Lstat(generationRelative); err == nil {
		if err := validateHandGenerationAt(rootHandle, s.Root, managed, digest); err != nil {
			return "", fmt.Errorf("existing managed Hand generation %s is invalid and will not be rewritten: %w", digest, err)
		}
		return managed, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect managed Hand generation: %w", err)
	}
	if err := renameRuntimePath(rootHandle, s.Root, stage, generation); err != nil {
		if validationErr := validateHandGenerationAt(rootHandle, s.Root, managed, digest); validationErr == nil {
			return managed, nil
		}
		return "", fmt.Errorf("publish managed Hand generation: %w", err)
	}
	stage = ""
	if err := validateHandGenerationAt(rootHandle, s.Root, managed, digest); err != nil {
		return "", fmt.Errorf("verify published managed Hand generation: %w", err)
	}
	return managed, nil
}

// HandExecutableGeneration resolves an already-published managed copy of
// source without changing the generation store.
func (s *Store) HandExecutableGeneration(source string) (string, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve Hand executable generation: %w", err)
	}
	digest, err := fileDigest(source)
	if err != nil {
		return "", fmt.Errorf("identify Hand executable generation: %w", err)
	}
	managed, err := s.HandGeneration("sha256:" + digest)
	if err != nil {
		return "", fmt.Errorf("resolve Hand executable generation: %w", err)
	}
	return managed, nil
}

// HandGeneration resolves and verifies one exact managed Hand generation.
func (s *Store) HandGeneration(generation string) (string, error) {
	rootHandle, err := openDirectRuntimeRoot(s.Root)
	if err != nil {
		return "", err
	}
	defer func() { _ = rootHandle.Close() }()
	return s.handGenerationAt(rootHandle, generation)
}

func (s *Store) handGenerationAt(rootHandle *os.Root, generation string) (string, error) {
	digest, ok := strings.CutPrefix(generation, "sha256:")
	if !ok || len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("invalid managed Hand generation %q", generation)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("invalid managed Hand generation %q", generation)
	}
	managed := filepath.Join(s.Root, "runtime", "hand-generations", digest, executableName("hand"))
	if err := validateHandGenerationAt(rootHandle, s.Root, managed, digest); err != nil {
		return "", err
	}
	return managed, nil
}

func validateHandGenerationAt(rootHandle *os.Root, root, path, digest string) error {
	file, info, err := openRuntimeFile(rootHandle, root, path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0111 == 0) {
		return errors.New("managed Hand generation is not an executable regular file")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != digest {
		return fmt.Errorf("managed Hand generation digest mismatch: got %s, want %s", got, digest)
	}
	return nil
}
