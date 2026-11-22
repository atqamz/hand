package desktop

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
	"github.com/atqamz/hand/internal/desktop/remote"
	"github.com/atqamz/hand/internal/sidecar"
)

const defaultDeviceName = "Hand Desktop"

type remoteForm struct {
	link, name, err string
	pending         *remote.Link
	again           string
	host            string
	busy            bool
	retry           bool
}

func (a *App) remoteAvailable() bool { return a.Remote != nil || a.RemoteOff != "" }

func (a *App) remoteFleet() bool { return a.Fleet.Host != "" }

func (a *App) openRemoteForm(again Fleet) {
	if a.rf.busy {
		return
	}
	a.nfOpen = false
	a.resetNewFleet()
	a.rf = remoteForm{name: defaultDeviceName, again: again.ID, host: again.Host}
	a.rfOpen = true
}

func (a *App) closeRemoteForm() {
	if a.rf.busy {
		return
	}
	a.rfOpen = false
	a.rf = remoteForm{}
	a.rfGen++
}

func (a *App) checkRemoteLink() {
	if a.rf.busy {
		return
	}
	text := a.rf.link
	a.rf.link, a.rf.err = "", ""
	link, err := remote.ParsePasted(text)
	if err != nil {
		a.rf.err = LinkText(err)
		return
	}
	if a.rf.again != "" && link.Host != a.rf.host {
		a.rf.err = pairTexts["other_host"]
		return
	}
	if a.rf.again != "" && link.Fleet != a.rf.again {
		a.rf.err = pairTexts["other_fleet"]
		return
	}
	if a.hasFleet(link.Fleet) && a.rf.again == "" {
		a.rf.err = pairTexts["fleet_held"]
		return
	}
	a.rf.pending = &link
}

func (a *App) backToLink() {
	if a.rf.busy {
		return
	}
	a.rf.pending, a.rf.err, a.rf.retry = nil, "", false
}

func (a *App) addRemote() {
	if a.rf.busy || a.rf.pending == nil || a.Remote == nil {
		return
	}
	name := a.rf.name
	if name == "" {
		name = defaultDeviceName
	}
	held := make(map[string]bool, len(a.Fleets))
	for _, f := range a.Fleets {
		held[f.ID] = true
	}
	req := PairRequest{Link: *a.rf.pending, Name: name, Again: a.rf.again != "", Held: func(id string) bool { return held[id] }}
	a.rf.busy, a.rf.err = true, ""
	gen, desk, ctx := a.rfGen, a.Remote, a.Context
	a.offload(func() func() {
		ctx, stop := context.WithTimeout(ctx, 90*time.Second)
		defer stop()
		err := desk.Pair(ctx, req)
		return func() {
			if gen != a.rfGen {
				return
			}
			a.rf.busy = false
			var pe *PairError
			switch {
			case err == nil:
				a.pendingFleet = req.Link.Fleet
				a.rfOpen = false
				a.rf = remoteForm{}
				a.Refresh()
			case errors.As(err, &pe) && pe.Transient:
				a.rf.err, a.rf.retry = err.Error(), true
			case errors.Is(err, context.Canceled):
			default:
				a.rf.err, a.rf.pending = err.Error(), nil
			}
		}
	})
}

func (a *App) forgetHost(host string) {
	if a.Remote == nil {
		return
	}
	desk := a.Remote
	a.offload(func() func() {
		err := desk.Forget(host)
		return func() {
			if err != nil {
				a.ActionErr, a.ActionWarn = "Could not forget the host: "+err.Error(), false
			}
			a.Refresh()
		}
	})
}

