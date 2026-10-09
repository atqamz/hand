package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{"hand": fakeHandMain, "sleep": func([]string) int {
		time.Sleep(5 * time.Minute)
		return 0
	}, "luvus": func(args []string) int {
		if len(args) > 1 && args[0] == "session" && args[1] == "list" {
			b, _ := json.Marshal(map[string]any{"sessions": []map[string]any{{"name": os.Getenv("HAND_SESSION"), "session_dir": os.Getenv("HAND_SESSION_DIR"), "endpoint": map[string]string{"address": "x"}}}})
			fmt.Println(string(b))
			return 0
		}
		fmt.Println("luvus " + fakebin.Params()["version"])
		return 0
	}})
	os.Exit(m.Run())
}

func fakeHandMain(args []string) int {
	if len(args) > 0 && args[0] == "version" {
		fmt.Print(fakebin.Params()["version"])
		return 0
	}
	if calls := os.Getenv("HAND_CALLS"); calls != "" {
		wd, _ := os.Getwd()
		fakebin.Append(calls, "hand "+strings.Join(args, " ")+" @ "+wd)
	}
	if fail := os.Getenv("HAND_FAIL"); fail != "" && len(args) > 1 && args[0]+" "+args[1] == fail {
		fmt.Fprintln(os.Stderr, "stop failed")
		return 1
	}
	code, _ := strconv.Atoi(os.Getenv("HAND_EXIT"))
	return code
}

func hostTarget() (goos, arch string) {
	if runtime.GOOS == "windows" {
		return "windows", "amd64"
	}
	return "linux", "amd64"
}

func handAsset() string {
	goos, arch := hostTarget()
	ext, _ := packaging(goos)
	return "hand-" + goos + "-" + arch + ext
}

func handSums() string {
	if runtime.GOOS == "windows" {
		return "hand-windows-amd64.sha256"
	}
	return "checksums.txt"
}

func luvusAsset(version string) string {
	goos, _ := hostTarget()
	ext, _ := packaging(goos)
	return "luvus-v" + version + "-" + luvusTriples[goos+"/amd64"] + ext
}

type server struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	paths []string
}

func (s *server) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.paths)
}

func (s *server) set(name string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = body
}

func release(t *testing.T, files map[string][]byte) *server {
	t.Helper()
	s := &server{files: files}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, req.URL.Path)
		body, ok := s.files[filepath.Base(req.URL.Path)]
		s.mu.Unlock()
		switch {
		case !ok:
			http.NotFound(w, req)
		case body == nil:
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

var tarballs = struct {
	sync.Mutex
	m map[[2]string][]byte
}{m: map[[2]string][]byte{}}

func tarball(t *testing.T, name, body string) []byte {
	t.Helper()
	tarballs.Lock()
	defer tarballs.Unlock()
	if b, ok := tarballs.m[[2]string{name, body}]; ok {
		return b
	}
	var buf bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&buf, gzip.NoCompression)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	tarballs.m[[2]string{name, body}] = buf.Bytes()
	return buf.Bytes()
}

