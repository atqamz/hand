package e2eapi_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/sidecar"
	"github.com/atqamz/hand/internal/state"
)

var (
	win         = runtime.GOOS == "windows"
	exe         = map[bool]string{true: ".exe"}[win]
	goarch      = map[bool]string{true: "arm64", false: "amd64"}[runtime.GOARCH == "arm64"]
	schema      = strconv.Itoa(state.SchemaVersion)
	version     = "0.0.0-e2e"
	luvusTested = luvus.Tested
	irohVersion = sidecar.Version
	asset       = map[bool]string{true: "hand-windows-amd64.zip", false: "hand-" + runtime.GOOS + "-" + goarch + ".tar.gz"}[win]
	irohAsset   = map[bool]string{true: "hand-iroh-windows-amd64.zip", false: "hand-iroh-" + runtime.GOOS + "-" + goarch + ".tar.gz"}[win]
	sums        = map[bool]string{true: "hand-windows-amd64.sha256", false: "checksums.txt"}[win]
	shells      = map[bool][]string{true: {"powershell", "pwsh"}, false: {"sh"}}[win]
	harnesses   = []string{"claude", "codex", "opencode", "agy", "copilot", "omp"}
	dropped     = regexp.MustCompile(`(?i)^(PSModulePath$|LUVUS_|HAND_|SECONDHAND_|CLAUDE|ANTHROPIC|OPENAI|CODEX|GH_|GITHUB_|ACTIONS_|RUNNER_|XDG_|DBUS_SESSION_BUS_ADDRESS$|NODE_OPTIONS$)`)
	contentType = map[string]string{".sh": "text/plain; charset=utf-8", ".ps1": "text/plain; charset=utf-8", ".txt": "text/plain; charset=utf-8", ".sha256": "text/plain; charset=utf-8", ".gz": "application/gzip", ".zip": "application/zip"}
)

type config struct{ tmp, dist, bin, luvusCache string }

var (
	cfgOnce sync.Once
	cfg     config
	cfgErr  error
	candMu  sync.Mutex
	candBin string
)

func enabled(t *testing.T) config {
	t.Helper()
	cfgOnce.Do(func() {
		tmp := os.Getenv("E2E_TMP")
		dist := os.Getenv("E2E_DIST")
		if tmp == "" && dist == "" {
			return
		}
		if tmp == "" {
			tmp, cfgErr = os.MkdirTemp("", "hand-e2e-")
		}
		if dist == "" {
			dist = filepath.Join(tmp, "dist")
		}
		tmp, _ = filepath.Abs(tmp)
		dist, _ = filepath.Abs(dist)
		cfg = config{tmp: tmp, dist: dist, bin: filepath.Join(tmp, "bin-api"), luvusCache: filepath.Join(tmp, "luvus-cache")}
	})
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	if cfg.dist == "" {
		t.Skip("set E2E_TMP or E2E_DIST to the folder that .github/scripts/e2e-dist.sh wrote")
	}
	if _, err := os.Stat(filepath.Join(cfg.dist, "commit.txt")); err != nil {
		t.Fatalf("no release artifacts in %s: %v", cfg.dist, err)
	}
	return cfg
}

func readCommit(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string([]rune(strings.TrimSpace(string(b)))[:12])
}

func commit(t *testing.T) string { return readCommit(t, filepath.Join(enabled(t).dist, "commit.txt")) }
func nextCommit(t *testing.T) string {
	return readCommit(t, filepath.Join(enabled(t).dist, "next", "commit.txt"))
}

func check(t *testing.T, ok bool, msg string) {
	t.Helper()
	if !ok {
		t.Fatalf("FAIL %s", msg)
	}
	t.Logf("ok - %s", msg)
}

func equal(t *testing.T, got, want any, msg string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FAIL %s: got %#v, want %#v", msg, got, want)
	}
	t.Logf("ok - %s", msg)
}

func shorten(s string) string {
	if len(s) > 1200 {
		return s[:1200]
	}
	return s
}

func matches(t *testing.T, text, re, msg string) {
	t.Helper()
	if !regexp.MustCompile(re).MatchString(text) {
		t.Fatalf("FAIL %s: %s not found in:\n%s", msg, re, shorten(text))
	}
	t.Logf("ok - %s", msg)
}

