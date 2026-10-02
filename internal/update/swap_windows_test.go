package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSwapWithLockedOld(t *testing.T) {
	dir := t.TempDir()
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
	CleanOld(target)
}
