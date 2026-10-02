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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type release struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
	cut   string
}

func (r *release) asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.paths)
}

func fakeRelease(t *testing.T, version string, mutate func(files map[string][]byte)) *release {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	script := []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo 'version: " + version + "'; fi\n")
	if err := tw.WriteHeader(&tar.Header{Name: "hand", Mode: 0o755, Size: int64(len(script))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(script); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	sum := sha256.Sum256(archive.Bytes())
	files := map[string][]byte{}
	var sums strings.Builder
	for _, name := range []string{"hand-linux-amd64.tar.gz", "hand-linux-arm64.tar.gz", "hand-darwin-amd64.tar.gz", "hand-darwin-arm64.tar.gz"} {
		files[name] = archive.Bytes()
		sums.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	}
	files["checksums.txt"] = []byte(sums.String())
	if mutate != nil {
		mutate(files)
	}
	r := &release{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.paths = append(r.paths, req.URL.Path)
		cut := r.cut == filepath.Base(req.URL.Path)
		r.mu.Unlock()
		body, ok := files[filepath.Base(req.URL.Path)]
		switch {
		case !ok:
			http.NotFound(w, req)
		case body == nil:
			http.Error(w, "boom", http.StatusInternalServerError)
		case cut:
			w.Header().Set("Content-Length", strconv.Itoa(2*len(body)))
			_, _ = w.Write(body)
		default:
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(r.Close)
	return r
}

func runInstall(t *testing.T, srv *release, env map[string]string, system, machine string) (dir, stdout, stderr string, code int) {
	t.Helper()
	stub := t.TempDir()
	uname := "#!/bin/sh\ncase \"$1\" in -s) echo " + system + ";; -m) echo " + machine + ";; esac\n"
	if err := os.WriteFile(filepath.Join(stub, "uname"), []byte(uname), 0o755); err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	cmd := exec.Command("sh", "install.sh")
	cmd.Env = append(os.Environ(), "PATH="+stub+":"+os.Getenv("PATH"), "HAND_INSTALL_BASE="+srv.URL, "HAND_INSTALL_DIR="+dir, "HAND_INSTALL_VERSION=", "no_proxy=*")
	for k, v := range env {
		if k == "PATH" {
			v = stub + ":" + v
		}
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code = 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return dir, out.String(), errOut.String(), code
}

func installed(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestInstallScriptInstallsTheLatestRelease(t *testing.T) {
	srv := fakeRelease(t, "9.9.9", nil)
	dir, out, errOut, code := runInstall(t, srv, nil, "Linux", "x86_64")
	if code != 0 {
		t.Fatalf("code %d: %s", code, errOut)
	}
	hand := filepath.Join(dir, "hand")
	if fi, err := os.Stat(hand); err != nil || fi.Mode().Perm() != 0o755 || !slices.Equal(installed(t, dir), []string{"hand"}) {
		t.Fatalf("installed %q, %v", installed(t, dir), err)
	}
	if got, err := exec.Command(hand, "version").Output(); err != nil || strings.TrimSpace(string(got)) != "version: 9.9.9" {
		t.Fatalf("hand version = %q, %v", got, err)
	}
	if !strings.Contains(out, "installed hand version: 9.9.9 to "+hand) || !slices.Contains(srv.asked(), "/latest/download/hand-linux-amd64.tar.gz") {
		t.Fatalf("out %q, asked %q", out, srv.asked())
	}
}

func TestInstallScriptInstallsEdgeOrATag(t *testing.T) {
	for version, want := range map[string]string{"edge": "/download/edge/hand-linux-arm64.tar.gz", "v0.9.0": "/download/v0.9.0/hand-linux-arm64.tar.gz"} {
		srv := fakeRelease(t, "9.9.9", nil)
		if _, _, errOut, code := runInstall(t, srv, map[string]string{"HAND_INSTALL_VERSION": version}, "Linux", "aarch64"); code != 0 || !slices.Contains(srv.asked(), want) {
			t.Fatalf("%s: code %d %s, asked %q", version, code, errOut, srv.asked())
		}
	}
}

func TestInstallScriptRefusesABadChecksum(t *testing.T) {
	for name, mutate := range map[string]func(map[string][]byte){
		"corrupt": func(f map[string][]byte) {
			f["checksums.txt"] = []byte(strings.Repeat("0", 64) + "  hand-linux-amd64.tar.gz\n")
		},
		"missing": func(f map[string][]byte) { f["checksums.txt"] = []byte(strings.Repeat("0", 64) + "  other.tar.gz\n") },
	} {
		dir, _, errOut, code := runInstall(t, fakeRelease(t, "9.9.9", mutate), nil, "Linux", "x86_64")
		if code == 0 || !strings.Contains(errOut, "install.sh: checksum mismatch for hand-linux-amd64.tar.gz") || len(installed(t, dir)) != 0 {
			t.Fatalf("%s: code %d %q, installed %q", name, code, errOut, installed(t, dir))
		}
	}
}

func TestInstallScriptRefusesAFailedDownload(t *testing.T) {
	failed := fakeRelease(t, "9.9.9", func(f map[string][]byte) { f["hand-linux-amd64.tar.gz"] = nil })
	cut := fakeRelease(t, "9.9.9", nil)
	cut.cut = "hand-linux-amd64.tar.gz"
	for name, srv := range map[string]*release{"500": failed, "closed early": cut} {
		dir, _, _, code := runInstall(t, srv, nil, "Linux", "x86_64")
		if code == 0 || len(installed(t, dir)) != 0 {
			t.Fatalf("%s: code %d, installed %q", name, code, installed(t, dir))
		}
	}
}

func TestInstallScriptWorksWithBusyboxSha256sum(t *testing.T) {
	real, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Fatal(err)
	}
	busybox := t.TempDir()
	stub := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = --status ] && { echo 'sha256sum: unrecognized option' >&2; exit 1; }; done\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(busybox, "sha256sum"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	dir, _, errOut, code := runInstall(t, fakeRelease(t, "9.9.9", nil), map[string]string{"PATH": busybox + ":" + os.Getenv("PATH")}, "Linux", "x86_64")
	if code != 0 || !slices.Equal(installed(t, dir), []string{"hand"}) {
		t.Fatalf("code %d %q, installed %q", code, errOut, installed(t, dir))
	}
}

func TestInstallScriptRefusesOtherPlatforms(t *testing.T) {
	srv := fakeRelease(t, "9.9.9", nil)
	if _, _, errOut, code := runInstall(t, srv, nil, "FreeBSD", "amd64"); code == 0 || !strings.Contains(errOut, "install.sh: Hand runs on Linux and macOS only") {
		t.Fatalf("freebsd: code %d %q", code, errOut)
	}
	if _, _, errOut, code := runInstall(t, srv, nil, "Linux", "s390x"); code == 0 || !strings.Contains(errOut, "install.sh: unsupported architecture s390x") {
		t.Fatalf("s390x: code %d %q", code, errOut)
	}
}

func TestInstallScriptInstallsForMacOS(t *testing.T) {
	srv := fakeRelease(t, "9.9.9", nil)
	dir, _, errOut, code := runInstall(t, srv, nil, "Darwin", "arm64")
	if code != 0 || !slices.Equal(installed(t, dir), []string{"hand"}) || !slices.Contains(srv.asked(), "/latest/download/hand-darwin-arm64.tar.gz") {
		t.Fatalf("code %d %q, asked %q", code, errOut, srv.asked())
	}
}

func bsdTools(t *testing.T, shasum bool) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"curl", "tar", "gzip", "mktemp", "grep", "cp", "chmod", "mv", "mkdir", "rm", "head"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(p, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if shasum {
		real, err := exec.LookPath("sha256sum")
		if err != nil {
			t.Fatal(err)
		}
		stub := "#!/bin/sh\nif [ \"$1\" = -a ] && [ \"$2\" = 256 ]; then shift 2; fi\nexec " + real + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(dir, "shasum"), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestInstallScriptUsesShasum(t *testing.T) {
	dir, _, errOut, code := runInstall(t, fakeRelease(t, "9.9.9", nil), map[string]string{"PATH": bsdTools(t, true)}, "Darwin", "arm64")
	if code != 0 || !slices.Equal(installed(t, dir), []string{"hand"}) {
		t.Fatalf("code %d %q, installed %q", code, errOut, installed(t, dir))
	}
}

func TestInstallScriptNeedsAChecksumTool(t *testing.T) {
	srv := fakeRelease(t, "9.9.9", nil)
	dir, _, errOut, code := runInstall(t, srv, map[string]string{"PATH": bsdTools(t, false)}, "Darwin", "arm64")
	if code == 0 || !strings.Contains(errOut, "install.sh: sha256sum or shasum is required") || len(srv.asked()) != 0 || len(installed(t, dir)) != 0 {
		t.Fatalf("code %d %q, asked %q", code, errOut, srv.asked())
	}
}
