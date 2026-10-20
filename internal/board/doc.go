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
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/atqamz/hand/internal/markdown"
)

const (
	maxDoc     = 1 << 20
	maxListing = 500
)

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

func docURL(base, rel string) string {
	return base + "/doc/" + (&url.URL{Path: rel}).EscapedPath()
}

func DocHref(home, base, p string) string {
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

func (b *Board) linked(ds []Deliverable) []Deliverable {
	for i, d := range ds {
		if d.Kind == "file" {
			ds[i].Href = DocHref(b.o.Home, b.o.Base, d.Ref)
		}
	}
	return ds
}

type crumb struct{ Name, Href string }

type entry struct {
	Name, Href, Kind, Size, At string
	Dir, Image                 bool
}

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
	data := map[string]any{"Title": name, "Page": "doc", "Fleets": b.fleets(ctx), "StripData": b.stripData(ctx), "Name": name, "Path": filepath.Join(b.o.Home, filepath.FromSlash(rel)), "Crumbs": b.crumbs(rel), "At": fi.ModTime().UTC().Format(time.RFC3339)}
	if fi.IsDir() {
		data["Folder"] = true
		data["Entries"], data["More"] = b.listing(f, rel)
		b.render(w, http.StatusOK, "doc.html", data)
		return
	}
	head, err := io.ReadAll(io.LimitReader(f, 512))
	if err != nil {
		b.failErr(w, err)
		return
	}
	sniffed := http.DetectContentType(head)
	_, image := imageExt[sniffed]
	image = image && imageLike.MatchString(name)
	if download := r.URL.Query().Has("download"); download || image {
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
	data["Download"] = docURL(b.o.Base, rel) + "?download"
	if fi.Size() > maxDoc {
		data["Notice"] = "This file is " + size(fi.Size()) + ". The Board shows text files up to 1 MiB; download it to read it."
		b.render(w, http.StatusOK, "doc.html", data)
		return
	}
	rest, err := io.ReadAll(io.LimitReader(f, maxDoc-int64(len(head))))
	if err != nil {
		b.failErr(w, err)
		return
	}
	text := append(head, rest...)
	switch ext := strings.ToLower(path.Ext(name)); {
	case !utf8.Valid(text) || bytes.IndexByte(text, 0) >= 0:
		data["Notice"] = "This file is not text, so the Board does not show it; download it to open it."
	case ext == ".md" || ext == ".markdown":
		data["Body"] = markdown.RenderDoc(string(text), b.o.Base, b.refTitle(ctx))
	default:
		data["Plain"] = string(text)
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

func (b *Board) listing(f *os.File, rel string) ([]entry, int) {
	items, _ := f.ReadDir(-1)
	items = slices.DeleteFunc(items, func(d fs.DirEntry) bool { return strings.HasPrefix(d.Name(), ".") })
	slices.SortFunc(items, func(x, y fs.DirEntry) int {
		if x.IsDir() != y.IsDir() {
			if x.IsDir() {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(x.Name()), strings.ToLower(y.Name()))
	})
	more := max(0, len(items)-maxListing)
	out := make([]entry, 0, len(items)-more)
	for _, it := range items[:len(items)-more] {
		e := entry{Name: it.Name(), Href: docURL(b.o.Base, rel+"/"+it.Name()), Dir: it.IsDir(), Kind: "DIR"}
		switch {
		case it.Type()&fs.ModeSymlink != 0:
			e.Kind = "LINK"
		case !e.Dir:
			e.Kind, e.Image = fileKind(e.Name), imageLike.MatchString(e.Name)
		}
		if info, err := it.Info(); err == nil {
			e.At = info.ModTime().UTC().Format(time.RFC3339)
			if info.Mode().IsRegular() {
				e.Size = size(info.Size())
			}
		}
		out = append(out, e)
	}
	return out, more
}
