package viewlink

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const Head = "handboard://view/"

var (
	ErrNotViewLink = errors.New("not a view link: expected handboard://view/<fleet id>/[task/tN | decision/dN | attempt/aN | doc/PATH | tasks[?q=QUERY] | log | companion | alerts][#rN]")

	fleetRe = regexp.MustCompile(`^f[0-9a-f]{12}$`)
	refRe   = regexp.MustCompile(`^(?:task/t|decision/d|attempt/a)[1-9][0-9]{0,8}$`)
	repRe   = regexp.MustCompile(`^r[1-9][0-9]{0,8}$`)
)

var kinds = map[string]string{"t": "task/", "d": "decision/", "a": "attempt/"}

const (
	Fleet     = ""
	Item      = "item"
	Doc       = "doc"
	Tasks     = "tasks"
	Log       = "log"
	Companion = "companion"
	Alerts    = "alerts"
)

type Link struct {
	Fleet  string
	Page   string
	Ref    string
	Report string
	Path   string
	Query  string
}

func Format(l Link) (string, error) {
	if !fleetRe.MatchString(l.Fleet) {
		return "", ErrNotViewLink
	}
	head := Head + l.Fleet + "/"
	switch l.Page {
	case Fleet:
		return head, nil
	case Item:
		kind, ok := kinds[l.Ref[:min(1, len(l.Ref))]]
		if !ok || !refRe.MatchString(kind+l.Ref) || l.Report != "" && (l.Ref[0] != 't' || !repRe.MatchString(l.Report)) {
			return "", ErrNotViewLink
		}
		out := head + kind + l.Ref
		if l.Report != "" {
			out += "#" + l.Report
		}
		return out, nil
	case Doc:
		segs, ok := docSegments(l.Path)
		if !ok {
			return "", ErrNotViewLink
		}
		for i, s := range segs {
			segs[i] = url.PathEscape(s)
		}
		return head + "doc/" + strings.Join(segs, "/"), nil
	case Tasks:
		if l.Query == "" {
			return head + "tasks", nil
		}
		if badText(l.Query) {
			return "", ErrNotViewLink
		}
		return head + "tasks?q=" + url.QueryEscape(l.Query), nil
	case Log, Companion, Alerts:
		return head + l.Page, nil
	}
	return "", ErrNotViewLink
}

func Parse(link string) (Link, error) {
	rest, ok := strings.CutPrefix(link, Head)
	if !ok {
		return Link{}, ErrNotViewLink
	}
	fleet, page, ok := strings.Cut(rest, "/")
	if !ok || !fleetRe.MatchString(fleet) {
		return Link{}, ErrNotViewLink
	}
	l := Link{Fleet: fleet}
	switch {
	case page == "":
		return l, nil
	case page == Log || page == Companion || page == Alerts:
		l.Page = page
		return l, nil
	case page == Tasks:
		l.Page = Tasks
		return l, nil
	case strings.HasPrefix(page, "tasks?q="):
		q, err := url.QueryUnescape(page[len("tasks?q="):])
		if err != nil || q == "" || strings.ContainsAny(page[len("tasks?q="):], "&#?") || badText(q) {
			return Link{}, ErrNotViewLink
		}
		l.Page, l.Query = Tasks, q
		return l, nil
	case strings.HasPrefix(page, "doc/"):
		raw := page[len("doc/"):]
		if strings.ContainsAny(raw, "?#\\") {
			return Link{}, ErrNotViewLink
		}
		segs := strings.Split(raw, "/")
		for i, s := range segs {
			u, err := url.PathUnescape(s)
			if err != nil || strings.Contains(u, "/") {
				return Link{}, ErrNotViewLink
			}
			segs[i] = u
		}
		if _, ok := docSegments(strings.Join(segs, "/")); !ok {
			return Link{}, ErrNotViewLink
		}
		l.Page, l.Path = Doc, strings.Join(segs, "/")
		return l, nil
	}
	base, report, hasReport := strings.Cut(page, "#")
	if !refRe.MatchString(base) || hasReport && (!strings.HasPrefix(base, "task/") || !repRe.MatchString(report)) {
		return Link{}, ErrNotViewLink
	}
	l.Page, l.Ref = Item, base[strings.IndexByte(base, '/')+1:]
	l.Report = report
	return l, nil
}

func docSegments(p string) ([]string, bool) {
	if p == "" || len(p) > 4096 {
		return nil, false
	}
	segs := strings.Split(p, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || badText(s) || strings.ContainsAny(s, "\\") {
			return nil, false
		}
	}
	return segs, true
}

func badText(s string) bool {
	return !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsControl(r) || r == 0x200E || r == 0x200F || r == 0x061C || r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069
	})
}
