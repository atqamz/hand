package desktop

import (
	"bytes"
	"context"
	"errors"
	"image"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

const (
	docPixelsMax = 36 << 20
	docTooLarge  = "This picture is larger than this window reads. Open it in the browser."
)

const notText = "This file is not text, so the Board does not show it; download it to open it."

type docState struct {
	path     string
	page     int
	from     int
	froms    []int
	err      string
	refused  bool
	conflict bool

	doc    Doc
	ok     bool
	window DocWindow
	body   string
	image  *ui.Bitmap
	row    string
}

type docRead struct {
	doc    Doc
	window DocWindow
	body   string
	image  *ui.Bitmap
	row    string
	err    error
}

var docKindNouns = map[string]string{
	"pdf": "A PDF", "html": "An HTML page", "svg": "An SVG picture", "csv": "A CSV table",
	"tsv": "A TSV table", "video": "A video", "audio": "An audio file",
}

func readDoc(ctx context.Context, m ReadModel, fleet string, req docState) docRead {
	var r docRead
	r.doc = req.doc
	if !req.ok {
		if r.doc, r.err = m.Doc(ctx, fleet, req.path, req.page); r.err != nil {
			return r
		}
	}
	d := r.doc
	switch d.Kind {
	case "folder":
	case "markdown":
		b, err := m.DocBytes(ctx, fleet, req.path)
		switch {
		case err != nil:
			r.err = err
		case !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0:
			r.row = notText
		default:
			r.body = string(b)
		}
	case "text":
		r.window, r.err = m.DocWindow(ctx, fleet, req.path, req.from)
	case "image":
		if d.Size > docRawMax {
			r.row = docTooLarge
			break
		}
		b, err := m.DocBytes(ctx, fleet, req.path)
		if err != nil {
			r.err = err
			break
		}
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(b)); err == nil && int64(cfg.Width)*int64(cfg.Height) > docPixelsMax {
			r.row = docTooLarge
		} else if r.image, err = ui.DecodeBitmap(b); err != nil {
			r.row = "This picture could not be drawn here. Open it in the browser."
		}
	case "binary":
		r.row = notText
	default:
		noun, ok := docKindNouns[d.Kind]
		if !ok {
			noun = "This file"
		}
		r.row = noun + " opens in the Board's viewer, in the browser."
	}
	return r
}

func docRefusal(err error) *APIError {
	var api *APIError
	if errors.As(err, &api) && (api.Status == http.StatusForbidden && api.Code == "forbidden" ||
		api.Status == http.StatusNotFound && api.Code == "not_found" ||
		api.Status == http.StatusConflict && api.Code == "conflict") {
		return api
	}
	return nil
}

func (a *App) OpenDoc(rel string) { a.showDoc(rel, 1, true) }

func (a *App) showDoc(rel string, page int, push bool) {
	if rel == "" || a.Fleet.ID == "" {
		return
	}
	if a.Screen == DocScreen && a.Ref == rel && a.doc.page == page {
		return
	}
	if a.Screen == BoardScreen || a.Screen == TasksScreen {
		a.base = a.Screen
		a.viewerFocus = true
		a.tabFor = ""
	}
	if push && a.Screen != BoardScreen && a.Screen != TasksScreen && len(a.back) < viewerStackMax-1 {
		a.back = append(a.back, backStep{Screen: a.Screen, Ref: a.Ref, Report: a.pageReport,
			Target: a.pageTarget, HasTarget: a.pageTargetOK, Page: a.doc.page})
	}
	a.Screen, a.Ref, a.pageReport, a.scrollTo = DocScreen, rel, "", ""
	a.pageTarget, a.pageTargetOK, a.pageTargetNeed = Report{}, false, pageTargetSettled
	a.pageTargetGen++
	a.answerRef, a.AnswerChoice, a.AnswerNote = "", "", ""
	a.viewerScroll, a.asideScroll = ui.ScrollState{}, ui.ScrollState{}
	a.doc = docState{path: rel, page: page, from: 1}
	a.loadDoc()
}

func (a *App) resetDoc() {
	a.docGen++
	if a.docCancel != nil {
		a.docCancel()
		a.docCancel = nil
	}
	a.doc = docState{}
}

func (a *App) reloadDoc() {
	if a.Screen != DocScreen {
		return
	}
	a.doc.ok = false
	a.loadDoc()
}

func (a *App) loadDoc() {
	if a.Model == nil || a.Fleet.ID == "" || a.Screen != DocScreen {
		return
	}
	if a.docCancel != nil {
		a.docCancel()
	}
	a.docGen++
	gen, model, fleet, post := a.docGen, a.Model, a.Fleet.ID, a.Post
	req := a.doc
	a.doc.err, a.doc.refused, a.doc.conflict = "", false, false
	ctx, cancel := context.WithTimeout(a.Context, a.pollTimeout())
	a.docCancel = cancel
	apply := func(r docRead) {
		if a.docGen != gen || a.Fleet.ID != fleet {
			return
		}
		st := &a.doc
		if r.err != nil {
			st.err = friendly(r.err)
			if api := docRefusal(r.err); api != nil {
				st.err, st.refused, st.conflict = api.Message, true, api.Status == http.StatusConflict
			}
			return
		}
		st.doc, st.ok = r.doc, true
		st.window, st.body, st.image, st.row = r.window, r.body, r.image, r.row
	}
	if post == nil {
		r := readDoc(ctx, model, fleet, req)
		cancel()
		apply(r)
		return
	}
	go func() {
		r := readDoc(ctx, model, fleet, req)
		cancel()
		post(func() { apply(r) })
	}()
}

func (a *App) docWindowAt(from int, later bool) {
	st := &a.doc
	if later {
		st.froms = append(st.froms, st.from)
	} else if n := len(st.froms); n > 0 {
		st.froms = st.froms[:n-1]
	}
	st.from = from
	a.viewerScroll = ui.ScrollState{}
	a.loadDoc()
}

func (a *App) docEarlier() {
	if n := len(a.doc.froms); n > 0 {
		a.docWindowAt(a.doc.froms[n-1], false)
	}
}

func docRel(fleet, dest string) (string, bool) {
	args, ok := boardOpenArgs(fleet, dest)
	if !ok || len(args) != 2 || args[0] != "--doc" {
		return "", false
	}
	return args[1], true
}

func (a *App) openDocLink(dest string) bool {
	rel, ok := docRel(a.Fleet.ID, dest)
	if ok {
		a.OpenDoc(rel)
	}
	return ok
}

func (a *App) docLink(dest string) (string, bool) {
	if rel, ok := docRel(a.Fleet.ID, dest); ok {
		return rel, true
	}
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	rel := path.Join(path.Dir(a.Ref), u.Path)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}
