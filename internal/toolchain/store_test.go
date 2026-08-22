package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractRejectsArchiveTraversalBeforeWritingOutsideDestination(t *testing.T) {
	archive := tarGzipFixture(t, "../escape", []byte("unsafe"))
	destination := t.TempDir()
	err := extract(archive, destination, Component{
		Format: "tar.gz",
		Files:  []ExpectedFile{{Path: "safe", Regular: true}},
	})
	if err == nil {
		t.Fatal("archive traversal was accepted")
	}
	if _, statErr := os.Stat(filepath.Join(destination, "..", "escape")); !os.IsNotExist(statErr) {
		t.Fatalf("archive wrote outside destination: %v", statErr)
	}
}

func TestSafeJoinRejectsAbsoluteAndParentPaths(t *testing.T) {
	for _, name := range []string{"/tmp/escape", "../escape", "nested/../../escape"} {
		if _, err := safeJoin(t.TempDir(), name); err == nil {
			t.Fatalf("safeJoin accepted %q", name)
		}
	}
}

func TestStatusUsesSelectedBundleAndVerifiesInstalledManifest(t *testing.T) {
	lock, err := LoadLock()
	if err != nil {
		t.Fatal(err)
	}
	target, err := lock.Target("linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bundleName := filepath.Join("bundles", "old")
	bundle := filepath.Join(root, "runtime", bundleName)
	for name, component := range target.Components {
		for _, file := range component.Files {
			path, err := componentRootPath(filepath.Join(bundle, name), component.Root)
			if err != nil {
				t.Fatal(err)
			}
			path, err = safeJoin(path, file.Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, manifestName), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := targetDigest(target)
	if err != nil {
		t.Fatal(err)
	}
	current := Current{Schema: lock.Schema, RuntimeID: lock.RuntimeID, Target: "linux/amd64", Bundle: filepath.ToSlash(bundleName), ManifestSHA256: digest, SelectedAt: time.Now().UTC()}
	data, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtime", currentName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root, lock)
	if err != nil {
		t.Fatal(err)
	}
	status, err := store.Status("linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.BundleDir != bundle {
		t.Fatalf("status = %+v, want ready selected bundle %s", status, bundle)
	}

	if err := os.WriteFile(filepath.Join(bundle, manifestName), []byte(fmt.Sprintf("%q", "not a target")), 0o600); err != nil {
		t.Fatal(err)
	}
	status, err = store.Status("linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || status.Reason == "" {
		t.Fatalf("status = %+v, want manifest failure", status)
	}
}

func tarGzipFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o700, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture.tar.gz")
	if err := os.WriteFile(path, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
