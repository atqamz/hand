//go:build e2e

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/webhost"
)

func init() {
	if os.Getenv("HAND_WEBPROBE_URL") != "" {
		probe = runProbe
	}
}

func runProbe() {
	f := desktop.DocFrame{
		Kind: os.Getenv("HAND_WEBPROBE_KIND"),
		Host: "probe",
		URL:  os.Getenv("HAND_WEBPROBE_URL"),
		Cookie: desktop.WebCookie{
			Name:   os.Getenv("HAND_WEBPROBE_COOKIE"),
			Value:  os.Getenv("HAND_WEBPROBE_TOKEN"),
			Domain: os.Getenv("HAND_WEBPROBE_DOMAIN"),
		},
	}
	mygo.App.WhenReady(func() {
		var handle desktop.WebHandle
		win := mygo.NewWindow(mygo.WindowOptions{Title: "probe", Width: 900, Height: 700, Content: ui.View(func(c *ui.Context) {
			if handle.View != nil {
				ui.WebView(c, handle.View).Grow(1)
			}
		})})
		var err error
		handle, err = (&webhost.Host{Engine: newMygoEngine(win)}).Open(f)
		fmt.Println("open error:", err)
		win.Update(func() {})
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Println("run error:", err)
	}
}
