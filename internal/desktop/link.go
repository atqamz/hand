package desktop

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrNotOpenLink = errors.New("not an open link: expected handboard://open/<fleet id>/[task/tN | decision/dN | attempt/aN][#rN]")

	linkFleet = regexp.MustCompile(`^f[0-9a-f]{12}$`)
	linkPage  = regexp.MustCompile(`^(?:(?:task/t|decision/d|attempt/a)[1-9][0-9]{0,8})?$`)
	linkRep   = regexp.MustCompile(`^r[1-9][0-9]{0,8}$`)
)

const linkHead = "handboard://open/"

type OpenLink struct {
	Fleet string
	Ref   string
}

func ParseOpenLink(link string) (OpenLink, error) {
	rest, ok := strings.CutPrefix(link, linkHead)
	if !ok {
		return OpenLink{}, ErrNotOpenLink
	}
	rest, report, hasReport := strings.Cut(rest, "#")
	fleet, page, ok := strings.Cut(rest, "/")
	if !ok || !linkFleet.MatchString(fleet) || !linkPage.MatchString(page) {
		return OpenLink{}, ErrNotOpenLink
	}
	ref := ""
	if page != "" {
		ref = page[strings.IndexByte(page, '/')+1:]
	}
	if hasReport {
		if !strings.HasPrefix(page, "task/") || !linkRep.MatchString(report) {
			return OpenLink{}, ErrNotOpenLink
		}
		ref = report
	}
	return OpenLink{Fleet: fleet, Ref: ref}, nil
}

func (a *App) OpenLink(link string) error {
	l, err := ParseOpenLink(link)
	if err != nil {
		a.setErr(err.Error())
		return err
	}
	if a.Model == nil || len(a.Fleets) == 0 {
		a.pendingLink = link
		return nil
	}
	if !a.hasFleet(l.Fleet) {
		err := fmt.Errorf("no registered fleet has the id %s; run `hand init` in its folder or check the alert", l.Fleet)
		a.setErr(err.Error())
		return err
	}
	a.SelectFleet(l.Fleet)
	a.OpenRef(l.Ref)
	return nil
}

func (a *App) Fail(msg string) { a.setErr(msg) }
