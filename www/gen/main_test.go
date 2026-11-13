package main

import (
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const noNotes = "This page is built from GitHub Releases when the site deploys; this build had no release data."

var (
	idPattern   = regexp.MustCompile(`id="([^"]+)"`)
	hrefPattern = regexp.MustCompile(`<a [^>]*href="([^"]*)"`)
)

func TestEveryManifestPathIsInTheRepository(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := manifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range sources {
		if _, err := source(root, src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
	for _, bad := range []string{"../outside.md", "/etc/passwd", "docs/missing.md", "docs"} {
		if _, err := source(root, bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func siteRoot(t *testing.T, docs ...string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"www/docs.txt":      strings.Join(docs, "\n") + "\n",
		"www/template.html": "<!doctype html>{{.Body}}",
		"www/style.css":     "",
		"www/CNAME":         "",
		"www/favicon.svg":   "",
	}
	for name, body := range files {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAManifestPathOutsideTheRepositoryFails(t *testing.T) {
	for _, line := range []string{"../outside.md", "missing.md"} {
		root := siteRoot(t, line)
		if err := os.WriteFile(filepath.Join(root, "..", "outside.md"), []byte("# Outside\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := build(root, t.TempDir(), "v0", nil, noNotes); err == nil {
			t.Errorf("%q did not fail the build", line)
		}
	}
}

func TestASymlinkedManifestPathOutsideTheRepositoryFails(t *testing.T) {
	root := siteRoot(t, "link.md")
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("# Outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.md")); err != nil {
		t.Skip(err)
	}
	if err := build(root, t.TempDir(), "v0", nil, noNotes); err == nil {
		t.Fatal("a manifest path through a symlink outside the repository did not fail")
	}
}

func TestAnUnpublishedManifestPathFails(t *testing.T) {
	for _, line := range []string{"DESIGN.md", "PRODUCT.md", "docs/spec.md", "docs/e2e.md", "docs/dogfood/README.md", "docs/images/desktop/a.png"} {
		root := siteRoot(t, line)
		file := filepath.Join(root, filepath.FromSlash(line))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("# Hidden\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := build(root, t.TempDir(), "v0", nil, noNotes); err == nil {
			t.Errorf("%q was published", line)
		}
	}
}

func TestChangelogSharesHeadingIDs(t *testing.T) {
	got := changelog([]release{{TagName: "v1.0.0", Body: "## Notes"}, {TagName: "v1.1.0", Body: "## Notes"}}, "")
	if !strings.Contains(got, `id="notes"`) || !strings.Contains(got, `id="notes-1"`) {
		t.Errorf("changelog ids collide: %s", got)
	}
}

func TestEveryRenderedLinkResolves(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := build(root, out, "v9.9.9", nil, noNotes); err != nil {
		t.Fatal(err)
	}
	pages := readPages(t, out)
	if len(pages) != 16 {
		t.Fatalf("built %d pages, want 16", len(pages))
	}
	ids := map[string]map[string]bool{}
	for url, body := range pages {
		ids[url] = map[string]bool{}
		for _, m := range idPattern.FindAllStringSubmatch(body, -1) {
			ids[url][m[1]] = true
		}
	}
	for url, body := range pages {
		for _, m := range hrefPattern.FindAllStringSubmatch(body, -1) {
			ref := html.UnescapeString(m[1])
			switch {
			case strings.HasPrefix(ref, "#"):
				if !ids[url][strings.TrimPrefix(ref, "#")] {
					t.Errorf("%s: anchor %q is missing", url, ref)
				}
			case strings.HasPrefix(ref, "/") && !strings.HasPrefix(ref, "//"):
				page, frag, _ := strings.Cut(ref, "#")
				if _, ok := pages[page]; !ok {
					t.Errorf("%s: link %q names no page", url, ref)
					continue
				}
				if frag != "" && !ids[page][frag] {
					t.Errorf("%s: link %q names no anchor", url, ref)
				}
			case !strings.Contains(ref, ":"):
				t.Errorf("%s: relative link %q was not rewritten", url, ref)
			}
		}
	}
}

func TestTheHomePageCarriesTheNavAndTheVersion(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := build(root, out, "v9.9.9", nil, noNotes); err != nil {
		t.Fatal(err)
	}
	pages := readPages(t, out)
	home := pages["/"]
	for _, want := range []string{
		`<a class="wordmark" href="/">Hand</a>`,
		`href="/install/"`, `href="/update/"`, `href="/board/"`, `href="/phone/"`,
		`href="/api/"`, `href="/fleets/"`, `href="/safe-use/"`, `href="/changelog/"`,
		`href="https://github.com/atqamz/hand"`,
		"Hand v9.9.9 · MIT License",
	} {
		if !strings.Contains(home, want) {
			t.Errorf("the home page lacks %q", want)
		}
	}
	if changelog := pages["/changelog/"]; !strings.Contains(changelog, noNotes) {
		t.Error("the changelog does not say that it has no release data")
	}
	phone := pages["/phone/"]
	for _, want := range []string{"Hand Companion", "Hand host", "https://github.com/atqamz/hand/tree/main/android"} {
		if !strings.Contains(phone, want) {
			t.Errorf("the phone page lacks %q", want)
		}
	}
}

func TestTheCNAMEFileIsNotPublished(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := build(root, out, "v9.9.9", nil, noNotes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "CNAME")); !os.IsNotExist(err) {
		t.Fatalf("the build published CNAME: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "www", "CNAME")); err != nil {
		t.Fatalf("www/CNAME no longer records the domain: %v", err)
	}
}

func TestDescribeMatchesReleaseTagsOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "one")
	git("tag", "v0.1.0")
	git("commit", "-q", "--allow-empty", "-m", "two")
	git("tag", "edge")
	if got := describe(dir); got != "v0.1.0" {
		t.Errorf("describe = %q, want v0.1.0", got)
	}
	git("tag", "-d", "v0.1.0")
	if got := describe(dir); got != "unreleased" {
		t.Errorf("describe without a release tag = %q, want unreleased", got)
	}
}

func readPages(t *testing.T, out string) map[string]string {
	t.Helper()
	pages := map[string]string{}
	add := func(url, file string) {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		pages[url] = string(b)
	}
	add("/", filepath.Join(out, "index.html"))
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			add("/"+e.Name()+"/", filepath.Join(out, e.Name(), "index.html"))
		}
	}
	return pages
}

func TestReleasesReadBodiesFromTheAPI(t *testing.T) {
	rs, err := decodeReleases([]byte(`[{"tag_name":"v1.2.3","published_at":"2026-11-01T12:00:00Z","body":"# Notes"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].TagName != "v1.2.3" || rs[0].Body != "# Notes" {
		t.Fatalf("decoded %#v", rs)
	}
}