func (a *App) remoteFields(c *ui.Context, k kit.Colors, p palette) {
	switch {
	case a.Remote == nil:
		ui.Text(c, a.RemoteOff).FontSize(kit.Body.Size)
		return
	case a.rf.pending != nil:
		l := a.rf.pending
		if l.Name != "" {
			ui.Text(c, "The link says it is “"+l.Name+"”.").FontSize(kit.Body.Size)
		}
		ui.Text(c, "Only the host's ID identifies it. Check "+remote.Short(l.Host)+" against the Companion page on that host.").
			FontSize(kit.Meta.Size).TextColor(k.Muted)
	default:
		kit.FieldLabel(c, k, "Link")
		submit := ui.TextInput(c, &a.rf.link).Placeholder("handboard://c#v=1&…").Label("Pairing link").Submitted()
		kit.FieldLabel(c, k, "Name")
		ui.TextInput(c, &a.rf.name).Placeholder(defaultDeviceName).Label("Device name")
		a.rf.name = clipRunes(a.rf.name, 40)
		if submit {
			a.checkRemoteLink()
		}
	}
	if a.rf.err != "" {
		ui.Text(c, a.rf.err).FontSize(kit.Body.Size).TextColor(p.fail)
	}
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (a *App) remoteButtons(c *ui.Context, k kit.Colors) {
	switch {
	case a.Remote == nil:
	case a.rf.pending != nil:
		label := "Add"
		switch {
		case a.rf.busy:
			label = "Connecting…"
		case a.rf.retry:
			label = "Try again"
		}
		if button(c, k, "rf:add", label, true) {
			a.addRemote()
		}
		if button(c, k, "rf:back", "Back", false) {
			a.backToLink()
		}
		return
	default:
		if button(c, k, "rf:continue", "Continue", true) {
			a.checkRemoteLink()
		}
	}
	if button(c, k, "rf:cancel", "Cancel", false) {
		a.closeRemoteForm()
	}
}

func (a *App) remoteTitle() (title, body string) {
	title, body = "Connect to a remote fleet.", "Paste the pairing link from the Companion page of the host's Board. This window never saves the link or its code."
	if l := a.rf.pending; l != nil {
		title, body = "Add "+remote.Short(l.Host)+"?", "This window will pair with that host and list its fleet."
	}
	if a.rf.again != "" && a.rf.pending == nil {
		title, body = "Pair again.", "Show a new code on the host's Companion page and paste its link."
	}
	return
}

type refusalText struct{ title, body string }

var refusals = map[string]refusalText{
	CodeRevoked:       {"Revoked on the host.", "The host removed this device. Pair it again with a new code, or forget the host."},
	CodeNotRecognised: {"The host does not recognise this device.", "Its pairing may have lapsed after 90 days, or the host removed it. Pair it again, or forget the host."},
	CodeKeyChanged:    {"This device's key is not the one the host paired.", "The key in this window's remote folder was replaced. Pair it again, or forget the host."},
}

func (a *App) refusal() string {
	if !a.remoteFleet() {
		return ""
	}
	r, ok := a.Model.(interface{ Refusal(string) string })
	if !ok {
		return ""
	}
	return r.Refusal(a.Fleet.ID)
}

type linkMark struct{ word, icon, tone string }

func linkFor(st sidecar.ClientStatus, refusal string) linkMark {
	switch refusal {
	case CodeRevoked:
		return linkMark{"Revoked", "failure", "fail"}
	case CodeNotRecognised:
		return linkMark{"Not recognised", "failure", "fail"}
	case CodeKeyChanged:
		return linkMark{"Key replaced", "failure", "fail"}
	}
	switch {
	case st.State == sidecar.Failed || st.State == sidecar.Stopped:
		return linkMark{"Can't reach host", "failure", "fail"}
	case st.State != sidecar.Ready:
		return linkMark{"Connecting", "running", "accent"}
	}
	switch st.Link {
	case sidecar.Connected:
		return linkMark{"Connected", "passing", "pass"}
	case sidecar.Busy:
		return linkMark{"Host busy", "waiting", "wait"}
	case sidecar.Unreachable:
		return linkMark{"Can't reach host", "failure", "fail"}
	case sidecar.BoardDown:
		return linkMark{"Board not running", "idle", "wait"}
	}
	return linkMark{"Connecting", "running", "accent"}
}

func (a *App) linkOf(f Fleet) (linkMark, bool) {
	if f.Host == "" || a.Remote == nil {
		return linkMark{}, false
	}
	code := ""
	if r, ok := a.Model.(interface{ Refusal(string) string }); ok {
		code = r.Refusal(f.ID)
	}
	return linkFor(a.Remote.Status(f.Host), code), true
}

func (a *App) loadHosts() {
	a.hosts = nil
	if a.Remote == nil {
		return
	}
	hosts, _ := a.Remote.Hosts()
	a.hosts = hosts
}

func (a *App) hostOf(id string) (remote.Host, bool) {
	i := slices.IndexFunc(a.hosts, func(h remote.Host) bool { return h.ID == id })
	if i < 0 {
		return remote.Host{}, false
	}
	return a.hosts[i], true
}

func (a *App) pairedLine(host string) string {
	h, ok := a.hostOf(host)
	if !ok || h.PairedAt.IsZero() {
		return "Host " + remote.Short(host)
	}
	return "Host " + remote.Short(host) + " · paired " + h.PairedAt.UTC().Format("2006-01-02")
}

func (a *App) linkPill(c *ui.Context, p palette) {
	m, ok := a.linkOf(a.Fleet)
	if !ok {
		return
	}
	bg, fg := toneColors(m.tone, p)
	ui.Row(c).Gap(5).Padding(1, 8).Radius(999).Background(bg).Label(m.word).Children(func() {
		ui.Icon(c, kit.Icon(m.icon)).TextColor(fg)
		ui.Text(c, m.word).FontSize(kit.Meta.Size).Bold().TextColor(fg)
	}).Tooltip(a.pairedLine(a.Fleet.Host))
}

func (a *App) refusalCard(c *ui.Context, k kit.Colors, p palette, code string) {
	t := refusals[code]
	host := a.Fleet.Host
	fleet := a.Fleet
	ui.Column(c).MaxWidth(640).AlignSelf(ui.Center).Border(1, k.Line).Radius(kit.RadiusLG).Clip().Background(k.BG).Children(func() {
		ui.Row(c).Background(k.Card).Height(kit.Row).Padding(0, 15).Gap(kit.Space10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Host").FontSize(kit.Meta.Size).Bold().TextColor(p.fail)
			ui.Text(c, a.pairedLine(host)).FontSize(kit.Meta.Size).TextColor(k.Muted)
		})
		ui.Column(c).Padding(15, 15, 16).Gap(kit.Space12).Children(func() {
			ui.Text(c, t.title).FontSize(kit.Title.Size).Bold()
			ui.Text(c, t.body).FontSize(kit.Body.Size)
			ui.Row(c).Gap(kit.Space8).Children(func() {
				if a.Remote != nil && button(c, k, "rf:again", "Pair again", true) {
					a.openRemoteForm(fleet)
				}
				if a.Remote != nil && button(c, k, "rf:forget", "Forget host", false) {
					a.forgetHost(host)
				}
			})
			ui.Text(c, "Forget removes this device's key and cookies from this computer. The host keeps the device until someone revokes it there.").
				FontSize(kit.Meta.Size).TextColor(k.Muted)
		})
	})
}
