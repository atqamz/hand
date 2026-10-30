package cli_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
)

var handRepo, _ = filepath.Abs("/home/me/hand")

func exe(dir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

func fakeBehaviors() map[string]func([]string) int {
	return map[string]func([]string) int{"args": fakebin.RecordArgs, "fake": fakeMain, "lister": listerMain, "ocexport": ocexportMain, "ocapi": ocapiMain, "ocsession": ocsessionMain, "wrap": wrapMain, "die": func([]string) int { fmt.Fprint(os.Stderr, "board cannot start\n"); return 1 }, "reporter": reporterMain, "idle": idleMain, "notifyenv": notifyEnvMain, "tailscale": tailscaleMain, "opener": func(args []string) int {
		fakebin.Append(os.Getenv("XDG_LOG"), args[len(args)-1])
		return 0
	}}
}

func ocexportMain(args []string) int {
	p := fakebin.Params()
	if len(args) > 1 && args[1] == "list" {
		fakebin.Append(p["count"], "list")
		fmt.Print(p["list"])
		return 0
	}
	if args[0] == "api" {
		fakebin.Append(p["count"], "api")
		if p["api"] == "" {
			fmt.Fprintln(os.Stderr, "no api")
			return 1
		}
		fmt.Print(p["api"])
		return 0
	}
	fakebin.Append(p["count"], "export")
	if p["ids"] != "" && !slices.Contains(strings.Split(p["ids"], ","), args[len(args)-1]) {
		fmt.Fprintln(os.Stderr, "no such session")
		return 1
	}
	fmt.Print(p["export"])
	return 0
}

func ocapiMain(args []string) int {
	p := fakebin.Params()
	switch {
	case len(args) == 1 && args[0] == "--version":
		fmt.Println("opencode 9.9")
	case len(args) > 0 && args[0] == "models":
		fmt.Print(p["models"])
	case len(args) > 4 && args[0] == "api" && args[2] == "session.create":
		fakebin.Append(p["log"], "create "+args[4])
		if p["die"] != "" {
			fmt.Fprintln(os.Stderr, p["die"])
			return 1
		}
		if p["reject"] != "" {
			fmt.Printf(`{"_tag":"InvalidRequestError","message":%q,"kind":"Payload"}`+"\n", p["reject"])
			fmt.Fprintln(os.Stderr, "HTTP 400 Bad Request")
			return 1
		}
		var body struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(args[4]), &body)
		id := body.ID
		if p["answer"] != "" {
			id = p["answer"]
		}
		if p["garbage"] != "" {
			fmt.Print(p["garbage"])
			return 0
		}
		fmt.Printf(`{"data":{"id":%q,"model":{"id":"m2","providerID":"local"},"location":{"directory":"/x"}}}`, id)
	case len(args) > 2 && args[0] == "api" && args[2] == "session.message.list":
		fmt.Print(`{"data":[]}`)
	default:
		time.Sleep(5 * time.Minute)
	}
	return 0
}

func ocsessionMain(args []string) int {
	p := fakebin.Params()
	if len(args) > 0 {
		if out, ok := p["on "+args[0]]; ok {
			fmt.Print(out)
			return 0
		}
	}
	d, _ := time.ParseDuration(p["apisleep"])
	time.Sleep(d)
	want := []string{"api", "--standalone", "session.message.list", "--param", "sessionID=", "--param", "type=user", "--param", "limit=1"}
	if len(args) != len(want) || !strings.HasPrefix(args[4], "sessionID=ses_hand") || strings.Contains(strings.Join(args, ""), "&") {
		fmt.Fprintf(os.Stderr, "unexpected argv %q\n", args)
		return 1
	}
	for i, w := range want {
		if i != 4 && args[i] != w {
			fmt.Fprintf(os.Stderr, "unexpected argv %q\n", args)
			return 1
		}
	}
	if body, ok := p["body"]; ok {
		fmt.Print(body)
		return 0
	}
	data := []string{}
	if slices.Contains(strings.Split(p["messages"], ","), "user") {
		data = append(data, `{"id":"msg_1","type":"user","text":"brief"}`)
	}
	fmt.Printf(`{"data":[%s]}`, strings.Join(data, ","))
	return 0
}

func idleMain([]string) int {
	fmt.Print("> ")
	time.Sleep(time.Hour)
	return 0
}

