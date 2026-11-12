package main

import (
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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

func TestAManifestPathOutsideTheRepositoryFails(t *testing.T) {
	for _, line := range []string{"../outside.md", "missing.md"} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "www"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "www", "docs.txt"), []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := build(root, t.TempDir(), "v0", nil, noNotes); err == nil {
			t.Errorf("%q did not fail the build", line)
		}
	}
}

func TestASymlinkedManifestPathOutsideTheRepositoryFails(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "www"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("# Outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.md")); err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(root, "www", "docs.txt"), []byte("link.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := build(root, t.TempDir(), "v0", nil, noNotes); err == nil {
		t.Fatal("a manifest path through a symlink outside the repository did not fail")
	}
}

func TestADocFileMissingFromTheManifestFails(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"www", "docs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "new.md"), []byte("# New\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "www", "docs.txt"), []byte("README.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := manifest(root); err == nil {
		t.Fatal("an unlisted docs/*.md file did not fail the manifest")
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
	if len(pages) != 18 {
		t.Fatalf("built %d pages, want 18", len(pages))
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
		`href="/install/"`, `href="/commands/"`, `href="/update/"`, `href="/board/"`, `href="/phone/"`,
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
	for _, want := range []string{"Hand Companion", "Hand laptop", "hand-android"} {
		if !strings.Contains(phone, want) {
			t.Errorf("the phone page lacks %q", want)
		}
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
