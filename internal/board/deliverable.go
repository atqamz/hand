package board

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/state"
)

type Deliverable struct {
	Ref  string
	Kind string
}

var (
	deliveredLine = regexp.MustCompile(`(?m)^[ \t]*(?:[-*][ \t]+)?Delivered:(.+)$`)
	changeRequest = regexp.MustCompile("https?://[^\\s<>\"'()\\[\\]`]+?/(?:-/merge_requests|pulls?|pull-requests)/[0-9]+")
	prPath        = regexp.MustCompile(`/(?:pulls?|pull-requests)/[0-9]+(?:/|$)`)
	absolutePath  = regexp.MustCompile(`^(?:/|[A-Za-z]:[\\/]|\\\\)[^\s/\\]`)
)

func Deliverables(body string) []Deliverable {
	var out []Deliverable
	add := func(ref string) {
		if kind := DeliverableKind(ref); kind != "" && !slices.ContainsFunc(out, func(d Deliverable) bool { return d.Ref == ref }) {
			out = append(out, Deliverable{ref, kind})
		}
	}
	lines := deliveredLine.FindAllStringSubmatch(body, -1)
	for _, m := range lines {
		add(strings.Trim(strings.TrimSpace(m[1]), "`"))
	}
	if len(lines) == 0 {
		for _, ref := range changeRequest.FindAllString(body, -1) {
			add(ref)
		}
	}
	return out
}

func Newest(reports []state.Report, events []state.Event) []Deliverable {
	type dated struct {
		at time.Time
		d  Deliverable
	}
	var all []dated
	for _, r := range reports {
		for _, d := range Deliverables(r.Body) {
			all = append(all, dated{parse(r.CreatedAt), d})
		}
	}
	for _, e := range events {
		if kind := DeliverableKind(e.Detail); kind != "" {
			all = append(all, dated{parse(e.At), Deliverable{e.Detail, kind}})
		}
	}
	slices.SortStableFunc(all, func(x, y dated) int { return x.at.Compare(y.at) })
	var out []Deliverable
	for i := len(all) - 1; i >= 0; i-- {
		if !slices.Contains(out, all[i].d) {
			out = append(out, all[i].d)
		}
	}
	return out
}

func DeliverableKind(ref string) string {
	if absolutePath.MatchString(ref) {
		return "file"
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" || strings.ContainsAny(ref, " \t") || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	switch {
	case strings.Contains(u.Path, "/-/merge_requests/"):
		return "MR"
	case prPath.MatchString(u.Path):
		return "PR"
	}
	return "link"
}

func base(path string) string {
	path = strings.TrimRight(path, `/\`)
	return path[strings.LastIndexAny(path, `/\`)+1:]
}
