//go:build unix

package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var realHand = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "hand-real")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "hand")
	cmd := exec.Command("go", "build", "-ldflags", "-X github.com/atqamz/hand/internal/cli.Channel=edge -X github.com/atqamz/hand/internal/cli.Commit=0123456789ab", "-o", path, "github.com/atqamz/hand")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", &exec.ExitError{Stderr: out, ProcessState: cmd.ProcessState}
	}
	return path, nil
})

func TestRunLeavesAFleetThatStillHasA07FileIntact(t *testing.T) {
	path, err := realHand()
	if err != nil {
		t.Fatalf("go build: %v: %s", err, err.(*exec.ExitError).Stderr)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f := newCycle(t)
	archive := tarball(t, "hand", string(b))
	s := sum(archive)
	f.srv.files["hand-linux-amd64.tar.gz"] = archive
	f.srv.files["hand-darwin-arm64.tar.gz"] = archive
	f.srv.files["checksums.txt"] = []byte(s + "  hand-linux-amd64.tar.gz\n" + s + "  hand-linux-arm64.tar.gz\n" + s + "  hand-darwin-arm64.tar.gz\n")
	f.setenv("SECONDHAND_HOME=" + f.root)
	marker := filepath.Join(f.alpha, "state", "hand.db")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("0.7"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != "updated" || rep.Failed {
		t.Fatalf("report = %+v", rep)
	}
	for _, r := range rep.Fleets {
		if r.Name == "alpha" && r.Init != "ok" {
			t.Fatalf("alpha init = %q", r.Init)
		}
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "0.7" {
		t.Fatalf("marker = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(f.alpha, "AGENTS.md")); err != nil {
		t.Fatalf("init did not refresh the fleet: %v", err)
	}
}
