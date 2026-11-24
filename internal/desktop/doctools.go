package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

const notTextCopy = "Copy contents is for text files. This file is not text, so nothing was copied."

func (a *App) docTextual() bool {
	d, st := a.doc.doc, &a.doc
	return d.Kind == "text" || d.Kind == "markdown" && st.row == ""
}

func (a *App) docLinesShown() bool {
	d := a.doc.doc
	return d.Kind == "text" || d.Kind == "markdown" && a.DocSource && a.doc.row == ""
}

func (a *App) docTools(c *ui.Context, k kit.Colors, p palette, d Doc) {
	if d.Kind == "folder" {
		return
	}
	a.flushClip(c)
	ui.Column(c).Gap(kit.Space6).Children(func() {
		ui.Row(c).Gap(kit.Space8).AlignItems(ui.Center).Wrap().Label("Document actions").Children(func() {
			if d.Kind == "markdown" && a.doc.row == "" {
				if kit.DocToggle(c, k, "doc:tool:rendered", "Rendered", !a.DocSource) && a.DocSource {
					a.setDocView(func() { a.DocSource = false })
				}
				if kit.DocToggle(c, k, "doc:tool:source", "Source", a.DocSource) && !a.DocSource {
					a.setDocView(func() { a.DocSource = true })
				}
			}
			label := "Copy contents"
			if d.Kind == "text" && d.Size > docRawMax {
				label = "Copy window"
			}
			key := "doc:contents:" + a.Ref
			if a.copiedKey == key && !a.copiedAt.IsZero() && c.Now().Sub(a.copiedAt) < copyReceipt {
				label = "Copied"
			}
			if kit.Button(c, k, "doc:tool:copy", label, 0, a.doc.copying) {
				a.copyDoc(c)
			}
			if kit.Button(c, k, "doc:tool:download", "Download", 0, a.doc.saving || a.SaveAs == nil) {
				a.downloadDoc()
			}
			if a.docTextual() && kit.DocToggle(c, k, "doc:tool:wide", "Wide", a.DocWide) {
				a.setDocView(func() { a.DocWide = !a.DocWide })
			}
			if a.docLinesShown() && kit.DocToggle(c, k, "doc:tool:wrap", "Wrap", a.DocWrap) {
				a.setDocView(func() { a.DocWrap = !a.DocWrap })
			}
		})
		if a.doc.note != "" {
			if a.doc.note != a.doc.said {
				a.doc.said = a.doc.note
				c.Announce(a.doc.note)
			}
			tone := p.neutral
			if a.doc.noteBad {
				tone = p.fail
			}
			ui.Text(c, shownText(a.doc.note)).FontSize(kit.Meta.Size).TextColor(tone)
		}
	})
}

func (a *App) setDocView(set func()) {
	set()
	a.doc.note = ""
	a.viewerScroll = ui.ScrollState{}
	a.settlePrefs()
}

func (a *App) flushClip(c *ui.Context) {
	st := &a.doc
	if !st.clipReady {
		return
	}
	text := st.clip
	st.clip, st.clipReady = "", false
	c.WriteClipboard(text)
	c.Announce("Copied")
	a.copiedKey, a.copiedAt = "doc:contents:"+a.Ref, c.Now()
	st.note, st.noteBad = "", false
	c.After(copyReceipt)
}

func (a *App) copyDoc(c *ui.Context) {
	st, d := &a.doc, a.doc.doc
	st.note, st.noteBad = "", false
	switch {
	case d.Kind == "markdown" && st.row == "":
		st.clip, st.clipReady = st.body, true
	case d.Kind == "text" && d.Size > docRawMax:
		st.clip, st.clipReady = strings.Join(st.window.Lines, "\n"), true
	case d.Kind == "text":
		a.fetchCopy()
	default:
		st.note, st.noteBad = notTextCopy, true
	}
	a.flushClip(c)
}

func (a *App) fetchCopy() {
	st, model, fleet, rel := &a.doc, a.Model, a.Fleet.ID, a.Ref
	if model == nil {
		return
	}
	st.copying = true
	a.offload(func() func() {
		ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
		defer cancel()
		b, err := model.DocBytes(ctx, fleet, rel)
		return func() {
			if a.Screen != DocScreen || a.Ref != rel || a.Fleet.ID != fleet {
				return
			}
			st := &a.doc
			st.copying = false
			if err != nil {
				st.note, st.noteBad = "Nothing was copied: "+friendly(err), true
				return
			}
			st.clip, st.clipReady = string(b), true
		}
	})
}

func (a *App) downloadDoc() {
	st, d := &a.doc, a.doc.doc
	if a.SaveAs == nil || st.saving || a.Model == nil {
		return
	}
	st.saving, st.note, st.noteBad = true, "", false
	model, fleet, rel, name, save := a.Model, a.Fleet.ID, a.Ref, d.Name, a.SaveAs
	a.offload(func() func() {
		dest := save(name)
		var n int64
		var err error
		if dest != "" {
			n, err = saveDoc(a.Context, model, fleet, rel, dest)
		}
		return func() {
			if a.Screen != DocScreen || a.Ref != rel || a.Fleet.ID != fleet {
				return
			}
			st := &a.doc
			st.saving = false
			switch {
			case dest == "":
			case err != nil:
				st.note, st.noteBad = "Not saved: "+friendly(err), true
			default:
				st.note = fmt.Sprintf("Saved %s to %s", docSize(n), shownText(filepath.Base(dest)))
			}
		}
	})
}

func saveDoc(ctx context.Context, m ReadModel, fleet, rel, dest string) (int64, error) {
	f, err := os.CreateTemp(filepath.Dir(dest), ".hand-download-*")
	if err != nil {
		return 0, err
	}
	tmp := f.Name()
	n, err := m.DocStream(ctx, fleet, rel, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o644)
	}
	if err == nil {
		err = os.Rename(tmp, dest)
	}
	if err != nil {
		os.Remove(tmp)
		return 0, err
	}
	return n, nil
}
