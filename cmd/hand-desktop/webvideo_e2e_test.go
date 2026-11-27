//go:build e2e

package main

import (
	"image"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func needVideoTools(t *testing.T) string {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "gst-inspect-1.0", "import", "xdotool"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	for _, el := range []string{"qtdemux", "avdec_h264"} {
		if err := exec.Command("gst-inspect-1.0", "--exists", el).Run(); err != nil {
			t.Skipf("the GStreamer element %s is missing (gst-plugins-base, gst-plugins-good and gst-libav): a video cannot play", el)
		}
	}
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	if b, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=320x240:rate=15", "-t", "20",
		"-an", "-pix_fmt", "yuv420p", "-c:v", "libx264", "-movflags", "+faststart", clip).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot make the fixture: %v\n%s", err, b)
	}
	return clip
}

func videoServer(t *testing.T, clip string) (*probeLog, string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	log := &probeLog{}
	go func() {
		_ = http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.add("board", r)
			if !strings.Contains(r.Header.Get("Cookie"), probeCookie+"="+probeToken) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if !strings.HasPrefix(r.URL.Path, "/f0/api/v1/doc/clip.mp4") {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			f, err := os.Open(clip)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			defer f.Close()
			st, _ := f.Stat()
			h := w.Header()
			h.Set("Content-Type", "video/mp4")
			h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
			http.ServeContent(w, r, "clip.mp4", st.ModTime(), f)
		}))
	}()
	return log, "http://" + l.Addr().String()
}

func videoDiff(a, b image.Image, box probeBox) float64 {
	var sum, n float64
	for y := box.y; y < box.y+box.h*55/100; y += 3 {
		for x := box.x; x < box.x+box.w; x += 3 {
			r1, g1, b1, _ := a.At(x, y).RGBA()
			r2, g2, b2, _ := b.At(x, y).RGBA()
			for _, d := range []int64{int64(r1) - int64(r2), int64(g1) - int64(g2), int64(b1) - int64(b2)} {
				if d < 0 {
					d = -d
				}
				sum += float64(d) / 65535
				n++
			}
		}
	}
	return sum / n
}

func videoColoured(img image.Image, box probeBox) int {
	n := 0
	for y := box.y; y < box.y+box.h*55/100; y += 2 {
		for x := box.x; x < box.x+box.w; x += 2 {
			r, g, b, _ := img.At(x, y).RGBA()
			hi, lo := max(r, g, b), min(r, g, b)
			if hi-lo > 0x8000 {
				n++
			}
		}
	}
	return n
}

const videoStill = 0.003

func TestRealWebViewPlaysAVideoFromTheWrapperPage(t *testing.T) {
	clip := needVideoTools(t)
	bin := probeBinary(t)
	log, board := videoServer(t, clip)
	startProbe(t, bin, board+"/f0/api/v1/doc/clip.mp4?raw", "HAND_WEBPROBE_KIND=video")
	until(t, "the video request", 90*time.Second, func() bool { return log.saw("board", "/f0/api/v1/doc/clip.mp4") })
	g := probeGeometry(t)
	dir := t.TempDir()
	snap := func() image.Image { return screenshot(t, filepath.Join(dir, "screen.png")) }

	var first image.Image
	until(t, "the first frame drawn", 60*time.Second, func() bool {
		first = snap()
		return videoColoured(first, g) >= renderMin
	})
	still := func(what string) image.Image {
		t.Helper()
		before := snap()
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
			if d := videoDiff(before, snap(), g); d > videoStill {
				t.Fatalf("the picture moves %s: mean difference %.4f", what, d)
			}
		}
		return before
	}
	paused := still("before any click: the page autoplays")

	click := func() {
		t.Helper()
		x, y := strconv.Itoa(g.x+g.w/2), strconv.Itoa(g.y+g.h*3/10)
		if b, err := exec.Command("xdotool", "mousemove", x, y, "click", "1").CombinedOutput(); err != nil {
			t.Fatalf("xdotool: %v\n%s", err, b)
		}
	}
	click()
	until(t, "the picture to move after a click", 30*time.Second, func() bool { return videoDiff(paused, snap(), g) > 0.01 })
	prev, moved := snap(), 0
	for end := time.Now().Add(10 * time.Second); moved < 3 && time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		cur := snap()
		if videoDiff(prev, cur, g) > 0.01 {
			moved++
		}
		prev = cur
	}
	if moved < 3 {
		t.Fatalf("the picture changed %d times in 10 s after the click, want playback to continue", moved)
	}
	click()
	time.Sleep(time.Second)
	still("after a second click: the video does not pause")

	checkNoContextMenu(t, dir, g.x+g.w/4, g.y+g.h/8)

	for _, h := range log.all() {
		t.Logf("%s %s %s cookie=%t", h.server, h.method, h.uri, strings.Contains(h.cookie, probeToken))
		if !strings.HasPrefix(h.uri, "/f0/api/v1/doc/clip.mp4") {
			t.Errorf("the view asked the Board for %s", h.uri)
		}
		if !strings.Contains(h.cookie, probeCookie+"="+probeToken) {
			t.Errorf("a request for %s carries no session cookie", h.uri)
		}
	}
}
