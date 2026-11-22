package desktop

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const docTabWidth = 8

func (a *App) docName() string {
	name := path.Base(a.Ref)
	if d := a.doc.doc; a.doc.ok && d.Path == a.Ref {
		name = d.Name
		if d.Kind == "folder" {
			name += "/"
		}
	}
	return shownText(name)
}

func (a *App) docBody(c *ui.Context, k kit.Colors, p palette) {
	st := &a.doc
	switch {
	case st.err != "" && !st.refused:
		a.pageScroll(c, func() { a.viewerError(c, p, st.err) })
	case st.err != "":
		a.pageScroll(c, func() {
			ui.Text(c, shownText(a.Ref)).Font("monospace").FontSize(kit.Meta.Size).TextColor(p.neutral)
			a.docRow(c, k, shownText(st.err), st.conflict)
		})
	case !st.ok:
		a.readingPage(c, p)
	default:
		a.pageScroll(c, func() { a.docPage(c, k, p) })
	}
}

func (a *App) docRow(c *ui.Context, k kit.Colors, text string, retry bool) {
	kit.DocNotice(c, k, text, func() {
		if !a.remoteFleet() && kit.Button(c, k, "doc:open-row", "Open in browser", 0, false) {
			a.OpenInBrowser()
		}
		if retry && kit.Button(c, k, "doc:retry", "Retry", 0, false) {
			a.Retry()
		}
	})
}

func (a *App) docPage(c *ui.Context, k kit.Colors, p palette) {
	d := a.doc.doc
	a.docHead(c, k, p, d)
	switch d.Kind {
	case "folder":
		a.docFolder(c, k, p, d)
	case "markdown":
		if a.doc.row != "" {
			a.docRow(c, k, a.doc.row, false)
			return
		}
		a.markdownOf(c, a.linkedRefs(shownText(a.doc.body)), kit.DocTheme(c))
	case "text":
		a.docText(c, k, p, d)
	case "image":
		if a.doc.row != "" {
			a.docRow(c, k, a.doc.row, false)
			return
		}
		w, h := c.Size()
		bw, bh := kit.ViewerBox(w, h)
		kit.DocPicture(c, k, a.doc.image, bw-2*kit.Space20, max(bh-300, 120), shownText(d.Name))
	default:
		a.docRow(c, k, a.doc.row, false)
	}
}

func (a *App) docHead(c *ui.Context, k kit.Colors, p palette, d Doc) {
	ui.Column(c).Gap(kit.Space6).Children(func() {
		a.docCrumbs(c, k, p, d)
		title := shownText(d.Name)
		if d.Kind == "folder" {
			title += "/"
		}
		ui.Text(c, title).FontSize(kit.Display.Size).FixedLineHeight(kit.Display.Line).FontWeight(600)
		ui.Row(c).GapY(kit.Space4).GapX(kit.Space12).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, shownText(d.Path)).Font("monospace").FontSize(kit.Meta.Size).
				FixedLineHeight(kit.Meta.Line).TextColor(p.neutral).MinWidth(0)
			a.copyPath(c, k, d.Path)
			if d.Kind != "folder" {
				ui.Text(c, docSize(d.Size)).FontSize(kit.Meta.Size).TextColor(p.neutral)
			}
			if d.At != "" {
				ui.Text(c, stamp(d.At)).FontSize(kit.Meta.Size).TextColor(p.neutral).FontFeatures("tnum")
			}
		})
		if d.Prev != nil || d.Next != nil {
			ui.Row(c).Gap(kit.Space16).Children(func() {
				if d.Prev != nil && kit.DocStep(c, k, shownText(d.Prev.Name), false) {
					a.showDoc(d.Prev.Path, 1, false)
				}
				ui.Spacer(c)
				if d.Next != nil && kit.DocStep(c, k, shownText(d.Next.Name), true) {
					a.showDoc(d.Next.Path, 1, false)
				}
			})
		}
		ui.Box(c).Height(1).Background(p.line)
	})
}

func (a *App) docCrumbs(c *ui.Context, k kit.Colors, p palette, d Doc) {
	dirs := strings.Split(d.Path, "/")
	dirs = dirs[:len(dirs)-1]
	ui.Row(c).Gap(kit.Space4).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Text(c, "Fleets / "+shownText(fleetName(a.Fleet))+" /").FontSize(kit.Body.Size).TextColor(p.neutral)
		for i, dir := range dirs {
			rel, page := strings.Join(dirs[:i+1], "/"), 1
			if i == len(dirs)-1 && d.Kind != "folder" {
				page = max(d.FolderPage, 1)
			}
			crumb := ui.ButtonBase(c.Key("doc:crumb:" + rel)).Cursor(ui.CursorPointer).Label("folder " + rel)
			crumb.Children(func() {
				ui.Text(c, shownText(dir)).FontSize(kit.Body.Size).TextColor(p.accent).SingleLine()
			})
			if crumb.Clicked() {
				a.showDoc(rel, page, true)
			}
			if i < len(dirs)-1 {
				ui.Text(c, "/").FontSize(kit.Body.Size).TextColor(p.neutral)
			}
		}
	})
}

