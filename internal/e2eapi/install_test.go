package e2eapi_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const registry = "[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)"

func userPath(t *testing.T, shell string) string {
	t.Helper()
	script := "$k = " + registry + "; $k.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames); $k.Close()"
	res := run(t, shell, []string{"-NoProfile", "-NonInteractive", "-Command", script}, opts{env: toList(cleanEnv())})
	if res.code != 0 {
		t.Fatalf("cannot read the user PATH: %s", res.err)
	}
	return strings.TrimSpace(res.out)
}

func setUserPath(t *testing.T, shell, value string) {
	t.Helper()
	script := "$k = " + registry + "; $k.SetValue('Path', $env:E2E_PATH, [Microsoft.Win32.RegistryValueKind]::ExpandString); $k.Close()"
	env := cleanEnv()
	env["E2E_PATH"] = value
	res := run(t, shell, []string{"-NoProfile", "-NonInteractive", "-Command", script}, opts{env: toList(env)})
	if res.code != 0 {
		t.Fatalf("cannot restore the user PATH: %s", res.err)
	}
}

func installed(t *testing.T, b *box, dir, msg string) {
	t.Helper()
	res := run(t, filepath.Join(dir, "hand"+exe), []string{"version"}, opts{env: b.env(nil, nil, "")})
	v := toon(res.out).fields
	equal(t, []string{v["version"], v["channel"], v["commit"], v["schema"], v["luvus"], v["iroh"]}, []string{version, "stable", commit(t), schema, luvusTested, irohVersion}, msg+": hand version reports the candidate")
}

func TestInstall(t *testing.T) {
	c := enabled(t)
	good := serve(t, c.dist, "")
	bad := serve(t, c.dist, asset)
	badIroh := serve(t, c.dist, irohAsset)
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) { installShell(t, shell, good, bad, badIroh) })
	}
	for _, n := range []string{"install.sh", "install.ps1"} {
		got := read(filepath.Join(c.dist, n))
		want := strings.ReplaceAll(read(filepath.Join("..", "..", n)), "\r\n", "\n")
		check(t, got != "" && got == want, n+" served from dist is the file in the repository")
	}
}

