package cli

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/proc"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
	"github.com/atqamz/hand/internal/viewlink"
)

var launchWait = 2 * time.Second

const openUsage = "usage: hand open [supervisor | companion | phone | tasks [--] [QUERY] | tN | dN | rN | aN] [--delivered] | hand open --doc PATH"

func init() {
	commands["open"] = cmdOpen
}

func cmdOpen(r *runner, args []string) error {
	if slices.ContainsFunc(args, func(a string) bool { return a == "--pr" || a == "-pr" }) {
		return usageError{"open: --pr is gone; use --delivered, as in `hand open t3 --delivered`"}
	}
	set := flags("open")
	delivered := set.Bool("delivered", false, "open the task's newest deliverable instead of its page")
	doc := set.String("doc", "", "open a document of the fleet folder in the Hand window, as in `hand open --doc reports/t1/design.md`")
	printed := set.Bool("print", false, "print the link instead of opening it")
	if err := set.Parse(args); err != nil {
		return helpOr(set, err, usageError{fmt.Sprintf("open: %v; %s", err, openUsage)})
	}
	rest := set.Args()
	if len(rest) > 0 {
		if err := set.Parse(rest[1:]); err != nil {
			return helpOr(set, err, usageError{fmt.Sprintf("open: %v; %s", err, openUsage)})
		}
		rest = append(rest[:1:1], set.Args()...)
	}
	if len(rest) > 2 && rest[0] == "tasks" {
		if err := set.Parse(rest[2:]); err != nil {
			return helpOr(set, err, usageError{fmt.Sprintf("open: %v; %s", err, openUsage)})
		}
		rest = append(rest[:2:2], set.Args()...)
	}
	query := ""
	if len(rest) == 2 && rest[0] == "tasks" {
		query, rest = strings.TrimSpace(rest[1]), rest[:1]
	}
	if len(rest) > 1 {
		return usageError{openUsage}
	}
	ref := strings.Join(rest, "")
	docSet := false
	set.Visit(func(f *flag.Flag) { docSet = docSet || f.Name == "doc" })
	if docSet && (ref != "" || *delivered) {
		return usageError{"open: --doc takes a path alone; " + openUsage}
	}
	if ref == "screen" {
		return usageError{"open: screen is gone; use `hand attach` for the supervisor's terminal"}
	}
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := r.ctx()
	link := viewlink.Link{Fleet: r.fleet.ID}
	if docSet {
		rel, err := docRel(*doc, func() (string, error) { return board.DocPage(r.home, *doc) })
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%w: %s", state.ErrNotFound, *doc)
			}
			return usageError{fmt.Sprintf("open: --doc %s: %v", *doc, err)}
		}
		link.Page, link.Path = viewlink.Doc, rel
	} else if *delivered {
		d, err := newestDeliverable(ctx, st, ref)
		if err != nil {
			return err
		}
		if d.Kind == "file" {
			if href := board.DocHref(r.home, "", d.Ref); href != "" {
				rel, err := docRel(d.Ref, func() (string, error) { return href, nil })
				if err != nil {
					return err
				}
				link.Page, link.Path = viewlink.Doc, rel
			}
		}
		if link.Page != viewlink.Doc {
			if *printed {
				if d.Kind == "file" {
					var out toon.Doc
					out.Field("path", d.Ref)
					return r.print(&out)
				}
				return r.printLink(d.Ref, "", "")
			}
			if strings.HasPrefix(d.Ref, `\\`) {
				return fmt.Errorf("%w: UNC paths are not opened, because Windows would connect to the host they name: %s", state.ErrConflict, d.Ref)
			}
			target := d.Ref
			if d.Kind == "file" {
				target = folderOf(d.Ref)
			}
			return r.launch(target, target, "open "+target+" yourself")
		}
	} else if link, err = openLink(ctx, st, r.fleet.ID, ref, query); err != nil {
		return err
	}
	text, err := viewlink.Format(link)
	if err != nil {
		return usageError{fmt.Sprintf("open: no link for this target: %v", err)}
	}
	if *printed {
		var d toon.Doc
		d.Field("link", text)
		return r.print(&d)
	}
	return r.openDesktop(ctx, text)
}

