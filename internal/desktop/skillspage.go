package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

type skillsState struct {
	data    Skills
	loaded  bool
	err     string
	busy    bool
	gen     int
	acting  string
	runs    map[string]SkillsRun
	failed  map[string]string
	open    map[string]bool
	confirm string
	scroll  ui.ScrollState
}

func (a *App) resetSkills() {
	a.sk = skillsState{gen: a.sk.gen + 1}
}

func (a *App) openSkills() {
	if a.sk.acting != "" {
		return
	}
	a.sk = skillsState{gen: a.sk.gen + 1, runs: a.sk.runs, failed: a.sk.failed}
	a.loadSkills()
}

func (a *App) skillsBusy() bool { return a.sk.busy || a.sk.acting != "" }

func skillKey(sc SkillScope) string { return sc.Scope + "/" + sc.Project }

func skillsFriendly(err error) string {
	var api *APIError
	if errors.As(err, &api) && api.Status == 403 && api.Code == "forbidden" && api.Message != "" {
		return api.Message
	}
	return friendly(err)
}

var errNoSkills = errors.New("this board has no skills endpoints; update hand and restart the board")

func (a *App) loadSkills() {
	if a.Model == nil || a.Fleet.ID == "" || a.skillsBusy() {
		return
	}
	read, fleet, post := a.Model, a.Fleet.ID, a.Post
	a.sk.gen++
	gen := a.sk.gen
	a.sk.busy, a.sk.err = true, ""
	ctx, cancel := context.WithTimeout(a.Context, skillsWait)
	apply := func(s Skills, err error) {
		if a.Fleet.ID != fleet || a.sk.gen != gen {
			return
		}
		a.sk.busy = false
		if err != nil {
			a.sk.err = skillsFriendly(err)
			return
		}
		a.sk.data, a.sk.loaded = s, true
	}
	load := func() (Skills, error) {
		defer cancel()
		if fs, err := read.Features(ctx, fleet); err == nil && !slices.Contains(fs, "skills") {
			return Skills{}, errNoSkills
		}
		return read.Skills(ctx, fleet)
	}
	if post == nil {
		apply(load())
		return
	}
	go func() {
		s, err := load()
		post(func() { apply(s, err) })
	}()
}

func (a *App) runSkills(verb string, sc SkillScope, name, yes string) {
	if a.Model == nil || a.Fleet.ID == "" || a.skillsBusy() {
		return
	}
	read, fleet, post := a.Model, a.Fleet.ID, a.Post
	key := skillKey(sc)
	a.sk.gen++
	gen := a.sk.gen
	a.sk.acting, a.sk.confirm = key, ""
	delete(a.sk.failed, key)
	req := SkillsRequest{Scope: sc.Scope, Project: sc.Project, Name: name, Yes: yes}
	ctx, cancel := context.WithTimeout(a.Context, skillsWait)
	apply := func(out SkillsRun, err error) {
		if a.Fleet.ID != fleet || a.sk.gen != gen {
			return
		}
		a.sk.acting = ""
		var unread *AppliedUnread
		if err != nil && !errors.As(err, &unread) {
			a.sk.failed = withEntry(a.sk.failed, key, skillsFriendly(err))
			return
		}
		if unread != nil {
			a.sk.failed = withEntry(a.sk.failed, key, "the board took the "+verb+" but its answer could not be read; the list below is read again")
		} else {
			a.sk.runs = withEntry(a.sk.runs, key, out)
			a.showReceipt(out.Receipt, out.Warn, "skills:"+verb+":"+key)
		}
		a.loadSkills()
	}
	run := func() (SkillsRun, error) {
		defer cancel()
		return read.SkillsRun(ctx, fleet, verb, req)
	}
	if post == nil {
		apply(run())
		return
	}
	go func() {
		out, err := run()
		post(func() { apply(out, err) })
	}()
}

func withEntry[V any](m map[string]V, key string, v V) map[string]V {
	if m == nil {
		m = map[string]V{}
	}
	m[key] = v
	return m
}

