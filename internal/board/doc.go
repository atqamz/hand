package board

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/markdown"
)

const maxMarkdown = 1 << 20

var (
	errHidden  = errors.New("the Board does not show Hand's own files, dot files, luvus/, locks/ or projects/")
	errOutside = errors.New("this path leads outside the fleet folder, so the Board does not show it")
	errKind    = errors.New("the Board shows files and folders only")
	errChanged = errors.New("this file changed while the Board opened it; reload the page")
	ownFolders = []string{"luvus", "locks", "projects"}
)

func hidden(rel string, dir bool) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return slices.ContainsFunc(parts, func(p string) bool { return strings.HasPrefix(p, ".") }) ||
		len(parts) == 1 && !dir ||
		slices.ContainsFunc(ownFolders, func(f string) bool { return strings.EqualFold(f, parts[0]) })
}

func Resolve(home, rel string) (root, real string, fi fs.FileInfo, err error) {
	if !filepath.IsAbs(home) || strings.ContainsAny(rel, "\\\x00") || slices.Contains(strings.Split(rel, "/"), "..") {
		return "", "", nil, errOutside
	}
	rel = path.Clean(rel)
	if hidden(rel, true) {
		return "", "", nil, errHidden
	}
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", "", nil, errOutside
	}
	if root, err = filepath.EvalSymlinks(home); err != nil {
		return "", "", nil, err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", "", nil, err
	}
	if real, err = filepath.Rel(root, full); err != nil || !filepath.IsLocal(real) {
		return "", "", nil, errOutside
	}
	if fi, err = os.Lstat(full); err != nil {
		return "", "", nil, err
	}
	if !fi.Mode().IsRegular() && !fi.IsDir() {
		return "", "", nil, errKind
	}
	if hidden(real, fi.IsDir()) {
		return "", "", nil, errHidden
	}
	return root, real, fi, nil
}

func openSame(home, rel, root, real string) (*os.File, fs.FileInfo, error) {
	f, err := os.OpenInRoot(root, real)
	if err != nil {
		return nil, nil, err
	}
	got, err := f.Stat()
	_, again, now, rerr := Resolve(home, rel)
	if err != nil || rerr != nil || again != real || !os.SameFile(got, now) {
		f.Close()
		return nil, nil, errChanged
	}
	return f, got, nil
}

func isMarkdown(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".md" || ext == ".markdown"
}

func docURL(base, rel string) string {
	return base + "/doc/" + (&url.URL{Path: rel}).EscapedPath()
}

func DocHref(home, base, p string) string {
	if slices.Contains(strings.Split(filepath.ToSlash(p), "/"), "..") {
		return ""
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		user, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		p = filepath.Join(user, rest)
	}
	if !filepath.IsAbs(p) || strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//") {
		return ""
	}
	homes := []string{home}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		homes = append(homes, real)
	}
	for _, h := range homes {
		if rel, err := filepath.Rel(h, p); err == nil && filepath.IsLocal(rel) {
			if _, _, _, err := Resolve(home, filepath.ToSlash(rel)); err == nil {
				return docURL(base, filepath.ToSlash(rel))
			}
			return ""
		}
	}
	return ""
}

func (b *Board) local() func(string) string {
	seen := map[string]string{}
	return func(p string) string {
		if h, ok := seen[p]; ok {
			return h
		}
		h := DocHref(b.o.Home, b.o.Base, p)
		seen[p] = h
		return h
	}
}

func (b *Board) linked(ds []Deliverable) []Deliverable {
	for i, d := range ds {
		if d.Kind == "file" {
			ds[i].Href = DocHref(b.o.Home, b.o.Base, d.Ref)
		}
	}
	return ds
}

type crumb struct{ Name, Href string }

