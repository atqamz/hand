package desktop

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

type pick struct {
	model, other, effort string
}

func (a *App) modelsKey() string {
	sup := a.Status.Supervisor
	if sup == nil {
		return a.Fleet.ID + "|none"
	}
	live := "live"
	if !sup.Live {
		live = "down"
	}
	return a.Fleet.ID + "|" + sup.Ref + "|" + live
}

func (a *App) syncModels() {
	if a.Model == nil || a.Fleet.ID == "" || !a.known() || a.modelsBusy {
		return
	}
	if key := a.modelsKey(); key != a.modelsFor {
		a.modelsFor = key
		a.loadModels()
	}
}

func (a *App) loadModels() {
	if a.Model == nil || a.Fleet.ID == "" {
		return
	}
	read, fleet := a.Model, a.Fleet.ID
	a.modelsGen++
	gen := a.modelsGen
	a.modelsBusy = true
	post := a.Post
	ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
	apply := func(m Models, err error) {
		if a.Fleet.ID != fleet || a.modelsGen != gen {
			return
		}
		a.modelsBusy = false
		if err != nil {
			a.modelErr = friendly(err)
			return
		}
		a.Models, a.modelErr = m, ""
		a.modelsFor = a.modelsKey()
		a.picks, a.change = nil, pick{}
	}
	if post == nil {
		m, err := read.Models(ctx, fleet)
		cancel()
		apply(m, err)
		return
	}
	go func() {
		m, err := read.Models(ctx, fleet)
		cancel()
		post(func() { apply(m, err) })
	}()
}

func (a *App) startSupervisor(harness, model, effort string) {
	sup := a.Status.Supervisor
	if a.Busy || a.Model == nil || a.Fleet.ID == "" || sup != nil && sup.Live {
		return
	}
	read, fleet := a.Model, a.Fleet.ID
	a.modelOpen = false
	a.runControl("start", func(ctx context.Context) (Control, error) {
		return read.StartWith(ctx, fleet, harness, model, effort)
	})
}

func (a *App) switchSupervisor(sw Switch) {
	sup := a.Status.Supervisor
	if a.Busy || a.Model == nil || a.Fleet.ID == "" || sup == nil {
		return
	}
	if sw.Ref == "" {
		sw.Ref = sup.Ref
	}
	read, fleet := a.Model, a.Fleet.ID
	a.modelOpen = false
	a.runControl("switch", func(ctx context.Context) (Control, error) {
		return read.Switch(ctx, fleet, sw)
	})
}

func (a *App) cancelSwitch(ref string) {
	if ref == "" {
		ref = a.Models.DropRef
	}
	a.switchSupervisor(Switch{Ref: ref, Cancel: true})
}

func (a *App) refreshModels() {
	if a.Model == nil || a.Fleet.ID == "" || a.modelsBusy {
		return
	}
	read, fleet := a.Model, a.Fleet.ID
	a.modelsGen++
	gen := a.modelsGen
	a.modelsBusy = true
	post := a.Post
	ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
	apply := func(r Refreshed, err error) {
		if a.Fleet.ID != fleet || a.modelsGen != gen {
			return
		}
		a.modelsBusy = false
		if err != nil {
			a.ActionErr, a.ActionWarn = friendly(err), false
			return
		}
		a.Models, a.modelErr = r.Models, ""
		a.modelsFor = a.modelsKey()
		a.picks, a.change = nil, pick{}
		a.showReceipt(r.Receipt, r.Warn, "models:refresh")
	}
	if post == nil {
		r, err := read.RefreshModels(ctx, fleet, "")
		cancel()
		apply(r, err)
		return
	}
	go func() {
		r, err := read.RefreshModels(ctx, fleet, "")
		cancel()
		post(func() { apply(r, err) })
	}()
}

func (a *App) pickFor(h Harness) *pick {
	if a.picks == nil {
		a.picks = map[string]*pick{}
	}
	p, ok := a.picks[h.Name]
	if !ok {
		p = &pick{effort: h.Effort}
		if _, found := modelNamed(h, h.Model); found {
			p.model = h.Model
		}
		a.picks[h.Name] = p
	}
	return p
}