func zipball(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func handScript(t *testing.T, version, channel, commit, schema, luvus string) string {
	t.Helper()
	lines := "version: " + version + "\nchannel: " + channel + "\ncommit: " + commit + "\n"
	if schema != "" {
		lines += "schema: " + schema + "\n"
	}
	lines += "luvus: " + luvus + "\n"
	return string(fakebin.Embed(t, "hand", map[string]string{"version": lines}))
}

func fakeHand(t *testing.T, version, channel, commit, schema, luvus string) map[string][]byte {
	t.Helper()
	script := handScript(t, version, channel, commit, schema, luvus)
	archive := tarball(t, "hand", script)
	archiveZip := zipball(t, "hand.exe", script)
	s := sum(archive)
	return map[string][]byte{
		"hand-linux-amd64.tar.gz":   archive,
		"hand-darwin-arm64.tar.gz":  archive,
		"checksums.txt":             []byte(s + "  hand-linux-amd64.tar.gz\n" + s + "  hand-linux-arm64.tar.gz\n" + s + "  hand-darwin-arm64.tar.gz\n"),
		"hand-windows-amd64.zip":    archiveZip,
		"hand-windows-amd64.sha256": []byte(sum(archiveZip) + "  hand-windows-amd64.zip\n"),
	}
}

func fakeLuvus(t *testing.T, version string) map[string][]byte {
	t.Helper()
	script := string(fakebin.Embed(t, "luvus", map[string]string{"version": version}))
	archive := tarball(t, "luvus", script)
	files := map[string][]byte{}
	winZip := zipball(t, "luvus.exe", script)
	winName := "luvus-v" + version + "-x86_64-pc-windows-msvc"
	files[winName+".zip"] = winZip
	files[winName+".sha256"] = []byte(sum(winZip) + "  " + winName + ".zip\n")
	for _, triple := range []string{"x86_64-unknown-linux-musl", "aarch64-apple-darwin"} {
		name := "luvus-v" + version + "-" + triple
		files[name+".tar.gz"] = archive
		files[name+".sha256"] = []byte(sum(archive) + "  " + name + ".tar.gz\n")
	}
	return files
}

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s exists (%v)", path, err)
	}
}

func TestFetchHandReadsTheBuild(t *testing.T) {
	srv := release(t, fakeHand(t, "0.9.0", "edge", "0123456789ab", "9", "0.14.4"))
	goos, arch := hostTarget()
	_, exe := packaging(goos)
	for channel, prefix := range map[string]string{"stable": "/latest/download/", "edge": "/download/edge/"} {
		dir := t.TempDir()
		b, err := FetchHand(context.Background(), srv.URL, channel, goos, arch, dir, os.Environ())
		if err != nil {
			t.Fatal(err)
		}
		if b != (Build{Path: filepath.Join(dir, "hand"+exe), Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: 9, Luvus: "0.14.4"}) {
			t.Fatalf("%s build = %+v", channel, b)
		}
		if !slices.Contains(srv.asked(), prefix+handAsset()) || !slices.Contains(srv.asked(), prefix+handSums()) {
			t.Fatalf("%s asked %q", channel, srv.asked())
		}
	}
}

func TestFetchHandReadsAWindowsZip(t *testing.T) {
	srv := release(t, fakeHand(t, "0.9.0", "edge", "0123456789ab", "9", "0.14.4"))
	dir := t.TempDir()
	b, err := FetchHand(context.Background(), srv.URL, "edge", "windows", "amd64", dir, os.Environ())
	if err != nil || b.Path != filepath.Join(dir, "hand.exe") || b.Version != "0.9.0" {
		t.Fatalf("build = %+v, %v", b, err)
	}
	if !slices.Contains(srv.asked(), "/download/edge/hand-windows-amd64.zip") || !slices.Contains(srv.asked(), "/download/edge/hand-windows-amd64.sha256") {
		t.Fatalf("asked %q", srv.asked())
	}
	files := fakeHand(t, "0.9.0", "edge", "0123456789ab", "9", "0.14.4")
	files["hand-windows-amd64.sha256"] = []byte(strings.Repeat("0", 64) + "  hand-windows-amd64.zip\n")
	other := t.TempDir()
	if _, err := FetchHand(context.Background(), release(t, files).URL, "edge", "windows", "amd64", other, os.Environ()); err == nil || !strings.Contains(err.Error(), "checksum mismatch for hand-windows-amd64.zip") {
		t.Fatalf("wrong sha256: err = %v", err)
	}
	absent(t, filepath.Join(other, "hand.exe"))
}

