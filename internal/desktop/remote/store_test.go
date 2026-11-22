package remote

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	cookieA = "1.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cookieB = "22.fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	fleet2  = "f00000000000a"
	fleet3  = "f00000000000b"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state", "remote")
	return OpenStore(dir), dir
}

func hostRec(id string) Host {
	return Host{ID: id, Relay: "https://relay.example.test/", Label: "Laptop", PairedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)}
}

func mustAdd(t *testing.T, s *Store, h Host, f, cookie string) {
	t.Helper()
	if err := s.Add(h, Fleet{ID: f, Name: "Fleet " + f}, cookie, nil); err != nil {
		t.Fatal(err)
	}
}

func unixOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no mode bits")
	}
}

func TestAHostAndItsFleetsComeBackAndOnlyTheCookieFileHoldsTheCookie(t *testing.T) {
	s, dir := newStore(t)
	mustAdd(t, s, hostRec(hostA), flt, cookieA)
	mustAdd(t, s, hostRec(hostA), fleet2, cookieB)
	hosts, err := s.Hosts()
	if err != nil || len(hosts) != 1 || hosts[0].ID != hostA || hosts[0].Relay != "https://relay.example.test/" || hosts[0].Label != "Laptop" || len(hosts[0].Fleets) != 2 || !hosts[0].PairedAt.Equal(hostRec(hostA).PairedAt) {
		t.Fatalf("hosts = %+v, %v", hosts, err)
	}
	for _, f := range []struct{ id, want string }{{flt, cookieA}, {fleet2, cookieB}} {
		if got, err := s.Cookie(hostA, f.id); err != nil || got != f.want {
			t.Fatalf("cookie of %s = %q, %v", f.id, got, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		holds := strings.Contains(string(raw), cookieA[2:]) || strings.Contains(string(raw), cookieB[3:])
		if holds != (e.Name() == "cookies.json") {
			t.Errorf("%s holds a cookie: %v", e.Name(), holds)
		}
	}
}

func TestTheStateIsPrivateOnDisk(t *testing.T) {
	unixOnly(t)
	s, dir := newStore(t)
	mustAdd(t, s, hostRec(hostA), flt, cookieA)
	keys, err := s.KeyDir(hostA)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, keys: 0o700, filepath.Join(dir, "cookies.json"): 0o600, filepath.Join(dir, "hosts.json"): 0o600} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s is %v, %v; want %v", path, fi, err, want)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".remote-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestACookieFileOthersCanReadIsRefused(t *testing.T) {
	unixOnly(t)
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660, 0o620, 0o602, 0o601, 0o610} {
		s, dir := newStore(t)
		mustAdd(t, s, hostRec(hostA), flt, cookieA)
		path := filepath.Join(dir, "cookies.json")
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		got, err := s.Cookie(hostA, flt)
		if !errors.Is(err, ErrPermissions) || got != "" {
			t.Errorf("mode %v: cookie %q, error %v", mode, got, err)
		}
		if err := s.Add(hostRec(hostB), Fleet{ID: fleet2}, cookieB, nil); !errors.Is(err, ErrPermissions) {
			t.Errorf("mode %v: Add error %v", mode, err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Cookie(hostA, flt); err != nil || got != cookieA {
			t.Errorf("after chmod 600: %q, %v", got, err)
		}
	}
}

func TestACookieIsLookedUpOnItsOwnHostOnly(t *testing.T) {
	s, _ := newStore(t)
	mustAdd(t, s, hostRec(hostA), flt, cookieA)
	mustAdd(t, s, hostRec(hostB), fleet2, cookieB)
	for _, c := range []struct{ host, fleet, want string }{
		{hostA, flt, cookieA}, {hostB, fleet2, cookieB}, {hostB, flt, ""}, {hostA, fleet2, ""}, {"", flt, ""}, {hostA, "", ""},
	} {
		if got, err := s.Cookie(c.host, c.fleet); err != nil || got != c.want {
			t.Errorf("Cookie(%.4s, %s) = %q, %v; want %q", c.host, c.fleet, got, err, c.want)
		}
	}
}

func TestAFleetAlreadyHeldIsRefusedWholeAndChangesNothing(t *testing.T) {
	s, dir := newStore(t)
	mustAdd(t, s, hostRec(hostA), flt, cookieA)
	before, _ := os.ReadFile(filepath.Join(dir, "hosts.json"))
	cases := []struct {
		name  string
		host  Host
		fleet string
		local func(string) bool
	}{
		{"on another host", hostRec(hostB), flt, nil},
		{"on the same host", hostRec(hostA), flt, nil},
		{"in a local folder", hostRec(hostB), fleet2, func(id string) bool { return id == fleet2 }},
	}
	for _, c := range cases {
		if err := s.Add(c.host, Fleet{ID: c.fleet}, cookieB, c.local); !errors.Is(err, ErrFleetHeld) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "hosts.json"))
	if string(before) != string(after) {
		t.Errorf("hosts.json changed:\n%s\n%s", before, after)
	}
	if got, _ := s.Cookie(hostA, flt); got != cookieA {
		t.Errorf("the held fleet's cookie is %q", got)
	}
	if got, _ := s.Cookie(hostB, fleet2); got != "" {
		t.Errorf("a refused fleet left a cookie: %q", got)
	}
	if err := s.Add(hostRec(hostB), Fleet{ID: fleet2}, cookieB, func(string) bool { return false }); err != nil {
		t.Errorf("a free fleet: %v", err)
	}
}

func TestMalformedInputIsRefusedBeforeItTouchesAPath(t *testing.T) {
	s, dir := newStore(t)
	for _, c := range []struct {
		host   string
		fleet  string
		cookie string
	}{
		{"../x", flt, cookieA}, {hostA[1:], flt, cookieA}, {strings.ToUpper(hostA), flt, cookieA},
		{hostA, "f0123", cookieA}, {hostA, flt, "0.abc"}, {hostA, flt, "1." + strings.Repeat("A", 64)}, {hostA, flt, ""},
	} {
		if err := s.Add(Host{ID: c.host}, Fleet{ID: c.fleet}, c.cookie, nil); !errors.Is(err, ErrBad) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	for _, bad := range []string{"../x", "", "a/b", hostA + "/.."} {
		if _, err := s.KeyDir(bad); !errors.Is(err, ErrBad) {
			t.Errorf("KeyDir(%q): %v", bad, err)
		}
		if err := s.Forget(bad); !errors.Is(err, ErrBad) {
			t.Errorf("Forget(%q): %v", bad, err)
		}
		if err := s.Remove(bad, flt); !errors.Is(err, ErrBad) {
			t.Errorf("Remove(%q): %v", bad, err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a refusal made %s: %v", dir, err)
	}
}

func TestRemovingOneFleetKeepsTheOthersAndTheLastOneTakesTheHostAndItsKey(t *testing.T) {
	s, dir := newStore(t)
	mustAdd(t, s, hostRec(hostA), flt, cookieA)
	mustAdd(t, s, hostRec(hostA), fleet2, cookieB)
	mustAdd(t, s, hostRec(hostB), fleet3, cookieB)
	keyA, _ := s.KeyDir(hostA)
	keyB, _ := s.KeyDir(hostB)
	if err := os.WriteFile(filepath.Join(keyA, keyFile), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(hostA, flt); err != nil {
		t.Fatal(err)
	}
	hosts, _ := s.Hosts()
	if len(hosts) != 2 || len(hosts[0].Fleets) != 1 || hosts[0].Fleets[0].ID != fleet2 {
		t.Fatalf("hosts = %+v", hosts)
	}
	if got, _ := s.Cookie(hostA, flt); got != "" {
		t.Errorf("the removed fleet keeps its cookie %q", got)
	}
	if got, _ := s.Cookie(hostA, fleet2); got != cookieB {
		t.Errorf("the other fleet lost its cookie: %q", got)
	}
	if _, err := os.Stat(filepath.Join(keyA, keyFile)); err != nil {
		t.Errorf("the key went with a fleet that was not the last: %v", err)
	}
	if err := s.Remove(hostA, flt); !errors.Is(err, ErrUnknown) {
		t.Errorf("removing twice: %v", err)
	}
	if err := s.Remove(hostA, fleet2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keyA); !os.IsNotExist(err) {
		t.Errorf("the last fleet left the key folder: %v", err)
	}
	if _, err := os.Stat(keyB); err != nil {
		t.Errorf("another host's key folder went: %v", err)
	}
	if hosts, _ = s.Hosts(); len(hosts) != 1 || hosts[0].ID != hostB {
		t.Errorf("hosts = %+v", hosts)
	}
	if got, _ := s.Cookie(hostB, fleet3); got != cookieB {
		t.Errorf("another host lost its cookie")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

func TestForgetDeletesTheHostItsCookiesAndItsKeyAndAnAbortedPairingToo(t *testing.T) {
	s, dir := newStore(t)
	mustAdd(t, s, hostRec(hostA), flt, cookieA)
	mustAdd(t, s, hostRec(hostA), fleet2, cookieA)
	mustAdd(t, s, hostRec(hostB), fleet3, cookieB)
	keyA, _ := s.KeyDir(hostA)
	if err := s.Forget(hostA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keyA); !os.IsNotExist(err) {
		t.Errorf("the key folder stays: %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "cookies.json")); strings.Contains(string(raw), cookieA[2:]) || !strings.Contains(string(raw), cookieB[3:]) {
		t.Errorf("cookies.json = %s", raw)
	}
	if hosts, _ := s.Hosts(); len(hosts) != 1 || hosts[0].ID != hostB {
		t.Errorf("hosts = %+v", hosts)
	}
	started, _ := s.KeyDir(hostA)
	if err := s.Forget(hostA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(started); !os.IsNotExist(err) {
		t.Errorf("a key folder with no host record stays: %v", err)
	}
}

func TestAHandEditedFileThatIsNotValidIsAnErrorNotAnEmptyStore(t *testing.T) {
	s, dir := newStore(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hosts.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hosts(); err == nil {
		t.Error("an invalid hosts.json read as empty")
	}
	if err := s.Add(hostRec(hostA), Fleet{ID: flt}, cookieA, nil); err == nil {
		t.Error("Add overwrote an invalid hosts.json")
	}
	if err := os.WriteFile(filepath.Join(dir, "hosts.json"), []byte(strings.Repeat(" ", maxFile+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Hosts(); err == nil {
		t.Error("an oversized hosts.json was read")
	}
}

func TestTwoAddsAtOnceKeepBothFleets(t *testing.T) {
	s, _ := newStore(t)
	var wg sync.WaitGroup
	for i, f := range []string{flt, fleet2, fleet3} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			host := hostA
			if i == 2 {
				host = hostB
			}
			if err := s.Add(hostRec(host), Fleet{ID: f}, cookieA, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	n := 0
	hosts, _ := s.Hosts()
	for _, h := range hosts {
		n += len(h.Fleets)
	}
	if n != 3 {
		t.Fatalf("%d fleets held, want 3", n)
	}
}

func TestDirSitsBesideTheDraftsFolder(t *testing.T) {
	if got, want := Dir(filepath.Join("s", "hand-desktop", "drafts")), filepath.Join("s", "hand-desktop", "remote"); got != want {
		t.Fatalf("%q, want %q", got, want)
	}
	if Dir("") != "" {
		t.Fatal("no drafts folder, no remote folder")
	}
}
