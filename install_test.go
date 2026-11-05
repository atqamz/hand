//go:build unix

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
	return releaseOf(t, []string{"version: " + version}, mutate)
}

func tarball(t *testing.T, name string, script []byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(script))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(script); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return archive.Bytes()
}

func irohAssets(t *testing.T, version string) func(map[string][]byte) {
	return func(files map[string][]byte) {
		archive := tarball(t, "hand-iroh", []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'hand-iroh "+version+" iroh 1.4.1'; fi\n"))
		sum := sha256.Sum256(archive)
		for _, stem := range []string{"hand-iroh-linux-amd64", "hand-iroh-linux-arm64", "hand-iroh-darwin-amd64", "hand-iroh-darwin-arm64"} {
			files[stem+".tar.gz"] = archive
			files[stem+".sha256"] = []byte(hex.EncodeToString(sum[:]) + "  " + stem + ".tar.gz\n")
		}
	}
}

func releaseOf(t *testing.T, lines []string, mutate func(files map[string][]byte)) *release {
	t.Helper()
	printed := "printf '%s\\n'"
	for _, l := range lines {
		printed += " '" + l + "'"
	}
	archive := bytes.NewBuffer(tarball(t, "hand", []byte("#!/bin/sh\nif [ \"$1\" = version ]; then "+printed+"; fi\n")))
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
	archive := "hand-linux-amd64.tar.gz"
	failedArchive := fakeRelease(t, "9.9.9", func(f map[string][]byte) { f[archive] = nil })
	failedSums := fakeRelease(t, "9.9.9", func(f map[string][]byte) { delete(f, "checksums.txt") })
	cut := fakeRelease(t, "9.9.9", nil)
	cut.cut = archive
	for name, c := range map[string]struct {
		srv  *release
		file string
	}{"500": {failedArchive, archive}, "closed early": {cut, archive}, "no checksums": {failedSums, "checksums.txt"}} {
		dir, _, errOut, code := runInstall(t, c.srv, nil, "Linux", "x86_64")
		if want := "install.sh: download failed: " + c.srv.URL + "/latest/download/" + c.file + "\n"; code != 1 || errOut != want || len(installed(t, dir)) != 0 {
			t.Fatalf("%s: code %d %q, installed %q", name, code, errOut, installed(t, dir))
		}
	}
	dead := fakeRelease(t, "9.9.9", nil)
	dead.Close()
	dir, _, errOut, code := runInstall(t, dead, nil, "Linux", "x86_64")
	if want := "install.sh: download failed: " + dead.URL + "/latest/download/" + archive + "\n"; code != 1 || errOut != want || len(installed(t, dir)) != 0 {
		t.Fatalf("unreachable: code %d %q, installed %q", code, errOut, installed(t, dir))
	}
}

func TestInstallScriptRefusesAnUnwritableFolder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"under a file": filepath.Join(file, "bin"), "a file": file} {
		_, _, errOut, code := runInstall(t, fakeRelease(t, "9.9.9", nil), map[string]string{"HAND_INSTALL_DIR": dir}, "Linux", "x86_64")
		if code != 1 || !strings.HasPrefix(errOut, "install.sh: cannot ") || strings.Count(errOut, "\n") != 1 {
			t.Fatalf("%s: code %d %q", name, code, errOut)
		}
	}
}

