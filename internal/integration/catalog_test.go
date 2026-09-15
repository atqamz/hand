package integration

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestDefaultStoreUsesPrivateSecondhandHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SECONDHAND_HOME", root)
	if got := DefaultStore().Root; got != root {
		t.Fatalf("store root = %q, want %q", got, root)
	}
}

func TestCatalogIsClosedAndDoesNotIncludeHarnesses(t *testing.T) {
	got := Catalog()
	want := []string{"github/gh", "gitlab/glab", "delivery/no-mistakes", "delivery/witness"}
	if len(got) != len(want) {
		t.Fatalf("catalog length = %d, want %d", len(got), len(want))
	}
	for i, capability := range got {
		if capability.ID != want[i] {
			t.Fatalf("catalog[%d] = %q, want %q", i, capability.ID, want[i])
		}
		if capability.Executable == "" || capability.Owner == "" {
			t.Fatalf("catalog[%d] has incomplete descriptor: %+v", i, capability)
		}
	}
}

func TestMissingCapabilityIsExplicitAndActionable(t *testing.T) {
	store := NewStore(t.TempDir())
	_, err := store.Resolve("github/gh")
	if err == nil {
		t.Fatal("missing capability resolved")
	}
	var missing *MissingError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %T %v, want MissingError", err, err)
	}
	if missing.Command != "hand integration install github/gh" {
		t.Fatalf("repair command = %q", missing.Command)
	}
}

func TestInstallCopiesAndSelectsExplicitExecutable(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(root)
	installed, err := store.Install("github/gh", source)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.Resolve("github/gh"); err != nil || got != installed {
		t.Fatalf("Resolve() = %q, %v; want %q", got, err, installed)
	}
	if err := os.WriteFile(installed, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve("github/gh"); err == nil {
		t.Fatal("Resolve() accepted a modified payload")
	}
	if err := store.Remove("github/gh"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Resolve("github/gh"); err == nil {
		t.Fatal("removed capability still resolves")
	}
}

func TestInstallRejectsSymlinkAtExactPayload(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := digestFile(source)
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(root, "integrations", "github", "gh", "payloads", digest)
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "matching-gh")
	if err := os.WriteFile(external, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(bundle, executable("gh"))); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := NewStore(root).Install("github/gh", source); err == nil {
		t.Fatal("install adopted a matching payload through a symlink")
	}
	if _, err := os.Stat(filepath.Join(root, "integrations", "github", "gh", "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("install published a selection for an indirect payload: %v", err)
	}
}

func TestIntegrationFileAccessRemainsAnchoredAcrossRootReplacement(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "secondhand")
	path := filepath.Join(root, "integrations", "github", "gh", "current.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootHandle, err := openDirectIntegrationRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rootHandle.Close() }()
	moved := filepath.Join(parent, "moved")
	if err := os.Rename(root, moved); err != nil {
		if runtime.GOOS == "windows" {
			return
		}
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := readIntegrationFile(rootHandle, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("rooted read followed replacement path: %q", data)
	}
}

func TestConcurrentInstallPublishesOneCompletePayload(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(source, bytes.Repeat([]byte("payload"), 2<<20), 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(root)
	start := make(chan struct{})
	results := make(chan error, 16)
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := store.Install("github/gh", source)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent install: %v", err)
		}
	}
	payloads := filepath.Join(root, "integrations", "github", "gh", "payloads")
	entries, err := os.ReadDir(payloads)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].IsDir() || len(entries[0].Name()) != 64 {
		t.Fatalf("payload publications = %v, want one complete digest directory", entries)
	}
	if _, err := store.Resolve("github/gh"); err != nil {
		t.Fatalf("resolve concurrently published payload: %v", err)
	}
}

func TestInstallIgnoresInterruptedPrivateStage(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "gh")
	body := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(source, body, 0o700); err != nil {
		t.Fatal(err)
	}
	payloads := filepath.Join(root, "integrations", "github", "gh", "payloads")
	residue := filepath.Join(payloads, ".staging-interrupted", executable("gh"))
	if err := os.MkdirAll(filepath.Dir(residue), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(residue, []byte("partial"), 0o700); err != nil {
		t.Fatal(err)
	}

	store := NewStore(root)
	installed, err := store.Install("github/gh", source)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(installed) == filepath.Dir(residue) {
		t.Fatal("interrupted private stage was published as the exact payload")
	}
	resolved, err := store.Resolve("github/gh")
	if err != nil || resolved != installed {
		t.Fatalf("Resolve() = %q, %v; want complete publication %q", resolved, err, installed)
	}
	if got, err := os.ReadFile(installed); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("published payload = %q, %v; want complete source", got, err)
	}
	if got, err := os.ReadFile(residue); err != nil || string(got) != "partial" {
		t.Fatalf("foreign crash residue changed: %q, %v", got, err)
	}
}
