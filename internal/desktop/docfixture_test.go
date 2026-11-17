package desktop_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
)

func (f *fixture) Doc(ctx context.Context, id, path string, page int) (desktop.Doc, error) {
	f.note("doc:" + path)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.docErr[path]; err != nil {
		return desktop.Doc{}, err
	}
	key := path
	if page > 1 {
		key = fmt.Sprintf("%s#%d", path, page)
	}
	d, ok := f.docs[key]
	if !ok {
		return desktop.Doc{}, &desktop.APIError{Status: 404, Code: "not_found", Message: "there is no file or folder at this path"}
	}
	return d, nil
}

func (f *fixture) DocWindow(ctx context.Context, id, path string, from int) (desktop.DocWindow, error) {
	f.note(fmt.Sprintf("window:%s:%d", path, from))
	f.mu.Lock()
	defer f.mu.Unlock()
	lines, ok := f.docLines[path]
	if !ok || from < 1 || from > max(len(lines), 1) {
		return desktop.DocWindow{}, &desktop.APIError{Status: 400, Code: "bad_request", Message: "this file has no such line"}
	}
	size := f.docWindowSize
	if size == 0 {
		size = 500
	}
	end := min(from-1+size, len(lines))
	w := desktop.DocWindow{Path: path, Name: path, From: from, Lines: lines[from-1 : end]}
	if end < len(lines) {
		w.Next = end + 1
	}
	return w, nil
}

func (f *fixture) DocBytes(ctx context.Context, id, path string) ([]byte, error) {
	f.note("raw:" + path)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.docErr[path]; err != nil {
		return nil, err
	}
	b, ok := f.docBytes[path]
	if !ok {
		return nil, &desktop.APIError{Status: 404, Code: "not_found", Message: "there is no file or folder at this path"}
	}
	return b, nil
}

func stripes(t *testing.T, w, h int, tones ...color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, tones[x*len(tones)/w])
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const designMD = "# Nightly export pipeline: design\n\nProposal for `t1`, written by its worker. The [API notes](notes.txt) list the endpoints, and the [export notes](/f00000000e0aa/doc/reports/t1/sub/deep.md) explain why the old export is retired.\n\n## Goals\n\n- One job per night that is safe to retry\n- A manifest written last\n- A backfill of the last 30 days\n\n## Data flow\n\nThe export runs once a night, writes one CSV per region, and publishes a manifest last, so a reader never sees half a night.\n"

func at(day string) string { return "2026-11-0" + day + "T04:40:00Z" }

func sz(n int64) *int64 { return &n }

func docFixture(t *testing.T) *fixture {
	t.Helper()
	return withDocs(t, goldenFixture(t))
}

func withDocs(t *testing.T, f *fixture) *fixture {
	t.Helper()
	blue, red, green := color.RGBA{0x40, 0x7a, 0xcc, 255}, color.RGBA{0xc4, 0x2b, 0x32, 255}, color.RGBA{0x1a, 0x7a, 0x3e, 255}
	lines := make([]string, 5)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d of the export log", i+1)
	}
	lines[3] = "\tindented\tby tabs"
	f.docWindowSize = 3
	f.docLines = map[string][]string{"reports/t1/notes.txt": lines}
	f.docBytes = map[string][]byte{
		"reports/t1/design.md":  []byte(designMD),
		"reports/t1/shot-1.png": stripes(t, 480, 270, blue, red, green),
		"reports/t1/shot-2.png": stripes(t, 480, 270, green, blue),
		"reports/t1/shot-3.png": stripes(t, 480, 270, red, green, blue),
	}
	f.docErr = map[string]error{}
	entry := func(name, kind, label string, size int64) desktop.DocEntry {
		return desktop.DocEntry{Name: name, Path: "reports/t1/" + name, Kind: kind, Label: label, Size: sz(size), At: at("1"),
			Image: label == "PNG"}
	}
	file := func(path, name, kind string, size int64, prev, next string, page int) desktop.Doc {
		d := desktop.Doc{Path: path, Name: name, Kind: kind, Text: kind == "text" || kind == "markdown", Size: size, At: at("1"), FolderPage: page}
		if prev != "" {
			d.Prev = &desktop.DocStep{Name: prev, Path: "reports/t1/" + prev}
		}
		if next != "" {
			d.Next = &desktop.DocStep{Name: next, Path: "reports/t1/" + next}
		}
		return d
	}
	sub := desktop.DocEntry{Name: "sub", Path: "reports/t1/sub", Kind: "folder", Label: "DIR", Dir: true, At: at("1")}
	f.docs = map[string]desktop.Doc{
		"reports/t1": {Path: "reports/t1", Name: "t1", Kind: "folder", At: at("1"), Page: 1, PageSize: 4, Total: 8, Pages: 2,
			Entries: []desktop.DocEntry{sub, entry("design.md", "file", "MD", 1536), entry("notes.txt", "file", "TXT", 120), entry("plan.pdf", "file", "PDF", 52000)}},
		"reports/t1#2": {Path: "reports/t1", Name: "t1", Kind: "folder", At: at("1"), Page: 2, PageSize: 4, Total: 8, Pages: 2,
			Entries: []desktop.DocEntry{entry("shot-1.png", "file", "PNG", 6000), entry("shot-2.png", "file", "PNG", 6100),
				entry("shot-3.png", "file", "PNG", 6200), entry("z-last.txt", "file", "TXT", 12)}},
		"reports/t1/sub": {Path: "reports/t1/sub", Name: "sub", Kind: "folder", At: at("1"), Page: 1, PageSize: 4, Total: 1, Pages: 1,
			Entries: []desktop.DocEntry{{Name: "deep.md", Path: "reports/t1/sub/deep.md", Kind: "file", Label: "MD", Size: sz(40), At: at("1")}}},
		"reports/t1/design.md":   file("reports/t1/design.md", "design.md", "markdown", 1536, "", "notes.txt", 1),
		"reports/t1/notes.txt":   file("reports/t1/notes.txt", "notes.txt", "text", 120, "design.md", "plan.pdf", 1),
		"reports/t1/plan.pdf":    file("reports/t1/plan.pdf", "plan.pdf", "pdf", 52000, "notes.txt", "shot-1.png", 1),
		"reports/t1/shot-1.png":  file("reports/t1/shot-1.png", "shot-1.png", "image", 6000, "", "shot-2.png", 2),
		"reports/t1/shot-2.png":  file("reports/t1/shot-2.png", "shot-2.png", "image", 6100, "shot-1.png", "shot-3.png", 2),
		"reports/t1/shot-3.png":  file("reports/t1/shot-3.png", "shot-3.png", "image", 6200, "shot-2.png", "", 2),
		"reports/t1/z-last.txt":  file("reports/t1/z-last.txt", "z-last.txt", "text", 12, "shot-3.png", "", 2),
		"reports/t1/sub/deep.md": {Path: "reports/t1/sub/deep.md", Name: "deep.md", Kind: "markdown", Text: true, Size: 40, At: at("1"), FolderPage: 1},
	}
	f.docBytes["reports/t1/sub/deep.md"] = []byte("# Export notes\n\nThe old export is retired.\n")
	f.docLines["reports/t1/z-last.txt"] = []string{"the end"}
	f.docErr["reports/t1/flaky.md"] = errors.New("the connection was reset")
	f.docs["reports/t1/flaky.md"] = file("reports/t1/flaky.md", "flaky.md", "markdown", 10, "", "", 1)
	f.docBytes["reports/t1/flaky.md"] = []byte("# Steady now\n")
	return f
}
