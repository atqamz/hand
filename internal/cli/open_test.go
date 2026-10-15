package cli_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/state"
)

type openFixture struct {
	h    *harness
	id   string
	base string
	stop func() string
	log  string
	dir  string
}

func newOpenFixture(t *testing.T) *openFixture {
	t.Helper()
	h := initWithProject(t)
	h.ok("task", "add", "hand", "Fix login")
	h.ok("task", "add", "hand", "Write docs")
	h.ok("task", "start", "t1")
	dir := t.TempDir()
	extra := [][]string{
		{"https://docs.example.com/q3-review", filepath.Join(dir, "notes.md")},
		{`C:\Users\me\out\report.docx`},
		{dir},
		{"https://docs.example.com/older", `\\evil\share\dir\report.docx`},
	}
	for i := range extra {
		h.ok("task", "add", "hand", "Deliverable task")
		h.ok("task", "start", "t"+strconv.Itoa(i+3))
	}
	st, err := state.Open(filepath.Join(h.home, "hand.db"), func() time.Time { return h.now })
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: 1, Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}}, "/w")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AttemptRunning(ctx, a.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddReport(ctx, a.ID, state.ReportDone, "First try https://github.com/atqamz/hand/pull/40"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddReport(ctx, a.ID, state.ReportDone, "Done: https://github.com/atqamz/hand/pull/41 then https://github.com/atqamz/hand/pull/42"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Ask(ctx, 1, "Keep the old cookie name?"); err != nil {
		t.Fatal(err)
	}
	for i, refs := range extra {
		w, err := st.AddAttempt(ctx, state.AttemptSpec{TaskID: int64(i + 3), Harness: "claude", Model: "sonnet", Effort: "low", Argv: []string{"/bin/claude", "x"}}, "/w")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.AttemptRunning(ctx, w.ID, state.Terminal{ServerGeneration: "g", TerminalID: "t", PaneID: "2", PID: 1, StartMarker: "1"}); err != nil {
			t.Fatal(err)
		}
		for _, ref := range refs {
			if _, err := st.AddReport(ctx, w.ID, state.ReportProgress, "Done.\nDelivered: "+ref+"\n"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	fx := &openFixture{h: h, id: field(h.ok("init"), "id"), log: filepath.Join(t.TempDir(), "opened"), dir: dir}
	bin := t.TempDir()
	fakebin.Install(t, bin, openerName(), "opener", nil)
	h.vars["PATH"] = bin
	h.vars["XDG_LOG"] = fx.log
	fx.base, fx.stop = startBoard(t, h, "127.0.0.1")
	return fx
}

func (fx *openFixture) opened(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(fx.log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (fx *openFixture) token(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fx.h.home, "board.token"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestOpenDeepLinks(t *testing.T) {
	fx := newOpenFixture(t)
	for _, c := range []struct {
		args       []string
		path, frag string
	}{
		{nil, "/", ""},
		{[]string{"supervisor"}, "/", ""},
		{[]string{"t2"}, "/task/t2", ""},
		{[]string{"d1"}, "/decision/d1", ""},
		{[]string{"r2"}, "/task/t1", "#r2"},
		{[]string{"a1"}, "/task/t1", "#a1"},
	} {
		out := fx.h.ok(append([]string{"open"}, c.args...)...)
		opened := fx.opened(t)
		want := fx.base + "/" + fx.id + c.path + "?token=" + fx.token(t) + c.frag
		if got := opened[len(opened)-1]; got != want {
			t.Fatalf("open %q = %q, want %q", c.args, got, want)
		}
		if field(out, "opened") != fx.base+"/"+fx.id+c.path+c.frag {
			t.Fatalf("open %q printed %q", c.args, out)
		}
	}
}

func TestOpenNeverPrintsTheToken(t *testing.T) {
	fx := newOpenFixture(t)
	fx.h.ok("open")
	tok := fx.token(t)
	check := func(args ...string) {
		t.Helper()
		out, errOut, _ := fx.h.run(append([]string{"open"}, args...)...)
		if strings.Contains(out+errOut, tok) {
			t.Fatalf("open %q leaked the token:\n%s%s", args, out, errOut)
		}
	}
	for _, args := range [][]string{nil, {"supervisor"}, {"t1"}, {"d1"}, {"r1"}, {"a1"}, {"t1", "--delivered"}, {"t9"}, {"x1"}} {
		check(args...)
	}
	path := fx.h.vars["PATH"]
	fx.h.vars["PATH"] = t.TempDir()
	check("t1")
	fx.h.vars["PATH"] = path
	fx.stop()
	check("t1")
	if err := os.WriteFile(addrFile(fx.h), []byte("127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("t1")
}

func TestOpenDelivered(t *testing.T) {
	fx := newOpenFixture(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"t1", "--delivered"}, "https://github.com/atqamz/hand/pull/42"},
		{[]string{"--delivered", "t1"}, "https://github.com/atqamz/hand/pull/42"},
		{[]string{"t3", "--delivered"}, fx.dir},
		{[]string{"t4", "--delivered"}, `C:\Users\me\out`},
		{[]string{"t5", "--delivered"}, fx.dir},
	} {
		out := fx.h.ok(append([]string{"open"}, c.args...)...)
		opened := fx.opened(t)
		if got := opened[len(opened)-1]; got != c.want || !strings.Contains(out, strings.ReplaceAll(c.want, `\`, `\\`)) {
			t.Fatalf("open %q = %q (%q), want %q", c.args, got, out, c.want)
		}
	}
	before := len(fx.opened(t))
	unc := `\\evil\share\dir\report.docx`
	if out := fx.h.ok("open", "--print", "--delivered", "t6"); field(out, "path") != unc {
		t.Fatalf("open --print --delivered t6 = %q", out)
	}
	for _, c := range []struct{ ref, key, want string }{
		{"t1", "url", "https://github.com/atqamz/hand/pull/42"},
		{"t3", "path", filepath.Join(fx.dir, "notes.md")},
		{"t4", "path", `C:\Users\me\out\report.docx`},
	} {
		if out := fx.h.ok("open", "--print", "--delivered", c.ref); field(out, c.key) != c.want {
			t.Fatalf("open --print --delivered %s = %q, want %s %q", c.ref, out, c.key, c.want)
		}
	}
	if got := len(fx.opened(t)); got != before {
		t.Fatalf("--print opened something: %d lines, was %d", got, before)
	}
	for _, args := range [][]string{{"t6", "--delivered"}, {"--delivered", "t6"}} {
		if _, errOut, code := fx.h.run(append([]string{"open"}, args...)...); code == 0 || !strings.Contains(errOut, "UNC paths are not opened") || !strings.Contains(errOut, unc) || strings.Count(strings.TrimSpace(errOut), "\n") != 0 {
			t.Fatalf("open %q on a UNC deliverable: code=%d stderr=%q", args, code, errOut)
		}
	}
	if got := len(fx.opened(t)); got != before {
		t.Fatalf("a UNC deliverable reached the opener: %d lines, was %d", got, before)
	}
	if _, errOut, code := fx.h.run("open", "t2", "--delivered"); code != 3 || !strings.Contains(errOut, "t2 has no deliverable in its reports") {
		t.Fatalf("a task with no deliverable: code=%d stderr=%q", code, errOut)
	}
	if _, _, code := fx.h.run("open", "d1", "--delivered"); code != 2 {
		t.Fatalf("--delivered on a decision: code=%d", code)
	}
	for _, args := range [][]string{{"t1", "--pr"}, {"--pr", "t1"}, {"-pr", "t1"}} {
		if _, errOut, code := fx.h.run(append([]string{"open"}, args...)...); code != 2 || !strings.Contains(errOut, "--delivered") {
			t.Fatalf("open %q: code=%d stderr=%q", args, code, errOut)
		}
	}
	fx.stop()
	if _, errOut, code := fx.h.run("open", "t1", "--delivered"); code != 0 {
		t.Fatalf("--delivered needs no board: code=%d stderr=%q", code, errOut)
	}
}

func TestOpenRefusals(t *testing.T) {
	fx := newOpenFixture(t)
	for _, c := range []struct {
		args []string
		code int
	}{{[]string{"x1"}, 2}, {[]string{"t0"}, 2}, {[]string{"t1", "t2"}, 2}, {[]string{"t9"}, 3}, {[]string{"d9"}, 3}, {[]string{"r9"}, 3}, {[]string{"a9"}, 3}} {
		if _, errOut, code := fx.h.run(append([]string{"open"}, c.args...)...); code != c.code {
			t.Fatalf("open %q: code=%d stderr=%q, want %d", c.args, code, errOut, c.code)
		}
	}
	path := fx.h.vars["PATH"]
	fx.h.vars["PATH"] = t.TempDir()
	_, errOut, code := fx.h.run("open", "t1")
	if code != 3 || !strings.Contains(errOut, fx.base+"/"+fx.id+"/task/t1") || !strings.Contains(errOut, "board.token") {
		t.Fatalf("no xdg-open: code=%d stderr=%q", code, errOut)
	}
	fx.h.vars["PATH"] = path
	fx.stop()
	if _, errOut, code := fx.h.run("open"); code != 3 || !strings.Contains(errOut, "hand board") {
		t.Fatalf("no board.addr: code=%d stderr=%q", code, errOut)
	}
	if err := os.WriteFile(addrFile(fx.h), []byte("127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := fx.h.run("open"); code != 3 || !strings.Contains(errOut, "hand board") {
		t.Fatalf("stale board.addr: code=%d stderr=%q", code, errOut)
	}
}

func TestConcurrentOpensShareOneToken(t *testing.T) {
	fx := newOpenFixture(t)
	codes := make(chan int, 4)
	for range 4 {
		go func() {
			_, _, code := fx.h.run("open")
			codes <- code
		}()
	}
	for range 4 {
		if code := <-codes; code != 0 {
			t.Fatalf("a concurrent open exited %d", code)
		}
	}
	tok := fx.token(t)
	opened := fx.opened(t)
	if len(tok) != 48 || len(opened) != 4 {
		t.Fatalf("token %q, opened %q", tok, opened)
	}
	for _, u := range opened {
		if !strings.HasSuffix(u, "?token="+tok) {
			t.Fatalf("concurrent opens used different tokens: %q", opened)
		}
	}
}

func TestOpenRefusesAnImpostor(t *testing.T) {
	fx := newOpenFixture(t)
	fx.h.ok("open")
	before := len(fx.opened(t))
	var mu sync.Mutex
	var seen []string
	impostor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.String())
		mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(impostor.Close)
	if err := os.WriteFile(addrFile(fx.h), []byte(strings.TrimPrefix(impostor.URL, "http://")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	enableBoard(t, fx.h)
	if _, errOut, code := fx.h.run("open", "t1"); code != 3 || !strings.Contains(errOut, "hand board") {
		t.Fatalf("open against an impostor: code=%d stderr=%q", code, errOut)
	}
	if n := boardSpawns(fx.h); n != 0 {
		t.Fatalf("boards started against an impostor = %d, want 0", n)
	}
	if got := len(fx.opened(t)); got != before {
		t.Fatalf("xdg-open ran against an impostor")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, u := range seen {
		if strings.Contains(u, fx.token(t)) {
			t.Fatalf("the impostor saw the token in %q", u)
		}
	}
}

func TestOpenPrintPrintsTheLoginLinkOnly(t *testing.T) {
	fx := newOpenFixture(t)
	out, errOut, code := fx.h.run("open", "--print", "t1")
	if code != 0 || errOut != "" || !strings.Contains(out, `url: "`+fx.base+"/"+fx.id+"/task/t1?token="+fx.token(t)+`"`) || !strings.Contains(out, "holds the fleet's token") {
		t.Fatalf("open --print: code=%d out=%q stderr=%q", code, out, errOut)
	}
	if _, err := os.Stat(fx.log); !os.IsNotExist(err) {
		t.Fatalf("open --print launched a browser: %v", err)
	}
}

func openerName() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "windows":
		return "rundll32"
	}
	return "xdg-open"
}

func notifierName() string {
	if runtime.GOOS == "darwin" {
		return "osascript"
	}
	return "notify-send"
}