func (b *Board) doc(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	root, real, _, err := Resolve(b.o.Home, r.PathValue("path"))
	var f *os.File
	var fi fs.FileInfo
	if err == nil {
		f, fi, err = openSame(b.o.Home, r.PathValue("path"), root, real)
	}
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		b.fail(w, http.StatusNotFound, "there is no file or folder at this path")
		return
	case errors.Is(err, errChanged):
		b.fail(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, errHidden), errors.Is(err, errOutside), errors.Is(err, errKind):
		b.fail(w, http.StatusForbidden, err.Error())
		return
	case err != nil:
		b.fail(w, http.StatusForbidden, "the Board cannot read this path")
		return
	}
	defer f.Close()
	rel := path.Clean(r.PathValue("path"))
	name := path.Base(rel)
	data := map[string]any{"Title": name, "Page": "doc", "Fleets": b.fleets(ctx), "StripData": b.stripData(ctx), "Name": name, "Path": filepath.Join(b.o.Home, filepath.FromSlash(rel)), "Crumbs": b.crumbs(rel), "At": fi.ModTime().UTC().Format(time.RFC3339), "Self": docURL(b.o.Base, rel)}
	if fi.IsDir() {
		data["Folder"] = true
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		data["Entries"], data["Pager"] = b.listing(root, real, f, rel, page)
		b.render(w, http.StatusOK, "doc.html", data)
		return
	}
	head := make([]byte, headBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		b.failErr(w, err)
		return
	}
	head = head[:n]
	sniffed := http.DetectContentType(head)
	_, image := imageExt[sniffed]
	image = image && imageLike.MatchString(name)
	text := textHead(head)
	q := r.URL.Query()
	if q.Has("raw") {
		b.raw(w, r, f, fi, name, sniffed, head)
		return
	}
	if q.Has("from") && text {
		b.follow(w, r, f, fi)
		return
	}
	view := image && q.Has("view")
	if download := q.Has("download"); download || image && !view {
		h := w.Header()
		h.Set("Content-Type", sniffed)
		if download {
			h.Set("Content-Type", "application/octet-stream")
			h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		}
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "private, no-cache")
		http.ServeContent(w, r, "", fi.ModTime(), f)
		return
	}
	data["Size"] = size(fi.Size())
	self := data["Self"].(string)
	data["Download"] = self + "?download"
	zoom := q.Get("view") == "actual"
	tail := "?view"
	if zoom {
		tail = "?view=actual"
	}
	ext := strings.ToLower(path.Ext(name))
	asText := q.Get("as") == "text"
	prev, next, at := b.around(root, rel, view, tail, asText && isMarkdown(name))
	if prev != nil || next != nil {
		data["Prev"], data["Next"] = prev, next
	}
	if crumbs := data["Crumbs"].([]crumb); at > 1 && len(crumbs) > 0 {
		crumbs[len(crumbs)-1].Href += "?page=" + strconv.Itoa(at)
	}
	if v, ok := views[ext]; ok && text && (ext != ".json" || fi.Size() <= maxPretty) && (!isMarkdown(name) || fi.Size() <= maxMarkdown) {
		data["Views"] = []choice{{v[0], self, !asText}, {v[1], self + "?as=text", asText}}
		data["Remember"] = isMarkdown(name)
	}
	switch kind := media(sniffed, name); {
	case view:
		data["Picture"], data["Zoom"] = self, zoom
		data["Views"] = []choice{{"Fit", self + "?view", !zoom}, {"Actual size", self + "?view=actual", zoom}}
	case kind != "":
		data["Media"] = map[string]string{"Tag": strings.SplitN(kind, "/", 2)[0], "Src": self + "?raw"}
	case isPDF(head, name):
		data["Frame"] = map[string]string{"Src": self + "?raw", "Kind": "pdf"}
	case !text:
		data["Notice"] = "This file is not text, so the Board does not show it; download it to open it."
	case !asText && isMarkdown(name) && fi.Size() <= maxMarkdown:
		md, err := io.ReadAll(io.NewSectionReader(f, 0, maxMarkdown))
		if err != nil {
			b.failErr(w, err)
			return
		}
		if !utf8.Valid(md) || bytes.IndexByte(md, 0) >= 0 {
			delete(data, "Views")
			delete(data, "Remember")
			data["Notice"] = "This file is not text, so the Board does not show it; download it to open it."
			break
		}
		data["Body"] = markdown.RenderDoc(string(md), b.o.Base, b.refTitle(ctx), b.local())
	case !asText && isHTML(name):
		data["Frame"] = map[string]string{"Src": self + "?raw", "Kind": "html"}
	case !asText && ext == ".svg":
		data["Picture"] = self + "?raw"
	case !asText && (ext == ".csv" || ext == ".tsv"):
		comma := ','
		if ext == ".tsv" {
			comma = '\t'
		}
		rows, numeric, cut, err := table(io.NewSectionReader(f, 0, fi.Size()), comma)
		if err != nil || len(rows) == 0 {
			delete(data, "Views")
			b.textPage(w, r, f, fi, data, nil)
			return
		}
		data["Rows"], data["Numeric"], data["Cut"] = rows, numeric, cut
	default:
		var pretty []byte
		if ext == ".json" && !asText && data["Views"] != nil {
			var note string
			if pretty, note = indented(io.NewSectionReader(f, 0, maxPretty)); pretty == nil {
				delete(data, "Views")
				data["Notice"] = note
			}
		}
		b.textPage(w, r, f, fi, data, pretty)
		return
	}
	b.render(w, http.StatusOK, "doc.html", data)
}

func (b *Board) crumbs(rel string) []crumb {
	parts := strings.Split(rel, "/")
	out := make([]crumb, 0, len(parts)-1)
	for i := range parts[:len(parts)-1] {
		out = append(out, crumb{parts[i], docURL(b.o.Base, strings.Join(parts[:i+1], "/"))})
	}
	return out
}
