package desktop

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

// ErrNoFleets says a start found no fleet to run in, so the window shows how
// to make one instead of a command's failure.
var ErrNoFleets = errors.New("no fleet on this computer yet")

var (
	boardStartTimeout = 20 * time.Second
	boardProbeTimeout = 2 * time.Second
)

// StartBoard returns the window's way to start a Board: it asks the board.addr
// the window holds whether anything still answers, and runs `hand board start`
// in home only when nothing does. It reads no token and prints none.
func StartBoard(getenv func(string) string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, home string) (string, error) {
		root, err := localRoot(getenv, runtime.GOOS)
		if err != nil {
			return err.Error(), err
		}
		if boardAnswers(ctx, root) {
			return "", nil
		}
		if home == "" {
			fleets, _, err := localFleets(root)
			if err != nil {
				return err.Error(), err
			}
			if len(fleets) == 0 {
				return "", ErrNoFleets
			}
			home = fleets[0].Home
		}
		bin, err := handBinary()
		if err != nil {
			return "`hand` was not found next to hand-desktop or on PATH; install it or add it to PATH", err
		}
		runCtx, cancel := context.WithTimeout(ctx, boardStartTimeout)
		defer cancel()
		cmd := exec.CommandContext(runCtx, bin, "--home", home, "board", "start")
		cmd.Dir = home
		cmd.WaitDelay = time.Second
		out, err := cmd.CombinedOutput()
		if err != nil {
			if runCtx.Err() != nil && ctx.Err() == nil {
				const line = "`hand board start` did not finish in time"
				return line, errors.New(line)
			}
			line := failureLine(out, err)
			return line, errors.New(line)
		}
		return "", nil
	}
}

// boardAnswers reports whether board.addr names a service that answers HTTP at
// all. The window sends no token: any answer, a refusal included, means the
// Board is there, and starting another one would take a port already in use.
func boardAnswers(ctx context.Context, root string) bool {
	base, err := localBoard(root)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, boardProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/meta", nil)
	if err != nil {
		return false
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return true
}

// TryStart runs the start again after the window said it did not start.
func (a *App) TryStart() {
	if a.Start == nil {
		a.Refresh()
		return
	}
	a.autoStarted, a.wantStart, a.StartErr = false, true, ""
	a.noFleets = false
	a.maybeStart()
}

func (a *App) maybeStart() {
	if a.Start == nil || !a.wantStart || a.Starting || a.Post == nil {
		return
	}
	home := a.Fleet.Home
	if home == "" && len(a.Fleets) > 0 {
		home = a.Fleets[0].Home
	}
	a.Starting, a.StartErr, a.wantStart = true, "", false
	start, post, ctx := a.Start, a.Post, a.Context
	go func() {
		line, err := start(ctx, home)
		post(func() {
			a.Starting, a.autoStarted = false, true
			switch {
			case errors.Is(err, ErrNoFleets):
				a.noFleets = true
			case err != nil:
				a.StartErr = line
			default:
				a.Refresh()
			}
		})
	}()
}

func (a *App) stateCard(c *ui.Context, k kit.Colors, p palette) {
	card := func(label string, tone ui.Color, meta, title, body, command, foot string, buttons func()) {
		ui.Column(c).MaxWidth(840).AlignSelf(ui.Center).Border(1, k.Line).Radius(kit.RadiusLG).
			Clip().Background(k.BG).Children(func() {
			ui.Row(c).Background(k.Panel).Height(kit.Row).Padding(0, 15).Gap(kit.Space10).
				AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).FontSize(kit.Meta.Size).Bold().TextColor(tone)
				ui.Text(c, meta).FontSize(kit.Meta.Size).TextColor(k.Muted)
			})
			ui.Column(c).Padding(15, 15, 16).Gap(kit.Space12).Children(func() {
				ui.Text(c, title).FontSize(kit.Title.Size).Bold()
				if body != "" {
					ui.Text(c, body).FontSize(kit.Body.Size)
				}
				if command != "" {
					ui.Text(c, command).Font("monospace").FontSize(kit.Body.Size)
				}
				if buttons != nil {
					ui.Row(c).Gap(kit.Space8).Children(buttons)
				}
				if foot != "" {
					ui.Text(c, foot).FontSize(kit.Meta.Size).TextColor(k.Muted)
				}
			})
		})
	}
	copyButton := func(key, label, text string) {
		if button(c, k, key, label, false) {
			c.WriteClipboard(text)
		}
	}
	switch {
	case a.Starting:
		card("Board", p.accent, "starting", "Starting the Board…",
			"No Board was running on this computer, so this window started one with hand board start. It keeps running after you close the window; closing the window never stops it.",
			"", "", nil)
	case a.StartErr != "":
		card("Board", p.fail, "did not start", "The Board did not start.",
			"This window ran hand board start and it stopped:", a.StartErr,
			"Nothing else was changed. The fleets and their workers keep running without the Board.",
			func() {
				if button(c, k, "start:again", "Try again", true) {
					a.TryStart()
				}
				copyButton("start:copy", "Copy the command", "hand board start")
			})
	case a.auth:
		card("Board", p.fail, "refused", "The Board refused this window.",
			"The Board answers, but it did not accept this fleet's token. Open the fleet once in a terminal, then try again:",
			"hand open", "The fleet and its workers keep running without this window.",
			func() {
				if button(c, k, "start:retry", "Try again", true) {
					a.Refresh()
				}
			})
	case a.noFleets:
		card("Fleets", k.Muted, "none yet", "No fleet on this computer yet.",
			"A fleet is a folder with its own supervisor. Make one in a terminal:",
			"cd ~/work && hand init", "Look again finds a fleet you just made.",
			func() {
				if button(c, k, "start:look", "Look again", true) {
					a.Refresh()
				}
				copyButton("fleets:copy", "Copy the command", "hand init")
			})
	default:
		a.errorCard(c, p)
	}
}

func button(c *ui.Context, k kit.Colors, key, label string, primary bool) bool {
	bg, fg, edge := k.BG, k.FG, k.LineStrong
	if primary {
		bg, fg, edge = k.Primary, k.OnPrimary, ui.Transparent
	}
	b := ui.ButtonBase(c.Key(key)).Height(kit.Control).Padding(0, kit.Space12).
		Radius(kit.RadiusMD).Background(bg).Border(1, edge).Justify(ui.Center).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label)
	if primary {
		b.Children(func() {
			ui.Text(c, label).FontSize(kit.Body.Size).TextColor(fg).Bold().SingleLine()
		})
	} else {
		b.Children(func() {
			ui.Text(c, label).FontSize(kit.Body.Size).TextColor(fg).SingleLine()
		})
	}
	return b.Clicked()
}