func modelNamed(h Harness, name string) (ModelOption, bool) {
	for _, g := range h.Groups {
		for _, m := range g.Models {
			if name != "" && m.Name == name {
				return m, true
			}
		}
	}
	return ModelOption{}, false
}

func (a *App) harnessNamed(name string) (Harness, bool) {
	for _, h := range a.Models.Harnesses {
		if h.Name == name {
			return h, true
		}
	}
	return Harness{}, false
}

func effortWord(h Harness) string {
	if h.Name == "opencode" {
		return "Variant"
	}
	return "Effort"
}

func modelOptions(h Harness, keep string) []kit.Option {
	options := []kit.Option{{Label: keep}}
	for _, g := range h.Groups {
		for _, m := range g.Models {
			options = append(options, kit.Option{Value: m.Name, Label: m.Text})
		}
	}
	return options
}

func effortOptions(h Harness, model, keep string) ([]kit.Option, bool) {
	listed := h.Efforts
	if m, ok := modelNamed(h, model); ok {
		listed = m.Efforts
	}
	if len(listed) == 0 {
		return nil, true
	}
	options := make([]kit.Option, 0, len(listed)+1)
	options = append(options, kit.Option{Label: keep})
	for _, e := range listed {
		options = append(options, kit.Option{Value: e, Label: e})
	}
	return options, false
}

func listedEffort(h Harness, model, effort string) string {
	options, _ := effortOptions(h, model, "")
	for _, o := range options {
		if o.Value == effort {
			return effort
		}
	}
	return ""
}

func (a *App) chosen(h Harness, p *pick) (string, string) {
	model := strings.TrimSpace(p.other)
	if model == "" {
		model = p.model
	}
	effort := strings.TrimSpace(p.effort)
	if _, hidden := effortOptions(h, p.model, ""); !hidden {
		effort = listedEffort(h, p.model, effort)
	}
	return model, effort
}

func unverified(h Harness, p *pick) bool {
	if p.model == "" {
		return false
	}
	m, ok := modelNamed(h, p.model)
	return ok && m.Unverified
}

func (a *App) modelHead(c *ui.Context, k kit.Colors, p palette, sup *Supervisor) {
	if a.Models.Switching != "" {
		return
	}
	if a.Models.Current != "" && !a.Models.Switchable {
		ui.Text(c, modelLine(sup)).FontSize(kit.Body.Size).Bold()
		return
	}
	pill := ui.ButtonBase(c.Key("model-menu")).Height(kit.Control).Padding(0, kit.Space10).Gap(kit.Space6).
		Radius(kit.RadiusMD).Justify(ui.Center).AlignItems(ui.Center).Shrink(1).MinWidth(0).
		Cursor(ui.CursorPointer).Label("Model: " + modelLine(sup))
	pill.Children(func() {
		ui.Text(c, modelLine(sup)).FontSize(kit.Body.Size).Bold().SingleLine().Ellipsis("…").Shrink(1)
		ui.Icon(c, kit.Icon("chevron")).TextColor(k.Muted)
	})
	if pill.Clicked() {
		a.modelOpen = !a.modelOpen
		if a.modelOpen {
			a.change = pick{}
			a.loadModels()
		}
	}
	if !a.modelOpen {
		return
	}
	w, h := c.Size()
	width, room := float32(min(300, w-24)), max(float32(h)-120, 240)
	ui.PopoverBase(c, pill, &a.modelOpen, func(panel ui.Element) {
		kit.MenuPanel(c, k, panel, width, room)
		panel.Children(func() {
			ui.Scroll(c).FillWidth().MaxHeight(room - 2*kit.Space12).Children(func() {
				ui.Column(c).FillWidth().Gap(kit.Space8).Children(func() {
					switch {
					case a.modelErr != "":
						a.modelsError(c, k, p)
					case a.modelsBusy && len(a.Models.Harnesses) == 0:
						more(c, p, "Reading the models…")
					default:
						inner := width - 2*kit.Space12
						a.menuNow(c, k, p)
						a.menuChange(c, k, p, inner)
						a.menuHarnesses(c, k, p, inner)
					}
				})
			})
		})
	})
}

