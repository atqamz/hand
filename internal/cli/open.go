package cli

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/toon"
)

const launchWait = 2 * time.Second

const (
	openUsage = "usage: hand open [supervisor | tN | dN | rN | aN] [--pr]"
	boardHint = "start it with `hand board`, or `systemctl --user start secondhand-board`"
)

func init() {
	commands["open"] = cmdOpen
}

func cmdOpen(r *runner, args []string) error {
	set := flags("open")
	pr := set.Bool("pr", false, "open the task's newest PR link instead of its board page")
	printed := set.Bool("print", false, "print the link instead of opening it; a board link holds the fleet's token")
	if err := set.Parse(args); err != nil {
		return usageError{fmt.Sprintf("open: %v; %s", err, openUsage)}
	}
	rest := set.Args()
	if len(rest) > 0 {
		if err := set.Parse(rest[1:]); err != nil {
			return usageError{fmt.Sprintf("open: %v; %s", err, openUsage)}
		}
		rest = append(rest[:1:1], set.Args()...)
	}
	if len(rest) > 1 {
		return usageError{openUsage}
	}
	ref := strings.Join(rest, "")
	st, err := r.store()
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := r.ctx()
	if *pr {
		link, err := newestPR(ctx, st, ref)
		if err != nil {
			return err
		}
		if *printed {
			return r.printLink(link, "")
		}
		return r.launch(link, link, "open "+link+" yourself")
	}
	path, frag, err := boardPage(ctx, st, ref)
	if err != nil {
		return err
	}
	token, err := boardToken(r.home)
	if err != nil {
		return err
	}
	addr, err := boardAddr(ctx, r.root, r.fleet.ID, token)
	if err != nil {
		return err
	}
	page := "http://" + addr + "/" + r.fleet.ID + path
	if *printed {
		return r.printLink(page+"?token="+token+frag, "The link holds the fleet's token; paste it only into your own browser")
	}
	return r.launch(page+"?token="+token+frag, page+frag, "open "+page+frag+" yourself and log in with the token in "+filepath.Join(r.home, "board.token"))
}

func boardPage(ctx context.Context, st *state.Store, ref string) (string, string, error) {
	if ref == "" || ref == "supervisor" {
		return "/", "", nil
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
		a, err := st.Attempt(ctx, id)
		if err != nil {
			return "", "", err
		}
		return "/task/" + state.TaskRef(a.TaskID), "#" + state.AttemptRef(id), nil
	}
}

func newestPR(ctx context.Context, st *state.Store, ref string) (string, error) {
	if !strings.HasPrefix(ref, "t") {
		return "", usageError{"open: --pr needs a task, as in `hand open t3 --pr`"}
	}
	id, err := parseID("t", ref)
	if err != nil {
		return "", err
	}
	if _, err := st.Task(ctx, id); err != nil {
		return "", err
	}
	reports, err := st.Reports(ctx, state.ReportFilter{TaskID: id}, 500)
	if err != nil {
		return "", err
	}
	for i := len(reports) - 1; i >= 0; i-- {
		if links := board.PRLinks(reports[i].Body); len(links) > 0 {
			return links[len(links)-1], nil
		}
	}
	return "", fmt.Errorf("%w: %s has no PR link in its reports", state.ErrNotFound, ref)
}

func boardAddr(ctx context.Context, root, id, token string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, "board.addr"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: no board is running; %s", state.ErrNotFound, boardHint)
	}
	if err != nil {
		return "", err
	}
	addr := strings.TrimSpace(string(b))
	nonce := rand.Text()
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
	if err != nil || !hmac.Equal([]byte(proof), []byte(board.Proof(token, nonce))) {
		return "", fmt.Errorf("%w: no board for this fleet answers at %s; %s", state.ErrNotFound, addr, boardHint)
	}
	return addr, nil
}

func (r *runner) launch(url, shown, yourself string) error {
	bin, err := harness.LookPath("xdg-open", r.env.Getenv("PATH"))
	if err != nil {
		return fmt.Errorf("%w: no xdg-open on PATH; %s", state.ErrNotFound, yourself)
	}
	cmd := exec.Command(bin, url)
	cmd.Env = r.env.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("xdg-open could not start; %s", yourself)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("xdg-open could not open %s (exit %d); %s", shown, exit.ExitCode(), yourself)
		}
		if err != nil {
			return fmt.Errorf("xdg-open could not open %s; %s", shown, yourself)
		}
	case <-time.After(launchWait):
	}
	var d toon.Doc
	d.Field("opened", shown)
	return r.print(&d)
}

func (r *runner) printLink(link, help string) error {
	var d toon.Doc
	d.Field("url", link)
	if help != "" {
		d.Help(help)
	}
	return r.print(&d)
}