func docRel(shown string, page func() (string, error)) (string, error) {
	href, err := page()
	if err != nil {
		return "", err
	}
	rel, ok := strings.CutPrefix(href, "/doc/")
	if ok {
		rel, err = url.PathUnescape(rel)
	}
	if !ok || err != nil || rel == "" {
		return "", usageError{fmt.Sprintf("open: %s is not a document of the fleet folder", shown)}
	}
	return rel, nil
}

func openLink(ctx context.Context, st *state.Store, fleet, ref, query string) (viewlink.Link, error) {
	l := viewlink.Link{Fleet: fleet}
	switch ref {
	case "", "supervisor":
		return l, nil
	case "companion", "phone":
		l.Page = viewlink.Companion
		return l, nil
	case "tasks":
		l.Page, l.Query = viewlink.Tasks, query
		return l, nil
	}
	page, frag, err := boardPage(ctx, st, ref)
	if err != nil {
		return l, err
	}
	l.Page = viewlink.Item
	switch {
	case strings.HasPrefix(page, "/task/"):
		l.Ref = strings.TrimPrefix(page, "/task/")
		l.Report = strings.TrimPrefix(frag, "#")
	case strings.HasPrefix(page, "/decision/"):
		l.Ref = strings.TrimPrefix(page, "/decision/")
	default:
		l.Ref = strings.TrimPrefix(page, "/attempt/")
	}
	return l, nil
}

var desktopUnsupported = func() string {
	if desktopasset.Supported(runtime.GOOS, runtime.GOARCH, Channel) {
		return ""
	}
	return desktopasset.Unsupported(runtime.GOOS, runtime.GOARCH)
}

func (r *runner) openDesktop(ctx context.Context, link string) error {
	if why := desktopUnsupported(); why != "" {
		return fmt.Errorf("the Hand window cannot open this: %s; link: %s", why, link)
	}
	token, err := boardToken(r.home)
	if err != nil {
		return err
	}
	if err := r.ensureBoard(ctx, token); err != nil {
		return err
	}
	bin, err := desktopasset.Locate(selfPath(), r.env.Getenv("PATH"), runtime.GOOS, r.env.Getenv("HOME"))
	if err != nil {
		return fmt.Errorf("the Hand window cannot open this: %v; link: %s", err, link)
	}
	return r.start(bin, []string{"--open", link}, link, "hand-desktop", "run hand-desktop --open "+link+" yourself")
}

func boardPage(ctx context.Context, st *state.Store, ref string) (string, string, error) {
	if ref == "" || ref == "supervisor" {
		return "/", "", nil
	}
	switch ref {
	case "screen":
		return "/supervisor", "", nil
	case "companion", "phone":
		return "/connect", "", nil
	case "tasks":
		return "/tasks", "", nil
	}
	prefix := ref[:1]
	if !strings.Contains("tdra", prefix) {
		return "", "", usageError{"open: " + ref + " is not a ref; " + openUsage}
	}
	id, err := parseID(prefix, ref)
	if err != nil {
		return "", "", err
	}
	switch prefix {
	case "t":
		if _, err := st.Task(ctx, id); err != nil {
			return "", "", err
		}
		return "/task/" + state.TaskRef(id), "", nil
	case "d":
		if _, err := st.Decision(ctx, id); err != nil {
			return "", "", err
		}
		return "/decision/" + state.DecisionRef(id), "", nil
	case "r":
		rep, err := st.Report(ctx, id)
		if err != nil {
			return "", "", err
		}
		return "/task/" + state.TaskRef(rep.TaskID), "#" + state.ReportRef(id), nil
	default:
		if _, err := st.Attempt(ctx, id); err != nil {
			return "", "", err
		}
		return "/attempt/" + state.AttemptRef(id), "", nil
	}
}

