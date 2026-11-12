package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strconv"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type options struct {
	version bool
	fleet   string
	open    string
}

func parseArgs(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("hand-desktop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&o.version, "version", false, "print the version and commit, then exit")
	fs.StringVar(&o.fleet, "fleet", "", "fleet id or name to open")
	fs.StringVar(&o.open, "open", "", "open link: handboard://open/<fleet id>/[task/tN|decision/dN|attempt/aN]")
	if fs.Parse(args) != nil {
		return o, errors.New("unexpected flag; hand-desktop takes --open LINK, --fleet NAME and --version")
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument %s", clip(fs.Arg(0)))
	}
	if o.open != "" {
		if _, err := desktop.ParseOpenLink(o.open); err != nil {
			return o, fmt.Errorf("--open %s: %w", clip(o.open), err)
		}
	}
	return o, nil
}

func clip(s string) string {
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return strconv.Quote(s)
}

func handoff(app *desktop.App, args []string) {
	o, err := parseArgs(args)
	if err != nil {
		app.Fail(err.Error())
		return
	}
	if o.open != "" {
		_ = app.OpenLink(o.open)
	}
}

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "hand-desktop:", err)
		os.Exit(2)
	}
	if opts.version {
		fmt.Println(desktopasset.Stamp())
		return
	}
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "hand-desktop runs on Linux for now; the Board still works in a browser on this system.")
		os.Exit(2)
	}
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	app := desktop.New(func() (desktop.ReadModel, error) { return desktop.NewLocal(os.Getenv) })
	app.DraftDir = desktop.DraftDir(os.Getenv)
	app.Boot()
	if opts.fleet != "" {
		app.OpenFleet(opts.fleet)
	}
	var win *mygo.Window
	var early [][]string
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		if win == nil {
			early = append(early, args)
			return
		}
		win.Update(func() { handoff(app, args) })
		win.Restore()
		win.Focus()
	})
	if opts.open != "" {
		if err := app.OpenLink(opts.open); err != nil && app.Model != nil {
			fmt.Fprintln(os.Stderr, "hand-desktop:", err)
			os.Exit(2)
		}
	}
	mygo.App.WhenReady(func() {
		win = mygo.NewWindow(mygo.WindowOptions{
			Title: windowTitle(app.Fleet), Width: 1100, Height: 760, MinWidth: 780, MinHeight: 480,
			Content: ui.View(app.View),
		})
		app.Post = win.Update
		app.OnFleet = func(f desktop.Fleet) { setFleetTitle(win.SetTitle, f) }
		win.OnFocus(func() { app.SetFocused(true) })
		win.OnBlur(func() { app.SetFocused(false) })
		win.OnMinimize(func() { app.SetMinimized(true) })
		win.OnRestore(func() { app.SetMinimized(false) })
		win.OnClosed(func() { app.Close() })
		for _, args := range early {
			handoff(app, args)
		}
		app.StartRefresh()
	})
	err = mygo.App.Run()
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