func (a *App) menuNow(c *ui.Context, k kit.Colors, p palette) {
	kit.FieldLabel(c, k, "Running now")
	dl := func(label, value string) {
		ui.Row(c).Gap(kit.Space8).Children(func() {
			ui.Text(c, label).FontSize(kit.Meta.Size).Bold().TextColor(p.neutral).Width(56)
			ui.Text(c, value).FontSize(kit.Body.Size).SingleLine().Ellipsis("…").Grow(1)
		})
	}
	dl("Harness", a.Models.Current)
	if a.Models.Model != "" {
		dl("Model", a.Models.Model)
	}
	if a.Models.Effort != "" {
		h, _ := a.harnessNamed(a.Models.Current)
		dl(effortWord(h), a.Models.Effort)
	}
	if a.Models.ModelNote != "" {
		ui.Text(c, a.Models.ModelNote).FontSize(kit.Meta.Size).TextColor(p.wait)
	}
}

func (a *App) menuChange(c *ui.Context, k kit.Colors, p palette, width float32) {
	h, ok := a.harnessNamed(a.Models.Current)
	if !ok {
		return
	}
	kit.FieldLabel(c, k, "Change model or "+strings.ToLower(effortWord(h)))
	kit.Form(c, k, func() {
		a.menuFields(c, k, p, h, &a.change, "keep "+orText(a.Models.Model, "current"),
			"keep "+orText(a.Models.Effort, "current"))
		label := "Switch after this turn"
		if a.Status.AgentState == "ready" {
			label = "Switch now"
		}
		if kit.Button(c, k, "switch:now", label, width, a.Busy) {
			model, effort := a.chosen(h, &a.change)
			a.switchSupervisor(Switch{Model: model, Effort: effort})
		}
		if h.Note != "" {
			ui.Text(c, h.Note).FontSize(kit.Meta.Size).TextColor(p.wait)
		}
	})
}

