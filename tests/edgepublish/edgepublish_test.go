// Package edgepublish runs .github/scripts/edge-publish.sh, the script
// .github/workflows/edge.yaml invokes, against the faketool gh release store and
// a throwaway git remote, and asserts the release state a run leaves behind.
package edgepublish

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/faketool"
)

// The commit an interrupted predecessor was publishing, which is the group key
// its edge-previous-* backups carry. Only its shape as an asset-name segment
// matters, so it never has to exist in the throwaway repository.
const interrupted = "b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1"

var releaseAssets = []string{
	"hand-linux-amd64.tar.gz",
	"hand-linux-arm64.tar.gz",
	"hand-darwin-amd64.tar.gz",
	"hand-darwin-arm64.tar.gz",
	"hand-windows-amd64.zip",
	"checksums.txt",
}

type harness struct {
	dir    string
	origin string
	bin    string
	sha    string
}

func setup(t *testing.T, store *faketool.GHReleaseStore) harness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the publish script is bash and runs only on the ubuntu runner")
	}
	for _, tool := range []string{"bash", "jq", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}
	bin := faketool.Bin(t)
	dir := filepath.Join(t.TempDir(), "work")
	faketool.InitRepo(t, dir)
	origin := filepath.Join(t.TempDir(), "origin.git")
	git(t, "", "init", "--bare", "-q", origin)
	git(t, dir, "remote", "add", "origin", origin)
	for _, asset := range releaseAssets {
		if err := os.WriteFile(filepath.Join(dir, asset), []byte("candidate "+asset), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	faketool.GH{ReleaseStore: store}.Install(t, bin)
	return harness{dir: dir, origin: origin, bin: bin, sha: git(t, dir, "rev-parse", "HEAD")}
}

func (h harness) publish(t *testing.T) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), ".github", "scripts", "edge-publish.sh"))
	cmd.Dir = h.dir
	cmd.Env = append(os.Environ(), "CANDIDATE="+h.sha, "GH_TOKEN=fake")
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run edge-publish.sh: %v", err)
	}
	t.Logf("edge-publish.sh -> exit %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	return errOut.String(), code
}

func TestBootstrapPublishesTheCompleteSetAndLeavesNoTemporaryRef(t *testing.T) {
	h := setup(t, &faketool.GHReleaseStore{})

	if _, code := h.publish(t); code != 0 {
		t.Fatalf("exit %d, want the first publication to succeed", code)
	}

	store := faketool.GHReleases(t, h.bin)
	if len(store.Releases) != 1 {
		t.Fatalf("releases = %+v, want only the promoted edge release", store.Releases)
	}
	release, ok := store.Release("edge")
	if !ok {
		t.Fatalf("releases = %+v, want the bootstrap release retagged to edge", store.Releases)
	}
	if release.Draft || !release.Prerelease {
		t.Fatalf("release = %+v, want a published prerelease", release)
	}
	requireExactSet(t, store.AssetNames("edge"))

	// The bootstrap release is only ever a draft, so the temporary tag names no
	// ref anywhere and nothing has to delete one.
	for _, repo := range []string{h.dir, h.origin} {
		if tags := git(t, repo, "tag", "--list", "edge-bootstrap-*"); tags != "" {
			t.Fatalf("%s carries %q, want the bootstrap tag to have stayed a draft's reservation", repo, tags)
		}
	}
	if got := git(t, h.origin, "rev-parse", "refs/tags/edge"); got != h.sha {
		t.Fatalf("origin edge = %q, want the candidate %q", got, h.sha)
	}
}

// The regression atqamz/hand#210 review found: a predecessor killed between the
// backup and promote loops leaves five exact names from its own candidate, and
// filling only the missing one from a backup publishes two commits at once.
func TestRecoveryRestoresOneBackupGroupWholeAfterPartialPromotion(t *testing.T) {
	assets := []faketool.GHReleaseAsset{
		{ID: 206, Name: "edge-staging-" + interrupted + "-checksums.txt"},
	}
	for i, name := range releaseAssets[:len(releaseAssets)-1] {
		assets = append(assets, faketool.GHReleaseAsset{ID: 200 + i, Name: name})
	}
	backups := map[string]int{}
	for i, name := range releaseAssets {
		id := 100 + i
		backups[name] = id
		assets = append(assets, faketool.GHReleaseAsset{ID: id, Name: "edge-previous-" + interrupted + "-" + name})
	}
	h := setup(t, &faketool.GHReleaseStore{
		NextID:   1000,
		Releases: []faketool.GHReleaseRecord{{ID: 1, TagName: "edge", Prerelease: true, Assets: assets}},
	})

	// Stops the run right after reconciliation and before the trap that would
	// roll back, so the assertion sees the set recovery alone produced.
	if err := os.Remove(filepath.Join(h.dir, "checksums.txt")); err != nil {
		t.Fatal(err)
	}
	if _, code := h.publish(t); code == 0 {
		t.Fatal("exit 0, want the run to stop once a candidate asset is missing")
	}

	store := faketool.GHReleases(t, h.bin)
	requireExactSet(t, store.AssetNames("edge"))
	release, _ := store.Release("edge")
	for _, asset := range release.Assets {
		if want := backups[asset.Name]; asset.ID != want {
			t.Fatalf("%s is asset %d, want the %d the interrupted predecessor backed up: recovery mixed two commits",
				asset.Name, asset.ID, want)
		}
	}
}

func TestRecoveryRefusesToPublishWhenNoBackupGroupIsComplete(t *testing.T) {
	assets := []faketool.GHReleaseAsset{
		{ID: 201, Name: releaseAssets[0]},
		{ID: 202, Name: releaseAssets[1]},
	}
	for i, name := range releaseAssets[:3] {
		assets = append(assets, faketool.GHReleaseAsset{ID: 100 + i, Name: "edge-previous-" + interrupted + "-" + name})
	}
	for i, name := range releaseAssets[3:] {
		assets = append(assets, faketool.GHReleaseAsset{ID: 110 + i, Name: "edge-previous-c2c2-" + name})
	}
	h := setup(t, &faketool.GHReleaseStore{
		NextID:   1000,
		Releases: []faketool.GHReleaseRecord{{ID: 1, TagName: "edge", Prerelease: true, Assets: assets}},
	})
	before := faketool.GHReleases(t, h.bin)

	stderr, code := h.publish(t)
	if code == 0 {
		t.Fatal("exit 0, want a refusal while no single backup group can restore the set")
	}
	if !strings.Contains(stderr, "no single backup group") {
		t.Fatalf("stderr = %q, want the refusal to name the missing backup group", stderr)
	}

	after := faketool.GHReleases(t, h.bin)
	if got, want := inventory(after), inventory(before); got != want {
		t.Fatalf("assets = %q, want the refusal to have changed nothing: %q", got, want)
	}
}

func requireExactSet(t *testing.T, names []string) {
	t.Helper()
	got := append([]string(nil), names...)
	want := append([]string(nil), releaseAssets...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("assets = %q, want exactly the download names %q", got, want)
	}
}

func inventory(store faketool.GHReleaseStore) string {
	var entries []string
	for _, release := range store.Releases {
		for _, asset := range release.Assets {
			entries = append(entries, release.TagName+":"+asset.Name)
		}
	}
	sort.Strings(entries)
	return strings.Join(entries, " ")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate the test source")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(source)))
}