func (a *App) skillsPage(c *ui.Context, k kit.Colors, p palette) {
	w, _ := c.Size()
	m := max(float32(kit.Space20), (w-kit.Measure)/2)
	page := ui.Scroll(c).Key("skills-scroll").Grow(1).Padding(kit.Space20, m, 32).
		TrackScroll(&a.sk.scroll).Focusable().FocusRing(false).Label("Skills")
	page.Children(func() {
		ui.Column(c).Gap(kit.Space16).Selectable().Children(func() {
			ui.Text(c, "Fleets / "+fleetName(a.Fleet)+" / skills").FontSize(kit.Body.Size).TextColor(p.neutral)
			ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Skills").FontSize(kit.Display.Size).Bold()
				ui.Spacer(c)
				if a.sk.busy && a.sk.acting == "" {
					ui.Text(c, "Reading the sources…").FontSize(kit.Meta.Size).TextColor(p.neutral)
				}
				if kit.Button(c, k, "skills:check", "Check again", 120, a.skillsBusy()) {
					a.loadSkills()
				}
			})
			ui.Text(c, "The window has no add or remove control. Run hand skills add or hand skills remove in a terminal; the next check lists the change.").
				FontSize(kit.Meta.Size).TextColor(p.neutral)
			if a.remoteFleet() {
				ui.Text(c, "This fleet is on a paired host. A paired Companion cannot change skills; use the host's Board.").
					FontSize(kit.Meta.Size).TextColor(p.wait)
			}
			a.skillsBody(c, k, p)
		})
	})
}

func (a *App) skillsBody(c *ui.Context, k kit.Colors, p palette) {
	switch {
	case !a.sk.loaded && a.sk.err != "":
		ui.Column(c).Gap(kit.Space8).Padding(kit.Space16).Radius(kit.RadiusLG).
			Background(p.failBG).Border(1, p.fail).Children(func() {
			ui.Text(c, "The skills could not be read").FontSize(kit.Heading.Size).Bold().TextColor(p.fail)
			ui.Text(c, a.sk.err).FontSize(kit.Body.Size)
			if kit.Button(c, k, "skills:retry", "Retry", 100, a.skillsBusy()) {
				a.loadSkills()
			}
		})
		return
	case !a.sk.loaded:
		a.emptyCard(c, p, "Reading the skills", "The board fetches each source once; this takes seconds on a fast link and up to a few minutes.")
		return
	}
	if a.sk.err != "" {
		ui.Text(c, "Stale · "+a.sk.err).FontSize(kit.Meta.Size).TextColor(p.wait)
	}
	if len(a.sk.data.Scopes) == 0 {
		a.emptyCard(c, p, "No scopes", "The board listed no skill scope.")
		return
	}
	for _, sc := range a.sk.data.Scopes {
		a.skillScope(c, k, p, sc)
	}
}

func skillScopeTitle(sc SkillScope) (title, note string) {
	switch sc.Scope {
	case "fleet":
		return "Fleet", "The supervisor's own skills, in the fleet folder. Sync applies an own source with a backup and never a third-party one without your yes."
	case "project":
		return "Project " + sc.Project, "Committed in the project's checkout, so every worker sees them. Sync opens a bump pull request for each skill; Update edits the checkout in place for you to commit."
	case "user":
		return "User", "Machine-wide, for every harness process. Sync applies an own source with a backup and never a third-party one without your yes."
	}
	return sc.Scope, ""
}

