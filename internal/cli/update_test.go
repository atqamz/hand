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
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

func TestUpdateRefusesASourceBuildWithoutAChannel(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_INSTALL_BASE"] = "http://127.0.0.1:1"
	if out, errOut, code := h.run("update"); code != 2 || out != "" || !strings.Contains(errOut, "update: this hand was built from source; pass --channel edge or --channel stable") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestUpdateRefusesAnUnknownChannel(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_INSTALL_BASE"] = "http://127.0.0.1:1"
	if _, errOut, code := h.run("update", "--channel", "nightly"); code != 2 || !strings.Contains(errOut, "update: --channel must be edge or stable") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestUpdateTakesNoArguments(t *testing.T) {
	h := newHarness(t)
	h.vars["HAND_INSTALL_BASE"] = "http://127.0.0.1:1"
	if _, errOut, code := h.run("update", "--channel", "edge", "now"); code != 2 || !strings.Contains(errOut, "want 0 argument(s)") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestUpdateCheckReportsTheNewBuild(t *testing.T) {
	script := string(fakebin.Embed(t, "fake", map[string]string{"on version": "version: 0.9.0\nchannel: edge\ncommit: 0123456789ab\nschema: 8\nluvus: 0.14.4\n"}))
	files := map[string][]byte{}
	if runtime.GOOS == "windows" {
		var archive bytes.Buffer
		zw := zip.NewWriter(&archive)
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: "hand.exe", Method: zip.Store})
		_, _ = w.Write([]byte(script))
		_ = zw.Close()
		sum := sha256.Sum256(archive.Bytes())
		files["hand-windows-amd64.zip"] = archive.Bytes()
		files["hand-windows-amd64.sha256"] = []byte(hex.EncodeToString(sum[:]) + "  hand-windows-amd64.zip\n")
	} else {
		var archive bytes.Buffer
		gz, _ := gzip.NewWriterLevel(&archive, gzip.NoCompression)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: "hand", Mode: 0o755, Size: int64(len(script))})
		_, _ = tw.Write([]byte(script))
		_ = tw.Close()
		_ = gz.Close()
		sum := sha256.Sum256(archive.Bytes())
		var sums strings.Builder
		for _, name := range []string{"hand-linux-amd64.tar.gz", "hand-linux-arm64.tar.gz", "hand-darwin-amd64.tar.gz", "hand-darwin-arm64.tar.gz"} {
			files[name] = archive.Bytes()
			sums.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
		}
		files["checksums.txt"] = []byte(sums.String())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, ok := files[filepath.Base(r.URL.Path)]; ok && strings.HasPrefix(r.URL.Path, "/download/edge/") {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	h := newHarness(t)
	h.vars["HAND_INSTALL_BASE"] = srv.URL
	out, errOut, code := h.run("update", "--channel", "edge", "--check")
	if code != 0 || !strings.Contains(out, "status: checked\n") || !strings.Contains(out, "to: 0.9.0 edge 0123456789ab\n") || !strings.Contains(out, "luvus: none -> 0.14.4\n") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errOut)
	}
}
