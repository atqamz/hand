package board

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type APIInboxFile struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Size  int64  `json:"size"`
}

func (b *Board) apiInboxUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !b.apiCSRF(w, r) {
		return
	}
	if b.o.Control == nil {
		b.apiFail(w, http.StatusForbidden, "forbidden", "the board's controls are off")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPost)
	f, h, err := r.FormFile("file")
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		b.apiFail(w, http.StatusRequestEntityTooLarge, "too_large", "the request is too large")
		return
	case err != nil:
		b.apiFail(w, http.StatusBadRequest, "bad_request", "send one file as multipart form data in the field file")
		return
	}
	defer f.Close()
	name, err := saveImage(filepath.Join(b.o.Home, "inbox"), b.now(), h.Filename, f)
	switch {
	case errors.Is(err, errTooLarge):
		b.apiFail(w, http.StatusRequestEntityTooLarge, "too_large", err.Error())
		return
	case err != nil:
		b.apiFailErr(w, err)
		return
	}
	fl, err := b.st.Fleet(ctx)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	b.apiOK(w, fl, "", APIInboxFile{Name: name, Label: label(name), Kind: attachKind(name), Size: h.Size})
}

func (b *Board) apiInbox(w http.ResponseWriter, r *http.Request) {
	b.serveInbox(w, r, func() { b.apiFail(w, http.StatusNotFound, "not_found", "not found: file in inbox/") })
}

func (b *Board) apiAttachLines(w http.ResponseWriter, names []string) (string, bool) {
	dir := filepath.Join(b.o.Home, "inbox")
	var lines strings.Builder
	for _, name := range names {
		if !shotName.MatchString(name) {
			b.apiFail(w, http.StatusBadRequest, "bad_request", "an attachment must be the name of a file in inbox/, as POST /inbox answered it")
			return "", false
		}
		fi, err := os.Lstat(filepath.Join(dir, name))
		switch {
		case errors.Is(err, fs.ErrNotExist) || err == nil && !fi.Mode().IsRegular():
			b.apiFail(w, http.StatusNotFound, "not_found", fmt.Sprintf("not found: %s in inbox/", label(name)))
			return "", false
		case err != nil:
			b.apiFailErr(w, err)
			return "", false
		}
		fmt.Fprintf(&lines, "[%s: %s]\n", attachKind(name), filepath.Join(dir, name))
	}
	return lines.String(), true
}
