package cli_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

type luvusRelease struct {
	mu    sync.Mutex
	asked []string
}

func (l *luvusRelease) requests() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.asked...)
}

func serveLuvus(t *testing.T, h *harness, corrupt bool) *luvusRelease {
	t.Helper()
	body := fakebin.Embed(t, "fake", map[string]string{"on --version": "luvus 0.14.3\n"})
	exeName, ext := "luvus", ".tar.gz"
	if runtime.GOOS == "windows" {
		exeName, ext = "luvus.exe", ".zip"
	}
	var archive bytes.Buffer
	if ext == ".zip" {
		zw := zip.NewWriter(&archive)
		w, err := zw.Create(exeName)
		if err == nil {
			_, err = w.Write(body)
		}
		if err == nil {
			err = zw.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&archive)
		tw := tar.NewWriter(gz)
		err := tw.WriteHeader(&tar.Header{Name: exeName, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		if err == nil {
			_, err = tw.Write(body)
		}
		if err == nil {
			err = tw.Close()
		}
		if err == nil {
			err = gz.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256(archive.Bytes())
	digest := hex.EncodeToString(sum[:])
	if corrupt {
		digest = strings.Repeat("0", 64)
	}
	rel := &luvusRelease{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel.mu.Lock()
		rel.asked = append(rel.asked, r.URL.Path)
		rel.mu.Unlock()
		name := path.Base(r.URL.Path)
		switch {
		case !strings.HasPrefix(r.URL.Path, "/download/v0.14.3/luvus-v0.14.3-"):
			http.NotFound(w, r)
		case strings.HasSuffix(name, ".sha256"):
			_, _ = w.Write([]byte(digest + "  " + strings.TrimSuffix(name, ".sha256") + ext + "\n"))
		default:
			_, _ = w.Write(archive.Bytes())
		}
	}))
	t.Cleanup(srv.Close)
	h.vars["HAND_LUVUS_BASE"] = srv.URL
	return rel
}

func noLuvusOnPath(h *harness, t *testing.T) {
	t.Helper()
	h.vars["PATH"] = t.TempDir()
}

func TestInitDownloadsTheTestedLuvusWhenNoneIsOnPath(t *testing.T) {
	h := newHarness(t)
	noLuvusOnPath(h, t)
	rel := serveLuvus(t, h, false)
	root := h.vars["SECONDHAND_HOME"]
	out := h.ok("init")
	if !strings.HasPrefix(field(out, "luvus"), "downloaded and pinned 0.14.3 ("+filepath.Join(root, "luvus", "0.14.3-")) {
		t.Fatalf("init = %q", out)
	}
	if strings.Count(out, "Install Luvus") != 0 || len(rel.requests()) != 2 {
		t.Fatalf("init = %q, asked %q", out, rel.requests())
	}
	if left, _ := filepath.Glob(filepath.Join(root, "luvus", ".fetch-*")); len(left) != 0 {
		t.Fatalf("left %q", left)
	}
	if out := h.ok("luvus", "show"); !strings.HasPrefix(field(out, "pin"), "0.14.3 ") {
		t.Fatalf("show = %q", out)
	}
}

func TestInitRefusesADownloadWithABadChecksum(t *testing.T) {
	h := newHarness(t)
	noLuvusOnPath(h, t)
	serveLuvus(t, h, true)
	has(t, "init", h.ok("init"), "Install Luvus, then pin it: `hand luvus pin` (download failed: checksum mismatch for luvus-v0.14.3-")
	if _, err := os.Stat(filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "pin.json")); err == nil {
		t.Fatal("an unverified luvus was pinned")
	}
	if left, _ := filepath.Glob(filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "*", "luvus*")); len(left) != 0 {
		t.Fatalf("kept %q", left)
	}
}

func TestInitKeepsTheLuvusOnPathWithoutDownloading(t *testing.T) {
	h := newHarness(t)
	pinnable(t, h, "0.14.3")
	rel := serveLuvus(t, h, false)
	if out := h.ok("init"); !strings.HasPrefix(field(out, "luvus"), "pinned 0.14.3 (") {
		t.Fatalf("init = %q", out)
	}
	if got := rel.requests(); len(got) != 0 {
		t.Fatalf("asked %q", got)
	}
}

func TestInitAndPinDoNotDownloadWhenPinned(t *testing.T) {
	h := newHarness(t)
	script := pinnable(t, h, "0.14.3")
	h.ok("init")
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	rel := serveLuvus(t, h, false)
	has(t, "second init", h.ok("init"), "luvus: 0.14.3 (pinned)")
	if _, errOut, code := h.run("luvus", "pin"); code != 2 || !strings.Contains(errOut, "luvus not found on PATH") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if got := rel.requests(); len(got) != 0 {
		t.Fatalf("asked %q", got)
	}
}

func TestLuvusPinDownloadsWhenNoneIsOnPathOrPinned(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_HOME"], h.cwd = "", t.TempDir()
	noLuvusOnPath(h, t)
	serveLuvus(t, h, true)
	if _, errOut, code := h.run("luvus", "pin"); code != 2 || !strings.Contains(errOut, "luvus not found on PATH (download failed: checksum mismatch for luvus-v0.14.3-") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(h.vars["SECONDHAND_HOME"], "luvus", "pin.json")); err == nil {
		t.Fatal("an unverified luvus was pinned")
	}
	serveLuvus(t, h, false)
	has(t, "pin", h.ok("luvus", "pin"), "version: 0.14.3", "previous: none")
}