func TestFetchHandRefusesABadChecksum(t *testing.T) {
	for name, line := range map[string]string{"corrupt": strings.Repeat("0", 64) + "  hand-linux-amd64.tar.gz\n", "missing": strings.Repeat("0", 64) + "  other.tar.gz\n"} {
		files := fakeHand(t, "0.9.0", "edge", "0123456789ab", "9", "0.14.4")
		files["checksums.txt"] = []byte(line)
		dir := t.TempDir()
		if _, err := FetchHand(context.Background(), release(t, files).URL, "edge", "linux", "amd64", dir, os.Environ()); err == nil || !strings.Contains(err.Error(), "checksum mismatch for hand-linux-amd64.tar.gz") {
			t.Fatalf("%s: err = %v", name, err)
		}
		absent(t, filepath.Join(dir, "hand"))
	}
}

func TestFetchHandRefusesAFailedDownload(t *testing.T) {
	files := fakeHand(t, "0.9.0", "edge", "0123456789ab", "9", "0.14.4")
	files["hand-linux-amd64.tar.gz"] = nil
	dir := t.TempDir()
	if _, err := FetchHand(context.Background(), release(t, files).URL, "edge", "linux", "amd64", dir, os.Environ()); err == nil || !strings.Contains(err.Error(), ": 500") {
		t.Fatalf("err = %v", err)
	}
	absent(t, filepath.Join(dir, "hand"))
}

func TestFetchLuvusChecksItsSha256(t *testing.T) {
	srv := release(t, fakeLuvus(t, "0.14.4"))
	dir := t.TempDir()
	bin, err := FetchLuvus(context.Background(), srv.URL, "0.14.4", "linux", "amd64", dir)
	if err != nil || bin != filepath.Join(dir, "luvus") {
		t.Fatalf("FetchLuvus = %q, %v", bin, err)
	}
	if !slices.Contains(srv.asked(), "/download/v0.14.4/luvus-v0.14.4-x86_64-unknown-linux-musl.tar.gz") {
		t.Fatalf("asked %q", srv.asked())
	}
	srv.set("luvus-v0.14.4-x86_64-unknown-linux-musl.sha256", []byte(strings.Repeat("0", 64)+"  luvus-v0.14.4-x86_64-unknown-linux-musl.tar.gz\n"))
	other := t.TempDir()
	if _, err := FetchLuvus(context.Background(), srv.URL, "0.14.4", "linux", "amd64", other); err == nil || !strings.Contains(err.Error(), "checksum mismatch for luvus-v0.14.4-x86_64-unknown-linux-musl.tar.gz") {
		t.Fatalf("wrong sha256: err = %v", err)
	}
	absent(t, filepath.Join(other, "luvus"))
	if _, err := FetchLuvus(context.Background(), srv.URL, "0.14.4", "linux", "riscv64", t.TempDir()); err == nil || err.Error() != "update: no Luvus build for linux/riscv64" {
		t.Fatalf("riscv64: err = %v", err)
	}
	if _, err := FetchLuvus(context.Background(), srv.URL, "0.14.4", "darwin", "arm64", t.TempDir()); err != nil || !slices.Contains(srv.asked(), "/download/v0.14.4/luvus-v0.14.4-aarch64-apple-darwin.tar.gz") {
		t.Fatalf("darwin: err = %v, asked %q", err, srv.asked())
	}
	win := t.TempDir()
	if bin, err := FetchLuvus(context.Background(), srv.URL, "0.14.4", "windows", "amd64", win); err != nil || bin != filepath.Join(win, "luvus.exe") || !slices.Contains(srv.asked(), "/download/v0.14.4/luvus-v0.14.4-x86_64-pc-windows-msvc.zip") {
		t.Fatalf("windows: %q, %v, asked %q", bin, err, srv.asked())
	}
}

func TestNewerComparesNumbers(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.14.10", "0.14.9", true}, {"0.14.4", "", true}, {"0.14.4", "0.14.4", false}, {"0.14.3", "0.15.0", false}, {"x", "0.1.0", false}} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Fatalf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