func TestInstallScriptPrintsOneLineWhenAToolFails(t *testing.T) {
	for tool, want := range map[string]string{"mktemp": "install.sh: cannot create a temporary folder\n", "cp": "install.sh: cannot install "} {
		noisy := t.TempDir()
		if err := os.WriteFile(filepath.Join(noisy, tool), []byte("#!/bin/sh\necho '"+tool+": boom' >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		dir, _, errOut, code := runInstall(t, fakeRelease(t, "9.9.9", nil), map[string]string{"PATH": noisy + ":" + os.Getenv("PATH")}, "Linux", "x86_64")
		if code != 1 || !strings.HasPrefix(errOut, want) || strings.Count(errOut, "\n") != 1 || len(installed(t, dir)) != 0 {
			t.Fatalf("%s: code %d %q, installed %q", tool, code, errOut, installed(t, dir))
		}
	}
}

func TestInstallScriptWorksWithBusyboxSha256sum(t *testing.T) {
	real, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Skip("no sha256sum on this host")
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
	if _, _, errOut, code := runInstall(t, srv, nil, "FreeBSD", "amd64"); code == 0 || !strings.Contains(errOut, "install.sh: Hand runs on Linux, macOS and Windows only") {
		t.Fatalf("freebsd: code %d %q", code, errOut)
	}
	if _, _, errOut, code := runInstall(t, srv, nil, "Linux", "s390x"); code == 0 || !strings.Contains(errOut, "install.sh: unsupported architecture s390x") {
		t.Fatalf("s390x: code %d %q", code, errOut)
	}
}

func TestInstallScriptPointsWindowsShellsToInstallPs1(t *testing.T) {
	srv := fakeRelease(t, "9.9.9", nil)
	for _, system := range []string{"MINGW64_NT-10.0-26100", "MSYS_NT-10.0-26100", "CYGWIN_NT-10.0-26100"} {
		dir, _, errOut, code := runInstall(t, srv, nil, system, "x86_64")
		if code == 0 || !strings.Contains(errOut, "install.sh: on Windows, run install.ps1 in PowerShell instead: see https://github.com/atqamz/hand#windows") || len(installed(t, dir)) != 0 {
			t.Fatalf("%s: code %d %q, installed %q", system, code, errOut, installed(t, dir))
		}
	}
	if len(srv.asked()) != 0 {
		t.Fatalf("asked %q", srv.asked())
	}
}

func TestInstallScriptHintsAFolderMissingFromPath(t *testing.T) {
	srv := fakeRelease(t, "9.9.9", nil)
	dir, out, errOut, code := runInstall(t, srv, nil, "Linux", "x86_64")
	if want := dir + ` is not on PATH; add this line to your shell profile: export PATH="` + dir + `:$PATH"` + "\n"; code != 0 || !strings.HasSuffix(out, want) {
		t.Fatalf("off PATH: code %d %q, out %q", code, errOut, out)
	}
	on := t.TempDir()
	_, out, errOut, code = runInstall(t, srv, map[string]string{"HAND_INSTALL_DIR": on, "PATH": "/usr/bin:" + on + ":" + os.Getenv("PATH")}, "Linux", "x86_64")
	if code != 0 || out != "installed hand version: 9.9.9 to "+filepath.Join(on, "hand")+"\n" {
		t.Fatalf("on PATH: code %d %q, out %q", code, errOut, out)
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
	for _, tool := range []string{"curl", "tar", "gzip", "mktemp", "grep", "cp", "chmod", "mv", "mkdir", "rm", "head", "sed"} {
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
			if real, err = exec.LookPath("shasum"); err != nil {
				t.Fatal(err)
			}
			real += " -a 256"
		}
		stub := "#!/bin/sh\nif [ \"$1 $2\" != \"-a 256\" ]; then exit 9; fi\nshift 2\nexec " + real + " \"$@\"\n"
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

func irohRelease(t *testing.T, needs, ships string, mutate func(map[string][]byte)) *release {
	t.Helper()
	return releaseOf(t, []string{"version: 9.9.9", "iroh: " + needs}, func(files map[string][]byte) {
		if ships != "" {
			irohAssets(t, ships)(files)
		}
		if mutate != nil {
			mutate(files)
		}
	})
}

func seed(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func content(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallScriptInstallsTheSidecarNextToHand(t *testing.T) {
	srv := irohRelease(t, "0.1.0", "0.1.0", nil)
	dir, out, errOut, code := runInstall(t, srv, nil, "Linux", "x86_64")
	if code != 0 {
		t.Fatalf("code %d: %s", code, errOut)
	}
	sidecar := filepath.Join(dir, "hand-iroh")
	if fi, err := os.Stat(sidecar); err != nil || fi.Mode().Perm() != 0o755 || !slices.Equal(installed(t, dir), []string{"hand", "hand-iroh"}) {
		t.Fatalf("installed %q, %v", installed(t, dir), err)
	}
	if got, err := exec.Command(sidecar, "--version").Output(); err != nil || strings.TrimSpace(string(got)) != "hand-iroh 0.1.0 iroh 1.4.1" {
		t.Fatalf("hand-iroh --version = %q, %v", got, err)
	}
	for _, want := range []string{"installed hand-iroh 0.1.0 to " + sidecar + "\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("out %q lacks %q", out, want)
		}
	}
	for _, want := range []string{"/latest/download/hand-iroh-linux-amd64.tar.gz", "/latest/download/hand-iroh-linux-amd64.sha256"} {
		if !slices.Contains(srv.asked(), want) {
			t.Fatalf("asked %q, lacks %q", srv.asked(), want)
		}
	}
}

func TestInstallScriptReplacesAnOlderSidecar(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, "hand-iroh", "the old sidecar")
	seed(t, dir, "hand", "the old hand")
	_, _, errOut, code := runInstall(t, irohRelease(t, "0.1.0", "0.1.0", nil), map[string]string{"HAND_INSTALL_DIR": dir}, "Linux", "x86_64")
	if code != 0 || !strings.Contains(content(t, filepath.Join(dir, "hand-iroh")), "hand-iroh 0.1.0") || strings.Contains(content(t, filepath.Join(dir, "hand")), "old") {
		t.Fatalf("code %d %q, sidecar %q", code, errOut, content(t, filepath.Join(dir, "hand-iroh")))
	}
	if !slices.Equal(installed(t, dir), []string{"hand", "hand-iroh"}) {
		t.Fatalf("installed %q", installed(t, dir))
	}
}

func TestInstallScriptKeepsWorkingWhenTheReleaseHasNoSidecar(t *testing.T) {
	srv := irohRelease(t, "0.1.0", "", nil)
	dir, out, errOut, code := runInstall(t, srv, map[string]string{"HAND_INSTALL_VERSION": "edge"}, "Linux", "x86_64")
	if code != 0 || !slices.Equal(installed(t, dir), []string{"hand"}) || errOut != "" {
		t.Fatalf("code %d %q, installed %q", code, errOut, installed(t, dir))
	}
	if !strings.Contains(out, "this release has no hand-iroh-linux-amd64.tar.gz; phone access over iroh is not installed\n") || !strings.Contains(out, "installed hand ") || strings.Contains(out, "installed hand-iroh") {
		t.Fatalf("out %q", out)
	}
	if !slices.Contains(srv.asked(), "/download/edge/hand-iroh-linux-amd64.tar.gz") {
		t.Fatalf("asked %q", srv.asked())
	}
	old := t.TempDir()
	seed(t, old, "hand-iroh", "the old sidecar")
	if _, _, errOut, code := runInstall(t, srv, map[string]string{"HAND_INSTALL_DIR": old}, "Linux", "x86_64"); code != 0 || content(t, filepath.Join(old, "hand-iroh")) != "the old sidecar" {
		t.Fatalf("code %d %q: an installed sidecar was touched", code, errOut)
	}
}

func TestInstallScriptAsksForNoSidecarWhenHandKnowsNone(t *testing.T) {
	srv := fakeRelease(t, "9.9.9", irohAssets(t, "0.1.0"))
	dir, out, errOut, code := runInstall(t, srv, nil, "Linux", "x86_64")
	if code != 0 || !slices.Equal(installed(t, dir), []string{"hand"}) || strings.Contains(out, "iroh") {
		t.Fatalf("code %d %q, installed %q, out %q", code, errOut, installed(t, dir), out)
	}
	if slices.ContainsFunc(srv.asked(), func(p string) bool { return strings.Contains(p, "hand-iroh") }) {
		t.Fatalf("asked %q", srv.asked())
	}
}

func TestInstallScriptReadsTheSidecarVersionFromTheFirstLineOnly(t *testing.T) {
	for name, c := range map[string]struct {
		line string
		ok   bool
	}{
		"trailing text":              {"hand-iroh 0.1.0 incompatible", false},
		"no iroh token":              {"hand-iroh 0.1.0", false},
		"no relay":                   {"hand-iroh 0.1.0 iroh", false},
		"extra token":                {"hand-iroh 0.1.0 iroh 1.4.1 extra", false},
		"first line wins":            {"hand-iroh 0.1.0 iroh 1.4.1\nnot a version", true},
		"leading and trailing space": {"  hand-iroh 0.1.0 iroh 1.4.1  ", true},
	} {
		t.Run(name, func(t *testing.T) {
			srv := irohRelease(t, "0.1.0", "0.1.0", func(f map[string][]byte) {
				archive := tarball(t, "hand-iroh", []byte("#!/bin/sh\nprintf '%s\\n' '"+c.line+"'\n"))
				sum := sha256.Sum256(archive)
				f["hand-iroh-linux-amd64.tar.gz"] = archive
				f["hand-iroh-linux-amd64.sha256"] = []byte(hex.EncodeToString(sum[:]) + "  hand-iroh-linux-amd64.tar.gz\n")
			})
			dir, _, errOut, code := runInstall(t, srv, nil, "Linux", "x86_64")
			if c.ok {
				if code != 0 || !slices.Equal(installed(t, dir), []string{"hand", "hand-iroh"}) {
					t.Fatalf("code %d %q, installed %q", code, errOut, installed(t, dir))
				}
				return
			}
			if code != 1 || !strings.Contains(errOut, "is hand-iroh unknown, but this hand needs 0.1.0") || len(installed(t, dir)) != 0 {
				t.Fatalf("code %d %q, installed %q", code, errOut, installed(t, dir))
			}
		})
	}
}

func TestInstallScriptRefusesASidecarPathThatIsADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "hand-iroh"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, errOut, code := runInstall(t, irohRelease(t, "0.1.0", "0.1.0", nil), map[string]string{"HAND_INSTALL_DIR": dir}, "Linux", "x86_64")
	if want := "install.sh: cannot install " + filepath.Join(dir, "hand-iroh") + ": it is a directory\n"; code != 1 || errOut != want {
		t.Fatalf("code %d %q, want %q", code, errOut, want)
	}
	if fi, err := os.Stat(filepath.Join(dir, "hand-iroh")); err != nil || !fi.IsDir() {
		t.Fatalf("the sidecar path changed: %v, %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hand")); !os.IsNotExist(err) {
		t.Fatalf("hand was installed: %v", err)
	}
}

func TestInstallScriptKeepsTheOldBinariesWhenTheSidecarFailsItsChecks(t *testing.T) {
	const stem = "hand-iroh-linux-amd64"
	for name, c := range map[string]struct {
		mutate func(map[string][]byte)
		ships  string
		want   string
	}{
		"corrupt checksum": {func(f map[string][]byte) {
			f[stem+".sha256"] = []byte(strings.Repeat("0", 64) + "  " + stem + ".tar.gz\n")
		}, "0.1.0", "install.sh: checksum mismatch for " + stem + ".tar.gz\n"},
		"unlisted":         {func(f map[string][]byte) { f[stem+".sha256"] = []byte(strings.Repeat("0", 64) + "  other.tar.gz\n") }, "0.1.0", "install.sh: checksum mismatch for " + stem + ".tar.gz\n"},
		"no checksum file": {func(f map[string][]byte) { delete(f, stem+".sha256") }, "0.1.0", "install.sh: download failed: URL/latest/download/" + stem + ".sha256\n"},
		"a server error":   {func(f map[string][]byte) { f[stem+".tar.gz"] = nil }, "0.1.0", "install.sh: download failed: URL/latest/download/" + stem + ".tar.gz (HTTP 500)\n"},
		"another version":  {nil, "0.2.0", "install.sh: " + stem + ".tar.gz is hand-iroh 0.2.0, but this hand needs 0.1.0\n"},
	} {
		srv := irohRelease(t, "0.1.0", c.ships, c.mutate)
		dir := t.TempDir()
		seed(t, dir, "hand-iroh", "the old sidecar")
		seed(t, dir, "hand", "the old hand")
		_, _, errOut, code := runInstall(t, srv, map[string]string{"HAND_INSTALL_DIR": dir}, "Linux", "x86_64")
		if want := strings.ReplaceAll(c.want, "URL", srv.URL); code != 1 || errOut != want {
			t.Fatalf("%s: code %d %q, want %q", name, code, errOut, want)
		}
		if content(t, filepath.Join(dir, "hand")) != "the old hand" || content(t, filepath.Join(dir, "hand-iroh")) != "the old sidecar" || !slices.Equal(installed(t, dir), []string{"hand", "hand-iroh"}) {
			t.Fatalf("%s: an old binary changed or a file is left: %q", name, installed(t, dir))
		}
	}
}

func TestInstallScriptRefusesASidecarThatDoesNotRunAndInstallsNothing(t *testing.T) {
	srv := irohRelease(t, "0.1.0", "0.1.0", func(f map[string][]byte) {
		archive := tarball(t, "hand-iroh", []byte("#!/bin/sh\nexit 1\n"))
		sum := sha256.Sum256(archive)
		f["hand-iroh-linux-amd64.tar.gz"] = archive
		f["hand-iroh-linux-amd64.sha256"] = []byte(hex.EncodeToString(sum[:]) + "  hand-iroh-linux-amd64.tar.gz\n")
	})
	dir, _, errOut, code := runInstall(t, srv, nil, "Linux", "x86_64")
	if code != 1 || !strings.Contains(errOut, "hand-iroh unknown, but this hand needs 0.1.0") || len(installed(t, dir)) != 0 {
		t.Fatalf("code %d %q, installed %q", code, errOut, installed(t, dir))
	}
}

func TestInstallScriptInstallsTheSidecarForMacOS(t *testing.T) {
	srv := irohRelease(t, "0.1.0", "0.1.0", nil)
	dir, _, errOut, code := runInstall(t, srv, nil, "Darwin", "arm64")
	if code != 0 || !slices.Equal(installed(t, dir), []string{"hand", "hand-iroh"}) || !slices.Contains(srv.asked(), "/latest/download/hand-iroh-darwin-arm64.tar.gz") {
		t.Fatalf("code %d %q, asked %q", code, errOut, srv.asked())
	}
}

func TestInstallScriptNeedsNoToolBeyondTheOnesItAlreadyNamedForTheSidecar(t *testing.T) {
	dir, _, errOut, code := runInstall(t, irohRelease(t, "0.1.0", "0.1.0", nil), map[string]string{"PATH": bsdTools(t, true)}, "Darwin", "arm64")
	if code != 0 || !slices.Equal(installed(t, dir), []string{"hand", "hand-iroh"}) {
		t.Fatalf("code %d %q, installed %q", code, errOut, installed(t, dir))
	}
}
