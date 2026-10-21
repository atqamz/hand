//go:build unix

package board_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestARefusedLinkIsListedWithItsReasonAndNoTarget(t *testing.T) {
	outside := fleetFolder(t, map[string]string{"secrets/id_ed25519": secret, "notes.md": secret})
	h, home := viewer(t, map[string]string{"hand.db": secret, "luvus/server.pid": secret, "reports/t1/design.md": design, "reports/t1/real/x.md": "x", "reports/t1/pic.png": string(flowPNG)})
	dir := filepath.Join(home, "reports", "t1")
	link(t, filepath.Join(outside, "secrets"), filepath.Join(dir, "escape"))
	link(t, filepath.Join(outside, "notes.md"), filepath.Join(dir, "notes.md"))
	link(t, "../../hand.db", filepath.Join(dir, "db-link"))
	link(t, "../../luvus", filepath.Join(dir, "luvus-link"))
	link(t, "gone.md", filepath.Join(dir, "dangling"))
	link(t, "loop-b", filepath.Join(dir, "loop-a"))
	link(t, "loop-a", filepath.Join(dir, "loop-b"))
	link(t, "design.md", filepath.Join(dir, "design-link.md"))
	link(t, "real", filepath.Join(dir, "real-link"))
	link(t, "pic.png", filepath.Join(dir, "pic-link.png"))
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	link(t, "pipe", filepath.Join(dir, "pipe-link"))
	body := get(t, h, "/doc/reports/t1")
	for name, reason := range map[string]string{
		"escape":    "leads outside the fleet folder",
		"notes.md":  "leads outside the fleet folder",
		"db-link":   "points at a file the Board never shows",
		"dangling":  "points at nothing",
		"loop-a":    "cannot be read",
		"loop-b":    "cannot be read",
		"pipe-link": "points at something that is not a file or folder",
	} {
		want := `<span class="refused"><span class="badge" aria-hidden="true">LINK</span><span class="name">` + name + `</span><span class="meta">` + reason + `</span></span>`
		if !strings.Contains(body, want) {
			t.Errorf("row %s lacks %q", name, want)
		}
	}
	contains(t, "luvus", body, `<span class="name">luvus-link</span><span class="meta">points at a file the Board never shows</span>`)
	contains(t, "links that work", body,
		`<li><a href="/doc/reports/t1/design-link.md"><span class="badge" aria-hidden="true">LINK</span><span class="name">design-link.md</span>`,
		`<li><a href="/doc/reports/t1/real-link"><span class="badge" aria-hidden="true">LINK</span><span class="name">real-link/</span>`,
		`<li><a href="/doc/reports/t1/pic-link.png?view"><img src="/doc/reports/t1/pic-link.png" alt="" loading="lazy">`)
	lacks(t, "folder", body, outside, "secrets/", "id_ed25519", filepath.Join(home, "hand.db"), `href="/doc/reports/t1/escape"`, `href="/doc/reports/t1/loop-a"`, "luvus/", "gone.md\"")
	if n := strings.Count(body, `class="refused"`); n != 8 {
		t.Errorf("%d refused rows, want 9", n)
	}
}

func TestStepsSkipRefusedEntriesAndFolderLinks(t *testing.T) {
	outside := fleetFolder(t, map[string]string{"n.md": secret})
	h, home := viewer(t, map[string]string{"r/a.md": "a", "r/d.md": "d", "r/real/x.md": "x", "r/e.md": "e"})
	dir := filepath.Join(home, "r")
	link(t, filepath.Join(outside, "n.md"), filepath.Join(dir, "b-out.md"))
	link(t, "real", filepath.Join(dir, "b-real"))
	link(t, "gone", filepath.Join(dir, "c-gone"))
	link(t, "../hand.db", filepath.Join(dir, "c-db"))
	for rel, want := range map[string][]string{"r/a.md": {`<a href="/doc/r/d.md" rel="next"`}, "r/d.md": {`<a href="/doc/r/a.md" rel="prev"`, `<a href="/doc/r/e.md" rel="next"`}} {
		body := get(t, h, "/doc/"+rel)
		contains(t, rel, body, want...)
		lacks(t, rel, body, "b-out", "b-real", "c-gone", "c-db", outside)
	}
}

func TestAVeryLargeFolderStaysBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("creates 100,000 files")
	}
	h, home := viewer(t, map[string]string{"big/seed.txt": "x"})
	dir := filepath.Join(home, "big")
	for i := range 100000 {
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("frame-%06d.png", i)))
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	peak := func(path string) (uint64, int, time.Duration) {
		runtime.GC()
		var base runtime.MemStats
		runtime.ReadMemStats(&base)
		var top atomic.Uint64
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			var m runtime.MemStats
			for {
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > top.Load() {
					top.Store(m.HeapAlloc)
				}
				select {
				case <-stop:
					return
				case <-time.After(time.Millisecond):
				}
			}
		}()
		start := time.Now()
		rec := request(h, "GET", path, nil, true)
		took := time.Since(start)
		close(stop)
		<-done
		if rec.Code != 200 {
			t.Fatalf("%s = %d", path, rec.Code)
		}
		return max(top.Load(), base.HeapAlloc) - base.HeapAlloc, rec.Body.Len(), took
	}
	for _, path := range []string{"/doc/big", "/doc/big?page=200", "/doc/big/frame-050000.png", "/doc/big/frame-050000.png?view"} {
		grew, bytes, took := peak(path)
		t.Logf("%s: heap grew %.1f MiB, %.0f KiB of HTML, %v", path, float64(grew)/(1<<20), float64(bytes)/1024, took.Round(time.Millisecond))
		if grew > 40<<20 || bytes > 1<<20 {
			t.Errorf("%s: heap grew %d bytes for %d bytes of HTML", path, grew, bytes)
		}
	}
	contains(t, "page 200", get(t, h, "/doc/big?page=200"), "100,001 entries, page 200 of 201", "frame-099500.png", "frame-099999.png")
	contains(t, "last page", get(t, h, "/doc/big?page=201"), "page 201 of 201", "seed.txt")
}
