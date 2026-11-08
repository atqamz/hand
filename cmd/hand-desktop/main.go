package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "hand-desktop runs on Linux for now; the Board still works in a browser on this system.")
		os.Exit(2)
	}
	fleet := flag.String("fleet", "", "fleet id or name to open")
	flag.Parse()
	app := desktop.New(func() (desktop.ReadModel, error) { return desktop.NewLocal(os.Getenv) })
	app.Boot()
	if *fleet != "" {
		app.OpenFleet(*fleet)
	}
	title := "Hand"
	if app.Fleet.ID != "" && app.Fleet.Name != "" {
		title += " · " + app.Fleet.Name
	}
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title: title, Width: 1100, Height: 760, MinWidth: 780, MinHeight: 480,
			Content: ui.View(app.View),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
