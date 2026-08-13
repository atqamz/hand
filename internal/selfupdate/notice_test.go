package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
)

func stableBuild(version string) BuildInfo {
	return BuildInfo{Version: version, Channel: ChannelStable}
}

func TestCheckNoticeSkipsWithoutStateDir(t *testing.T) {
	home := t.TempDir()
	writeFakeGH(t, "v0.5.0", t.TempDir())

	if notice := CheckNoticeForBuild(home, "atqamz/hand", stableBuild("v0.1.0")); notice != "" {
		t.Fatalf("got %q, want empty notice without state dir", notice)
	}
}

func TestCheckNoticeReturnsMessageWhenNewer(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGH(t, "v0.5.0", t.TempDir())

	notice := CheckNoticeForBuild(home, "atqamz/hand", stableBuild("v0.1.0"))
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

	if notice := CheckNoticeForBuild(home, "atqamz/hand", stableBuild("v0.1.0")); notice != "" {
		t.Fatalf("got %q, want empty notice when up to date", notice)
	}
}

func TestCheckNoticeUsesFreshCacheWithoutCallingGH(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A refusal fake, not an imitation of gh: a fresh cache must serve the
	// notice without any gh call at all, so any invocation is the failure.
	bin := faketool.Bin(t)
	faketool.GH{}.Install(t, bin)

	if err := writeCache(filepath.Join(home, "state", cacheFile), versionCache{
		CheckedAt: time.Now(),
		Latest:    "v0.5.0",
	}); err != nil {
		t.Fatal(err)
	}

	notice := CheckNoticeForBuild(home, "atqamz/hand", stableBuild("v0.1.0"))
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

	notice := CheckNoticeForBuild(home, "atqamz/hand", stableBuild("v0.1.0"))
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

	if notice := CheckNoticeForBuild(home, "atqamz/hand", stableBuild("v0.1.0")); notice != "" {
		t.Fatalf("got %q, want empty notice when gh unreachable", notice)
	}
}

func TestCheckNoticeCachesFailedCheck(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	if notice := CheckNoticeForBuild(home, "atqamz/hand", stableBuild("v0.1.0")); notice != "" {
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
	if notice := CheckNoticeForBuild(home, "atqamz/hand", stableBuild("v0.1.0")); notice != "" {
		t.Fatalf("got %q, want empty notice while the failed check is still cached", notice)
	}
}

func TestCheckNoticeSkipsUnparseableCurrentVersion(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Same refusal fake as TestCheckNoticeUsesFreshCacheWithoutCallingGH above;
	// an unparseable current version must skip the check entirely.
	bin := faketool.Bin(t)
	faketool.GH{}.Install(t, bin)

	if notice := CheckNoticeForBuild(home, "atqamz/hand", stableBuild("dev")); notice != "" {
		t.Fatalf("got %q, want empty notice for an unversioned build", notice)
	}
	if _, err := os.Stat(filepath.Join(home, "state", cacheFile)); err == nil {
		t.Fatal("want no version check for an unversioned build")
	}
}

func TestCheckNoticeForBuildReportsNewEdgeCommit(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeGHTarget(t, "v0.5.0", edgeTestCommit)

	info := BuildInfo{Version: "edge.aaaaaaaaaaaa", Channel: ChannelEdge, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	want := "A new edge build of hand is available: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa -> " + edgeTestCommit + "\nRun \"hand update\" to update"
	if got := CheckNoticeForBuild(home, "atqamz/hand", info); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCheckNoticeForBuildRefreshesCacheWhenChannelChanges(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeCache(filepath.Join(home, "state", cacheFile), versionCache{
		CheckedAt: time.Now(),
		Channel:   ChannelStable,
		Latest:    "v0.5.0",
	}); err != nil {
		t.Fatal(err)
	}
	writeFakeGHTarget(t, "v0.5.0", edgeTestCommit)

	info := BuildInfo{Version: "edge.aaaaaaaaaaaa", Channel: ChannelEdge, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if notice := CheckNoticeForBuild(home, "atqamz/hand", info); notice == "" || !strings.Contains(notice, "new edge build") {
		t.Fatalf("notice = %q, want a refreshed edge notice", notice)
	}
	cache, err := readCache(filepath.Join(home, "state", cacheFile))
	if err != nil {
		t.Fatal(err)
	}
	if cache.Channel != ChannelEdge || cache.Commit != edgeTestCommit {
		t.Fatalf("cache = %#v, want edge channel and commit", cache)
	}
}
