package desktop

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/atqamz/hand/internal/viewlink"
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

func ParseLink(link string) error {
	if strings.HasPrefix(link, viewlink.Head) {
		_, err := viewlink.Parse(link)
		return err
	}
	_, err := ParseOpenLink(link)
	return err
}

func (a *App) openView(link string) error {
	v, err := viewlink.Parse(link)
	if err != nil {
		a.setErr(err.Error())
		return err
	}
	if a.Model == nil || len(a.Fleets) == 0 {
		a.pendingLink = link
		return nil
	}
	if !a.hasFleet(v.Fleet) {
		err := fmt.Errorf("no registered fleet has the id %s; run `hand init` in its folder or check the alert", v.Fleet)
		a.setErr(err.Error())
		return err
	}
	a.SelectFleet(v.Fleet)
	switch v.Page {
	case viewlink.Item:
		if v.Report != "" {
			a.OpenRef(v.Report)
		} else {
			a.OpenRef(v.Ref)
		}
	case viewlink.Doc:
		a.OpenDoc(v.Path)
	case viewlink.Tasks:
		a.OpenTaskSearch(v.Query)
	case viewlink.Log:
		a.Show(HistoryScreen)
	case viewlink.Companion:
		a.Show(PhoneScreen)
	case viewlink.Alerts:
		a.Show(AlertsScreen)
	}
	return nil
}

func (a *App) OpenLink(link string) error {
	if strings.HasPrefix(link, viewlink.Head) {
		return a.openView(link)
	}
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
