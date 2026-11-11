package desktopasset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/atqamz/hand/internal/buildstamp"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/proc"
)

const Name = "hand-desktop"

var Version = "0.14.0"

var Commit = ""

const (
	versionMax = 4096
	reasonMax  = 200
)

var (
	ErrNotFound   = errors.New("hand-desktop not found")
	ErrNotDesktop = errors.New("not a hand-desktop binary")
	ErrVersion    = errors.New("hand-desktop version")
)

var versionLine = regexp.MustCompile(`^hand-desktop (\d+\.\d+\.\d+) (\S{1,64})$`)

func exeExt(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

func Supported(goos, goarch string) bool {
	return goos == "linux" && (goarch == "amd64" || goarch == "arm64")
}

func Unsupported(goos, goarch string) string {
	return "unsupported on " + goos + "/" + goarch + ": the window runs on Linux (amd64 and arm64) for now, and the Board works in a browser"
}

func Path(self, goos string) string {
	return filepath.Join(filepath.Dir(self), Name+exeExt(goos))
}

func Locate(self, path, goos string) (string, error) {
	next := Path(self, goos)
	if fi, err := os.Stat(next); err == nil && fi.Mode().IsRegular() && (goos == "windows" || fi.Mode()&0o111 != 0) {
		return next, nil
	}
	if bin, err := harness.LookPath(Name, path); err == nil {
		return bin, nil
	}
	if self == "" {
		return "", fmt.Errorf("%w: it is not on PATH", ErrNotFound)
	}
	return "", fmt.Errorf("%w: it is not next to hand (%s) or on PATH", ErrNotFound, filepath.Dir(self))
}

func Stamp() string {
	return Name + " " + Version + " " + buildstamp.Commit(Commit)
}

type capped struct{ b []byte }

func (c *capped) Write(p []byte) (int, error) {
	if room := versionMax - len(c.b); room > 0 {
		c.b = append(c.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func BinaryVersion(ctx context.Context, bin string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env, cmd.WaitDelay = []string{}, time.Second
	var out capped
	cmd.Stdout = &out
	proc.NoWindow(cmd)
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("%w: %s --version failed: %v", ErrNotDesktop, bin, err)
	}
	first, _, _ := strings.Cut(string(out.b), "\n")
	m := versionLine.FindStringSubmatch(strings.TrimSpace(first))
	if m == nil {
		return "", "", fmt.Errorf("%w: %s printed %q, not `hand-desktop X.Y.Z COMMIT`", ErrNotDesktop, bin, clean(first))
	}
	return m[1], m[2], nil
}

func Verify(ctx context.Context, bin, version, commit string) (string, error) {
	gotVersion, gotCommit, err := BinaryVersion(ctx, bin)
	if err != nil {
		return "", err
	}
	if gotVersion != version || gotCommit != commit {
		got := gotVersion + " " + gotCommit
		return got, fmt.Errorf("%w: %s is %s, but this hand is %s", ErrVersion, bin, got, version+" "+commit)
	}
	return gotVersion + " " + gotCommit, nil
}

func clean(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, s))
	if r := []rune(s); len(r) > reasonMax {
		s = string(r[:reasonMax]) + "..."
	}
	return s
}
