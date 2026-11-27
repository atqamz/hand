package desktop

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
	"github.com/atqamz/hand/internal/qr"
)

const (
	phoneReadEvery = 2 * time.Second
	phoneIdleEvery = 15 * time.Second
	phoneActWait   = 30 * time.Second
	phoneQuiet     = 4
	phoneQRMax     = 280
	phoneBaseHint  = "Type the address your Companion opens, like https://host.example"
	phoneTooLong   = "The link is too long for a QR code. Copy the link, or open the address and type the code."
	phoneOld       = "Update hand on the host and restart its Board to pair and manage Companions here."
	phoneRemote    = "This fleet is on a paired host. A paired Companion cannot manage Companions; use the host's Board."
)

type phoneTick struct {
	id, name    string
	current, on bool
}

type phoneNote struct {
	text  string
	warn  bool
	again *PhoneRevokeRequest
	label string
}

type phoneQR struct {
	link Secret
	bmp  *ui.Bitmap
	side int
	long bool
}

type phoneState struct {
	data        Phone
	loaded, old bool
	err, stale  string
	reading     bool
	rgen, agen  int
	busy        string
	actFor      string
	actErr      string
	base        string
	ticks       []phoneTick
	confirm     string
	note        *phoneNote
	qr, appQR   phoneQR
	scroll      ui.ScrollState
	fails, skip int
	tick        int
}

func (a *App) resetPhone() {
	p := a.ph
	a.ph = phoneState{rgen: p.rgen + 1, agen: p.agen + 1}
}

func (a *App) openPhone() {
	if a.Model == nil || a.Fleet.ID == "" {
		return
	}
	a.resetPhone()
	a.loadPhone()
}

func (a *App) phoneReady() bool {
	return a.Model != nil && a.Fleet.ID != "" && a.Screen == PhoneScreen && !a.remoteFleet()
}

func (a *App) offThreadFor(wait time.Duration, work func(ctx context.Context) func()) {
	ctx, cancel := context.WithTimeout(a.Context, wait)
	post := a.Post
	if post == nil {
		apply := work(ctx)
		cancel()
		apply()
		return
	}
	go func() {
		apply := work(ctx)
		cancel()
		post(apply)
	}()
}

func (a *App) phoneLive(expires string) bool {
	t, ok := parseAt(expires)
	return ok && a.now().Before(t)
}

func (a *App) phoneCode() *PhoneCode {
	if c := a.ph.data.Code; c != nil && a.phoneLive(c.ExpiresAt) {
		return c
	}
	return nil
}

func (a *App) phoneApp() *PhoneApp {
	if c := a.ph.data.App; c != nil && a.phoneLive(c.ExpiresAt) {
		return c
	}
	return nil
}

func (a *App) expirePhone() {
	if a.ph.data.Code != nil && a.phoneCode() == nil {
		a.ph.data.Code, a.ph.qr = nil, phoneQR{}
	}
	if a.ph.data.App != nil && a.phoneApp() == nil {
		a.ph.data.App, a.ph.appQR = nil, phoneQR{}
	}
}

