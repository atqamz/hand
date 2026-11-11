package update

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCleanOldRemovesTheSidecarsAndTheWindowsOldCopiesToo(t *testing.T) {
	dir := t.TempDir()
	random := rand.Text()
	hand := filepath.Join(dir, "hand.exe")
	for _, name := range []string{
		"hand.exe", "hand.exe.old", "hand-iroh.exe", "hand-iroh.exe.old", "hand-iroh.exe.old-" + random,
		"hand-desktop.exe", "hand-desktop.exe.old", "hand-desktop.exe.old-" + random, "other.exe.old",
		"hand-desktop.exe.older", "hand.exe.old-notes.txt",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	folder := "hand-desktop.exe.old-" + rand.Text()
	if err := os.Mkdir(filepath.Join(dir, folder), 0o755); err != nil {
		t.Fatal(err)
	}
	cleanOld(hand)
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	slices.Sort(left)
	want := []string{"hand-desktop.exe", "hand-desktop.exe.older", folder, "hand-iroh.exe", "hand.exe", "hand.exe.old-notes.txt", "other.exe.old"}
	slices.Sort(want)
	if !slices.Equal(left, want) {
		t.Fatalf("left %v, want %v", left, want)
	}
}
