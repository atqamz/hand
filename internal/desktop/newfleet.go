package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/atqamz/hand/internal/desktop/kit"
)

var initTimeout = 6 * time.Minute

type NewFleetRequest struct {
	Name, Folder string
	Fleets       []Fleet
	Made         *NewFleetResult
}

// NewFleetResult is what a try left behind: ID is set once `hand init`
// succeeded, Cmd is the command the operator can run by hand after a failure.
type NewFleetResult struct {
	ID, Folder, Warn, Cmd string
}

type fieldError string

func (e fieldError) Error() string { return string(e) }

// NewFleet returns the window's way to make a fleet before any Board can serve
// the step: it checks the form, runs `hand init`, then always `hand board
// start` in the new folder, because a new fleet has no board.token until that
// verb makes one, even when another Board already answers.
func NewFleet(getenv func(string) string) func(context.Context, NewFleetRequest) (NewFleetResult, error) {
	return func(ctx context.Context, req NewFleetRequest) (NewFleetResult, error) {
		var res NewFleetResult
		if req.Made != nil {
			res = *req.Made
		} else {
			name, dir, err := checkNewFleet(getenv, runtime.GOOS, req)
			if err != nil {
				return res, err
			}
			res.Cmd = shellJoin(runtime.GOOS, "hand", "init", "--name", name, dir)
			out, err := runHand(ctx, initTimeout, "", "init", "--name", name, dir)
			if err != nil {
				line := handLine("init", out, err)
				if errors.Is(err, errHandTimeout) {
					line += "; the folder may be half-made"
				}
				if errors.Is(err, errHandMissing) {
					res.Cmd = ""
				}
				return res, errors.New(line)
			}
			res.Folder = dir
			if res.ID = initID(out); res.ID == "" {
				return res, errors.New("`hand init` printed no fleet id")
			}
			res.Warn = luvusWarning(out)
		}
		res.Cmd = shellJoin(runtime.GOOS, "hand", "--home", res.Folder, "board", "start")
		out, err := runHand(ctx, boardStartTimeout, res.Folder, "--home", res.Folder, "board", "start")
		if err != nil {
			return res, errors.New("The fleet was made, but its Board did not start: " + handLine("board start", out, err))
		}
		res.Cmd = ""
		return res, nil
	}
}

func checkNewFleet(getenv func(string) string, goos string, req NewFleetRequest) (string, string, error) {
	name := strings.TrimSpace(req.Name)
	switch {
	case name == "":
		return "", "", fieldError("Name is required.")
	case len(name) > 64 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl):
		return "", "", fieldError("Name must be 1 to 64 bytes of printable text.")
	}
	for _, f := range req.Fleets {
		if f.Name == name {
			return "", "", fieldError(fmt.Sprintf("A fleet named %q already exists. Pick another name.", name))
		}
	}
	dir := expandHome(getenv, goos, strings.TrimSpace(req.Folder))
	if !filepath.IsAbs(dir) {
		return "", "", fieldError("Folder must be an absolute path.")
	}
	dir = filepath.Clean(dir)
	switch info, err := os.Stat(dir); {
	case err == nil && !info.IsDir():
		return "", "", fieldError("That folder is a file. Pick a new or empty folder.")
	case err == nil:
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", "", fieldError("That folder cannot be read: " + err.Error())
		}
		if len(entries) > 0 {
			return "", "", fieldError("That folder is not empty. Pick a new or empty folder.")
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", "", fieldError("That folder cannot be read: " + err.Error())
	}
	for up := filepath.Dir(dir); ; up = filepath.Dir(up) {
		if info, err := os.Stat(filepath.Join(up, "hand.db")); err == nil && info.Mode().IsRegular() {
			return "", "", fieldError("That folder is inside the fleet at " + up + ". Pick a folder outside it.")
		}
		if filepath.Dir(up) == up {
			break
		}
	}
	return name, dir, nil
}

func expandHome(getenv func(string) string, goos, path string) string {
	rest, ok := strings.CutPrefix(path, "~")
	if !ok || rest != "" && rest[0] != '/' && (goos != "windows" || rest[0] != '\\') {
		return path
	}
	home := getenv("HOME")
	if home == "" && goos == "windows" {
		home = getenv("USERPROFILE")
	}
	if home == "" {
		return path
	}
	return filepath.Join(home, rest)
}

func initID(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		if id, ok := strings.CutPrefix(strings.TrimSpace(line), "id: "); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

func luvusWarning(out []byte) string {
	text := string(out)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "luvus:") {
			return ""
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- ")); strings.Contains(line, "Luvus") {
			return line
		}
	}
	return "Luvus is not pinned; run `hand luvus pin`."
}

var plainArg = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./\\-]+$`)

func shellJoin(goos string, argv ...string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		switch {
		case plainArg.MatchString(a):
			parts[i] = a
		case goos == "windows":
			parts[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		default:
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}

func folderFor(name string) string {
	safe := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || unicode.IsControl(r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	if safe == "." || safe == ".." {
		safe = "-"
	}
	return "~/fleets/" + safe
}

type newFleetForm struct {
	name, folder, auto, err, cmd string
	busy, done                   bool
	made                         *NewFleetResult
}

func (a *App) submitNewFleet() {
	if a.NewFleet == nil || a.nf.busy || a.nf.done {
		return
	}
	req := NewFleetRequest{Name: a.nf.name, Folder: a.nf.folder, Fleets: a.Fleets, Made: a.nf.made}
	a.nf.busy, a.nf.err, a.nf.cmd = true, "", ""
	finish := func(res NewFleetResult, err error) {
		a.nf.busy = false
		switch {
		case err == nil:
			a.nf.done, a.nf.made = true, nil
			a.pendingFleet, a.pendingWarn = res.ID, res.Warn
			a.Refresh()
		default:
			a.nf.err, a.nf.cmd = err.Error(), res.Cmd
			a.nf.made = nil
			if res.ID != "" {
				a.nf.made = &res
			}
		}
	}
	run, ctx := a.NewFleet, a.Context
	if a.Post == nil {
		finish(run(ctx, req))
		return
	}
	post := a.Post
	go func() {
		res, err := run(ctx, req)
		post(func() { finish(res, err) })
	}()
}

func (a *App) newFleetFields(c *ui.Context, k kit.Colors, p palette) {
	if a.nf.folder == a.nf.auto {
		a.nf.folder = folderFor(a.nf.name)
		a.nf.auto = a.nf.folder
	}
	kit.FieldLabel(c, k, "Name")
	ui.TextInput(c, &a.nf.name).Placeholder("Fleet name").Label("Fleet name")
	kit.FieldLabel(c, k, "Folder")
	ui.TextInput(c, &a.nf.folder).Placeholder("Absolute folder").Label("Fleet folder")
	if a.nf.err == "" {
		return
	}
	ui.Text(c, a.nf.err).FontSize(kit.Body.Size).TextColor(p.fail)
	if a.nf.cmd != "" {
		ui.Text(c, "Run it yourself in a terminal:").FontSize(kit.Meta.Size).TextColor(k.Muted)
		ui.Text(c, a.nf.cmd).Font("monospace").FontSize(kit.Body.Size)
	}
}

func (a *App) newFleetLabel() string {
	switch {
	case a.nf.busy:
		return "Creating…"
	case a.nf.done:
		return "Opening…"
	case a.nf.made != nil:
		return "Start the Board"
	}
	return "Create fleet"
}