func (a *App) skillScope(c *ui.Context, k kit.Colors, p palette, sc SkillScope) {
	key := skillKey(sc)
	title, note := skillScopeTitle(sc)
	managed := 0
	var thirdParty []string
	for _, s := range sc.Skills {
		if s.Managed {
			managed++
			if !s.Trusted {
				thirdParty = append(thirdParty, s.Name)
			}
		}
	}
	busy := a.skillsBusy()
	ui.Column(c).Gap(kit.Space8).Label("scope " + key).Children(func() {
		ui.Row(c).Gap(kit.Space8).Padding(kit.Space14, 0, kit.Space4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(kit.Heading.Size).Bold()
			ui.Badge(c, fmt.Sprint(len(sc.Skills))).Background(p.neutralBG).TextColor(p.neutral).
				Label(fmt.Sprintf("%s %d skills", title, len(sc.Skills)))
			ui.Spacer(c)
			if a.sk.acting == key {
				ui.Text(c, "Working…").FontSize(kit.Meta.Size).TextColor(p.neutral)
			}
			if a.remoteFleet() {
				return
			}
			if kit.Button(c, k, "skills:sync:"+key, "Sync", 80, busy || managed == 0) {
				a.runSkills("sync", sc, "", "")
			}
			if kit.Button(c, k, "skills:update:"+key, "Update", 80, busy || managed == 0) {
				if len(thirdParty) > 0 {
					a.sk.confirm = key
				} else {
					a.runSkills("update", sc, "", "")
				}
			}
		})
		ui.Text(c, note).FontSize(kit.Meta.Size).TextColor(p.neutral)
		if a.sk.confirm == key && len(thirdParty) > 0 {
			a.skillsConfirm(c, k, p, sc, thirdParty)
		}
		if msg := a.sk.failed[key]; msg != "" {
			a.skillsFailure(c, k, p, key, msg)
		}
		if sc.Error != "" {
			ui.Column(c).Gap(kit.Space4).Padding(kit.Space12).Radius(kit.RadiusLG).
				Background(p.failBG).Border(1, p.fail).Children(func() {
				ui.Text(c, "This scope could not be read").FontSize(kit.Body.Size).Bold().TextColor(p.fail)
				ui.Text(c, sc.Error).FontSize(kit.Meta.Size)
			})
		} else if len(sc.Skills) == 0 {
			ui.Text(c, "No skills in this scope.").FontSize(kit.Body.Size).TextColor(p.neutral)
		}
		if run, ok := a.sk.runs[key]; ok {
			a.skillsRunCard(c, k, p, key, run)
		}
		ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Children(func() {
			for _, s := range sc.Skills {
				a.skillRow(c, k, p, sc, s)
			}
		})
	})
}

func (a *App) skillsConfirm(c *ui.Context, k kit.Colors, p palette, sc SkillScope, names []string) {
	key := skillKey(sc)
	ui.Column(c).Gap(kit.Space8).Padding(kit.Space12).Radius(kit.RadiusLG).
		Background(p.waitBG).Border(1, p.wait).Label("confirm update " + key).Children(func() {
		ui.Text(c, "Update does not wait for a yes").FontSize(kit.Body.Size).Bold()
		ui.Text(c, "It can move these third-party skills without a diff you confirmed: "+
			strings.Join(names, ", ")+". Review a behind skill's diff and apply it from its row instead, or update anyway.").
			FontSize(kit.Meta.Size)
		ui.Row(c).Gap(kit.Space8).Children(func() {
			if kit.Button(c, k, "skills:update-anyway:"+key, "Update anyway", 140, false) {
				a.runSkills("update", sc, "", "")
			}
			if kit.Button(c, k, "skills:update-cancel:"+key, "Cancel", 100, false) {
				a.sk.confirm = ""
			}
		})
	})
}

func (a *App) skillsFailure(c *ui.Context, k kit.Colors, p palette, key, msg string) {
	ui.Column(c).Gap(kit.Space8).Padding(kit.Space12).Radius(kit.RadiusLG).
		Background(p.failBG).Border(1, p.fail).Label("skills failure " + key).Children(func() {
		ui.Text(c, "The board refused the request").FontSize(kit.Body.Size).Bold().TextColor(p.fail)
		ui.Text(c, msg).FontSize(kit.Meta.Size)
		if kit.Button(c, k, "skills:dismiss-failure:"+key, "Dismiss", 100, false) {
			delete(a.sk.failed, key)
		}
	})
}

func skillResultTone(state string) string {
	switch state {
	case "updated", "bumped", "proposed", "restored", "removed", "current":
		return "pass"
	case "pending", "behind", "edited":
		return "wait"
	case "conflict", "refused":
		return "fail"
	}
	return "neutral"
}