func installShell(t *testing.T, shell string, good, bad, badIroh *server) {
	c := enabled(t)
	b := newBox(t, "install-"+shell)
	dir := filepath.Join(b.dir, "Hand Dir", "bin")
	target := filepath.Join(dir, "hand"+exe)
	before := ""
	if win {
		before = userPath(t, shell)
		defer func() { setUserPath(t, shell, before) }()
	}
	good.take()

	first := ok(t, install(t, shell, good.base(), dir, nil, b), shell+": the installer exits 0")
	matches(t, first.out, `(?m)^installed hand version: `+quote(version)+` to `+quote(target)+`\r?$`, shell+": it reports the version and the path")
	sumsName := "checksums.txt"
	if win {
		sumsName = ".sha256"
	}
	asked := good.take()
	check(t, slices.Contains(asked, "/latest/download/"+asset) && slices.ContainsFunc(asked, func(r string) bool {
		return strings.HasPrefix(r, "/latest/download/") && strings.HasSuffix(r, sumsName)
	}), shell+": it fetches the archive and its checksums from the latest release")
	if win {
		matches(t, first.out, `(?m)^added `+quote(dir)+` to your user PATH; open a new terminal to use it\r?$`, shell+": it adds the folder to the user PATH and says so")
		n := 0
		for _, p := range strings.Split(userPath(t, shell), ";") {
			if strings.TrimRight(p, `\`) == dir {
				n++
			}
		}
		equal(t, n, 1, shell+": the user PATH holds the folder once")
	} else {
		matches(t, first.out, `(?m)^`+quote(dir)+` is not on PATH; add this line to your shell profile: export PATH="`+quote(dir)+`:\$PATH"$`, shell+": it prints the PATH hint")
	}
	sidecar := filepath.Join(dir, "hand-iroh"+exe)
	matches(t, first.out, `(?m)^installed hand-iroh `+quote(irohVersion)+` to `+quote(sidecar)+`\r?$`, shell+": it reports the sidecar and its path")
	irohSum := regexp.MustCompile(`\.(tar\.gz|zip)$`).ReplaceAllString(irohAsset, ".sha256")
	check(t, slices.Contains(asked, "/latest/download/"+irohAsset) && slices.Contains(asked, "/latest/download/"+irohSum), shell+": it fetches the sidecar and its checksum from the same release")
	ran := run(t, sidecar, []string{"--version"}, opts{env: b.env(nil, nil, "")})
	matches(t, ran.out, `^hand-iroh `+quote(irohVersion)+` iroh `, shell+": the installed sidecar runs and reports the version hand needs")
	installed(t, b, dir, shell)

	again := ok(t, install(t, shell, good.base(), dir, nil, b, dir), shell+": a second install over the first exits 0")
	if win {
		lacks(t, again.out, `added .* to your user PATH`, shell+": the second install does not add the folder again")
		installed(t, b, dir, shell+" again")
		cleaned(t, dir, func() { installed(t, b, dir, shell+" probe") })
		equal(t, names(t, dir), []string{"hand-iroh" + exe, "hand" + exe}, shell+": the next hand run removed the old executables")
	} else {
		lacks(t, again.out, `is not on PATH`, shell+": no PATH hint when the folder is on PATH")
		equal(t, names(t, dir), []string{"hand", "hand-iroh"}, shell+": the folder holds only hand and hand-iroh, with no temporary file")
	}
	good.take()

	for _, o := range []struct {
		name, ver, prefix string
	}{{"a pinned version", "v" + version, "/download/v" + version + "/"}, {"the edge channel", "edge", "/download/edge/"}} {
		other := filepath.Join(b.dir, regexp.MustCompile(`\W+`).ReplaceAllString(o.name, "-"), "bin")
		res := ok(t, install(t, shell, good.base(), other, map[string]string{"HAND_INSTALL_VERSION": o.ver}, b), shell+": "+o.name+" installs")
		check(t, slices.ContainsFunc(good.take(), func(r string) bool { return strings.HasPrefix(r, o.prefix) && strings.HasSuffix(r, asset) }), shell+": "+o.name+" is fetched from "+o.prefix)
		installed(t, b, other, shell+": "+o.name)
		if o.ver == "edge" {
			check(t, !exists(filepath.Join(other, "hand-iroh"+exe)), shell+": the edge channel carries no sidecar, so none is installed")
			matches(t, res.out, `(?m)^this release has no `+quote(irohAsset)+`; Companion access over iroh is not installed\r?$`, shell+": the installer says the release has no sidecar")
		} else {
			check(t, exists(filepath.Join(other, "hand-iroh"+exe)), shell+": "+o.name+" installs the sidecar")
		}
	}

	hashes := []string{sha(t, filepath.Join(dir, "hand"+exe)), sha(t, filepath.Join(dir, "hand-iroh"+exe))}
	noSidecar := install(t, shell, badIroh.base(), dir, nil, b, dir)
	fails(t, noSidecar, shell+": a corrupted sidecar archive makes the installer exit 1", 1)
	matches(t, noSidecar.out+noSidecar.err, `install\.(sh|ps1): checksum mismatch for `+quote(irohAsset), shell+": it names the sidecar's checksum mismatch")
	equal(t, []string{sha(t, filepath.Join(dir, "hand"+exe)), sha(t, filepath.Join(dir, "hand-iroh"+exe))}, hashes, shell+": the installed hand and sidecar are the ones from before")

	refusedDir := filepath.Join(b.dir, "refused", "bin")
	res := install(t, shell, bad.base(), refusedDir, nil, b)
	fails(t, res, shell+": a corrupted archive makes the installer exit 1", 1)
	if win {
		file := run(t, shell, []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(c.dist, "install.ps1"), "-Dir", refusedDir, "-Base", bad.base()}, opts{env: toList(cleanEnv())})
		equal(t, file.code, 1, shell+": install.ps1 -File exits 1 on a corrupted archive")
		matches(t, file.out+file.err, `install\.ps1: checksum mismatch for `+quote(asset), shell+": install.ps1 -File names the checksum mismatch")
	}
	matches(t, res.out+res.err, `install\.(sh|ps1): checksum mismatch for `+quote(asset), shell+": it names the checksum mismatch")
	check(t, !exists(filepath.Join(refusedDir, "hand"+exe)), shell+": nothing is installed after the mismatch")

	if !win {
		unfetched := install(t, shell, good.base()+"/nope", refusedDir, nil, b)
		fails(t, unfetched, shell+": a failed download of install.sh makes the documented command exit non-zero", 22)
		check(t, !exists(filepath.Join(refusedDir, "hand"+exe)), shell+": nothing is installed after a failed download of install.sh")
	}

	if win {
		check(t, !exists(filepath.Join(os.Getenv("LOCALAPPDATA"), "hand")), shell+": HAND_INSTALL_DIR won over the default folder")
		left := []string{}
		for _, n := range names(t, os.TempDir()) {
			if regexp.MustCompile(`^hand-install-|^\.hand\.`).MatchString(n) {
				left = append(left, n)
			}
		}
		equal(t, left, []string{}, shell+": the installer left no temporary file")
		setUserPath(t, shell, before)
		equal(t, userPath(t, shell), before, shell+": the user PATH is back as it was")
		out := run(t, shell, []string{"-NoProfile", "-NonInteractive", "-Command", "[Environment]::GetEnvironmentVariable('HAND_INSTALL_REFRESH','User')"}, opts{env: toList(cleanEnv())})
		equal(t, strings.TrimSpace(out.out), "", shell+": no HAND_INSTALL_REFRESH variable is left in the user environment")
	} else {
		equal(t, names(t, b.home), []string{}, shell+": the installer left nothing in the home folder")
		equal(t, names(t, b.tmp), []string{}, shell+": the installer left no temporary file")
	}
}