func orText(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (a *App) menuHarnesses(c *ui.Context, k kit.Colors, p palette, width float32) {
	others := false
	for _, h := range a.Models.Harnesses {
		if h.Name == a.Models.Current {
			continue
		}
		if !others {
			kit.FieldLabel(c, k, "Switch the supervisor to another harness")
			others = true
		}
		harness := h
		kit.Form(c, k, func() {
			ui.Text(c, harness.Name).FontSize(kit.Body.Size).Bold()
			pk := a.pickFor(harness)
			a.menuFields(c, k, p, harness, pk, "default", "default")
			if kit.Button(c, k, "switch:"+harness.Name, "Switch to "+harness.Name, width, a.Busy || harness.Missing != "") {
				model, effort := a.chosen(harness, pk)
				a.switchSupervisor(Switch{Harness: harness.Name, Model: model, Effort: effort})
			}
			if harness.Missing != "" {
				ui.Text(c, harness.Missing).FontSize(kit.Body.Size).TextColor(p.wait)
			}
			if harness.Note != "" {
				ui.Text(c, harness.Note).FontSize(kit.Meta.Size).TextColor(p.wait)
			}
		})
	}
}

func (a *App) menuFields(c *ui.Context, k kit.Colors, p palette, h Harness, pk *pick, keepModel, keepEffort string) {
	if len(h.Groups) == 0 {
		kit.FieldLabel(c, k, "Model")
		ui.TextInput(c, &pk.other).Placeholder(orText(h.Model, "model")).Label(h.Name + " model")
		kit.FieldLabel(c, k, effortWord(h))
		ui.TextInput(c, &pk.effort).Placeholder(orText(h.Effort, "effort")).Label(h.Name + " " + strings.ToLower(effortWord(h)))
		return
	}
	kit.FieldLabel(c, k, "Model")
	kit.Select(c, k, &pk.model, modelOptions(h, keepModel))
	kit.FieldLabel(c, k, "Other model")
	ui.TextInput(c, &pk.other).Placeholder("Other model").Label(h.Name + " other model")
	options, hidden := effortOptions(h, pk.model, keepEffort)
	if hidden {
		if h.Name == "opencode" {
			kit.FieldLabel(c, k, effortWord(h))
			ui.TextInput(c, &pk.effort).Placeholder("variant").Label(h.Name + " variant")
		} else {
			pk.effort = ""
		}
		return
	}
	pk.effort = listedEffort(h, pk.model, pk.effort)
	kit.FieldLabel(c, k, effortWord(h))
	kit.Select(c, k, &pk.effort, options)
	if unverified(h, pk) {
		ui.Text(c, "efforts unverified").FontSize(kit.Meta.Size).Bold().TextColor(p.wait)
	}
}

func (a *App) modelsError(c *ui.Context, k kit.Colors, p palette) {
	ui.Text(c, "The models could not be read").FontSize(kit.Body.Size).Bold().TextColor(p.fail)
	ui.Text(c, a.modelErr).FontSize(kit.Meta.Size)
	if kit.Button(c, k, "models:retry", "Retry", 0, a.modelsBusy) {
		a.loadModels()
	}
}

func (a *App) startStrip(c *ui.Context, k kit.Colors, p palette, width float32) {
	if a.modelErr != "" {
		a.modelsError(c, k, p)
		return
	}
	if len(a.Models.Harnesses) == 0 {
		more(c, p, "Reading the models…")
		return
	}
	_, h := c.Size()
	ui.Scroll(c).FillWidth().MaxHeight(max(float32(h)-260, 200)).Children(func() {
		ui.Column(c).FillWidth().Gap(kit.Space8).Children(func() {
			kit.FieldLabel(c, k, "Other harness, model or effort")
			for _, harness := range a.Models.Harnesses {
				a.startRow(c, k, p, harness, width-2*kit.Space16)
			}
		})
	})
}

func (a *App) startRow(c *ui.Context, k kit.Colors, p palette, h Harness, width float32) {
	harness := h
	pk := a.pickFor(harness)
	start := func(w float32) {
		if kit.Button(c, k, "start:"+harness.Name, "Start "+harness.Name, w, a.Busy || harness.Missing != "") {
			model, effort := a.chosen(harness, pk)
			a.startSupervisor(harness.Name, model, effort)
		}
	}
	ui.Column(c).Gap(kit.Space4).Children(func() {
		ui.Row(c).FillWidth().Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
			a.stripFields(c, k, harness, pk, width >= 520)
			start(120)
		})
		if harness.Missing != "" {
			ui.Text(c, harness.Missing).FontSize(kit.Body.Size).TextColor(p.wait)
		}
		if unverified(harness, pk) {
			ui.Text(c, "efforts unverified").FontSize(kit.Meta.Size).Bold().TextColor(p.wait)
		}
		if harness.Note != "" {
			ui.Text(c, harness.Note).FontSize(kit.Meta.Size).TextColor(p.wait)
		}
	})
}

func (a *App) stripFields(c *ui.Context, k kit.Colors, h Harness, pk *pick, wide bool) {
	field := func(grow float32) ui.Element {
		return ui.Column(c).Grow(grow).MinWidth(0)
	}
	modelGrow, otherGrow, effortGrow := float32(2), float32(0), float32(1)
	if wide {
		modelGrow, otherGrow, effortGrow = 3, 2, 1.6
	}
	if len(h.Groups) == 0 {
		field(1).Children(func() {
			ui.TextInput(c, &pk.other).Placeholder("model").Label(h.Name + " model")
		})
		placeholder := "effort"
		if h.Name == "opencode" {
			placeholder = "variant"
		}
		field(1).Children(func() {
			ui.TextInput(c, &pk.effort).Placeholder(placeholder).Label(h.Name + " " + placeholder)
		})
		return
	}
	field(modelGrow).Children(func() { kit.Select(c, k, &pk.model, modelOptions(h, "default")) })
	if wide {
		field(otherGrow).Children(func() {
			ui.TextInput(c, &pk.other).Placeholder("Other model").Label(h.Name + " other model")
		})
	}
	options, hidden := effortOptions(h, pk.model, "default")
	if hidden {
		if h.Name != "opencode" {
			pk.effort = ""
			return
		}
		field(effortGrow).Children(func() {
			ui.TextInput(c, &pk.effort).Placeholder("variant").Label(h.Name + " variant")
		})
		return
	}
	pk.effort = listedEffort(h, pk.model, pk.effort)
	field(effortGrow).Children(func() { kit.Select(c, k, &pk.effort, options) })
}