func qrImage(link string) (*image.Gray, bool) {
	m, err := qr.Matrix(link)
	if err != nil {
		return nil, false
	}
	side := len(m) + 2*phoneQuiet
	img := image.NewGray(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	for y := range m {
		for x := range m[y] {
			if m[y][x] {
				img.SetGray(x+phoneQuiet, y+phoneQuiet, color.Gray{})
			}
		}
	}
	return img, true
}

func phoneQRFor(old phoneQR, link Secret) phoneQR {
	if link == "" {
		return phoneQR{}
	}
	if old.link == link {
		return old
	}
	img, ok := qrImage(link.Reveal())
	if !ok {
		return phoneQR{link: link, long: true}
	}
	side := img.Bounds().Dx()
	return phoneQR{link: link, bmp: ui.NewBitmap(img).Pixelated(), side: side}
}

func (a *App) setPhone(d Phone) {
	d.Receipt, d.Warn, d.Revoke = "", "", nil
	a.ph.data, a.ph.loaded = d, true
	a.ph.err, a.ph.stale, a.ph.fails, a.ph.skip = "", "", 0, 0
	a.syncTicks(d)
	if a.ph.base == "" && d.Code != nil {
		a.ph.base = d.Code.Address
	}
	a.expirePhone()
	var code, app Secret
	if a.ph.data.Code != nil {
		code = a.ph.data.Code.Link
	}
	if a.ph.data.App != nil {
		app = a.ph.data.App.Link
	}
	a.ph.qr = phoneQRFor(a.ph.qr, code)
	a.ph.appQR = phoneQRFor(a.ph.appQR, app)
}

func (a *App) syncTicks(d Phone) {
	prev := map[string]bool{}
	for _, t := range a.ph.ticks {
		prev[t.id] = t.on
	}
	first := len(a.ph.ticks) == 0
	a.ph.ticks = a.ph.ticks[:0]
	for _, f := range d.Fleets {
		on, ok := prev[f.ID]
		if first || !ok {
			on = f.Selected
		}
		a.ph.ticks = append(a.ph.ticks, phoneTick{id: f.ID, name: f.Name, current: f.Current, on: on || f.Current})
	}
}

func (a *App) loadPhone() {
	if !a.phoneReady() || a.ph.reading || a.ph.busy != "" {
		return
	}
	a.ph.rgen++
	a.ph.reading = true
	rgen, fleet, model, first := a.ph.rgen, a.Fleet.ID, a.Model, !a.ph.loaded
	a.offThread(func(ctx context.Context) func() {
		old := false
		if first {
			if fs, err := model.Features(ctx, fleet); err == nil && !slices.Contains(fs, "phone") {
				old = true
			}
		}
		var d Phone
		var err error
		if !old {
			d, err = model.Phone(ctx, fleet)
		}
		return func() {
			if a.Fleet.ID != fleet || a.ph.rgen != rgen {
				return
			}
			a.ph.reading = false
			if old {
				a.ph.old = true
				return
			}
			if err != nil {
				a.phoneFailed(err)
				return
			}
			a.setPhone(d)
		}
	})
}

func (a *App) phoneFailed(err error) {
	if a.ph.loaded {
		a.ph.stale = skillsFriendly(err)
	} else {
		a.ph.err = skillsFriendly(err)
	}
	a.ph.fails++
	a.ph.skip = a.pollSkips(a.ph.fails)
}

func (a *App) RetryPhone() {
	a.ph.err = ""
	if a.Err != "" {
		a.Retry()
	}
	a.loadPhone()
}

func (a *App) phoneEvery(d time.Duration) int {
	return max(1, int(d/a.pollDelay()))
}

func (a *App) pollPhone() {
	if !a.phoneReady() {
		return
	}
	a.expirePhone()
	if a.ph.busy != "" {
		return
	}
	a.ph.tick++
	switch {
	case a.ph.stale != "" || a.ph.err != "":
		if a.ph.skip > 0 {
			a.ph.skip--
			return
		}
		a.loadPhone()
	case (a.ph.data.Code != nil || a.ph.data.App != nil) && a.ph.tick%a.phoneEvery(phoneReadEvery) == 0:
		a.loadPhone()
	case a.ph.tick%a.phoneEvery(phoneIdleEvery) == 0:
		a.loadPhone()
	}
}

func (a *App) phoneAct(kind string, act func(ctx context.Context, m ReadModel, fleet string) (Phone, error), after func(Phone)) {
	if !a.phoneReady() || a.ph.busy != "" {
		return
	}
	a.ph.agen++
	a.ph.rgen++
	a.ph.reading = false
	agen, fleet, model := a.ph.agen, a.Fleet.ID, a.Model
	a.ph.busy, a.ph.actFor, a.ph.actErr, a.ph.confirm = kind, kind, "", ""
	a.offThreadFor(phoneActWait, func(ctx context.Context) func() {
		d, err := act(ctx, model, fleet)
		return func() {
			if a.Fleet.ID != fleet || a.ph.agen != agen {
				return
			}
			a.ph.busy = ""
			var unread *AppliedUnread
			if err != nil && !errors.As(err, &unread) {
				a.ph.actErr = skillsFriendly(err)
				return
			}
			if unread != nil {
				a.ph.actErr = "the board took the request but its answer could not be read; the list is read again"
				a.loadPhone()
				return
			}
			a.setPhone2(d, after)
		}
	})
}

func (a *App) setPhone2(d Phone, after func(Phone)) {
	after(d)
	a.setPhone(d)
}

func (a *App) mintPhoneCode() {
	base := strings.TrimSpace(a.ph.base)
	if base == "" {
		a.ph.actFor, a.ph.actErr = "code", phoneBaseHint
		return
	}
	var ids []string
	for _, t := range a.ph.ticks {
		if t.on && !t.current {
			ids = append(ids, t.id)
		}
	}
	a.ph.note = nil
	a.phoneAct("code", func(ctx context.Context, m ReadModel, fleet string) (Phone, error) {
		return m.PhoneCode(ctx, fleet, PhoneCodeRequest{Base: base, Fleets: ids})
	}, func(Phone) {})
}

func (a *App) mintPhoneApp() {
	a.ph.note = nil
	a.phoneAct("app", func(ctx context.Context, m ReadModel, fleet string) (Phone, error) {
		return m.PhoneApp(ctx, fleet)
	}, func(Phone) {})
}

func (a *App) revokePhone(req PhoneRevokeRequest) {
	a.phoneAct("revoke", func(ctx context.Context, m ReadModel, fleet string) (Phone, error) {
		return m.PhoneRevoke(ctx, fleet, req)
	}, func(d Phone) { a.ph.note = phoneRevokeNote(req, d) })
}

func phoneRevokeNote(req PhoneRevokeRequest, d Phone) *phoneNote {
	var done, failed []string
	if d.Revoke != nil {
		for _, f := range d.Revoke.Revoked {
			done = append(done, f.Name)
		}
		for _, f := range d.Revoke.Failed {
			failed = append(failed, f.Name)
		}
	}
	again := "Revoke everywhere"
	if req.Fleet != "" || req.Phone == "" {
		again = "Revoke"
	}
	switch {
	case len(failed) > 0 || d.Warn != "":
		text := d.Warn
		if text == "" {
			text = phoneRevokeText(done, failed, again)
		}
		return &phoneNote{text: text, warn: true, again: &req, label: again}
	case d.Receipt != "":
		return &phoneNote{text: d.Receipt}
	case len(done) > 0:
		return &phoneNote{text: "Revoked in " + joinNames(done)}
	}
	return nil
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func phoneRevokeText(done, failed []string, again string) string {
	var sb strings.Builder
	if len(done) > 0 {
		sb.WriteString("Revoked in " + joinNames(done) + ". ")
	} else {
		sb.WriteString("Nothing was revoked. ")
	}
	back := "it is back"
	if len(failed) == 1 {
		sb.WriteString(failed[0] + " could not be reached: its record of this Companion, if it has one, is still active. ")
	} else {
		sb.WriteString(joinNames(failed) + " could not be reached: their records of this Companion, if they have any, are still active. ")
		back = "they are back"
	}
	sb.WriteString("Press " + again + " again when " + back + ".")
	return sb.String()
}

func (a *App) phonePage(c *ui.Context, k kit.Colors, p palette) {
	w, _ := c.Size()
	m := max(float32(kit.Space20), (w-kit.Measure)/2)
	page := ui.Scroll(c).Key("phone-scroll").Grow(1).Padding(kit.Space20, m, 32).
		TrackScroll(&a.ph.scroll).Focusable().FocusRing(false).Label("Companion")
	page.Children(func() {
		ui.Column(c).Gap(kit.Space16).Children(func() {
			ui.Text(c, "Fleets / "+fleetName(a.Fleet)+" / companion").FontSize(kit.Body.Size).TextColor(p.neutral)
			ui.Text(c, "Connect your Companion").FontSize(kit.Display.Size).Bold()
			a.phoneBody(c, k, p)
		})
	})
}

func (a *App) phoneBody(c *ui.Context, k kit.Colors, p palette) {
	switch {
	case a.remoteFleet():
		a.emptyCard(c, p, "This fleet cannot manage Companions", phoneRemote)
		return
	case a.ph.old:
		a.emptyCard(c, p, "This host is too old for the Companion page", phoneOld)
		return
	case !a.ph.loaded && a.ph.err != "":
		a.phoneErrorCard(c, k, p, a.ph.err)
		return
	case !a.ph.loaded && a.Err != "":
		a.phoneErrorCard(c, k, p, a.Err)
		return
	case !a.ph.loaded:
		a.emptyCard(c, p, "Reading the Companions", "The window waits for the board to answer.")
		return
	}
	d := a.ph.data
	if a.ph.stale != "" {
		ui.Row(c).MinHeight(24).AlignItems(ui.Center).Children(func() {
			ui.Spacer(c)
			ui.Text(c, "Stale · retrying").FontSize(kit.Meta.Size).TextColor(p.wait).Tooltip(a.ph.stale)
			if kit.Button(c, k, "phone:retry", "Retry", 80, false) {
				a.RetryPhone()
			}
		})
	}
	a.phoneNetwork(c, k, p, d)
	a.phonePair(c, k, p, d)
	a.phoneAppPair(c, k, p, d)
	a.phoneDevices(c, k, p, d)
}

func (a *App) phoneErrorCard(c *ui.Context, k kit.Colors, p palette, msg string) {
	ui.Column(c).Gap(kit.Space10).Padding(kit.Space16).Radius(kit.RadiusLG).Background(p.failBG).
		Border(1, p.fail).Children(func() {
		ui.Text(c, "The board could not be read").FontSize(kit.Heading.Size).Bold().TextColor(p.fail)
		ui.Text(c, msg).FontSize(kit.Body.Size)
		if kit.Button(c, k, "phone:retry", "Retry", 100, false) {
			a.RetryPhone()
		}
	})
}

func (a *App) phoneSection(c *ui.Context, k kit.Colors, p palette, title string, body func()) {
	ui.Column(c).Gap(kit.Space10).Padding(kit.Space16).Radius(kit.RadiusLG).Background(k.Panel).
		Border(1, p.line).Label(title).Children(func() {
		ui.Text(c, title).FontSize(kit.Heading.Size).Bold()
		body()
	})
}

func (a *App) phoneNoteCard(c *ui.Context, k kit.Colors, p palette) {
	n := a.ph.note
	if n == nil {
		return
	}
	bg, line := p.passBG, p.pass
	if n.warn {
		bg, line = p.waitBG, p.wait
	}
	ui.Column(c).Gap(kit.Space8).Padding(kit.Space12).Radius(kit.RadiusLG).Background(bg).
		Border(1, line).Label("revoke result").Children(func() {
		ui.Text(c, n.text).FontSize(kit.Body.Size)
		ui.Row(c).Gap(kit.Space8).Children(func() {
			if n.again != nil && kit.Button(c, k, "phone:again", n.label, 0, a.ph.busy != "") {
				req := *n.again
				a.revokePhone(req)
			}
			if kit.TextButton(c, k, "phone:note:dismiss", "Dismiss") {
				a.ph.note = nil
			}
		})
	})
}

func (a *App) phoneNetwork(c *ui.Context, k kit.Colors, p palette, d Phone) {
	a.phoneSection(c, k, p, "Network", func() {
		ui.Text(c, phoneReach(d.Iroh)).FontSize(kit.Body.Size).TextColor(p.neutral)
		if d.Iroh == nil {
			return
		}
		bg, fg := toneColors(d.Iroh.Tone, p)
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Wrap().Label("iroh " + d.Iroh.State).Children(func() {
			ui.Badge(c, d.Iroh.State).Background(bg).TextColor(fg)
			ui.Text(c, d.Iroh.Text).FontSize(kit.Meta.Size)
			if d.Iroh.ID != "" {
				ui.Text(c, "Endpoint ID").FontSize(kit.Meta.Size).TextColor(p.neutral)
				ui.Text(c, shortEndpointID(d.Iroh.ID)).Font(kit.Mono).FontSize(kit.Meta.Size).Tooltip(d.Iroh.ID)
				a.phoneCopyID(c, k, d.Iroh.ID)
			}
		})
	})
}

func (a *App) phonePair(c *ui.Context, k kit.Colors, p palette, d Phone) {
	a.phoneSection(c, k, p, "Pair a Companion", func() {
		kit.Field(c, k, "Address the Companion opens", "https://host.example", &a.ph.base)
		if len(a.ph.ticks) > 1 {
			ui.Text(c, "Fleets this Companion may open").FontSize(kit.Meta.Size).Bold().TextColor(k.Muted)
			for i := range a.ph.ticks {
				t := &a.ph.ticks[i]
				label := t.name
				if t.current {
					label += " (this fleet)"
				}
				ui.Checkbox(c.Key("phone:fleet:"+t.id), &t.on, label).Disabled(t.current)
			}
			ui.Text(c, "The Companion can read every fleet you tick and message its supervisor. Tick only the fleets it needs.").
				FontSize(kit.Meta.Size).TextColor(p.neutral)
		}
		label := "Show pairing code"
		code := a.phoneCode()
		if code != nil {
			label = "New code"
		}
		if kit.PrimaryButton(c, k, "phone:code", label, 160, a.ph.busy != "") {
			a.mintPhoneCode()
		}
		a.phoneActState(c, p, "code", "Making the code…")
		if code != nil {
			a.phoneCodeBlock(c, k, p, code)
		}
	})
}

func (a *App) phoneActState(c *ui.Context, p palette, kind, busy string) {
	switch {
	case a.ph.busy == kind:
		ui.Text(c, busy).FontSize(kit.Meta.Size).TextColor(p.neutral)
	case a.ph.actErr != "" && a.ph.actFor == kind:
		ui.Text(c, a.ph.actErr).FontSize(kit.Meta.Size).TextColor(p.fail)
	}
}

func (a *App) phoneCopy(c *ui.Context, k kit.Colors, key string, link Secret, expires string) {
	label := "Copy link"
	if a.copiedKey == key && !a.copiedAt.IsZero() && c.Now().Sub(a.copiedAt) < copyReceipt {
		label = "Copied"
	}
	if kit.Button(c, k, key, label, 0, false) {
		if !a.phoneLive(expires) {
			a.expirePhone()
			return
		}
		c.WriteClipboard(link.Reveal())
		a.copiedKey, a.copiedAt = key, c.Now()
		c.After(copyReceipt)
	}
}

func shortEndpointID(id string) string {
	r := []rune(id)
	if len(r) <= 9 {
		return id
	}
	return string(r[:4]) + "…" + string(r[len(r)-4:])
}

func phoneReach(i *PhoneIroh) string {
	if i != nil && i.State == "ready" {
		return "The Hand app reaches this computer over iroh with no address to type; any other Companion needs a tunnel you set up to the address you type below."
	}
	return "A Companion reaches this computer only through a tunnel you set up to the address you type below."
}

func (a *App) phoneCopyID(c *ui.Context, k kit.Colors, id string) {
	const key = "phone:id:copy"
	label := "Copy"
	if a.copiedKey == key && !a.copiedAt.IsZero() && c.Now().Sub(a.copiedAt) < copyReceipt {
		label = "Copied"
	}
	if kit.LabeledButton(c, k, key, label, "Copy endpoint ID", 0, false) {
		c.WriteClipboard(id)
		a.copiedKey, a.copiedAt = key, c.Now()
		c.After(copyReceipt)
	}
}

func (a *App) phoneQRView(c *ui.Context, p palette, q phoneQR, label string) {
	switch {
	case q.bmp != nil:
		scale := max(2, phoneQRMax/q.side)
		size := float32(q.side * scale)
		ui.Image(c, q.bmp).Size(size, size).Label(label)
	case q.long:
		ui.Text(c, phoneTooLong).FontSize(kit.Meta.Size).TextColor(p.wait)
	}
}

func phoneLeft(d time.Duration) string {
	d = max(d, 0)
	secs := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

func (a *App) phoneUntil(c *ui.Context, p palette, expires, what string) {
	t, _ := parseAt(expires)
	left := phoneLeft(t.Sub(a.now()))
	c.After(time.Second)
	ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Badge(c, left+" left").Background(p.waitBG).TextColor(p.wait)
		ui.Text(c, what+" until "+t.Format("15:04")+". The codes are kept in memory only, so restarting the board cancels them.").
			FontSize(kit.Meta.Size).TextColor(p.neutral)
	})
}

func (a *App) phoneCodeBlock(c *ui.Context, k kit.Colors, p palette, code *PhoneCode) {
	ui.Row(c).Gap(kit.Space16).AlignItems(ui.Start).Label("pairing code").Children(func() {
		a.phoneQRView(c, p, a.ph.qr, "QR code for the pairing link")
		ui.Column(c).Gap(kit.Space8).Grow(1).MinWidth(0).Children(func() {
			lead := "On the Companion, open this link:"
			if a.ph.qr.bmp != nil {
				lead = "Scan the code with the Companion's camera, or open this link on the Companion:"
			}
			ui.Text(c, lead).FontSize(kit.Body.Size)
			ui.Text(c, code.Link.Reveal()).Font(kit.Mono).FontSize(kit.Meta.Size).Selectable()
			a.phoneCopy(c, k, "phone:copy", code.Link, code.ExpiresAt)
			ui.Text(c, "Or open "+code.URL+" and type the code:").FontSize(kit.Body.Size)
			ui.Text(c, code.Code.Reveal()).Font(kit.Mono).FontSize(kit.Display.Size).Bold().Selectable()
			if len(code.Fleets) > 1 {
				names := make([]string, len(code.Fleets))
				for i, f := range code.Fleets {
					names[i] = f.Name
				}
				ui.Text(c, "It pairs the Companion with "+joinNames(names)+".").FontSize(kit.Body.Size)
			}
			a.phoneUntil(c, p, code.ExpiresAt, "It works once,")
			if code.PlainHTTP {
				ui.Text(c, "This address is plain HTTP on a network. The code and the Companion's session cross it unencrypted unless the tunnel encrypts them, as SSH and WireGuard do.").
					FontSize(kit.Meta.Size).TextColor(p.wait)
			}
		})
	})
}

func (a *App) phoneAppPair(c *ui.Context, k kit.Colors, p palette, d Phone) {
	if d.Iroh == nil {
		return
	}
	a.phoneSection(c, k, p, "The Hand app", func() {
		if d.Iroh.State != "ready" {
			ui.Text(c, "Pairing the Hand app needs the iroh sidecar ready with a relay. "+d.Iroh.Text).
				FontSize(kit.Body.Size).TextColor(p.neutral)
			return
		}
		app := a.phoneApp()
		label := "Pair the Hand app"
		if app != nil {
			label = "New app code"
		}
		if kit.PrimaryButton(c, k, "phone:app", label, 180, a.ph.busy != "") {
			a.mintPhoneApp()
		}
		a.phoneActState(c, p, "app", "Making the link…")
		if app == nil {
			return
		}
		ui.Row(c).Gap(kit.Space16).AlignItems(ui.Start).Label("app pairing link").Children(func() {
			a.phoneQRView(c, p, a.ph.appQR, "QR code for the Hand app pairing link")
			ui.Column(c).Gap(kit.Space8).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, "Scan the code with the Hand app, or open this link on the Companion:").FontSize(kit.Body.Size)
				ui.Text(c, app.Link.Reveal()).Font(kit.Mono).FontSize(kit.Meta.Size).Selectable()
				a.phoneCopy(c, k, "phone:app:copy", app.Link, app.ExpiresAt)
				a.phoneUntil(c, p, app.ExpiresAt, "It pairs one Companion with this fleet, once,")
				ui.Text(c, "A QR code that may have leaked is worth revoking after the Companion pairs.").
					FontSize(kit.Meta.Size).TextColor(p.neutral)
			})
		})
	})
}

