package board

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"time"
)

const (
	apiDefaultLines = chunkLines
	apiMaxLines     = 2000
)

type APIDocEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Dir     bool   `json:"dir"`
	Image   bool   `json:"image"`
	Size    *int64 `json:"size,omitempty"`
	At      string `json:"at,omitempty"`
	Refused string `json:"refused,omitempty"`
}

type APIDocFolder struct {
	Path     string        `json:"path"`
	Name     string        `json:"name"`
	Kind     string        `json:"kind"`
	At       string        `json:"at"`
	Entries  []APIDocEntry `json:"entries"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
	Total    int           `json:"total"`
	Pages    int           `json:"pages"`
}

type APIDocStep struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type APIDocFile struct {
	Path       string      `json:"path"`
	Name       string      `json:"name"`
	Kind       string      `json:"kind"`
	Text       bool        `json:"text"`
	Size       int64       `json:"size"`
	At         string      `json:"at"`
	Prev       *APIDocStep `json:"prev,omitempty"`
	Next       *APIDocStep `json:"next,omitempty"`
	FolderPage int         `json:"folder_page"`
}

type APIDocCut struct {
	Line  int `json:"line"`
	Bytes int `json:"bytes"`
}

type APIDocWindow struct {
	Path    string      `json:"path"`
	Name    string      `json:"name"`
	From    int         `json:"from"`
	Lines   []string    `json:"lines"`
	Cut     []APIDocCut `json:"cut,omitempty"`
	Partial bool        `json:"partial,omitempty"`
	Next    int         `json:"next,omitempty"`
}

func (b *Board) apiDoc(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ranged := q.Has("from") || q.Has("lines")
	root, real, f, fi, err := openDoc(b.o.Home, r.PathValue("path"))
	if err != nil {
		status, msg := docRefusal(err)
		b.apiFail(w, status, apiCode(status), msg)
		return
	}
	defer f.Close()
	rel := path.Clean(r.PathValue("path"))
	name := path.Base(rel)
	at := fi.ModTime().UTC().Format(time.RFC3339)
	reply := func(data any) {
		fl, err := b.st.Fleet(r.Context())
		if err != nil {
			b.apiFailErr(w, err)
			return
		}
		b.apiOK(w, fl, "", data)
	}
	if fi.IsDir() {
		if q.Has("raw") || ranged {
			b.apiFail(w, http.StatusBadRequest, "bad_request", "a folder has no bytes or lines; ask for its listing")
			return
		}
		page := 1
		if s := q.Get("page"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 {
				b.apiFail(w, http.StatusBadRequest, "bad_request", "page must be a number, 1 or more")
				return
			}
			page = n
		}
		ms, page, pages, total := b.members(root, real, f, rel, page)
		out := APIDocFolder{Path: rel, Name: name, Kind: "folder", At: at, Entries: make([]APIDocEntry, 0, len(ms)), Page: page, PageSize: folderPage, Total: total, Pages: pages}
		for _, m := range ms {
			e := APIDocEntry{Name: m.name, Path: rel + "/" + m.name, Kind: "file", Label: m.kind, Dir: m.dir, Image: m.image, At: m.at, Refused: m.refused}
			switch {
			case m.kind == "LINK":
				e.Kind = "link"
			case m.dir:
				e.Kind = "folder"
			}
			if m.size >= 0 {
				e.Size = &m.size
			}
			out.Entries = append(out.Entries, e)
		}
		reply(out)
		return
	}
	head, err := readHead(f)
	if err != nil {
		b.apiFailErr(w, err)
		return
	}
	sniffed := http.DetectContentType(head)
	if q.Has("raw") {
		b.raw(w, r, f, fi, rel, sniffed, head)
		return
	}
	if ranged {
		b.apiWindow(w, r, f, fi, rel, head, reply)
		return
	}
	kind := docKind(head, sniffed, fi.Size(), name)
	out := APIDocFile{Path: rel, Name: name, Kind: kind, Text: textHead(head), Size: fi.Size(), At: at}
	prev, next, page := b.neighbours(root, rel, kind == "image")
	step := func(n string) *APIDocStep {
		if n == "" {
			return nil
		}
		return &APIDocStep{Name: n, Path: path.Dir(rel) + "/" + n}
	}
	out.Prev, out.Next, out.FolderPage = step(prev), step(next), page
	reply(out)
}

func (b *Board) apiWindow(w http.ResponseWriter, r *http.Request, f *os.File, fi os.FileInfo, rel string, head []byte, reply func(any)) {
	if !textHead(head) {
		b.apiFail(w, http.StatusBadRequest, "bad_request", notText)
		return
	}
	q := r.URL.Query()
	first, n := 1, apiDefaultLines
	if from := q.Get("from"); from != "" {
		v, err := strconv.Atoi(from)
		if err != nil || v < 1 {
			b.apiFail(w, http.StatusBadRequest, "bad_request", "from must be a line number, 1 or more")
			return
		}
		first = v
	}
	if count := q.Get("lines"); count != "" {
		v, err := strconv.Atoi(count)
		if err != nil || v < 1 {
			b.apiFail(w, http.StatusBadRequest, "bad_request", "lines must be a number, 1 or more")
			return
		}
		n = min(v, apiMaxLines)
	}
	ctx := r.Context()
	start, ok := lineStart(ctx, f, fi.Size(), first)
	if ctx.Err() != nil {
		return
	}
	if !ok {
		b.apiFail(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("line %d is past the end of this file", first))
		return
	}
	out := APIDocWindow{Path: rel, Name: path.Base(rel), From: first, Lines: []string{}}
	br := bufio.NewReaderSize(io.NewSectionReader(f, start, fi.Size()-start), 64<<10)
	var buf []byte
	var used int64
	for len(out.Lines) < n && used < window {
		l, err := readLine(br, buf)
		buf = l.text
		if l.hasData {
			used += int64(l.read)
			if l.cut > 0 {
				out.Cut = append(out.Cut, APIDocCut{Line: first + len(out.Lines), Bytes: l.cut})
			}
			out.Lines = append(out.Lines, string(l.text))
			out.Partial = !l.ended
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				b.apiFailErr(w, err)
				return
			}
			break
		}
	}
	if start+used < fi.Size() {
		out.Next = first + len(out.Lines)
	}
	reply(out)
}
