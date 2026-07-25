package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckNoticeSkipsWithoutStateDir(t *testing.T) {
	home := t.TempDir()
	writeFakeGH(t, "v0.5.0", t.TempDir())

	if notice := CheckNotice(home, "atqamz/secondhand", "v0.1.0"); notice != "" {
		t.Fatalf("got %q, want empty notice without state dir", notice)
	}
}

func TestCheckNoticeReturnsMessageWhenNewer(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, "v0.5.0", t.TempDir())

	notice := CheckNotice(home, "atqamz/secondhand", "v0.1.0")
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

	if notice := CheckNotice(home, "atqamz/secondhand", "v0.1.0"); notice != "" {
		t.Fatalf("got %q, want empty notice when up to date", notice)
	}
}

func TestCheckNoticeUsesFreshCacheWithoutCallingGH(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}

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

	notice := CheckNotice(home, "atqamz/secondhand", "v0.1.0")
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

	notice := CheckNotice(home, "atqamz/secondhand", "v0.1.0")
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

	if notice := CheckNotice(home, "atqamz/secondhand", "v0.1.0"); notice != "" {
		t.Fatalf("got %q, want empty notice when gh unreachable", notice)
	}
}

func TestCheckNoticeCachesFailedCheck(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	if notice := CheckNotice(home, "atqamz/secondhand", "v0.1.0"); notice != "" {
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
	if notice := CheckNotice(home, "atqamz/secondhand", "v0.1.0"); notice != "" {
		t.Fatalf("got %q, want empty notice while the failed check is still cached", notice)
	}
}

func TestCheckNoticeSkipsUnparseableCurrentVersion(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	script := "#!/bin/sh\necho 'gh should not be called' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if notice := CheckNotice(home, "atqamz/secondhand", "dev"); notice != "" {
		t.Fatalf("got %q, want empty notice for an unversioned build", notice)
	}
	if _, err := os.Stat(filepath.Join(home, "state", cacheFile)); err == nil {
		t.Fatal("want no version check for an unversioned build")
	}
}
