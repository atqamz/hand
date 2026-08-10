package selfupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCheckNoticeSkipsWithoutStateDir(t *testing.T) {
	home := t.TempDir()
	writeFakeGH(t, "v0.5.0", t.TempDir())

	if notice := CheckNotice(home, "atqamz/hand", "v0.1.0"); notice != "" {
		t.Fatalf("got %q, want empty notice without state dir", notice)
	}
}

func TestCheckNoticeReturnsMessageWhenNewer(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, "v0.5.0", t.TempDir())

	notice := CheckNotice(home, "atqamz/hand", "v0.1.0")
	want := "A new version of hand is available: v0.1.0 -> v0.5.0\nRun \"hand update\" to update"
	if notice != want {
		t.Fatalf("got %q, want %q", notice, want)
	}
}

func TestCheckNoticeEmptyWhenUpToDate(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, "v0.1.0", t.TempDir())

	if notice := CheckNotice(home, "atqamz/hand", "v0.1.0"); notice != "" {
		t.Fatalf("got %q, want empty notice when up to date", notice)
	}
}

func TestCheckNoticeUsesFreshCacheWithoutCallingGH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a POSIX shell script, not supported on windows")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A refusal fake, not an imitation of gh: a fresh cache must serve the
	// notice without any gh call at all, so any invocation is the failure.
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'gh should not be called' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := writeCache(filepath.Join(home, "state", cacheFile), versionCache{
		CheckedAt: time.Now(),
		Latest:    "v0.5.0",
	}); err != nil {
		t.Fatal(err)
	}

	notice := CheckNotice(home, "atqamz/hand", "v0.1.0")
	want := "A new version of hand is available: v0.1.0 -> v0.5.0\nRun \"hand update\" to update"
	if notice != want {
		t.Fatalf("got %q, want %q", notice, want)
	}
}

func TestCheckNoticeRefreshesStaleCache(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, "v0.6.0", t.TempDir())

	if err := writeCache(filepath.Join(home, "state", cacheFile), versionCache{
		CheckedAt: time.Now().Add(-25 * time.Hour),
		Latest:    "v0.5.0",
	}); err != nil {
		t.Fatal(err)
	}

	notice := CheckNotice(home, "atqamz/hand", "v0.1.0")
	want := "A new version of hand is available: v0.1.0 -> v0.6.0\nRun \"hand update\" to update"
	if notice != want {
		t.Fatalf("got %q, want %q", notice, want)
	}
}

func TestCheckNoticeEmptyWhenGHUnreachable(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	if notice := CheckNotice(home, "atqamz/hand", "v0.1.0"); notice != "" {
		t.Fatalf("got %q, want empty notice when gh unreachable", notice)
	}
}

func TestCheckNoticeCachesFailedCheck(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	if notice := CheckNotice(home, "atqamz/hand", "v0.1.0"); notice != "" {
		t.Fatalf("got %q, want empty notice when gh unreachable", notice)
	}

	cache, err := readCache(filepath.Join(home, "state", cacheFile))
	if err != nil {
		t.Fatalf("failed check did not write cache: %v", err)
	}
	if cache.CheckedAt.IsZero() {
		t.Fatal("cache has zero CheckedAt, want the failed attempt recorded")
	}
	if cache.Latest != "" {
		t.Fatalf("got cached latest %q, want empty after a failed check", cache.Latest)
	}

	writeFakeGH(t, "v0.5.0", t.TempDir())
	if notice := CheckNotice(home, "atqamz/hand", "v0.1.0"); notice != "" {
		t.Fatalf("got %q, want empty notice while the failed check is still cached", notice)
	}
}

func TestCheckNoticeSkipsUnparseableCurrentVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a POSIX shell script, not supported on windows")
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Same refusal fake as TestCheckNoticeUsesFreshCacheWithoutCallingGH above;
	// an unparseable current version must skip the check entirely.
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'gh should not be called' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if notice := CheckNotice(home, "atqamz/hand", "dev"); notice != "" {
		t.Fatalf("got %q, want empty notice for an unversioned build", notice)
	}
	if _, err := os.Stat(filepath.Join(home, "state", cacheFile)); err == nil {
		t.Fatal("want no version check for an unversioned build")
	}
}
