package board

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/inbox"
)

type APIInboxFile struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Size  int64  `json:"size"`
}

type APITaskFile struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Size    int64  `json:"size"`
	Missing bool   `json:"missing,omitempty"`
}

func (b *Board) apiTaskFiles(ctx context.Context, id int64) ([]APITaskFile, error) {
	files, err := b.st.TaskFiles(ctx, id)
	if err != nil {
		return nil, err
	}
	var out []APITaskFile
	for _, f := range files {
		a := APITaskFile{Name: f.Name, Label: label(f.Name), Kind: attachKind(f.Name)}
		if fi, err := os.Lstat(filepath.Join(b.o.Home, "inbox", f.Name)); err == nil && fi.Mode().IsRegular() {
			a.Size = fi.Size()
		} else {
			a.Missing = true
		}
		out = append(out, a)
	}
	return out, nil
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

var attachmentToken = regexp.MustCompile(`\[attachment ([1-9][0-9]{0,3})\]`)

func (b *Board) apiAttach(w http.ResponseWriter, text string, names []string) (string, bool) {
	dir := filepath.Join(b.o.Home, "inbox")
	refs := make([]string, len(names))
	for i, name := range names {
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
		refs[i] = inbox.Line(b.o.Home, i+1, name)
	}
	used := make([]bool, len(names))
	text = attachmentToken.ReplaceAllStringFunc(text, func(tok string) string {
		if n, _ := strconv.Atoi(tok[len("[attachment ") : len(tok)-1]); n <= len(names) {
			used[n-1] = true
			return refs[n-1]
		}
		return tok
	})
	var lines strings.Builder
	for i, ref := range refs {
		if !used[i] {
			lines.WriteString(ref + "\n")
		}
	}
	if lines.Len() > 0 && text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text + lines.String(), true
}