func lacks(t *testing.T, text, re, msg string) {
	t.Helper()
	if regexp.MustCompile(re).MatchString(text) {
		t.Fatalf("FAIL %s: %s found in:\n%s", msg, re, shorten(text))
	}
	t.Logf("ok - %s", msg)
}

func quote(s string) string { return regexp.QuoteMeta(s) }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 300 {
		if cond() {
			t.Logf("ok - %s", what)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("FAIL %s", what)
}

type result struct {
	code     int
	out, err string
	shown    string
}

type opts struct {
	dir     string
	env     []string
	input   string
	timeout time.Duration
}

func run(t *testing.T, name string, args []string, o opts) result {
	t.Helper()
	parts := append([]string{name}, args...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \"'$") {
			parts[i] = strconv.Quote(p)
		}
	}
	shown := strings.Join(parts, " ")
	limit := o.timeout
	if limit == 0 {
		limit = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = o.dir
	cmd.Env = o.env
	cmd.Stdin = strings.NewReader(o.input)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	if cmd.ProcessState == nil {
		t.Fatalf("%s: %v", shown, err)
	}
	code := cmd.ProcessState.ExitCode()
	if ctx.Err() != nil {
		code = -1
	}
	res := result{code: code, out: out.String(), err: errb.String(), shown: shown}
	t.Logf("$ %s\nexit %d\n%s%s", shown, code, tail(res.out), map[bool]string{true: "stderr:\n" + tail(res.err)}[strings.TrimSpace(res.err) != ""])
	return res
}

func tail(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if len(s) > 4000 {
		s = "..." + s[len(s)-4000:]
	}
	if s == "" {
		return ""
	}
	return s + "\n"
}

func ok(t *testing.T, res result, msg string) result {
	t.Helper()
	if res.code != 0 {
		t.Fatalf("FAIL %s: %s exited %d\n%s\n%s", msg, res.shown, res.code, res.out, res.err)
	}
	t.Logf("ok - %s", msg)
	return res
}

func fails(t *testing.T, res result, msg string, code int) result {
	t.Helper()
	if res.code == 0 || (code != 0 && res.code != code) {
		t.Fatalf("FAIL %s: %s exited %d, want %d\n%s\n%s", msg, res.shown, res.code, code, res.out, res.err)
	}
	t.Logf("ok - %s", msg)
	return res
}

func sha(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	slices.Sort(out)
	return out
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

type toonDoc struct {
	fields map[string]string
	tables map[string][]map[string]string
	lists  map[string][]string
}

var (
	tableRe = regexp.MustCompile(`^(\S[^\[]*)\[(\d+)\]\{(.*)\}:$`)
	listRe  = regexp.MustCompile(`^(\S[^\[]*)\[(\d+)\]:$`)
	fieldRe = regexp.MustCompile(`^([A-Za-z0-9_]+): (.*)$`)
)

func cell(s string) string {
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

func splitCells(line string) []string {
	var cells []string
	var cur strings.Builder
	quoted := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && quoted && i+1 < len(line):
			cur.WriteByte(c)
			i++
			cur.WriteByte(line[i])
		case c == '"':
			quoted = !quoted
			cur.WriteByte(c)
		case c == ',' && !quoted:
			cells = append(cells, cell(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(cells, cell(cur.String()))
}

func toon(text string) toonDoc {
	doc := toonDoc{fields: map[string]string{}, tables: map[string][]map[string]string{}, lists: map[string][]string{}}
	var cols []string
	table, list, block := "", "", ""
	var body []string
	flush := func() {
		if block != "" {
			doc.fields[block] = strings.TrimRight(strings.Join(body, "\n"), "\n")
			block, body = "", nil
		}
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if block != "" {
			if line == "" || strings.HasPrefix(line, "  ") {
				body = append(body, strings.TrimPrefix(line, "  "))
				continue
			}
			flush()
		}
		if m := tableRe.FindStringSubmatch(line); m != nil {
			cols = strings.Split(m[3], ",")
			table, list = m[1], ""
			doc.tables[table] = []map[string]string{}
		} else if m := listRe.FindStringSubmatch(line); m != nil {
			list, table = m[1], ""
			doc.lists[list] = []string{}
		} else if strings.HasPrefix(line, "  - ") && list != "" {
			doc.lists[list] = append(doc.lists[list], line[4:])
		} else if strings.HasPrefix(line, "  ") && len(line) > 2 && line[2] != ' ' && table != "" {
			row := map[string]string{}
			for i, c := range splitCells(line[2:]) {
				if i < len(cols) {
					row[cols[i]] = c
				}
			}
			doc.tables[table] = append(doc.tables[table], row)
		} else if m := fieldRe.FindStringSubmatch(line); m != nil {
			table, list = "", ""
			if m[2] == "|" {
				block = m[1]
				continue
			}
			doc.fields[m[1]] = cell(m[2])
		}
	}
	flush()
	return doc
}

func cleanEnv() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if k == "" || dropped.MatchString(k) {
			continue
		}
		env[k] = v
	}
	return env
}

func pathKey(env map[string]string) string {
	for k := range env {
		if strings.EqualFold(k, "path") {
			return k
		}
	}
	return "PATH"
}

func toList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func found(name string, dirs []string) []string {
	exts := []string{""}
	if win {
		exts = []string{"", ".exe", ".cmd", ".bat", ".com", ".ps1"}
	}
	var out []string
	for _, d := range dirs {
		for _, e := range exts {
			if p := filepath.Join(d, name+e); exists(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

type box struct {
	name, dir, home, root, fleet, tmp, bin string
}

func newBox(t *testing.T, name string) *box {
	t.Helper()
	c := enabled(t)
	d := filepath.Join(c.tmp, name)
	b := &box{name: name, dir: d, home: filepath.Join(d, "home"), root: filepath.Join(d, "sh"), fleet: filepath.Join(d, "fleet"), tmp: filepath.Join(d, "tmp"), bin: c.bin}
	if err := os.RemoveAll(d); err != nil {
		t.Fatal(err)
	}
	dirs := []string{b.home, b.tmp, b.fleet}
	if win {
		dirs = append(dirs, filepath.Join(b.home, "AppData", "Local"), filepath.Join(b.home, "AppData", "Roaming"))
	}
	for _, p := range dirs {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if t.Failed() {
			b.diagnostics(t)
		}
		if exists(filepath.Join(b.bin, "hand"+exe)) && exists(b.fleet) {
			b.handWith(t, opts{}, "board", "stop")
			b.handWith(t, opts{}, "watch", "stop")
		}
	})
	return b
}

func (b *box) env(extra map[string]string, dirs []string, harness string) []string {
	env := cleanEnv()
	key := pathKey(env)
	var own []string
	own = append(own, dirs...)
	base := os.Getenv("E2E_PATH")
	if base == "" {
		base = env[key]
	}
	for _, d := range strings.Split(base, string(os.PathListSeparator)) {
		if d != "" {
			own = append(own, d)
		}
	}
	if harness != "" {
		own = append([]string{harness}, own...)
	} else {
		var stray []string
		for _, h := range harnesses {
			stray = append(stray, found(h, own)...)
		}
		if len(stray) > 0 {
			panic(fmt.Sprintf("a harness is on PATH, so the no-harness checks cannot run (set E2E_PATH to a PATH without one): %v", stray))
		}
	}
	env[key] = strings.Join(own, string(os.PathListSeparator))
	for k, v := range map[string]string{"HOME": b.home, "SECONDHAND_HOME": b.root, "TMPDIR": b.tmp, "TMP": b.tmp, "TEMP": b.tmp, "LANG": "C.UTF-8", "GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_NOSYSTEM": "1"} {
		env[k] = v
	}
	if win {
		env["USERPROFILE"] = b.home
		env["LOCALAPPDATA"] = filepath.Join(b.home, "AppData", "Local")
		env["APPDATA"] = filepath.Join(b.home, "AppData", "Roaming")
	}
	for k, v := range extra {
		env[k] = v
	}
	return toList(env)
}

type handOpts struct {
	cwd     string
	bin     string
	extra   map[string]string
	harness string
	input   string
	timeout time.Duration
}

func (b *box) handWith(t *testing.T, o any, args ...string) result {
	t.Helper()
	var h handOpts
	switch v := o.(type) {
	case handOpts:
		h = v
	case opts:
		h = handOpts{cwd: v.dir, input: v.input, timeout: v.timeout}
	}
	bin := h.bin
	if bin == "" {
		bin = b.bin
	}
	if h.cwd == "" {
		h.cwd = b.fleet
	}
	return run(t, filepath.Join(bin, "hand"+exe), args, opts{dir: h.cwd, env: b.env(h.extra, []string{bin}, h.harness), input: h.input, timeout: h.timeout})
}

func (b *box) hand(t *testing.T, args ...string) result {
	t.Helper()
	return b.handWith(t, handOpts{}, args...)
}

func (b *box) git(t *testing.T, dir string, args ...string) result {
	t.Helper()
	full := append([]string{"-c", "user.name=e2e", "-c", "user.email=e2e@example.invalid", "-c", "commit.gpgsign=false"}, args...)
	return run(t, "git", full, opts{dir: dir, env: b.env(nil, []string{b.bin}, "")})
}

func (b *box) repo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(b.dir, "repo-"+name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok(t, b.git(t, dir, "init", "-q", "-b", "main"), "git init "+name)
	ok(t, b.git(t, dir, "add", "."), "git add "+name)
	ok(t, b.git(t, dir, "commit", "-q", "-m", "initial"), "git commit "+name)
	return dir
}

func (b *box) file(parts ...string) string {
	return filepath.Join(append([]string{b.root}, parts...)...)
}

func (b *box) read(parts ...string) (string, bool) {
	data, err := os.ReadFile(b.file(parts...))
	return string(data), err == nil
}

func (b *box) diagnostics(t *testing.T) {
	for _, rel := range []string{"board.log", "update.log", "update.json", "board.addr", "board.pid"} {
		if text, ok := b.read(rel); ok {
			t.Logf("--- %s\n%s", rel, last(text, 6000))
		}
	}
	if text := read(filepath.Join(b.fleet, "watch.log")); text != "" {
		t.Logf("--- watch.log\n%s", last(text, 4000))
	}
	if exists(filepath.Join(b.bin, "hand"+exe)) && exists(b.fleet) {
		b.hand(t, "doctor")
	}
}

func last(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func seedLuvus(t *testing.T, b *box, wanted string) bool {
	t.Helper()
	c := enabled(t)
	pinFile := filepath.Join(c.luvusCache, "pin.json")
	if os.Getenv("E2E_FRESH_LUVUS") == "true" || !exists(pinFile) {
		return false
	}
	var pin map[string]any
	if json.Unmarshal([]byte(read(pinFile)), &pin) != nil || pin["version"] != wanted {
		return false
	}
	dest := filepath.Join(b.root, "luvus")
	if err := copyTree(c.luvusCache, dest); err != nil {
		t.Fatal(err)
	}
	old, _ := pin["path"].(string)
	pin["path"] = filepath.Join(dest, filepath.Base(filepath.Dir(old)), filepath.Base(old))
	data, _ := json.MarshalIndent(pin, "", "  ")
	if err := os.WriteFile(filepath.Join(dest, "pin.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return true
}

func saveLuvus(t *testing.T, b *box) {
	t.Helper()
	c := enabled(t)
	if exists(filepath.Join(c.luvusCache, "pin.json")) {
		return
	}
	for _, store := range []string{filepath.Join(b.fleet, "luvus", runtime.GOOS+"-"+goarch), filepath.Join(b.root, "luvus")} {
		if exists(filepath.Join(store, "pin.json")) {
			if err := copyTree(store, c.luvusCache); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
}

func initialize(t *testing.T, b *box, msg string) result {
	t.Helper()
	var res result
	for attempt := 1; attempt <= 3; attempt++ {
		res = b.hand(t, "init")
		if res.code == 0 || !regexp.MustCompile(`(?i)update: |download|timed out|connection|EOF|reset`).MatchString(res.err) {
			break
		}
		time.Sleep(time.Duration(attempt) * 5 * time.Second)
	}
	return ok(t, res, msg)
}

func cleaned(t *testing.T, dir string, probe func()) {
	t.Helper()
	for range 6 {
		if !slices.ContainsFunc(names(t, dir), func(n string) bool { return strings.Contains(n, ".old") }) {
			return
		}
		probe()
		time.Sleep(2 * time.Second)
	}
}

type server struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []string
}

func (s *server) base() string { return s.URL }

func (s *server) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.reqs
	s.reqs = nil
	return out
}

func (s *server) saw(pred func(string) bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.reqs, pred)
}

func serve(t *testing.T, root string, corrupt string) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.reqs = append(s.reqs, r.URL.Path)
		s.mu.Unlock()
		file := filepath.Join(root, filepath.FromSlash(r.URL.Path))
		rel, err := filepath.Rel(root, file)
		info, serr := os.Stat(file)
		if err != nil || strings.HasPrefix(rel, "..") || serr != nil || info.IsDir() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if ct, ok := contentType[filepath.Ext(file)]; ok {
			w.Header().Set("Content-Type", ct)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		if corrupt != "" && filepath.Base(file) == corrupt {
			data, _ := os.ReadFile(file)
			data[len(data)>>1] ^= 0xff
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
			return
		}
		http.ServeFile(w, r, file)
	}))
	t.Cleanup(s.Close)
	return s
}

func install(t *testing.T, shell, base, dir string, extra map[string]string, b *box, dirs ...string) result {
	t.Helper()
	var env map[string]string
	var list []string
	if b != nil && !win {
		list = b.env(nil, dirs, "")
	} else {
		list = toList(cleanEnv())
	}
	env = map[string]string{"HAND_INSTALL_BASE": base, "HAND_INSTALL_DIR": dir}
	for k, v := range extra {
		env[k] = v
	}
	list = append(list, toList(env)...)
	if shell == "sh" {
		return run(t, "sh", []string{"-c", `s=$(curl -fsSL "$HAND_INSTALL_BASE/install.sh") && sh -c "$s"`}, opts{env: list})
	}
	return run(t, shell, []string{"-NoProfile", "-NonInteractive", "-Command", `irm "$env:HAND_INSTALL_BASE/install.ps1" | iex`}, opts{env: list})
}

func candidate(t *testing.T) string {
	t.Helper()
	c := enabled(t)
	candMu.Lock()
	defer candMu.Unlock()
	if candBin != "" {
		return candBin
	}
	srv := serve(t, c.dist, "")
	b := newBox(t, "installer")
	ok(t, install(t, shells[len(shells)-1], srv.base(), c.bin, nil, b), "the installer installs the candidate")
	check(t, exists(filepath.Join(c.bin, "hand"+exe)), "the candidate is installed")
	candBin = c.bin
	return candBin
}

type client struct {
	base string
	hdr  map[string]string
	jar  map[string]string
	hc   *http.Client
}

type resp struct {
	status int
	header http.Header
	body   string
}

func newClient(base string, hdr map[string]string) *client {
	return &client{base: base, hdr: hdr, jar: map[string]string{}, hc: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type reqOpts struct {
	json    string
	csrf    string
	headers map[string]string
	timeout time.Duration
}

func (c *client) request(t *testing.T, ctx context.Context, method, p string, o reqOpts) *http.Response {
	t.Helper()
	var body io.Reader
	if o.json != "" {
		body = strings.NewReader(o.json)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+p, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range c.hdr {
		req.Header.Set(k, v)
	}
	for k, v := range o.headers {
		req.Header.Set(k, v)
	}
	if o.json != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if o.csrf != "" {
		req.Header.Set("X-Hand-CSRF", o.csrf)
	}
	var cookies []string
	for k, v := range c.jar {
		cookies = append(cookies, k+"="+v)
	}
	slices.Sort(cookies)
	if len(cookies) > 0 {
		req.Header.Set("Cookie", strings.Join(cookies, "; "))
	}
	res, err := c.hc.Do(req)
	if err != nil {
		t.Fatalf("%s %s%s: %v", method, c.base, p, err)
	}
	return res
}

func (c *client) do(t *testing.T, method, p string, o reqOpts) resp {
	t.Helper()
	limit := o.timeout
	if limit == 0 {
		limit = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	res := c.request(t, ctx, method, p, o)
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		t.Fatalf("%s %s: %v", method, p, err)
	}
	return resp{status: res.StatusCode, header: res.Header, body: string(data)}
}

func (c *client) get(t *testing.T, p string) resp {
	t.Helper()
	return c.do(t, http.MethodGet, p, reqOpts{})
}

func (c *client) post(t *testing.T, p, body, csrf string) resp {
	t.Helper()
	return c.do(t, http.MethodPost, p, reqOpts{json: body, csrf: csrf})
}

func (c *client) stream(t *testing.T, p string, limit time.Duration, until func(string) bool) (int, http.Header, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	res := c.request(t, ctx, http.MethodGet, p, reqOpts{})
	defer res.Body.Close()
	var seen strings.Builder
	rd := bufio.NewReader(res.Body)
	for !until(seen.String()) {
		line, err := rd.ReadString('\n')
		seen.WriteString(line)
		if err != nil {
			break
		}
	}
	return res.StatusCode, res.Header, seen.String()
}

func peerClient(base, peerAddr, endpoint, secret string) *client {
	c := newClient(base, nil)
	c.hc.Transport = &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, network, peerAddr)
		if err != nil {
			return nil, err
		}
		if _, err := fmt.Fprintf(conn, "HAND-PEER 1 %s %s\n", endpoint, secret); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}}
	return c
}

type obj = map[string]any

func parse(t *testing.T, body string) obj {
	t.Helper()
	var o obj
	if err := json.Unmarshal([]byte(body), &o); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, shorten(body))
	}
	return o
}

func at(v any, path string) any {
	for _, k := range strings.Split(path, ".") {
		switch m := v.(type) {
		case obj:
			v = m[k]
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i >= len(m) {
				return nil
			}
			v = m[i]
		default:
			return nil
		}
	}
	return v
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func hmacHex(key, msg string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

func errorOf(t *testing.T, r resp) (int, string) {
	t.Helper()
	e := parse(t, r.body)
	return r.status, str(at(e, "error.code"))
}

type fleetBox struct {
	origin string
	id     string
	token  string
}

func (f fleetBox) host() *client {
	c := newClient(f.origin, nil)
	c.jar["hand_board_"+f.id] = f.token
	return c
}

func (f fleetBox) api(p string) string { return "/" + f.id + "/api/v1" + p }

func startBoard(t *testing.T, b *box) fleetBox {
	t.Helper()
	if addr := os.Getenv("E2E_BOARD_ADDR"); addr != "" {
		args := []string{"board", "--addr", addr}
		if exists(b.file("board.iroh")) {
			args = append(args, "--iroh")
		}
		spawn(t, b, args...)
		waitFor(t, "the Board started with E2E_BOARD_ADDR records its address", func() bool { _, ok := b.read("board.addr"); return ok })
	}
	started := b.hand(t, "board", "start")
	if started.code == 2 && strings.Contains(started.err, "want 0 argument") {
		ok(t, b.hand(t, "open", "--print"), "an older hand open --print starts the Board")
	} else {
		ok(t, started, "hand board start starts the Board")
	}
	id := toon(ok(t, b.hand(t, "init"), "hand init names the fleet").out).fields["id"]
	addr, _ := b.read("board.addr")
	addr = strings.TrimSpace(addr)
	origin := "http://" + addr
	// the first request to a fleet makes the Board create its board.token; the old release has no API route, so any path will do
	_, _ = (&http.Client{Timeout: 10 * time.Second}).Get(origin + "/" + id + "/")
	var token string
	for range 50 {
		if token = strings.TrimSpace(read(filepath.Join(b.fleet, "board.token"))); token != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	check(t, addr != "" && token != "", "the Board wrote its address and the fleet its token")
	return fleetBox{origin: origin, id: id, token: token}
}

func stopAll(t *testing.T, b *box) {
	t.Helper()
	b.hand(t, "board", "stop")
	b.hand(t, "watch", "stop")
}

func stub(t *testing.T, b *box) string {
	t.Helper()
	dir := filepath.Join(b.dir, "harness")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(enabled(t).dist, "stub", "claude-"+runtime.GOOS+"-"+goarch+exe))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude"+exe), data, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func jsonString(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func spawn(t *testing.T, b *box, args ...string) {
	t.Helper()
	out, err := os.OpenFile(filepath.Join(b.dir, "spawn.out"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(b.bin, "hand"+exe), args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = b.fleet, b.env(nil, []string{b.bin}, ""), out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait(); out.Close() }()
}
