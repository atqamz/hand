package integration

import (
	"errors"
	"os"
	"path/filepath"
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
