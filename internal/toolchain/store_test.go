package toolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestEnsureInstallsRuntimeBelowStoreRoot(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fixture-" + name))
	}))
	defer server.Close()

	components := make(map[string]Component, 3)
	for _, name := range []string{"git", "treehouse", "herdr"} {
		data := []byte("fixture-" + name)
		digest := sha256.Sum256(data)
		file := executableName(name)
		components[name] = Component{
			Name: name, Version: "test", Revision: "test", URL: server.URL + "/" + name,
			SHA256: hex.EncodeToString(digest[:]), Format: "binary", Root: ".",
			Files: []ExpectedFile{{Path: file, Executable: true, Regular: true}},
		}
	}
	lock := Lock{Schema: 1, GeneratedBy: "store-test", Targets: map[string]Target{
		runtime.GOOS + "/" + runtime.GOARCH: {Components: components},
	}}
	var err error
	lock.RuntimeID, err = lock.DeterministicID()
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	store, err := NewStore(root, lock)
	if err != nil {
		t.Fatal(err)
	}
	store.HTTPClient = server.Client()
	installed, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if installed.ID != lock.RuntimeID {
		t.Fatalf("runtime ID = %q, want %q", installed.ID, lock.RuntimeID)
	}
	if !strings.HasPrefix(installed.BundleDir, filepath.Join(root, "runtime")+string(filepath.Separator)) {
		t.Fatalf("bundle directory escaped store root: %s", installed.BundleDir)
	}
	for _, path := range []string{installed.GitPath, installed.TreehousePath, installed.HerdrPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("installed executable %s: %v", path, err)
		}
	}
	status, err := store.Status("", "")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.BundleDir != installed.BundleDir {
		t.Fatalf("status = %+v, want ready runtime %s", status, installed.BundleDir)
	}
}

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

func TestStatusRejectsBundleWithoutImmutableArtifacts(t *testing.T) {
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
	installedTarget, err := targetWithFileDigests(bundle, target)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(installedTarget)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, manifestName), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := targetDigest(installedTarget)
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
	if status.Ready || status.Reason == "" {
		t.Fatalf("status = %+v, want immutable artifact failure", status)
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
