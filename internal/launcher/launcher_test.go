package launcher

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/brand"
)

func env(t *testing.T) (func(string) string, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the launcher entry is Linux only")
	}
	data := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	return func(k string) string {
		if k == "XDG_DATA_HOME" {
			return data
		}
		return ""
	}, data
}

func TestSyncWritesRewritesAndSkips(t *testing.T) {
	getenv, data := env(t)
	entry := filepath.Join(data, "applications", "hand-desktop.desktop")
	pic := filepath.Join(data, "icons", "hicolor", "512x512", "apps", "hand-desktop.png")
	bin := filepath.Join(t.TempDir(), "hand-desktop")
	if _, err := Sync(getenv, bin); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(entry)
	for _, want := range []string{"Type=Application\n", "Name=Hand\n", "Exec=" + bin + "\n", "TryExec=" + bin + "\n", "Icon=hand-desktop\n", "X-Hand-Managed=true\n", "Terminal=false\n", "Categories=Development;\n", "StartupWMClass=hand-desktop\n"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("entry lacks %q:\n%s", want, got)
		}
	}
	if b, _ := os.ReadFile(pic); !bytes.Equal(b, brand.Icon512) {
		t.Fatal("icon not written")
	}
	old := time.Now().Add(-time.Hour).Round(time.Second)
	for _, p := range []string{entry, pic} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Sync(getenv, bin); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{entry, pic} {
		if fi, _ := os.Stat(p); !fi.ModTime().Equal(old) {
			t.Fatalf("%s rewritten although unchanged", p)
		}
	}
	other := filepath.Join(t.TempDir(), "hand-desktop")
	if _, err := Sync(getenv, other); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(entry); !strings.Contains(string(got), "Exec="+other+"\n") {
		t.Fatalf("path change not written:\n%s", got)
	}
	if left, _ := filepath.Glob(filepath.Join(data, "applications", ".hand-desktop.*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestSyncQuotesAPathWithSpaces(t *testing.T) {
	getenv, data := env(t)
	if _, err := Sync(getenv, "/home/a b/bin/hand-desktop"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(data, "applications", "hand-desktop.desktop"))
	if !strings.Contains(string(got), "Exec=\"/home/a b/bin/hand-desktop\"\n") || !strings.Contains(string(got), "TryExec=/home/a b/bin/hand-desktop\n") {
		t.Fatalf("entry:\n%s", got)
	}
}

func TestRemoveDeletesBothAndIsIdempotent(t *testing.T) {
	getenv, data := env(t)
	if _, err := Remove(getenv); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(getenv, "/x/hand-desktop"); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(getenv); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(data, "applications", "hand-desktop.desktop"), filepath.Join(data, "icons", "hicolor", "512x512", "apps", "hand-desktop.png")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s still there", p)
		}
	}
}

func TestCheck(t *testing.T) {
	getenv, data := env(t)
	bin := "/x/hand-desktop"
	if s, _ := Check(getenv, bin); s != Missing {
		t.Fatalf("fresh = %s", s)
	}
	if _, err := Sync(getenv, bin); err != nil {
		t.Fatal(err)
	}
	if s, _ := Check(getenv, bin); s != OK {
		t.Fatalf("synced = %s", s)
	}
	if s, _ := Check(getenv, "/y/hand-desktop"); s != Elsewhere {
		t.Fatalf("moved = %s", s)
	}
	if err := os.Remove(filepath.Join(data, "icons", "hicolor", "512x512", "apps", "hand-desktop.png")); err != nil {
		t.Fatal(err)
	}
	if s, _ := Check(getenv, bin); s != Missing {
		t.Fatalf("no icon = %s", s)
	}
}

func TestNeedsAnAbsoluteHome(t *testing.T) {
	env(t)
	if _, err := Sync(func(string) string { return "" }, "/x/hand-desktop"); err == nil {
		t.Fatal("no home accepted")
	}
}

func TestForeignEntryAndIconSurvive(t *testing.T) {
	getenv, data := env(t)
	entry := filepath.Join(data, "applications", "hand-desktop.desktop")
	pic := filepath.Join(data, "icons", "hicolor", "512x512", "apps", "hand-desktop.png")
	for p, b := range map[string]string{entry: "[Desktop Entry]\nExec=/mine\n", pic: "mine"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if s, err := Sync(getenv, "/x/hand-desktop"); err != nil || s != entry {
		t.Fatalf("Sync = %q, %v", s, err)
	}
	if s, err := Remove(getenv); err != nil || s != entry {
		t.Fatalf("Remove = %q, %v", s, err)
	}
	if b, _ := os.ReadFile(entry); string(b) != "[Desktop Entry]\nExec=/mine\n" {
		t.Fatalf("entry changed: %q", b)
	}
	if b, _ := os.ReadFile(pic); string(b) != "mine" {
		t.Fatalf("icon changed: %q", b)
	}
	if s, _ := Check(getenv, "/x/hand-desktop"); s != Foreign {
		t.Fatalf("check = %s", s)
	}
}

func TestRemoveLeavesAForeignIconWithoutAnEntry(t *testing.T) {
	getenv, data := env(t)
	pic := filepath.Join(data, "icons", "hicolor", "512x512", "apps", "hand-desktop.png")
	if err := os.MkdirAll(filepath.Dir(pic), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pic, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(getenv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pic); err != nil {
		t.Fatal(err)
	}
}

func TestPathEscapingAndNewlines(t *testing.T) {
	getenv, data := env(t)
	if _, err := Sync(getenv, "/a\nb/hand-desktop"); err == nil {
		t.Fatal("newline accepted")
	}
	if _, err := Sync(getenv, `/a\b/hand-desktop`); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(data, "applications", "hand-desktop.desktop"))
	if !strings.Contains(string(got), `TryExec=/a\\b/hand-desktop`+"\n") {
		t.Fatalf("entry:\n%s", got)
	}
}
