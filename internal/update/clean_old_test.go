package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanOldRemovesTheSidecarsAndTheWindowsOldCopiesToo(t *testing.T) {
	dir := t.TempDir()
	hand := filepath.Join(dir, "hand.exe")
	for _, name := range []string{
		"hand.exe", "hand.exe.old", "hand-iroh.exe", "hand-iroh.exe.old", "hand-iroh.exe.old-abc",
		"hand-desktop.exe", "hand-desktop.exe.old", "hand-desktop.exe.old-abc", "other.exe.old",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cleanOld(hand)
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if strings.Join(left, ",") != "hand-desktop.exe,hand-iroh.exe,hand.exe,other.exe.old" {
		t.Fatalf("left %v", left)
	}
}