func (a *App) skillsRunCard(c *ui.Context, k kit.Colors, p palette, key string, run SkillsRun) {
	bg, line := p.card, p.line
	if run.Refused > 0 {
		bg, line = p.failBG, p.fail
	}
	ui.Column(c).Gap(kit.Space6).Padding(kit.Space12).Radius(kit.RadiusLG).Background(bg).
		Border(1, line).Label("skills run " + key).Children(func() {
		title := run.Receipt
		if title == "" {
			title = "Last run"
		}
		ui.Text(c, title).FontSize(kit.Body.Size).Bold()
		if run.Refused > 0 {
			ui.Textf(c, "%d refused. A refused skill was not changed.", run.Refused).FontSize(kit.Meta.Size).TextColor(p.fail)
		}
		for _, r := range run.Results {
			a.skillsResultRow(c, k, p, r)
		}
		for _, cmd := range run.Reconcile {
			ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, cmd).Font("monospace").FontSize(kit.Meta.Size).Grow(1).MinWidth(0)
				if kit.TextButton(c, k, "skills:copy:"+key+":"+cmd, "Copy") {
					c.WriteClipboard(cmd)
				}
			})
		}
		if run.Warn != "" {
			ui.Text(c, run.Warn).FontSize(kit.Meta.Size).TextColor(p.wait)
		}
		if kit.TextButton(c, k, "skills:dismiss-run:"+key, "Dismiss") {
			delete(a.sk.runs, key)
		}
	})
}

func (a *App) skillsResultRow(c *ui.Context, k kit.Colors, p palette, r SkillResult) {
	tone := skillResultTone(r.State)
	_, fg := toneColors(tone, p)
	ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Label("result " + r.Name + " " + r.State).Children(func() {
		ui.Icon(c, stateIcon(tone)).TextColor(fg)
		ui.Text(c, r.Name).Font("monospace").FontSize(kit.Meta.Size).Bold().Shrink(0)
		ui.Text(c, r.State).FontSize(kit.Meta.Size).TextColor(fg).Shrink(0)
		switch {
		case strings.HasPrefix(r.Detail, "https://"):
			if kit.TextButton(c, k, "skills:open:"+r.Detail, r.Detail) {
				c.OpenURL(r.Detail)
			}
		case r.Detail != "":
			ui.Text(c, r.Detail).FontSize(kit.Meta.Size).TextColor(p.neutral).Grow(1).MinWidth(0)
		}
	})
}

func skillTone(s Skill) string {
	if !s.Managed {
		return "neutral"
	}
	switch s.State {
	case "current":
		return "pass"
	case "behind", "edited":
		return "wait"
	case "conflict", "error":
		return "fail"
	}
	return "neutral"
}

func skillStateWord(s Skill) string {
	if !s.Managed {
		return "unmanaged"
	}
	if s.State == "" {
		return "unknown"
	}
	return s.State
}

func shortCommit(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func skillSourceLine(s Skill) string {
	var parts []string
	if s.Source != "" {
		parts = append(parts, s.Source)
	}
	if s.Ref != "" {
		parts = append(parts, s.Ref)
	}
	if s.Commit != "" {
		at := shortCommit(s.Commit)
		if s.Latest != "" && s.Latest != s.Commit {
			at += " → " + shortCommit(s.Latest)
		}
		parts = append(parts, at)
	}
	return strings.Join(parts, " · ")
}

func (a *App) skillRow(c *ui.Context, k kit.Colors, p palette, sc SkillScope, s Skill) {
	key := skillKey(sc) + "/" + s.Name
	tone := skillTone(s)
	_, fg := toneColors(tone, p)
	word := skillStateWord(s)
	ui.Column(c).Gap(kit.Space4).Padding(kit.Space8, kit.Space4).BorderWidth(0, 0, 1, 0).BorderColor(p.line).
		Role(ui.RoleListItem).Label("skill " + s.Name + " " + word).Children(func() {
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, stateIcon(tone)).TextColor(fg).Label("state " + word)
			ui.Text(c, s.Name).Font("monospace").FontSize(kit.Body.Size).Bold().Shrink(0)
			ui.Text(c, word).FontSize(kit.Meta.Size).TextColor(fg).Shrink(0)
			if s.Managed {
				trust := "third-party"
				if s.Trusted {
					trust = "own source"
				}
				ui.Badge(c, trust).Background(p.neutralBG).TextColor(p.neutral).Label("source " + trust).Shrink(0)
			}
			ui.Spacer(c)
			a.skillActions(c, k, sc, s, key)
		})
		if line := skillSourceLine(s); line != "" {
			ui.Text(c, line).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
		}
		switch {
		case s.Detail != "":
			ui.Text(c, s.Detail).FontSize(kit.Meta.Size).TextColor(fg)
		case s.State == "conflict":
			ui.Text(c, "Edited here and moved at the source. Nothing updates; Sync names the command that asks a worker to reconcile it.").
				FontSize(kit.Meta.Size).TextColor(p.neutral)
		case s.State == "edited":
			ui.Text(c, "Edited here while the source did not move. Propose opens a pull request to the source.").
				FontSize(kit.Meta.Size).TextColor(p.neutral)
		}
		if a.sk.open[key] {
			a.skillDiff(c, k, p, sc, s)
		}
	})
}

