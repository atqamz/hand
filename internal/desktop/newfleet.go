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

type NewFleetResult struct {
	ID, Folder, Warn, Cmd string
}

type fieldError string

func (e fieldError) Error() string { return string(e) }

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
		res.Cmd = boardStartCommand(res.Folder)
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
	if why := unquotable(goos, name); why != "" {
		return "", "", fieldError("Name " + why)
	}
	for _, f := range req.Fleets {
		if strings.EqualFold(f.Name, name) {
			return "", "", fieldError(fmt.Sprintf("A fleet named %q already exists. Pick another name.", f.Name))
		}
	}
	dir := expandHome(getenv, goos, strings.TrimSpace(req.Folder))
	if !filepath.IsAbs(dir) {
		return "", "", fieldError("Folder must be an absolute path.")
	}
	dir = filepath.Clean(dir)
	if why := unquotable(goos, dir); why != "" {
		return "", "", fieldError("Folder " + why)
	}
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

var plainArg = regexp.MustCompile(`^[A-Za-z0-9_./:-]+$`)

var powerShellQuote = strings.NewReplacer("'", "''", "\u2018", "\u2018\u2018", "\u2019", "\u2019\u2019", "\u201a", "\u201a\u201a", "\u201b", "\u201b\u201b")

func unquotable(goos, arg string) string {
	if goos == "windows" && (strings.Contains(arg, `"`) || strings.HasSuffix(arg, `\`)) {
		return "cannot contain a double quote or end in a backslash: the command shown for PowerShell could not be quoted safely."
	}
	return ""
}

func shellJoin(goos string, argv ...string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		switch {
		case plainArg.MatchString(a):
			parts[i] = a
		case goos == "windows":
			parts[i] = "'" + powerShellQuote.Replace(a) + "'"
		default:
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}

func boardStartCommand(folder string) string {
	return shellJoin(runtime.GOOS, "hand", "--home", folder, "board", "start")
}

func terminalName(goos string) string {
	if goos == "windows" {
		return "PowerShell"
	}
	return "a terminal"
}

func folderFor(parent, name string) string {
	safe := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || unicode.IsControl(r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	if safe == "." || safe == ".." {
		safe = "-"
	}
	if parent == "" {
		return "~/fleets/" + safe
	}
	return filepath.Join(parent, safe)
}

type newFleetForm struct {
	name, folder, auto, parent, err, cmd string
	busy, done, picking                  bool
	made                                 *NewFleetResult
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
			a.nf.done, a.nf.made = true, &res
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
		a.nf.folder = folderFor(a.nf.parent, a.nf.name)
		a.nf.auto = a.nf.folder
	}
	kit.FieldLabel(c, k, "Name")
	submit := ui.TextInput(c, &a.nf.name).Placeholder("Fleet name").Label("Fleet name").Submitted()
	kit.FieldLabel(c, k, "Folder")
	ui.Row(c).Gap(kit.Space8).Children(func() {
		submit = ui.TextInput(c, &a.nf.folder).Placeholder("Absolute folder").Label("Fleet folder").Grow(1).Submitted() || submit
		if a.PickFolder != nil && button(c, k, "nf:choose", "Choose...", false) {
			a.chooseFolder()
		}
	})
	if submit {
		a.submitNewFleet()
	}
	if a.nf.err == "" {
		return
	}
	ui.Text(c, a.nf.err).FontSize(kit.Body.Size).TextColor(p.fail)
	if a.nf.cmd != "" {
		ui.Text(c, "Run it yourself in "+terminalName(runtime.GOOS)+":").FontSize(kit.Meta.Size).TextColor(k.Muted)
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

func (a *App) newFleetUnlisted() {
	if !a.nf.done {
		return
	}
	a.nf.done = false
	a.nf.err = "The fleet was made in " + a.nf.made.Folder + ", but this window does not list it yet. Start the Board to look again."
	a.nf.cmd = boardStartCommand(a.nf.made.Folder)
}

func (a *App) formShown() bool { return a.nfOpen || a.noFleets || a.rfOpen }

func (a *App) resetNewFleet() {
	a.nfGen++
	a.nf = newFleetForm{}
}

func (a *App) openNewFleet() {
	if a.noFleets || a.nfOpen {
		return
	}
	if !a.nf.busy {
		a.resetNewFleet()
	}
	a.rfOpen = false
	a.nfOpen = true
}

func (a *App) closeNewFleet() {
	if a.nf.busy {
		return
	}
	a.nfOpen = false
	a.resetNewFleet()
}

func (a *App) chooseFolder() {
	if a.nf.busy || a.nf.done || a.nf.picking {
		return
	}
	a.nf.picking = true
	gen, pick := a.nfGen, a.PickFolder
	a.offload(func() func() {
		dir := pick()
		return func() {
			if gen != a.nfGen {
				return
			}
			a.nf.picking = false
			if dir == "" || !a.formShown() || a.nf.busy || a.nf.done {
				return
			}
			a.nf.parent = dir
			a.nf.folder = folderFor(dir, a.nf.name)
			a.nf.auto = a.nf.folder
		}
	})
}
