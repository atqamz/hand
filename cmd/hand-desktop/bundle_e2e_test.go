//go:build e2e

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func targz(t *testing.T, name, from string) []byte {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func fakeRelease(t *testing.T, handBin, deskBin string) string {
	t.Helper()
	stem := "darwin-" + runtime.GOARCH
	hand, desk := targz(t, "hand", handBin), targz(t, "hand-desktop", deskBin)
	files := map[string][]byte{
		"hand-" + stem + ".tar.gz":         hand,
		"checksums.txt":                    []byte(sha(hand) + "  hand-" + stem + ".tar.gz\n"),
		"hand-desktop-" + stem + ".tar.gz": desk,
		"hand-desktop-" + stem + ".sha256": []byte(sha(desk) + "  hand-desktop-" + stem + ".tar.gz\n"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := files[path.Base(r.URL.Path)]; ok && strings.HasPrefix(r.URL.Path, "/download/edge/") {
			_, _ = w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func bundleFromUpdate(t *testing.T, run func(*exec.Cmd) *exec.Cmd, handBin, deskBin, home, logDir string) string {
	t.Helper()
	cmd := run(exec.Command(handBin, "update", "--channel", "edge"))
	cmd.Env = append(cmd.Env, "HAND_INSTALL_BASE="+fakeRelease(t, handBin, deskBin))
	out, err := cmd.CombinedOutput()
	_ = os.WriteFile(filepath.Join(logDir, "update.log"), out, 0o644)
	if err != nil {
		t.Fatalf("hand update: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "\ndesktop: ") || !strings.Contains(string(out), " installed\n") {
		t.Fatalf("hand update did not install the window:\n%s", out)
	}
	app := filepath.Join(home, "Applications", "Hand.app", "Contents")
	for _, p := range []string{"Info.plist", "PkgInfo", "Resources/Hand.icns", "MacOS/hand-desktop"} {
		if fi, err := os.Stat(filepath.Join(app, p)); err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
			t.Fatalf("Hand.app/Contents/%s: %v", p, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(app, "MacOS", "hand-desktop")); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("the window in the bundle is not executable: %v", err)
	}
	link, err := os.Readlink(filepath.Join(app, "MacOS", "hand"))
	if err != nil {
		t.Fatalf("MacOS/hand is no symlink: %v", err)
	}
	if want, _ := filepath.EvalSymlinks(handBin); link != want {
		t.Fatalf("MacOS/hand -> %s, want %s", link, want)
	}
	if b, err := os.ReadFile(filepath.Join(app, "MacOS", "hand-desktop")); err != nil || !bytes.Equal(b, mustRead(t, deskBin)) {
		t.Fatalf("the window in the bundle is not the one in the release: %v", err)
	}
	if b, err := exec.Command("plutil", "-lint", filepath.Join(app, "Info.plist")).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v\n%s", err, b)
	}
	iconset := filepath.Join(logDir, "Hand.iconset")
	if b, err := exec.Command("iconutil", "--convert", "iconset", "--output", iconset, filepath.Join(app, "Resources", "Hand.icns")).CombinedOutput(); err != nil {
		t.Fatalf("iconutil: %v\n%s", err, b)
	}
	if entries, err := os.ReadDir(iconset); err != nil || len(entries) < 10 {
		t.Fatalf("the iconset holds %d images, want 10: %v", len(entries), err)
	}
	if b, err := run(exec.Command(handBin, "update", "--channel", "edge", "--check")).CombinedOutput(); err != nil {
		t.Fatalf("hand update --check: %v\n%s", err, b)
	}
	return filepath.Join(app, "MacOS", "hand-desktop")
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