func (a *App) skillActions(c *ui.Context, k kit.Colors, sc SkillScope, s Skill, key string) {
	busy := a.skillsBusy()
	remote := a.remoteFleet()
	switch {
	case !s.Managed:
	case remote && s.State != "behind":
	case remote && s.Trusted:
	case s.State == "behind" && s.Trusted:
		if kit.Button(c, k, "skills:sync-one:"+key, "Sync", 80, busy) {
			a.runSkills("sync", sc, s.Name, "")
		}
	case s.State == "behind":
		label := "Review diff"
		if a.sk.open[key] {
			label = "Hide diff"
		}
		if kit.Button(c, k, "skills:diff:"+key, label, 120, false) {
			a.sk.open = withEntry(a.sk.open, key, !a.sk.open[key])
		}
	case s.State == "edited":
		if kit.Button(c, k, "skills:propose:"+key, "Propose", 90, busy) {
			a.runSkills("propose", sc, s.Name, "")
		}
	}
}

func (a *App) skillDiff(c *ui.Context, k kit.Colors, p palette, sc SkillScope, s Skill) {
	key := skillKey(sc) + "/" + s.Name
	ui.Column(c).Gap(kit.Space8).Padding(kit.Space12).Radius(kit.RadiusLG).Background(k.Panel).
		Border(1, p.line).Label("diff " + s.Name).Children(func() {
		switch {
		case s.Diff == "":
			ui.Text(c, "The board built no diff for this update, so it cannot be applied from here.").FontSize(kit.Meta.Size).TextColor(p.wait)
			return
		case s.DiffCut:
			ui.Text(c, "The diff is over 64 KiB and was cut. Read the rest at the source, then run `hand skills sync --yes "+s.Name+"@"+s.Latest+"`.").
				FontSize(kit.Meta.Size).TextColor(p.wait)
		}
		ui.Scroll(c).Key("skills-diff:"+key).Height(240).Padding(kit.Space8).Radius(kit.RadiusMD).
			Background(k.BG).Border(1, p.line).Focusable().Label("diff of " + s.Name).Children(func() {
			ui.Text(c, s.Diff).Font("monospace").FontSize(kit.Meta.Size)
		})
		if s.DiffCut || s.Latest == "" || a.remoteFleet() {
			return
		}
		applies := "Apply moves " + s.Name + " to " + shortCommit(s.Latest) + ", the commit of the diff above, with a backup."
		if sc.Scope == "project" {
			applies = "Apply opens a pull request that moves " + s.Name + " to " + shortCommit(s.Latest) + ", the commit of the diff above. The checkout is not changed."
		}
		ui.Text(c, applies).FontSize(kit.Meta.Size).TextColor(p.neutral)
		if kit.PrimaryButton(c, k, "skills:apply:"+key, "Apply this update", 180, a.skillsBusy()) {
			a.runSkills("sync", sc, s.Name, s.Name+"@"+s.Latest)
		}
	})
}
