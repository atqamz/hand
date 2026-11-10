package launcher

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(t *testing.T) (func(string) string, string) {
	t.Helper()
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
	pic := filepath.Join(data, "icons", "hicolor", "512x512", "apps", "hand.png")
	bin := filepath.Join(t.TempDir(), "hand-desktop")
	if err := Sync(getenv, bin); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(entry)
	for _, want := range []string{"Type=Application\n", "Name=Hand\n", "Exec=" + bin + "\n", "TryExec=" + bin + "\n", "Icon=hand\n", "Terminal=false\n", "Categories=Development;\n", "StartupWMClass=hand-desktop\n"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("entry lacks %q:\n%s", want, got)
		}
	}
	if b, _ := os.ReadFile(pic); !bytes.Equal(b, icon) {
		t.Fatal("icon not written")
	}
	old := time.Now().Add(-time.Hour).Round(time.Second)
	for _, p := range []string{entry, pic} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := Sync(getenv, bin); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{entry, pic} {
		if fi, _ := os.Stat(p); !fi.ModTime().Equal(old) {
			t.Fatalf("%s rewritten although unchanged", p)
		}
	}
	other := filepath.Join(t.TempDir(), "hand-desktop")
	if err := Sync(getenv, other); err != nil {
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
	if err := Sync(getenv, "/home/a b/bin/hand-desktop"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(data, "applications", "hand-desktop.desktop"))
	if !strings.Contains(string(got), "Exec=\"/home/a b/bin/hand-desktop\"\n") || !strings.Contains(string(got), "TryExec=/home/a b/bin/hand-desktop\n") {
		t.Fatalf("entry:\n%s", got)
	}
}

func TestRemoveDeletesBothAndIsIdempotent(t *testing.T) {
	getenv, data := env(t)
	if err := Remove(getenv); err != nil {
		t.Fatal(err)
	}
	if err := Sync(getenv, "/x/hand-desktop"); err != nil {
		t.Fatal(err)
	}
	if err := Remove(getenv); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(data, "applications", "hand-desktop.desktop"), filepath.Join(data, "icons", "hicolor", "512x512", "apps", "hand.png")} {
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
	if err := Sync(getenv, bin); err != nil {
		t.Fatal(err)
	}
	if s, _ := Check(getenv, bin); s != OK {
		t.Fatalf("synced = %s", s)
	}
	if s, _ := Check(getenv, "/y/hand-desktop"); s != Elsewhere {
		t.Fatalf("moved = %s", s)
	}
	if err := os.Remove(filepath.Join(data, "icons", "hicolor", "512x512", "apps", "hand.png")); err != nil {
		t.Fatal(err)
	}
	if s, _ := Check(getenv, bin); s != Missing {
		t.Fatalf("no icon = %s", s)
	}
}

func TestNeedsAnAbsoluteHome(t *testing.T) {
	if err := Sync(func(string) string { return "" }, "/x/hand-desktop"); err == nil {
		t.Fatal("no home accepted")
	}
}

func TestIconIsTheBoardsIcon(t *testing.T) {
	b, err := os.ReadFile("../board/static/icon-512.png")
	if err != nil || !bytes.Equal(b, icon) {
		t.Fatalf("launcher icon differs from the Board's icon-512.png: %v", err)
	}
}
