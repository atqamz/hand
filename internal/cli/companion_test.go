package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/board"
	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/qr"
)

const companionLink = "https://host.tailnet.example/f00000000e012/pair#K7QM3XRD2P"

type phoneBoard struct {
	h      *harness
	id     string
	token  string
	status int
	link   string
	posted chan string
}

func newPhoneBoard(t *testing.T) *phoneBoard {
	t.Helper()
	h := newHarness(t)
	id := field(h.ok("init"), "id")
	if _, _, code := h.run("companion", "code", "--base", "https://host.tailnet.example"); code != 3 {
		t.Fatalf("no board: code=%d", code)
	}
	raw, err := os.ReadFile(filepath.Join(h.home, "board.token"))
	if err != nil {
		t.Fatal(err)
	}
	p := &phoneBoard{h: h, id: id, token: strings.TrimSpace(string(raw)), status: http.StatusOK, link: companionLink, posted: make(chan string, 1)}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	if err := os.WriteFile(addrFile(h), []byte(strings.TrimPrefix(srv.URL, "http://")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *phoneBoard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := "/" + p.id
	reply := func(status int, v any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch r.URL.Path {
	case prefix + "/proof":
		_, _ = w.Write([]byte(board.Proof(p.token, r.URL.Query().Get("nonce"))))
		return
	}
	if c, err := r.Cookie("hand_board_" + p.id); err != nil || c.Value != p.token {
		reply(http.StatusForbidden, board.APIErrorResponse{Error: board.APIError{Status: 403, Code: "forbidden", Message: "no session"}})
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == prefix+"/api/v1/session":
		reply(http.StatusOK, board.APIEnvelope{Version: 1, Data: board.APISession{CSRF: "csrf-1"}})
	case r.Method == http.MethodPost && r.URL.Path == prefix+"/api/v1/phone/code":
		if r.Header.Get("X-Hand-CSRF") != "csrf-1" {
			reply(http.StatusForbidden, board.APIErrorResponse{Error: board.APIError{Status: 403, Code: "csrf_required", Message: "csrf"}})
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		p.posted <- in["base"]
		if p.status != http.StatusOK {
			reply(p.status, board.APIErrorResponse{Error: board.APIError{Status: p.status, Code: "forbidden", Message: "a paired Companion cannot manage Companions;\nuse the host's session"}})
			return
		}
		reply(http.StatusOK, board.APIEnvelope{Version: 1, Data: board.APIPhone{Code: &board.APIPhoneCode{Code: "K7QM3XRD2P", Link: p.link, ExpiresAt: "2026-11-24T10:05:00Z"}}})
	default:
		http.NotFound(w, r)
	}
}

func (p *phoneBoard) run(terminal bool, noColor bool) (string, string, int) {
	var out, errOut strings.Builder
	env := p.h.env(&out, &errOut)
	env.Terminal = func() bool { return terminal }
	if noColor {
		env.Getenv = func(k string) string {
			if k == "NO_COLOR" {
				return "1"
			}
			return p.h.env(nil, nil).Getenv(k)
		}
	}
	code := cli.Run([]string{"companion", "code", "--base", "https://host.tailnet.example"}, env)
	return out.String(), errOut.String(), code
}

func qrModules(t *testing.T, out string, colored bool) [][]bool {
	t.Helper()
	_, body, ok := strings.Cut(out, "qr: |\n")
	if !ok {
		t.Fatalf("no qr block in %q", out)
	}
	var rows [][]bool
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		line = strings.TrimPrefix(line, "  ")
		if colored {
			line = strings.TrimSuffix(strings.TrimPrefix(line, "\x1b[30;47m"), "\x1b[0m")
		}
		var top, bottom []bool
		for _, g := range line {
			switch g {
			case ' ':
				top, bottom = append(top, false), append(bottom, false)
			case '▀':
				top, bottom = append(top, true), append(bottom, false)
			case '▄':
				top, bottom = append(top, false), append(bottom, true)
			case '█':
				top, bottom = append(top, true), append(bottom, true)
			default:
				t.Fatalf("glyph %q is not a half block", g)
			}
		}
		rows = append(rows, top, bottom)
	}
	return rows
}

var svgRun = regexp.MustCompile(`M(\d+) (\d+)h(\d+)v1`)

func svgModules(t *testing.T, link string) [][]bool {
	t.Helper()
	svg, err := qr.SVG(link, "x")
	if err != nil {
		t.Fatal(err)
	}
	m, err := qr.Matrix(link)
	if err != nil {
		t.Fatal(err)
	}
	side := len(m) + 8
	grid := make([][]bool, side)
	for y := range grid {
		grid[y] = make([]bool, side)
	}
	for _, r := range svgRun.FindAllStringSubmatch(string(svg), -1) {
		x, y, w := atoi(r[1]), atoi(r[2]), atoi(r[3])
		for i := range w {
			grid[y][x+i] = true
		}
	}
	return grid
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestCompanionCodeMatchesSVG(t *testing.T) {
	t.Parallel()
	p := newPhoneBoard(t)
	want := svgModules(t, companionLink)
	for _, c := range []struct{ terminal, colored bool }{{false, false}, {true, true}} {
		out, errOut, code := p.run(c.terminal, false)
		if code != 0 || errOut != "" {
			t.Fatalf("code=%d stderr=%q", code, errOut)
		}
		if base := <-p.posted; base != "https://host.tailnet.example" {
			t.Fatalf("posted base %q", base)
		}
		got := qrModules(t, out, c.colored)
		if len(got) < len(want) || len(got) > len(want)+1 {
			t.Fatalf("rows %d, want %d", len(got), len(want))
		}
		for y := range got {
			if len(got[y]) != len(want) {
				t.Fatalf("row %d is %d wide, want %d", y, len(got[y]), len(want))
			}
			for x := range got[y] {
				if w := y < len(want) && want[y][x]; got[y][x] != w {
					t.Fatalf("module %d,%d: got %v, want %v", x, y, got[y][x], w)
				}
			}
		}
	}
}

func TestCompanionCodeOutput(t *testing.T) {
	t.Parallel()
	p := newPhoneBoard(t)
	out, _, code := p.run(false, false)
	<-p.posted
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if field(out, "link") != companionLink || field(out, "code") != "K7QM3XRD2P" || field(out, "expires") != "2026-11-24T10:05:00Z" {
		t.Fatalf("fields: %q", out)
	}
	golden, err := os.ReadFile("testdata/companion_qr.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, body, _ := strings.Cut(out, "qr: |\n"); body != string(golden) {
		t.Fatalf("qr differs from testdata/companion_qr.txt:\n%s", body)
	}
	colored, _, _ := p.run(true, false)
	<-p.posted
	plain, _, _ := p.run(true, true)
	<-p.posted
	if !strings.Contains(colored, "\x1b[30;47m") || strings.Contains(plain, "\x1b") || strings.Contains(out, "\x1b") {
		t.Fatalf("colour only on a terminal without NO_COLOR: colored=%q", colored)
	}
	for _, line := range strings.Split(strings.TrimRight(strings.SplitN(plain, "qr: |\n", 2)[1], "\n"), "\n") {
		if len([]rune(line)) > 80 {
			t.Fatalf("line wider than 80 columns: %d", len([]rune(line)))
		}
	}
}

func TestCompanionCodeQuietZone(t *testing.T) {
	t.Parallel()
	p := newPhoneBoard(t)
	out, _, _ := p.run(false, false)
	<-p.posted
	rows := qrModules(t, out, false)
	for y, row := range rows {
		for x, dark := range row {
			if dark && (y < 4 || x < 4 || x >= len(row)-4 || y >= len(row)-4) {
				t.Fatalf("dark module %d,%d inside the quiet zone", x, y)
			}
		}
	}
}

func TestCompanionCodeRefusals(t *testing.T) {
	t.Parallel()
	p := newPhoneBoard(t)
	p.status = http.StatusForbidden
	out, errOut, code := p.run(false, false)
	<-p.posted
	if code != 1 || out != "" || strings.Count(strings.TrimSpace(errOut), "\n") != 0 || !strings.Contains(errOut, "refused (403)") || strings.Contains(errOut, companionLink) {
		t.Fatalf("refused: code=%d out=%q stderr=%q", code, out, errOut)
	}
	p.status = http.StatusOK
	p.link = "https://host.tailnet.example/f00000000e012/pair#" + strings.Repeat("A", 4000)
	out, errOut, code = p.run(false, false)
	<-p.posted
	if code != 1 || out != "" || !strings.Contains(errOut, "too long for a QR code") || strings.Contains(errOut, "AAAA") {
		t.Fatalf("too long: code=%d out=%q stderr=%q", code, out, errOut)
	}
	if err := os.Remove(addrFile(p.h)); err != nil {
		t.Fatal(err)
	}
	out, errOut, code = p.run(false, false)
	if code != 3 || out != "" || !strings.Contains(errOut, "no board is running") {
		t.Fatalf("down: code=%d out=%q stderr=%q", code, out, errOut)
	}
	h := newHarness(t)
	h.ok("init")
	for _, args := range [][]string{{"companion"}, {"companion", "code"}, {"companion", "code", "extra", "--base", "https://x.example"}, {"companion", "nope"}} {
		if _, _, code := h.run(args...); code != 2 {
			t.Fatalf("%q: code=%d, want 2", args, code)
		}
	}
}

func TestCompanionCodeLive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	id := field(h.ok("init"), "id")
	startBoard(t, h, "127.0.0.1")
	out, errOut, code := h.run("companion", "code", "--base", "https://host.example")
	if code != 0 || errOut != "" {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
	link := field(out, "link")
	if !strings.HasPrefix(link, "https://host.example/"+id+"/pair#") || !strings.HasSuffix(link, field(out, "code")) {
		t.Fatalf("out=%q", out)
	}
	got := qrModules(t, out, false)
	want := svgModules(t, link)
	for y := range want {
		for x := range want[y] {
			if got[y][x] != want[y][x] {
				t.Fatalf("module %d,%d", x, y)
			}
		}
	}
}
