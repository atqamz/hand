package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSwapReplacesTarget(t *testing.T) {
	dir := t.TempDir()
	target, src := filepath.Join(dir, "hand"), filepath.Join(dir, "new")
	for path, body := range map[string]string{target: "current", src: "next"} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := swap(src, target); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "next" {
		t.Fatalf("target = %q, %v", b, err)
	}
	CleanOld(target)
}