func (a *App) phoneDevices(c *ui.Context, k kit.Colors, p palette, d Phone) {
	a.phoneSection(c, k, p, "Paired Companions", func() {
		a.phoneNoteCard(c, k, p)
		if len(d.Unreachable) > 0 {
			who, own := joinNames(d.Unreachable), "its"
			if len(d.Unreachable) > 1 {
				own = "their"
			}
			ui.Text(c, who+" could not be reached, so "+own+" Companions are not listed here.").
				FontSize(kit.Meta.Size).TextColor(p.wait)
		}
		if len(d.Devices) == 0 {
			none := "No Companion is paired."
			if len(d.Unreachable) > 0 {
				none = "No Companion was found in the fleets that could be read."
			}
			ui.Text(c, none).FontSize(kit.Body.Size).TextColor(p.neutral)
		} else {
			ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Label("paired Companions").Children(func() {
				for _, dev := range d.Devices {
					a.phoneDevice(c, k, p, dev)
				}
			})
		}
		a.phoneActState(c, p, "revoke", "Revoking…")
		ui.Text(c, "A Companion stays signed in for 90 days, or until you revoke it here. Revoke signs a Companion out of one fleet, and Revoke everywhere out of every fleet this window is signed in to; a fleet it is not signed in to is left alone. A Companion that reads “this fleet only” was paired before one code could pair several fleets: pair it again with more fleets ticked.").
			FontSize(kit.Meta.Size).TextColor(p.neutral)
	})
}

