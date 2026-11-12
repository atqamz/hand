package main

import (
	"bytes"
	"flag"
	"fmt"
	"html"
	"html/template"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/atqamz/hand/internal/markdown"
)

const (
	siteURL   = "https://hand.atqamz.com"
	repoURL   = "https://github.com/atqamz/hand"
	companion = `<h3 id="hand-companion">Hand Companion</h3><p>Hand Companion is the Android app for a paired phone. It needs a Hand laptop running the board, because the phone reaches that laptop through Hand and the app does not run Hand itself. Its source is <a href="https://github.com/atqamz/hand-android" rel="noopener">atqamz/hand-android</a>.</p>`
)

type navItem struct {
	Name   string
	Href   string
	Active bool
}

type pageData struct {
	Title     string
	PageTitle string
	Canonical string
	Edit      string
	Version   string
	Body      template.HTML
	Nav       []navItem
	Prev      *navItem
	Next      *navItem
}

type doc struct {
	Source string
	Title  string
	URL    string
	Body   template.HTML
}

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "www/dist", "output folder")
	version := flag.String("version", "", "version for the footer")
	flag.Parse()
	if *version == "" {
		*version = describe(*root)
	}
	rs, note := fetchReleases()
	if err := build(*root, *out, *version, rs, note); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(root, out, version string, rs []release, note string) error {
	sources, err := manifest(root)
	if err != nil {
		return err
	}
	urls := map[string]string{}
	for _, src := range sources {
		urls[path.Clean(src)] = pageURL(src)
	}
	var docs []doc
	for _, src := range sources {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(src)))
		if err != nil {
			return err
		}
		title, text := splitTitle(string(data), src)
		body := rewrite(string(markdown.RenderSite(images(text))), path.Dir(src), urls)
		if urls[path.Clean(src)] == "/phone/" {
			body += companion
		}
		docs = append(docs, doc{Source: src, Title: title, URL: urls[path.Clean(src)], Body: template.HTML(body)})
	}
	docs = append(docs, doc{Title: "Changelog", URL: "/changelog/", Body: template.HTML(changelog(rs, note))})
	tmpl, err := template.ParseFiles(filepath.Join(root, "www", "template.html"))
	if err != nil {
		return err
	}
	for i, d := range docs {
		p := pageData{Title: d.Title, PageTitle: d.Title + " · Hand", Canonical: siteURL + d.URL, Version: version, Body: d.Body, Nav: nav(d.URL)}
		if d.URL == "/" {
			p.PageTitle = "Hand"
		}
		if d.Source != "" {
			p.Edit = repoURL + "/edit/main/" + d.Source
		}
		if i > 0 {
			p.Prev = &navItem{Name: docs[i-1].Title, Href: docs[i-1].URL}
		}
		if i+1 < len(docs) {
			p.Next = &navItem{Name: docs[i+1].Title, Href: docs[i+1].URL}
		}
		var page bytes.Buffer
		if err := tmpl.ExecuteTemplate(&page, "template.html", p); err != nil {
			return err
		}
		if err := writePage(out, d.URL, page.Bytes()); err != nil {
			return err
		}
	}
	for _, name := range []string{"style.css", "favicon.svg"} {
		data, err := os.ReadFile(filepath.Join(root, "www", name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func pageURL(src string) string {
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	if strings.EqualFold(base, "README") {
		return "/"
	}
	return "/" + strings.ToLower(base) + "/"
}

func splitTitle(text, src string) (string, string) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if title, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(title), strings.Join(append(lines[:i:i], lines[i+1:]...), "\n")
		}
		if strings.TrimSpace(line) != "" {
			break
		}
	}
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	return base, text
}

var rawImage = regexp.MustCompile(`<img\s+[^>]*src="([^"]+)"[^>]*alt="([^"]*)"[^>]*>`)

func images(text string) string {
	return rawImage.ReplaceAllString(text, "![$2]($1)")
}

var attr = regexp.MustCompile(`(href|src)="([^"]*)"`)

func rewrite(out, dir string, pages map[string]string) string {
	return attr.ReplaceAllStringFunc(out, func(m string) string {
		sub := attr.FindStringSubmatch(m)
		if !relative(sub[2]) {
			return m
		}
		target, frag, _ := strings.Cut(sub[2], "#")
		if target == "" {
			return m
		}
		clean := path.Clean(path.Join(dir, target))
		var to string
		if u, ok := pages[clean]; ok {
			to = u
		} else if sub[1] == "src" {
			to = "https://raw.githubusercontent.com/atqamz/hand/main/" + clean
		} else {
			to = repoURL + "/blob/main/" + clean
		}
		if frag != "" {
			to += "#" + frag
		}
		return sub[1] + `="` + html.EscapeString(to) + `"`
	})
}

func relative(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "?") {
		return false
	}
	return !strings.Contains(ref, "://") && !strings.HasPrefix(ref, "//") && !strings.HasPrefix(ref, "mailto:")
}

func nav(url string) []navItem {
	items := []navItem{
		{Name: "Install", Href: "/install/"},
		{Name: "Update", Href: "/update/"},
		{Name: "Board", Href: "/board/"},
		{Name: "Phone", Href: "/phone/"},
		{Name: "API", Href: "/api/"},
		{Name: "Fleets", Href: "/fleets/"},
		{Name: "Safe use", Href: "/safe-use/"},
		{Name: "Changelog", Href: "/changelog/"},
		{Name: "GitHub", Href: repoURL},
	}
	for i := range items {
		items[i].Active = items[i].Href == url
	}
	return items
}

func writePage(out, url string, page []byte) error {
	dir := filepath.Join(out, filepath.FromSlash(strings.Trim(url, "/")))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.html"), page, 0o644)
}
