//go:build e2e

package main

import (
	"io"
	"os"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/plugins/automation"
)

func init() {
	onlyLinux = false
	if os.Getenv("MYGO_AUTOMATION") == "" {
		return
	}
	// The automation protocol has no quit; a runner ends the window by closing its stdin.
	mygo.App.WhenReady(func() {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			mygo.App.Quit()
		}()
	})
}