func (a *App) copyPath(c *ui.Context, k kit.Colors, text string) {
	label := "Copy path"
	key := "doc:copy:" + text
	if a.copiedKey == key && !a.copiedAt.IsZero() && c.Now().Sub(a.copiedAt) < copyReceipt {
		label = "Copied"
	}
	if kit.Button(c, k, "doc:copy", label, 0, false) {
		c.WriteClipboard(text)
		a.copiedKey, a.copiedAt = key, c.Now()
		c.After(copyReceipt)
	}
}

func (a *App) docFolder(c *ui.Context, k kit.Colors, p palette, d Doc) {
	a.docPager(c, k, p, d, "top")
	if len(d.Entries) == 0 {
		a.quiet(c, p, "This folder is empty.")
	}
	ui.Column(c).Gap(0).BorderWidth(1, 0, 0, 0).BorderColor(p.line).Children(func() {
		for _, e := range d.Entries {
			name := shownText(e.Name)
			if e.Dir {
				name += "/"
			}
			var meta []string
			if e.Size != nil {
				meta = append(meta, docSize(*e.Size))
			}
			if e.At != "" {
				meta = append(meta, stamp(e.At))
			}
			text := strings.Join(meta, " · ")
			if e.Refused != "" {
				text = shownText(e.Refused)
			}
			if kit.DocRow(c, k, "doc:entry:"+e.Path, shownText(e.Label), name, text, e.Refused != "") {
				a.OpenDoc(e.Path)
			}
		}
	})
	a.docPager(c, k, p, d, "bottom")
}

func (a *App) docPager(c *ui.Context, k kit.Colors, p palette, d Doc, at string) {
	if d.Pages <= 1 {
		return
	}
	ui.Row(c).GapX(kit.Space12).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Text(c, fmt.Sprintf("%s entries, page %d of %d", thousands(d.Total), d.Page, d.Pages)).
			FontSize(kit.Meta.Size).TextColor(p.neutral)
		step := func(label string, to int, show bool) {
			if show && kit.TextButton(c, k, "doc:page:"+label+":"+at, label) {
				a.showDoc(a.Ref, to, false)
			}
		}
		step("First", 1, d.Page > 2)
		step("Previous", d.Page-1, d.Page > 1)
		step("Next", d.Page+1, d.Page < d.Pages)
		step("Last", d.Pages, d.Page < d.Pages-1)
	})
}

func (a *App) docText(c *ui.Context, k kit.Colors, p palette, d Doc) {
	st, w := &a.doc, a.doc.window
	if w.From != st.from {
		a.quiet(c, p, "Reading…")
		return
	}
	if w.From > 1 || w.Next > 0 {
		ui.Row(c).GapY(kit.Space4).GapX(kit.Space12).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, fmt.Sprintf("From line %d of a %s file", w.From, docSize(d.Size))).
				FontSize(kit.Meta.Size).TextColor(p.neutral)
			if len(st.froms) > 0 && kit.TextButton(c, k, "doc:earlier", "Earlier") {
				a.docEarlier()
			}
			if w.Next > 0 && kit.TextButton(c, k, "doc:later", "Later") {
				a.docWindowAt(w.Next, true)
			}
		})
	}
	if len(w.Lines) == 0 {
		a.quiet(c, p, "This file is empty.")
		return
	}
	lines := make([]string, len(w.Lines))
	for i, line := range w.Lines {
		lines[i] = expandTabs(shownText(line))
	}
	for _, cut := range w.Cut {
		if i := cut.Line - w.From; i >= 0 && i < len(lines) {
			lines[i] += "… " + docSize(cut.Bytes) + " more on this line"
		}
	}
	kit.DocLines(c, k, w.From, lines)
}

func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var out strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := docTabWidth - col%docTabWidth
			out.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		out.WriteRune(r)
		col++
	}
	return out.String()
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func docSize(n int64) string {
	switch kib := (n + 1<<9) >> 10; {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case kib < 1<<10:
		return fmt.Sprintf("%d KiB", kib)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
}
