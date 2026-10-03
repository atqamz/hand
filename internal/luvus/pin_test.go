package luvus_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus"
)

var pinnedAt = time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)

func fakeLuvus(t *testing.T, version, out string) string {
	t.Helper()
	return embedAt(t, filepath.Join(t.TempDir(), "luvus"), map[string]string{"version": version, "out": out})
}

func embedAt(t *testing.T, path string, params map[string]string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if err := os.WriteFile(path, fakebin.Embed(t, "luvus", params), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func sum(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestKeepCopiesAndPins(t *testing.T) {
	root, bin := t.TempDir(), fakeLuvus(t, "0.14.3", "")
	pin, err := luvus.Keep(context.Background(), root, bin, nil, pinnedAt)
	if err != nil {
		t.Fatal(err)
	}
	hash := sum(t, bin)
	want := luvus.Pin{Path: filepath.Join(root, "luvus", "0.14.3-"+hash[:8], "luvus"), SHA256: hash, Version: "0.14.3", Source: bin, PinnedAt: "2026-10-01T09:30:00Z"}
	if pin != want {
		t.Fatalf("pin = %+v, want %+v", pin, want)
	}
	if sum(t, pin.Path) != hash {
		t.Fatal("the copy differs from its source")
	}
	if got, ok, err := luvus.LoadPin(root); err != nil || !ok || got != want {
		t.Fatalf("LoadPin = %+v %v %v", got, ok, err)
	}
}

func TestKeepReusesTheSameBuild(t *testing.T) {
	root, bin := t.TempDir(), fakeLuvus(t, "0.14.3", "")
	first, err := luvus.Keep(context.Background(), root, bin, nil, pinnedAt)
	if err != nil {
		t.Fatal(err)
	}
	second, err := luvus.Keep(context.Background(), root, bin, nil, pinnedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.Path != first.Path || second.PinnedAt != "2026-10-01T10:30:00Z" {
		t.Fatalf("second = %+v, first = %+v", second, first)
	}
	dir := filepath.Base(filepath.Dir(first.Path))
	if got := names(t, filepath.Join(root, "luvus")); !slices.Equal(got, []string{dir, "pin.json"}) {
		t.Fatalf("store = %q", got)
	}
	if got := names(t, filepath.Dir(first.Path)); !slices.Equal(got, []string{"luvus"}) {
		t.Fatalf("copy dir = %q", got)
	}
}

func TestKeepSeparatesBuildsOfOneVersion(t *testing.T) {
	root := t.TempDir()
	release, err := luvus.Keep(context.Background(), root, fakeLuvus(t, "0.14.2", ""), nil, pinnedAt)
	if err != nil {
		t.Fatal(err)
	}
	edge, err := luvus.Keep(context.Background(), root, fakeLuvus(t, "0.14.2", "edge"), nil, pinnedAt)
	if err != nil {
		t.Fatal(err)
	}
	if release.Path == edge.Path || !strings.HasPrefix(filepath.Base(filepath.Dir(edge.Path)), "0.14.2-") {
		t.Fatalf("release = %s, edge = %s", release.Path, edge.Path)
	}
	if _, err := os.Stat(release.Path); err != nil {
		t.Fatalf("the release copy is gone: %v", err)
	}
}

func TestKeepRefusesWhatIsNotLuvus(t *testing.T) {
	dir := t.TempDir()
	hello := embedAt(t, filepath.Join(dir, "hello"), map[string]string{"out": "hello"})
	failing := embedAt(t, filepath.Join(dir, "failing"), map[string]string{"exit": "1"})
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("luvus 0.14.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, bin := range []string{hello, failing, plain, filepath.Join(dir, "missing")} {
		if _, err := luvus.Keep(context.Background(), root, bin, nil, pinnedAt); !errors.Is(err, luvus.ErrNotLuvus) {
			t.Fatalf("%s: err = %v", filepath.Base(bin), err)
		}
	}
	if _, ok, err := luvus.LoadPin(root); ok || err != nil {
		t.Fatalf("a refused binary left a pin: %v %v", ok, err)
	}
}

func TestVerifyNoticesAChangedCopy(t *testing.T) {
	pin, err := luvus.Keep(context.Background(), t.TempDir(), fakeLuvus(t, "0.14.3", ""), nil, pinnedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := pin.Verify(); err != nil {
		t.Fatalf("fresh pin: %v", err)
	}
	if err := os.Chmod(pin.Path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pin.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pin.Path, []byte("tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pin.Verify(); !errors.Is(err, luvus.ErrPinChanged) || !strings.Contains(err.Error(), "changed on disk; re-pin with") {
		t.Fatalf("rewritten: %v", err)
	}
	if err := os.Remove(pin.Path); err != nil {
		t.Fatal(err)
	}
	if err := pin.Verify(); !errors.Is(err, luvus.ErrPinChanged) {
		t.Fatalf("removed: %v", err)
	}
}

func TestKeepReadsTheVersionOfTheCopy(t *testing.T) {
	bin := embedAt(t, filepath.Join(t.TempDir(), "luvus"), map[string]string{"version": "0.14.2", "copy": "0.14.3"})
	pin, err := luvus.Keep(context.Background(), t.TempDir(), bin, nil, pinnedAt)
	if err != nil || pin.Version != "0.14.3" {
		t.Fatalf("pin = %+v, %v", pin, err)
	}
}

func TestAnUnreadablePinSaysHowToRecover(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "luvus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "luvus", "pin.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := luvus.LoadPin(root); !errors.Is(err, luvus.ErrPinChanged) || !strings.Contains(err.Error(), "re-pin with `hand luvus pin`") {
		t.Fatalf("err = %v", err)
	}
	dirRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dirRoot, "luvus", "pin.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := luvus.LoadPin(dirRoot); !errors.Is(err, luvus.ErrPinChanged) {
		t.Fatalf("a pin.json that cannot be read: %v", err)
	}
}

func TestLoadPinWithoutAPin(t *testing.T) {
	if pin, ok, err := luvus.LoadPin(t.TempDir()); pin != (luvus.Pin{}) || ok || err != nil {
		t.Fatalf("LoadPin = %+v %v %v", pin, ok, err)
	}
}
