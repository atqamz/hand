package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwapWithLockedOld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a[1]")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "hand.exe")
	src := filepath.Join(dir, "new.exe")
	for path, body := range map[string]string{target: "current", src: "next", target + ".old": "stale"} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	held, err := os.Open(target + ".old")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := swap(src, target); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "next" {
		t.Fatalf("target = %q, %v", b, err)
	}
	held.Close()
	CleanOld(target)
	left, _ := os.ReadDir(dir)
	for _, e := range left {
		if strings.Contains(e.Name(), ".old") {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
}

func TestCleanOldRemovesTheSidecarsOldCopiesToo(t *testing.T) {
	dir := t.TempDir()
	hand := filepath.Join(dir, "hand.exe")
	for _, name := range []string{"hand.exe", "hand.exe.old", "hand-iroh.exe", "hand-iroh.exe.old", "hand-iroh.exe.old-abc", "other.exe.old"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	CleanOld(hand)
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if strings.Join(left, ",") != "hand-iroh.exe,hand.exe,other.exe.old" {
		t.Fatalf("left %v", left)
	}
}