func newestDeliverable(ctx context.Context, st *state.Store, ref string) (board.Deliverable, error) {
	if !strings.HasPrefix(ref, "t") {
		return board.Deliverable{}, usageError{"open: --delivered needs a task, as in `hand open t3 --delivered`"}
	}
	id, err := parseID("t", ref)
	if err != nil {
		return board.Deliverable{}, err
	}
	if _, err := st.Task(ctx, id); err != nil {
		return board.Deliverable{}, err
	}
	reports, err := st.Reports(ctx, state.ReportFilter{TaskID: id}, 500)
	if err != nil {
		return board.Deliverable{}, err
	}
	events, err := st.DeliveredEvents(ctx, id)
	if err != nil {
		return board.Deliverable{}, err
	}
	if found := board.Newest(reports, events); len(found) > 0 {
		return found[0], nil
	}
	return board.Deliverable{}, fmt.Errorf("%w: %s has no deliverable", state.ErrNotFound, ref)
}

func folderOf(path string) string {
	trimmed := strings.TrimRight(path, `/\`)
	i := strings.LastIndexAny(trimmed, `/\`)
	if i < 0 {
		return path
	}
	if info, err := os.Stat(trimmed); err == nil && info.IsDir() {
		return trimmed
	}
	if parent := trimmed[:i]; parent != "" && !strings.HasSuffix(parent, ":") {
		return parent
	}
	return trimmed[:i+1]
}

func boardAddr(ctx context.Context, root, id, token string) (string, error) {
	nonce := rand.Text()
	addr, proof, err := askBoard(ctx, root, id, nonce)
	if err != nil {
		return "", err
	}
	if !hmac.Equal([]byte(proof), []byte(board.Proof(token, nonce))) {
		return "", fmt.Errorf("%w: no board for this fleet answers at %s; %s", state.ErrConflict, addr, boardHint)
	}
	return addr, nil
}

func askBoard(ctx context.Context, root, id, nonce string) (string, string, error) {
	b, err := os.ReadFile(filepath.Join(root, "board.addr"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", fmt.Errorf("%w: no board is running; %s", state.ErrNotFound, boardHint)
	}
	if err != nil {
		return "", "", err
	}
	addr := strings.TrimSpace(string(b))
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	proof := ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/"+id+"/proof?nonce="+nonce, nil)
	if err == nil {
		var res *http.Response
		if res, err = http.DefaultClient.Do(req); err == nil {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 256))
			_ = res.Body.Close()
			proof = string(body)
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("%w: no board for this fleet answers at %s; %s", state.ErrNotFound, addr, boardHint)
	}
	return addr, proof, nil
}

func (r *runner) launch(url, shown, yourself string) error {
	opener := openerArgv[0]
	bin, err := harness.LookPath(opener, r.env.Getenv("PATH"))
	if err != nil {
		return fmt.Errorf("%w: no %s on PATH; %s", state.ErrNotFound, opener, yourself)
	}
	return r.start(bin, append(slices.Clone(openerArgv[1:]), url), shown, opener, yourself)
}

func (r *runner) start(bin string, argv []string, shown, opener, yourself string) error {
	cmd := exec.Command(bin, argv...)
	cmd.Env = r.env.Environ()
	proc.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s could not start; %s", opener, yourself)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("%s could not open %s (exit %d); %s", opener, shown, exit.ExitCode(), yourself)
		}
		if err != nil {
			return fmt.Errorf("%s could not open %s; %s", opener, shown, yourself)
		}
	case <-time.After(launchWait):
	}
	var d toon.Doc
	d.Field("opened", shown)
	return r.print(&d)
}

func appLink(fleet, path, frag string) string {
	return "handboard://open/" + fleet + path + frag
}

func (r *runner) printLink(link, app, help string) error {
	var d toon.Doc
	d.Field("url", link)
	if app != "" {
		d.Field("app", app)
	}
	if help != "" {
		d.Help(help)
	}
	return r.print(&d)
}
