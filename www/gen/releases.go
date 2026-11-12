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
	TagName     string `json:"tagName"`
	PublishedAt string `json:"publishedAt"`
	Body        string `json:"body"`
}

func changelog(rs []release, note string) string {
	if len(rs) == 0 {
		return "<p>" + html.EscapeString(note) + "</p>"
	}
	var b strings.Builder
	for _, r := range rs {
		date := r.PublishedAt
		if t, err := time.Parse(time.RFC3339, date); err == nil {
			date = t.Format("2 January 2006")
		}
		b.WriteString(`<section class="release"><h2 id="` + html.EscapeString(r.TagName) + `">` + html.EscapeString(r.TagName) + `</h2><p class="date">` + html.EscapeString(date) + `</p>`)
		b.WriteString(rewrite(string(markdown.RenderSite(r.Body)), ".", nil))
		b.WriteString("</section>")
	}
	return b.String()
}

func fetchReleases(root string) ([]release, string) {
	if os.Getenv("GH_TOKEN") == "" && os.Getenv("GITHUB_TOKEN") == "" {
		return nil, "This page is built from GitHub Releases when the site deploys; this build had no token, so it has no release notes."
	}
	cmd := exec.Command("gh", "release", "list", "--repo", "atqamz/hand", "--json", "tagName,publishedAt,body", "--limit", "40")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, "This page is built from GitHub Releases when the site deploys; this build could not read them."
	}
	var rs []release
	if err := json.Unmarshal(out, &rs); err != nil {
		return nil, "This page is built from GitHub Releases when the site deploys; this build could not read them."
	}
	return rs, ""
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
