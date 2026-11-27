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
	docTooLarge  = "This picture is larger than this window reads."
)

const docCannot = " cannot be shown in this window."

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
	table  *docTable

	note      string
	said      string
	noteBad   bool
	copying   bool
	saving    bool
	clip      string
	clipReady bool

	find        docFind
	follow      docFollow
	srcLines    []string
	mark        int
	scrollMark  bool
	scrollSeq   int
	scrollArmed int
	scrollFrom  float32
	lineIn      string
	lineNote    string
	lineBad     bool
	findSaid    string
	lineSaid    string
	focusFind   bool
	navGen      int
}

type docEnv struct {
	openAudio func([]byte) (AudioPlayer, error)
	web       bool
	source    bool
	keep      bool
}

type docRead struct {
	player AudioPlayer
	frame  *DocFrame
	keep   bool
	doc    Doc
	window DocWindow
	body   string
	image  *ui.Bitmap
	row    string
	table  *docTable
	err    error
}

var docKindNouns = map[string]string{
	"pdf": "A PDF", "html": "An HTML page", "svg": "An SVG picture", "video": "A video", "audio": "An audio file",
}

func readDoc(ctx context.Context, m ReadModel, fleet string, req docState, env docEnv) docRead {
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
		r.image, r.row = drawBitmap(b, docPixelsMax)
	case "binary":
		r.row = notText
	case "csv", "tsv":
		r.table, r.err = readTable(ctx, m, fleet, req.path, d.Kind)
	case "html":
		if r.window, r.err = m.DocWindow(ctx, fleet, req.path, req.from); r.err != nil {
			break
		}
		switch {
		case !env.web:
			r.row = docKindNouns[d.Kind] + docCannot
		case env.source:
		case env.keep:
			r.keep = true
		default:
			var f DocFrame
			if f, r.err = m.(DocFramer).DocFrame(ctx, fleet, req.path); r.err == nil {
				f.URL += "&frame"
				r.frame = &f
			}
		}
	case "audio":
		switch {
		case env.keep:
			r.keep = true
		case d.Size > docRawMax:
			r.row = audioTooLarge
		default:
			var b []byte
			if b, r.err = m.DocBytes(ctx, fleet, req.path); r.err == nil {
				r.player, r.row = openedAudio(env.openAudio, b)
			}
		}
	case "pdf":
		switch {
		case !env.web:
			r.row = docKindNouns[d.Kind] + docCannot
		case env.keep:
			r.keep = true
		default:
			var f DocFrame
			if f, r.err = m.(DocFramer).DocFrame(ctx, fleet, req.path); r.err == nil {
				r.frame = &f
			}
		}
	default:
		noun, ok := docKindNouns[d.Kind]
		if !ok {
			noun = "This file"
		}
		r.row = noun + docCannot
	}
	return r
}

func drawBitmap(b []byte, most int64) (*ui.Bitmap, string) {
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(b)); err == nil && int64(cfg.Width)*int64(cfg.Height) > most {
		return nil, docTooLarge
	}
	bmp, err := ui.DecodeBitmap(b)
	if err != nil {
		return nil, "This picture could not be drawn here."
	}
	return bmp, ""
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
	if isBase(a.Screen) {
		if a.Screen == PhoneScreen {
			a.resetPhone()
		}
		a.base = a.Screen
		a.tabFor = ""
	}
	a.viewerFocus = true
	if push && !isBase(a.Screen) && len(a.back) < viewerStackMax-1 {
		a.pushBack()
	}
	_, _, stop := a.nextLoad()
	stop()
	a.releaseMedia()
	a.Screen, a.Ref, a.pageReport, a.scrollTo = DocScreen, rel, "", ""
	a.pageTarget, a.pageTargetOK, a.pageTargetNeed = Report{}, false, pageTargetSettled
	a.pageTargetGen++
	a.answerRef, a.AnswerChoice, a.AnswerNote = "", "", ""
	a.viewerScroll, a.asideScroll, a.restore = ui.ScrollState{}, ui.ScrollState{}, scrollMemo{}
	a.docOp++
	a.doc = docState{path: rel, page: page, from: 1}
	a.loadDoc()
}

func (a *App) settleMedia(r docRead) {
	rel := a.Ref
	switch {
	case r.keep:
	case r.player != nil:
		a.releaseMedia()
		a.au = audioState{path: rel, player: r.player}
	case r.frame != nil:
		a.releaseMedia()
		a.openWeb(rel, docKindNouns[a.doc.doc.Kind], *r.frame)
	default:
		a.releaseMedia()
	}
}

func (a *App) resetDoc() {
	a.releaseMedia()
	a.docGen++
	a.docOp++
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
	env := docEnv{openAudio: a.audioOpener(), web: a.webReady(), source: a.DocSource,
		keep: a.audioHeld(a.Ref) || a.web.path == a.Ref && a.web.notice == ""}
	apply := func(r docRead) {
		if a.docGen != gen || a.Fleet.ID != fleet || a.closed {
			if r.player != nil {
				r.player.Close()
			}
			return
		}
		st := &a.doc
		if r.err != nil {
			a.releaseMedia()
			st.err = friendly(r.err)
			if api := docRefusal(r.err); api != nil {
				st.err, st.refused, st.conflict = api.Message, true, api.Status == http.StatusConflict
			}
			var bad *APIError
			if errors.As(r.err, &bad) && bad.Status == http.StatusBadRequest {
				st.from, st.froms = 1, nil
			}
			return
		}
		st.doc, st.ok = r.doc, true
		st.window, st.body, st.image, st.row, st.table = r.window, r.body, r.image, r.row, r.table
		st.srcLines = nil
		a.settleMedia(r)
	}
	if post == nil {
		r := readDoc(ctx, model, fleet, req, env)
		cancel()
		apply(r)
		return
	}
	go func() {
		r := readDoc(ctx, model, fleet, req, env)
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
	a.stopFollow("", false)
	a.viewerScroll = ui.ScrollState{}
	a.loadDoc()
}

func (a *App) docEarlier() {
	if n := len(a.doc.froms); n > 0 {
		a.docWindowAt(a.doc.froms[n-1], false)
	}
}

func (a *App) docRel(dest string) (string, bool) {
	if _, board := a.linkTarget(dest); !board {
		return "", false
	}
	args, ok := boardOpenArgs(a.Fleet.ID, dest)
	if !ok || len(args) != 2 || args[0] != "--doc" {
		return "", false
	}
	return args[1], true
}

func (a *App) openDocLink(dest string) bool {
	rel, ok := a.docRel(dest)
	if ok {
		a.OpenDoc(rel)
	}
	return ok
}

func (a *App) docLink(dest string) (string, bool) {
	if rel, ok := a.docRel(dest); ok {
		return rel, true
	}
	u, err := url.Parse(dest)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") ||
		strings.ContainsAny(u.Path, "\\\x00") {
		return "", false
	}
	rel := path.Join(path.Dir(a.Ref), u.Path)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}