func notifyEnvMain(args []string) int {
	line := strings.Join(append(args, os.Getenv("HAND_NOTIFY_KIND"), os.Getenv("HAND_NOTIFY_REF"), os.Getenv("HAND_NOTIFY_TEXT"), os.Getenv("HAND_NOTIFY_URL")), "|")
	fakebin.Append(fakebin.Params()["log"], line)
	return 0
}

func wrapMain(args []string) int {
	p := fakebin.Params()
	fmt.Fprint(os.Stderr, p["stderr"])
	cmd := exec.Command(p["exec"], args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

func listerMain(args []string) int {
	p := fakebin.Params()
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println(p["version"])
		return 0
	}
	listing := len(args) > 0 && slices.Contains([]string{"models", "debug", "-p"}, args[0])
	if p["log"] != "" && listing {
		fakebin.Append(p["log"], strings.Join(args, " "))
	}
	if !listing {
		hold, _ := time.ParseDuration(p["hold"])
		time.Sleep(hold)
	}
	d, _ := time.ParseDuration(p["sleep"])
	time.Sleep(d)
	fmt.Fprint(os.Stderr, p["stderr"])
	fmt.Print(p["stdout"])
	code, _ := strconv.Atoi(p["exit"])
	return code
}

func fakeMain(args []string) int {
	p := fakebin.Params()
	if len(args) > 0 {
		if out, ok := p["on "+args[0]]; ok {
			fmt.Print(out)
			return 0
		}
	}
	d, _ := time.ParseDuration(p["sleep"])
	time.Sleep(d)
	if log := cmp.Or(p["log"], os.Getenv(p["logenv"])); log != "" {
		fakebin.Append(log, strings.TrimSpace(p["prefix"]+" "+strings.Join(args, " ")))
	}
	fmt.Fprint(os.Stderr, p["stderr"])
	code, _ := strconv.Atoi(p["exit"])
	return code
}

type harness struct {
	name string
	t    *testing.T
	home string
	mu   *sync.Mutex
	now  time.Time
	vars map[string]string
	cwd  string
	in   io.Reader
	tty  bool
	exec [][]string
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, mu: new(sync.Mutex), home: t.TempDir(), now: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), vars: map[string]string{"SECONDHAND_HOME": t.TempDir(), "HAND_LUVUS_BASE": "http://127.0.0.1:1"}}
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = h.now.Add(d)
}

func (h *harness) env(out, errOut *bytes.Buffer) cli.Env {
	return cli.Env{
		Name:   h.name,
		Stdin:  h.stdin(),
		Stdout: out,
		Stderr: errOut,
		Getenv: func(k string) string {
			if v, ok := h.vars[k]; ok || k != "HAND_HOME" {
				return v
			}
			return h.home
		},
		Environ: func() []string {
			var kv []string
			for k, v := range h.vars {
				kv = append(kv, k+"="+v)
			}
			return kv
		},
		Now: h.clock,
		Exec: func(argv0 string, argv, _ []string) error {
			h.exec = append(h.exec, append([]string{argv0}, argv...))
			return nil
		},
		Terminal: func() bool { return h.tty },
		Getwd: func() (string, error) {
			if h.cwd != "" {
				return h.cwd, nil
			}
			return h.home, nil
		},
	}
}

func (h *harness) stdin() io.Reader {
	if h.in == nil {
		return strings.NewReader("")
	}
	return h.in
}

func (h *harness) run(args ...string) (string, string, int) {
	var out, errOut bytes.Buffer
	code := cli.Run(args, h.env(&out, &errOut))
	return out.String(), errOut.String(), code
}

func (h *harness) ok(args ...string) string {
	h.t.Helper()
	out, errOut, code := h.run(args...)
	if code != 0 {
		h.t.Fatalf("hand %q: exit %d: %s", args, code, errOut)
	}
	return out
}

func (h *harness) runCtx(ctx context.Context, args ...string) (string, string, int) {
	var out, errOut bytes.Buffer
	env := h.env(&out, &errOut)
	env.Context = ctx
	code := cli.Run(args, env)
	return out.String(), errOut.String(), code
}

func field(out, name string) string {
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, name+": "); ok {
			if u, err := strconv.Unquote(v); err == nil {
				return u
			}
			return v
		}
	}
	return ""
}

func (h *harness) worktree(name string) string {
	h.t.Helper()
	return filepath.Join(h.vars["SECONDHAND_HOME"], "worktrees", field(h.ok("init", h.home), "id"), name)
}

func (h *harness) branch(name string) string {
	h.t.Helper()
	return "hand/" + filepath.Base(filepath.Dir(h.worktree(name))) + "/" + name
}
