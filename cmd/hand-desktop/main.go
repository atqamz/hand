package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	showVersion := flag.Bool("version", false, "print the version and commit, then exit")
	fleet := flag.String("fleet", "", "fleet id or name to open")
	flag.Parse()
	if *showVersion {
		fmt.Println(desktopasset.Stamp())
		return
	}
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "hand-desktop runs on Linux for now; the Board still works in a browser on this system.")
		os.Exit(2)
	}
	app := desktop.New(func() (desktop.ReadModel, error) { return desktop.NewLocal(os.Getenv) })
	app.DraftDir = desktop.DraftDir(os.Getenv)
	app.Boot()
	if *fleet != "" {
		app.OpenFleet(*fleet)
	}
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title: windowTitle(app.Fleet), Width: 1100, Height: 760, MinWidth: 780, MinHeight: 480,
			Content: ui.View(app.View),
		})
		app.Post = win.Update
		app.OnFleet = func(f desktop.Fleet) { setFleetTitle(win.SetTitle, f) }
		win.OnFocus(func() { app.SetFocused(true) })
		win.OnBlur(func() { app.SetFocused(false) })
		win.OnClosed(func() { app.Close() })
		app.StartRefresh()
	})
	err := mygo.App.Run()
	app.FlushDrafts()
	if err != nil {
		log.Fatal(err)
	}
}

func setFleetTitle(set func(string), f desktop.Fleet) {
	set(windowTitle(f))
}

func windowTitle(f desktop.Fleet) string {
	if f.ID != "" && f.Name != "" {
		return "Hand · " + f.Name
	}
	return "Hand"
}
