//go:build unix

package board_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnHTMLPreviewDoesNotInlineAnImageBehindASymlinkOut(t *testing.T) {
	outside := fleetFolder(t, map[string]string{"x.png": string(flowPNG)})
	html := `<img src="out.png"><img src="dir/x.png"><img src="in.png">`
	h, home := viewer(t, map[string]string{"r/page.html": html, "r/real.png": string(flowPNG)})
	for link, target := range map[string]string{"r/out.png": filepath.Join(outside, "x.png"), "r/dir": outside, "r/in.png": "real.png"} {
		if err := os.Symlink(target, filepath.Join(home, link)); err != nil {
			t.Fatal(err)
		}
	}
	got := request(h, "GET", "/doc/r/page.html?frame", nil, true).Body.String()
	if !strings.HasPrefix(got, `<img src="out.png"><img src="dir/x.png"><img src="data:image/png;base64,`) {
		t.Errorf("body = %.300s", got)
	}
}
