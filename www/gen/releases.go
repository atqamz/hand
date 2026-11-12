package main

import (
	"encoding/json"
	"html"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/markdown"
)

type release struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
}

func changelog(rs []release, note string) string {
	if len(rs) == 0 {
		return "<p>" + html.EscapeString(note) + "</p>"
	}
	var b strings.Builder
	slugs := markdown.NewSlugger()
	for _, r := range rs {
		date := r.PublishedAt
		if t, err := time.Parse(time.RFC3339, date); err == nil {
			date = t.Format("2 January 2006")
		}
		b.WriteString(`<section class="release"><h2 id="` + html.EscapeString(r.TagName) + `">` + html.EscapeString(r.TagName) + `</h2><p class="date">` + html.EscapeString(date) + `</p>`)
		b.WriteString(rewrite(string(markdown.RenderSiteWith(r.Body, slugs)), ".", nil))
		b.WriteString("</section>")
	}
	return b.String()
}

func fetchReleases(root string) ([]release, string) {
	if os.Getenv("GH_TOKEN") == "" && os.Getenv("GITHUB_TOKEN") == "" {
		return nil, "This page is built from GitHub Releases when the site deploys; this build had no token, so it has no release notes."
	}
	cmd := exec.Command("gh", "api", "repos/atqamz/hand/releases?per_page=40")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, "This page is built from GitHub Releases when the site deploys; this build could not read them."
	}
	rs, err := decodeReleases(out)
	if err != nil {
		return nil, "This page is built from GitHub Releases when the site deploys; this build could not read them."
	}
	return rs, ""
}

func decodeReleases(data []byte) ([]release, error) {
	var rs []release
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

func describe(root string) string {
	out, err := exec.Command("git", "-C", root, "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		return "unreleased"
	}
	if v := strings.TrimSpace(string(out)); v != "" {
		return v
	}
	return "unreleased"
}
