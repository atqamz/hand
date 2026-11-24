package desktop

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

type alertsState struct {
	loaded, old bool
	scroll      ui.ScrollState
}

type AlertStats struct {
	Total   int    `json:"total"`
	Unacked int    `json:"unacked"`
	Slow    int    `json:"slow"`
	P50     *int64 `json:"p50_seconds"`
	P95     *int64 `json:"p95_seconds"`
	Max     *int64 `json:"max_seconds"`
}

type AlertReceipt struct {
	Seq       int64  `json:"seq"`
	At        string `json:"at"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	Text      string `json:"text"`
	FetchedAt string `json:"fetched_at"`
	AckedAt   string `json:"acked_at"`
	Delay     *int64 `json:"delay_seconds"`
	Slow      bool   `json:"slow"`
}

type AlertsData struct {
	Days      int            `json:"days"`
	SlowAfter int            `json:"slow_after_seconds"`
	Stats     AlertStats     `json:"stats"`
	Alerts    []AlertReceipt `json:"alerts"`
}

func (c *HTTPClient) Alerts(ctx context.Context, fleet string) (AlertsData, error) {
	var out AlertsData
	_, err := c.get(ctx, fleet, "/alerts", &out)
	return out, err
}

func (r *Router) Alerts(ctx context.Context, fleet string) (AlertsData, error) {
	return route(r, fleet, func(m ReadModel) (AlertsData, error) { return m.Alerts(ctx, fleet) })
}

func readAlerts(ctx context.Context, model ReadModel, fleet string) (AlertsData, bool, error) {
	if fs, err := model.Features(ctx, fleet); err == nil && !slices.Contains(fs, "alerts") {
		return AlertsData{}, true, nil
	}
	d, err := model.Alerts(ctx, fleet)
	return d, false, err
}

func alertDelay(secs int64) string {
	return (time.Duration(secs) * time.Second).String()
}

func alertRefLink(ref string) bool {
	if len(ref) < 2 || !strings.ContainsRune("tadr", rune(ref[0])) {
		return false
	}
	_, err := strconv.Atoi(ref[1:])
	return err == nil
}

func (a *App) alertsPage(c *ui.Context, k kit.Colors, p palette) {
	w, _ := c.Size()
	m := max(float32(kit.Space20), (w-kit.Measure)/2)
	page := ui.Scroll(c).Key("alerts-scroll").Grow(1).Padding(kit.Space20, m, 32).
		TrackScroll(&a.al.scroll).Focusable().FocusRing(false).Label("Alerts")
	page.Children(func() {
		ui.Column(c).Gap(kit.Space16).Selectable().Children(func() {
			ui.Text(c, "Fleets / "+fleetName(a.Fleet)+" / alerts").FontSize(kit.Body.Size).TextColor(p.neutral)
			ui.Text(c, "Alerts").FontSize(kit.Display.Size).Bold()
			a.alertsBody(c, p)
		})
	})
}

func (a *App) alertsBody(c *ui.Context, p palette) {
	switch {
	case a.al.old:
		a.emptyCard(c, p, "This host is too old for Alerts", "Update hand on the host and restart its Board to see its alert receipts here.")
		return
	case !a.al.loaded && a.Err != "":
		a.errorCardFor(c, p, a.Err)
		return
	case !a.al.loaded:
		a.emptyCard(c, p, "Reading the alerts", "The window waits for the board to answer.")
		return
	}
	d := a.Alerts
	days := d.Days
	if days == 0 {
		days = 7
	}
	slow := d.SlowAfter
	if slow == 0 {
		slow = 60
	}
	limit := alertDelay(int64(slow))
	if slow == 60 {
		limit = "1 minute"
	}
	ui.Textf(c, "The last %d days, as the Hand app reads them. Delay is the time from raised to acknowledged. Over %s counts as slow.", days, limit).
		FontSize(kit.Meta.Size).TextColor(p.neutral)
	a.alertStats(c, p, d.Stats)
	if len(d.Alerts) == 0 {
		a.emptyCard(c, p, "No alerts", fmt.Sprintf("No alerts in the last %d days.", days))
		return
	}
	ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Label("alert receipts").Children(func() {
		for _, r := range d.Alerts {
			a.alertRow(c, p, r)
		}
	})
}

func (a *App) alertStats(c *ui.Context, p palette, s AlertStats) {
	stat := func(n, label string, warn bool) {
		bg, fg := p.neutralBG, p.neutral
		if warn {
			bg, fg = p.waitBG, p.wait
		}
		ui.Row(c).Gap(kit.Space6).AlignItems(ui.Center).Label(label + " " + n).Children(func() {
			ui.Badge(c, n).Background(bg).TextColor(fg)
			ui.Text(c, label).FontSize(kit.Meta.Size).TextColor(p.neutral)
		})
	}
	ui.Row(c).Gap(kit.Space16).AlignItems(ui.Center).Wrap().Label("alert stats").Children(func() {
		stat(strconv.Itoa(s.Total), "alerts", false)
		stat(strconv.Itoa(s.Unacked), "not acknowledged", s.Unacked > 0)
		stat(strconv.Itoa(s.Slow), "slow", s.Slow > 0)
		if s.P50 != nil {
			stat(alertDelay(*s.P50), "median delay", false)
		}
		if s.P95 != nil {
			stat(alertDelay(*s.P95), "95th percentile delay", false)
		}
		if s.Max != nil {
			stat(alertDelay(*s.Max), "longest delay", false)
		}
	})
}

func (a *App) alertRow(c *ui.Context, p palette, r AlertReceipt) {
	key := strconv.FormatInt(r.Seq, 10)
	ui.Column(c).Gap(kit.Space4).Padding(kit.Space8, kit.Space4).BorderWidth(0, 0, 1, 0).BorderColor(p.line).
		Role(ui.RoleListItem).Label("alert " + key).Children(func() {
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, stamp(r.At)).FontSize(kit.Meta.Size).TextColor(p.neutral)
			ui.Text(c, r.Kind).Font("monospace").FontSize(kit.Meta.Size)
			if alertRefLink(r.Ref) {
				a.taskLink(c, p, "alert-ref:"+key, r.Ref, r.Ref)
			} else if r.Ref != "" {
				ui.Text(c, r.Ref).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
			}
		})
		ui.Text(c, r.Text).FontSize(kit.Body.Size)
		ui.Row(c).Gap(kit.Space12).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, "Fetched "+alertWhen(r.FetchedAt)).FontSize(kit.Meta.Size).TextColor(p.neutral)
			ui.Text(c, "Acknowledged "+alertWhen(r.AckedAt)).FontSize(kit.Meta.Size).TextColor(p.neutral)
			switch {
			case r.Delay == nil:
				ui.Text(c, "Delay no").FontSize(kit.Meta.Size).TextColor(p.neutral)
			case r.Slow:
				ui.Text(c, "Delay").FontSize(kit.Meta.Size).TextColor(p.neutral)
				ui.Badge(c, alertDelay(*r.Delay)).Background(p.waitBG).TextColor(p.wait)
				ui.Text(c, "slow").FontSize(kit.Meta.Size).Bold().TextColor(p.wait)
			default:
				ui.Text(c, "Delay "+alertDelay(*r.Delay)).FontSize(kit.Meta.Size).TextColor(p.neutral)
			}
		})
	})
}

func alertWhen(at string) string {
	if at == "" {
		return "no"
	}
	return stamp(at)
}

func (a *App) resetAlerts() {
	a.Alerts, a.al = AlertsData{}, alertsState{}
}
