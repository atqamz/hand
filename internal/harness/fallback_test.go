package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

func fakeNpmMain(args []string) int {
	p := fakebin.Params()
	fakebin.Append(p["count"], "x")
	if d, err := time.ParseDuration(p["sleep"]); err == nil {
		time.Sleep(d)
	}
	if p["fail"] != "" || strings.Join(args, " ") != "prefix -g" {
		return 1
	}
	fmt.Println(p["prefix"])
	return 0
}

func exeFile(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

type places struct {
	path, prefix, data, home string
	env                      map[string]string
}

func newPlaces(t *testing.T) *places {
	root := t.TempDir()
	p := &places{path: filepath.Join(root, "path"), prefix: filepath.Join(root, "npm"), data: filepath.Join(root, "data"), home: filepath.Join(root, "home")}
	p.env = map[string]string{"NPM_CONFIG_PREFIX": p.prefix, "XDG_DATA_HOME": p.data, "HOME": p.home, "USERPROFILE": p.home}
	for _, d := range []string{p.path, p.home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func (p *places) npm() string {
	if runtime.GOOS == "windows" {
		return p.prefix
	}
	return filepath.Join(p.prefix, "bin")
}

func (p *places) gh() string { return filepath.Join(p.data, "gh", "copilot") }

func (p *places) find(name string) (string, error) {
	return Find(name, Env{Path: p.path, Getenv: func(k string) string { return p.env[k] }})
}

func TestFindLooksInPathThenNpmThenGh(t *testing.T) {
	p := newPlaces(t)
	if _, err := p.find("copilot"); !errors.Is(err, state.ErrInvalid) || !strings.Contains(err.Error(), "copilot not found on PATH") {
		t.Fatalf("nothing installed: err = %v", err)
	}
	gh := exeFile(t, p.gh(), "copilot")
	npm := exeFile(t, p.npm(), "copilot")
	onPath := exeFile(t, p.path, "copilot")
	for _, tc := range []struct{ name, want string }{{"PATH, npm and gh", onPath}, {"npm and gh", npm}, {"gh only", gh}} {
		got, err := p.find("copilot")
		if err != nil || got != tc.want {
			t.Fatalf("%s: found %s, %v; want %s", tc.name, got, err, tc.want)
		}
		if err := os.Remove(tc.want); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.find("copilot"); err == nil {
		t.Fatal("found copilot after every copy was removed")
	}
}

func TestFindFallsBackForCopilotOnly(t *testing.T) {
	p := newPlaces(t)
	exeFile(t, p.gh(), "claude")
	exeFile(t, p.npm(), "claude")
	if got, err := p.find("claude"); err == nil {
		t.Fatalf("claude found as %s outside PATH", got)
	}
}

func TestFindIgnoresADirectoryNamedLikeTheBinary(t *testing.T) {
	p := newPlaces(t)
	name := "copilot"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.MkdirAll(filepath.Join(p.gh(), name), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := p.find("copilot"); err == nil {
		t.Fatalf("found a directory as %s", got)
	}
}

func TestFindReadsTheGhFolderFromHomeWhenXdgIsUnset(t *testing.T) {
	p := newPlaces(t)
	delete(p.env, "XDG_DATA_HOME")
	want := exeFile(t, filepath.Join(p.home, ".local", "share", "gh", "copilot"), "copilot")
	if got, err := p.find("copilot"); err != nil || got != want {
		t.Fatalf("found %s, %v; want %s", got, err, want)
	}
}

func TestFindHonoursBothSpellingsOfTheNpmPrefix(t *testing.T) {
	p := newPlaces(t)
	delete(p.env, "NPM_CONFIG_PREFIX")
	p.env["npm_config_prefix"] = p.prefix
	want := exeFile(t, p.npm(), "copilot")
	if got, err := p.find("copilot"); err != nil || got != want {
		t.Fatalf("found %s, %v; want %s", got, err, want)
	}
}

func TestGhCopilotDirFollowsGhsDataFolderRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{"linux home", "linux", map[string]string{"HOME": "/h"}, "/h/.local/share/gh/copilot"},
		{"linux xdg", "linux", map[string]string{"HOME": "/h", "XDG_DATA_HOME": "/x"}, "/x/gh/copilot"},
		{"macos ignores Library", "darwin", map[string]string{"HOME": "/Users/u"}, "/Users/u/.local/share/gh/copilot"},
		{"macos xdg", "darwin", map[string]string{"HOME": "/Users/u", "XDG_DATA_HOME": "/x"}, "/x/gh/copilot"},
		{"windows local app data", "windows", map[string]string{"LocalAppData": `C:\L`, "USERPROFILE": `C:\U`}, filepath.Join(`C:\L`, "GitHub CLI", "copilot")},
		{"windows xdg first", "windows", map[string]string{"LocalAppData": `C:\L`, "XDG_DATA_HOME": `C:\X`}, filepath.Join(`C:\X`, "gh", "copilot")},
		{"windows profile", "windows", map[string]string{"USERPROFILE": `C:\U`}, filepath.Join(`C:\U`, ".local", "share", "gh", "copilot")},
		{"linux ignores LocalAppData", "linux", map[string]string{"HOME": "/h", "LocalAppData": "/l"}, "/h/.local/share/gh/copilot"},
		{"no home", "linux", map[string]string{}, ""},
	} {
		if got := ghCopilotDir(func(k string) string { return tc.env[k] }, tc.goos); got != filepath.FromSlash(tc.want) {
			t.Errorf("%s: dir = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestGhFolderHoldsCopilotExeOnWindows(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "copilot.exe")
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := lookPathExt("copilot", dir, ".COM;.EXE;.BAT;.CMD"); err != nil || got != want {
		t.Fatalf("lookPathExt = %s, %v", got, err)
	}
}

func TestNpmBinDirIsThePrefixOnWindowsAndItsBinElsewhere(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ goos, prefix, want string }{
		{"linux", root, filepath.Join(root, "bin")},
		{"darwin", root, filepath.Join(root, "bin")},
		{"windows", root, root},
		{"linux", "relative", ""},
		{"linux", "", ""},
	} {
		if got := npmBinDir(tc.prefix, tc.goos); got != tc.want {
			t.Errorf("%s %q: dir = %q, want %q", tc.goos, tc.prefix, got, tc.want)
		}
	}
}

type npmLab struct {
	*places
	count string
}

func newNpmLab(t *testing.T, params map[string]string) *npmLab {
	p := newPlaces(t)
	delete(p.env, "NPM_CONFIG_PREFIX")
	l := &npmLab{p, filepath.Join(t.TempDir(), "count")}
	fakebin.Install(t, p.path, "npm", "npm", mergeParams(params, map[string]string{"prefix": p.prefix, "count": l.count}))
	return l
}

func (l *npmLab) spawns() int {
	b, _ := os.ReadFile(l.count)
	return strings.Count(string(b), "\n")
}

func TestFindAsksNpmForItsGlobalPrefixOnceBetweenCalls(t *testing.T) {
	l := newNpmLab(t, nil)
	want := exeFile(t, l.npm(), "copilot")
	for i := range 3 {
		if got, err := l.find("copilot"); err != nil || got != want {
			t.Fatalf("call %d: found %s, %v; want %s", i, got, err, want)
		}
	}
	if n := l.spawns(); n != 1 {
		t.Fatalf("npm ran %d times, want 1", n)
	}
}

func TestFindDoesNotSpawnNpmWhenPathHasCopilot(t *testing.T) {
	l := newNpmLab(t, nil)
	want := exeFile(t, l.path, "copilot")
	if got, err := l.find("copilot"); err != nil || got != want || l.spawns() != 0 {
		t.Fatalf("found %s, %v after %d spawns; want %s and none", got, err, l.spawns(), want)
	}
}

func TestFindDoesNotSpawnNpmWhenThePrefixIsSet(t *testing.T) {
	l := newNpmLab(t, nil)
	l.env["NPM_CONFIG_PREFIX"] = l.prefix
	want := exeFile(t, l.npm(), "copilot")
	if got, err := l.find("copilot"); err != nil || got != want || l.spawns() != 0 {
		t.Fatalf("found %s, %v after %d spawns; want %s and none", got, err, l.spawns(), want)
	}
}

func TestFindSkipsNpmThatFailsOrHangs(t *testing.T) {
	old := npmTimeout
	npmTimeout = 200 * time.Millisecond
	t.Cleanup(func() { npmTimeout = old })
	for name, params := range map[string]map[string]string{"failing": {"fail": "1"}, "slow": {"sleep": "10s"}} {
		l := newNpmLab(t, params)
		exeFile(t, l.npm(), "copilot")
		want := exeFile(t, l.gh(), "copilot")
		start := time.Now()
		got, err := l.find("copilot")
		if err != nil || got != want {
			t.Fatalf("%s npm: found %s, %v; want the gh copy %s", name, got, err, want)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("%s npm: took %v", name, d)
		}
		if _, err := l.find("copilot"); err != nil || l.spawns() != 1 {
			t.Fatalf("%s npm: second find err = %v after %d spawns, want the answer cached", name, err, l.spawns())
		}
	}
}