func phoneDay(at string) string {
	t, ok := parseAt(at)
	if !ok {
		return shownText(at)
	}
	return t.Format("Jan 2")
}

func phoneDeviceKey(d PhoneDevice) string {
	if d.Phone != "" {
		return d.Phone
	}
	return "id" + strconv.FormatInt(d.ID, 10)
}

func (a *App) phoneNote2(d PhoneDevice) string {
	var parts []string
	if d.Expired {
		parts = append(parts, "signed out after 90 days")
	}
	if d.Phone == "" {
		parts = append(parts, "this fleet only")
	}
	if d.Login != "" {
		parts = append(parts, d.Login)
	}
	paired := "paired " + phoneDay(d.PairedAt)
	if d.SeenAt != "" {
		paired += ", seen " + clockOf(d.SeenAt, false)
	}
	return strings.Join(append(parts, paired), " · ")
}

func (a *App) phoneDevice(c *ui.Context, k kit.Colors, p palette, d PhoneDevice) {
	key := phoneDeviceKey(d)
	ui.Column(c).Gap(kit.Space6).Padding(kit.Space10, kit.Space4).BorderWidth(0, 0, 1, 0).BorderColor(p.line).
		Role(ui.RoleListItem).Label("Companion " + d.Name).Children(func() {
		ui.Row(c).Gap(kit.Space10).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(kit.Space2).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, d.Name).FontSize(kit.Body.Size).Bold()
				ui.Text(c, a.phoneNote2(d)).FontSize(kit.Meta.Size).TextColor(p.neutral)
			})
			switch {
			case len(d.Fleets) == 0:
				a.phoneRevokeButton(c, k, p, key, d.Name, "Revoke", "Revoke "+d.Name,
					"Revoke "+d.Name+"? It is signed out of this fleet.", PhoneRevokeRequest{ID: d.ID})
			case len(d.Fleets) > 1:
				a.phoneRevokeButton(c, k, p, key+":all", d.Name, "Revoke everywhere", "Revoke "+d.Name+" everywhere",
					"Revoke "+d.Name+" everywhere? It is signed out of every fleet this window is signed in to.", PhoneRevokeRequest{Phone: d.Phone})
			}
		})
		for _, at := range d.Fleets {
			name := at.Name
			if at.Current {
				name += " (this fleet)"
			}
			if at.Expired {
				name += " · signed out after 90 days"
			}
			ui.Column(c).Gap(kit.Space6).Padding(0, 0, 0, kit.Space16).Children(func() {
				ui.Row(c).Gap(kit.Space10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, name).FontSize(kit.Meta.Size).Grow(1).MinWidth(0)
					a.phoneRevokeButton(c, k, p, key+":in:"+at.ID, d.Name, "Revoke", "Revoke "+d.Name+" in "+at.Name,
						"Revoke "+d.Name+" in "+at.Name+"? It is signed out of that fleet.", PhoneRevokeRequest{Phone: d.Phone, Fleet: at.ID})
				})
			})
		}
	})
}

func (a *App) phoneRevokeButton(c *ui.Context, k kit.Colors, p palette, key, name, text, label, ask string, req PhoneRevokeRequest) {
	if a.ph.confirm != key {
		if kit.LabeledButton(c, k, "phone:revoke:"+key, text, label, 0, a.ph.busy != "") {
			a.ph.confirm = key
		}
		return
	}
	ui.Column(c).Gap(kit.Space6).Padding(kit.Space8).Radius(kit.RadiusMD).Background(p.waitBG).
		Border(1, p.wait).Label("confirm revoke").Children(func() {
		ui.Text(c, ask).FontSize(kit.Meta.Size)
		ui.Row(c).Gap(kit.Space8).Children(func() {
			if kit.PrimaryButton(c, k, "phone:confirm", "Revoke now", 110, a.ph.busy != "") {
				a.revokePhone(req)
			}
			if kit.Button(c, k, "phone:cancel", "Cancel", 96, false) {
				a.ph.confirm = ""
			}
		})
	})
}
