package cli_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func runBoard(t *testing.T, h *harness, addr ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	a := "127.0.0.1:0"
	if len(addr) > 0 {
		a = addr[0]
	}
	out, errOut, code := h.runCtx(ctx, "board", "--addr", a)
	if code != 0 {
		t.Fatalf("board exit %d: %s", code, errOut)
	}
	return out
}

func addrFile(h *harness) string { return filepath.Join(h.vars["SECONDHAND_HOME"], "board.addr") }

func startBoard(t *testing.T, h *harness, host string, extra ...string) (string, func() string) {
	t.Helper()
	_ = os.Remove(addrFile(h))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string, 1)
	go func() {
		out, errOut, _ := h.runCtx(ctx, append([]string{"board", "--addr", host + ":0"}, extra...)...)
		done <- out + errOut
	}()
	var addr string
	eventually(t, func() bool {
		b, err := os.ReadFile(addrFile(h))
		addr = strings.TrimSpace(string(b))
		return err == nil && addr != ""
	})
	stop := sync.OnceValue(func() string { cancel(); return <-done })
	t.Cleanup(func() { stop() })
	return "http://" + addr, stop
}

func fleetPage(t *testing.T, base string, h *harness) (string, string) {
	t.Helper()
	id := field(h.ok("init"), "id")
	res, err := http.Get(base + "/" + id + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	b, err := os.ReadFile(filepath.Join(h.home, "board.token"))
	if err != nil {
		t.Fatal(err)
	}
	return base + "/" + id, strings.TrimSpace(string(b))
}

func login(t *testing.T, fleetURL, token string) (int, string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&http.Client{Jar: jar}).Get(fleetURL + "/?token=" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(body)
}

func TestBoardPrintsTheHostAndKeepsAStableToken(t *testing.T) {
	h := initWithProject(t)
	out := runBoard(t, h)
	if !regexp.MustCompile(`board: "http://127\.0\.0\.1:[0-9]+/"`).MatchString(out) || strings.Contains(out, "token=") || !strings.Contains(out, "hand open") {
		t.Fatalf("board out = %q", out)
	}
	base, stop := startBoard(t, h, "127.0.0.1")
	fleetURL, token := fleetPage(t, base, h)
	info, err := os.Stat(filepath.Join(h.home, "board.token"))
	if err != nil || len(token) != 48 {
		t.Fatalf("token file = %v, %v, %d chars", info, err, len(token))
	}
	stop()
	base, _ = startBoard(t, h, "127.0.0.1")
	again, _ := fleetPage(t, base, h)
	if code, _ := login(t, again, token); code != http.StatusOK {
		t.Fatalf("the token changed across boards: %s login = %d", fleetURL, code)
	}
}

func TestBoardRejectsABadAddress(t *testing.T) {
	h := initWithProject(t)
	if _, _, code := h.run("board", "--addr", "not-an-address"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestBoardFixesTokenPermissionsAndRefusesAMalformedToken(t *testing.T) {
	h := initWithProject(t)
	path := filepath.Join(h.home, "board.token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 48)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, stop := startBoard(t, h, "127.0.0.1")
	fleetURL, _ := fleetPage(t, base, h)
	if code, _ := login(t, fleetURL, strings.Repeat("a", 48)); code != http.StatusOK {
		t.Fatalf("existing token not reused: login = %d", code)
	}
	stop()
	id := fleetURL[strings.LastIndex(fleetURL, "/"):]
	for _, bad := range []string{"short", strings.Repeat("#", 48)} {
		if err := os.WriteFile(path, []byte(bad+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		base, stop := startBoard(t, h, "127.0.0.1")
		res, err := http.Get(base + id + "/")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusInternalServerError || !strings.Contains(string(b), "not a board token") || strings.Contains(string(b), h.home) {
			t.Fatalf("malformed token %q: %d %s", bad, res.StatusCode, b)
		}
		stop()
	}
}

func TestBoardWarnsOnANetworkAddress(t *testing.T) {
	h := initWithProject(t)
	if out := runBoard(t, h, "0.0.0.0:0"); !strings.Contains(out, "plain HTTP") {
		t.Fatalf("no warning for a network address: %q", out)
	}
	if out := runBoard(t, h); strings.Contains(out, "plain HTTP") {
		t.Fatalf("loopback board warns: %q", out)
	}
}

func TestOneBoardServesTwoFleets(t *testing.T) {
	a := newHarness(t)
	a.ok("init", "--name", "alpha")
	b := newHarness(t)
	b.vars = a.vars
	b.ok("init", "--name", "beta")
	base, _ := startBoard(t, a, "127.0.0.1")
	urlA, tokenA := fleetPage(t, base, a)
	urlB, tokenB := fleetPage(t, base, b)
	if tokenA == tokenB {
		t.Fatal("two fleets share one token")
	}
	if code, body := login(t, urlA, tokenA); code != http.StatusOK || !strings.Contains(body, `<h1 class="fleet-name" title="alpha">alpha</h1>`) {
		t.Fatalf("alpha = %d\n%s", code, body)
	}
	if code, body := login(t, urlB, tokenB); code != http.StatusOK || !strings.Contains(body, `<h1 class="fleet-name" title="beta">beta</h1>`) {
		t.Fatalf("beta = %d\n%s", code, body)
	}
	if code, _ := login(t, urlB, tokenA); code != http.StatusForbidden {
		t.Fatalf("beta with alpha's token = %d", code)
	}
	res, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(list), "alpha") || !strings.Contains(string(list), "beta") {
		t.Fatalf("fleet list:\n%s", list)
	}
}

func TestTheRailListsOnlyFleetsTheBrowserHoldsALiveTokenFor(t *testing.T) {
	a := newHarness(t)
	a.ok("init", "--name", "alpha")
	b := newHarness(t)
	b.vars = a.vars
	b.ok("init", "--name", "beta")
	c := newHarness(t)
	c.vars = a.vars
	c.ok("init", "--name", "gamma")
	base, _ := startBoard(t, a, "127.0.0.1")
	urlA, tokenA := fleetPage(t, base, a)
	urlB, tokenB := fleetPage(t, base, b)
	fleetPage(t, base, c)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(u string) string {
		t.Helper()
		res, err := (&http.Client{Jar: jar}).Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d %v", u, res.StatusCode, err)
		}
		return string(body)
	}
	label := func(name string) string { return `<span class="label">` + name + `</span>` }
	get(urlA + "/?token=" + tokenA)
	if body := get(urlA + "/"); !strings.Contains(body, label("alpha")) || strings.Contains(body, "beta") || strings.Contains(body, "gamma") {
		t.Fatalf("one login lists more than its fleet:\n%s", body)
	}
	get(urlB + "/?token=" + tokenB)
	body := get(urlA + "/")
	if !strings.Contains(body, label("alpha")) || !strings.Contains(body, label("beta")) || strings.Contains(body, "gamma") {
		t.Fatalf("two logins:\n%s", body)
	}
	path := filepath.Join(b.home, "board.token")
	if err := os.WriteFile(path, []byte(strings.Repeat("e", 48)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body := get(urlA + "/"); strings.Contains(body, "beta") {
		t.Fatalf("a rotated token still lists its fleet:\n%s", body)
	}
}

func TestAPairedPhoneListsItsFleetsThroughTheRealBoard(t *testing.T) {
	a := newHarness(t)
	a.ok("init", "--name", "alpha")
	b := newHarness(t)
	b.vars = a.vars
	b.ok("init", "--name", "beta")
	c := newHarness(t)
	c.vars = a.vars
	c.ok("init", "--name", "gamma")
	base, _ := startBoard(t, a, "127.0.0.1")
	urlA, tokenA := fleetPage(t, base, a)
	urlB, tokenB := fleetPage(t, base, b)
	fleetPage(t, base, c)
	proxied := map[string]string{"X-Forwarded-For": "100.64.0.7"}
	send := func(method, u string, form url.Values, cookies []*http.Cookie) (*http.Response, string) {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequest(method, u, body)
		if err != nil {
			t.Fatal(err)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for k, v := range proxied {
			req.Header.Set(k, v)
		}
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res, string(out)
	}
	pair := func(fleetURL, token, id string) *http.Cookie {
		t.Helper()
		_, page := send("POST", fleetURL+"/connect/code", url.Values{"csrf": {token}, "base": {"https://laptop.example"}}, []*http.Cookie{{Name: "hand_board_" + id, Value: token}})
		m := regexp.MustCompile(`class="code-big"[^>]*>([0-9A-Z]{5}-[0-9A-Z]{5})<`).FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("no pairing code:\n%s", page)
		}
		res, _ := send("POST", fleetURL+"/pair", url.Values{"code": {m[1]}, "name": {"Pixel"}}, nil)
		for _, ck := range res.Cookies() {
			if ck.Name == "hand_phone_"+id {
				return ck
			}
		}
		t.Fatalf("pair = %d, no phone cookie", res.StatusCode)
		return nil
	}
	idA, idB := path.Base(urlA), path.Base(urlB)
	phone := []*http.Cookie{pair(urlA, tokenA, idA), pair(urlB, tokenB, idB)}
	_, home := send("GET", base+"/", nil, phone)
	if !strings.Contains(home, ">alpha</a>") || !strings.Contains(home, ">beta</a>") || strings.Contains(home, "gamma") {
		t.Fatalf("the phone's fleet list:\n%s", home)
	}
	if _, nobody := send("GET", base+"/", nil, nil); strings.Contains(nobody, "alpha") || strings.Contains(nobody, "beta") {
		t.Fatalf("a proxied request with no phone session sees fleets:\n%s", nobody)
	}
	res, page := send("GET", urlA+"/", nil, phone)
	if res.StatusCode != http.StatusOK || !strings.Contains(page, `<span class="label">beta</span>`) || strings.Contains(page, "gamma") {
		t.Fatalf("alpha's rail on the phone = %d:\n%s", res.StatusCode, page)
	}
}

func TestOneCodePairsAPhoneWithSeveralFleetsThroughTheRealBoard(t *testing.T) {
	a := newHarness(t)
	a.ok("init", "--name", "alpha")
	b := newHarness(t)
	b.vars = a.vars
	b.ok("init", "--name", "beta")
	c := newHarness(t)
	c.vars = a.vars
	c.ok("init", "--name", "gamma")
	base, _ := startBoard(t, a, "127.0.0.1")
	urlA, tokenA := fleetPage(t, base, a)
	urlB, tokenB := fleetPage(t, base, b)
	urlC, _ := fleetPage(t, base, c)
	idA, idB, idC := path.Base(urlA), path.Base(urlB), path.Base(urlC)
	proxied := map[string]string{"X-Forwarded-For": "100.64.0.7"}
	send := func(method, u string, form url.Values, cookies []*http.Cookie) (*http.Response, string) {
		t.Helper()
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req, err := http.NewRequest(method, u, body)
		if err != nil {
			t.Fatal(err)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for k, v := range proxied {
			req.Header.Set(k, v)
		}
		for _, ck := range cookies {
			req.AddCookie(ck)
		}
		res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res, string(out)
	}
	laptop := []*http.Cookie{{Name: "hand_board_" + idA, Value: tokenA}, {Name: "hand_board_" + idB, Value: tokenB}}
	_, page := send("POST", urlA+"/connect/code", url.Values{"csrf": {tokenA}, "base": {"https://laptop.example"}, "fleet": {idC}}, laptop)
	if strings.Contains(page, "pairing link") || !strings.Contains(page, "not signed in to every fleet you chose") {
		t.Fatalf("a fleet the laptop holds no token for:\n%s", page)
	}
	_, page = send("POST", urlA+"/connect/code", url.Values{"csrf": {tokenA}, "base": {"https://laptop.example"}, "fleet": {idB}}, laptop)
	m := regexp.MustCompile(`class="code-big"[^>]*>([0-9A-Z]{5}-[0-9A-Z]{5})<`).FindStringSubmatch(page)
	if m == nil || !strings.Contains(page, "It pairs the phone with alpha and beta.") {
		t.Fatalf("no code for two fleets:\n%s", page)
	}
	res, result := send("POST", urlA+"/pair", url.Values{"code": {m[1]}, "name": {"Pixel"}}, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(result, "Paired with alpha and beta.") || strings.Contains(result, "gamma") {
		t.Fatalf("pair = %d:\n%s", res.StatusCode, result)
	}
	var phone []*http.Cookie
	for _, ck := range res.Cookies() {
		if ck.MaxAge > 0 {
			phone = append(phone, ck)
		}
	}
	if len(phone) != 2 {
		t.Fatalf("cookies = %v", phone)
	}
	for _, fleetURL := range []string{urlA, urlB} {
		res, page := send("GET", fleetURL+"/", nil, phone)
		if res.StatusCode != http.StatusOK || !strings.Contains(page, `href="/`+idA+`/"`) || !strings.Contains(page, `href="/`+idB+`/"`) || strings.Contains(page, "gamma") {
			t.Fatalf("%s on the phone = %d:\n%s", fleetURL, res.StatusCode, page)
		}
		if strings.Contains(page, tokenA) || strings.Contains(page, tokenB) {
			t.Fatalf("%s holds a fleet token", fleetURL)
		}
	}
	if res, _ := send("GET", urlC+"/", nil, phone); res.StatusCode != http.StatusForbidden {
		t.Fatalf("a fleet outside the code = %d", res.StatusCode)
	}
	if err := os.WriteFile(filepath.Join(b.home, "board.token"), []byte(strings.Repeat("d", 48)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _ := send("GET", urlB+"/", nil, phone); res.StatusCode != http.StatusForbidden {
		t.Fatalf("beta after its token changed = %d", res.StatusCode)
	}
	if res, _ := send("GET", urlA+"/", nil, phone); res.StatusCode != http.StatusOK {
		t.Fatalf("alpha after beta's token changed = %d", res.StatusCode)
	}
}

func TestAMovedFleetIsFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("moving a home whose files are open is Unix-only")
	}
	h := initWithProject(t)
	base, _ := startBoard(t, h, "127.0.0.1")
	fleetURL, token := fleetPage(t, base, h)
	if code, _ := login(t, fleetURL, token); code != http.StatusOK {
		t.Fatalf("before the move = %d", code)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(h.home, moved); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(fleetURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("a fleet whose home is gone = %d, want 404", res.StatusCode)
	}
	h.home = moved
	h.ok("init")
	h.ok("task", "add", "hand", "Filed after the move")
	fresh := strings.Repeat("c", 48)
	if err := os.WriteFile(filepath.Join(moved, "board.token"), []byte(fresh+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := login(t, fleetURL, token); code != http.StatusForbidden {
		t.Fatalf("the old home's token still works: %d", code)
	}
	if code, body := login(t, fleetURL, fresh); code != http.StatusOK || !strings.Contains(body, "Filed after the move") {
		t.Fatalf("after the move = %d\n%s", code, body)
	}
}

func TestBoardAddrIsWrittenAndRemoved(t *testing.T) {
	h := initWithProject(t)
	base, stop := startBoard(t, h, "127.0.0.1")
	b, err := os.ReadFile(addrFile(h))
	if err != nil || !regexp.MustCompile(`^127\.0\.0\.1:[0-9]+\n$`).Match(b) || "http://"+strings.TrimSpace(string(b)) != base {
		t.Fatalf("board.addr = %q, %v", b, err)
	}
	res, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("the recorded address answers %d", res.StatusCode)
	}
	stop()
	if _, err := os.Stat(addrFile(h)); !os.IsNotExist(err) {
		t.Fatalf("board.addr after stop: %v", err)
	}
	_, stop = startBoard(t, h, "127.0.0.1")
	if err := os.WriteFile(addrFile(h), []byte("127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop()
	if b, err := os.ReadFile(addrFile(h)); err != nil || string(b) != "127.0.0.1:1\n" {
		t.Fatalf("another board's address was removed: %q, %v", b, err)
	}
}

func TestANetworkBoardRecordsALoopbackAddress(t *testing.T) {
	h := initWithProject(t)
	base, _ := startBoard(t, h, "0.0.0.0")
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("board.addr for 0.0.0.0 = %q", base)
	}
}

func postBoard(t *testing.T, base, token, path string, form url.Values) int {
	t.Helper()
	form.Set("csrf", token)
	req, err := http.NewRequest("POST", base+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "hand_board_" + base[strings.LastIndex(base, "/")+1:], Value: token})
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

func TestALoopbackBoardServesControls(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	base, stop := startBoard(t, h, "127.0.0.1")
	fleetURL, token := fleetPage(t, base, h)
	if code := postBoard(t, fleetURL, token, "/supervisor/send", url.Values{"text": {"hello from the board"}}); code != http.StatusSeeOther {
		t.Fatalf("send = %d", code)
	}
	if got := rt.prompts(); !slices.Equal(got, []string{"hello from the board"}) {
		t.Fatalf("prompts = %q", got)
	}
	if out := stop(); !strings.Contains(out, "Chat with the supervisor") {
		t.Fatalf("board out = %q", out)
	}
}

func TestANetworkBoardIsReadOnly(t *testing.T) {
	h, rt := newSupervisorFixture(t)
	startClaudeSupervisor(h)
	base, stop := startBoard(t, h, "0.0.0.0")
	fleetURL, token := fleetPage(t, base, h)
	if code := postBoard(t, fleetURL, token, "/supervisor/send", url.Values{"text": {"hello"}}); code != http.StatusForbidden {
		t.Fatalf("send on a network board = %d", code)
	}
	if got := rt.prompts(); len(got) != 0 {
		t.Fatalf("prompts = %q", got)
	}
	if out := stop(); !strings.Contains(out, "Supervisor controls are off on a network address; answering decisions and acknowledging reports still work") {
		t.Fatalf("board out = %q", out)
	}
}

func TestARotatedTokenWorksWithoutARestart(t *testing.T) {
	h := initWithProject(t)
	base, _ := startBoard(t, h, "127.0.0.1")
	fleetURL, old := fleetPage(t, base, h)
	if code, _ := login(t, fleetURL, old); code != http.StatusOK {
		t.Fatalf("first token = %d", code)
	}
	path := filepath.Join(h.home, "board.token")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	fresh := strings.Repeat("d", 48)
	if err := os.WriteFile(path, []byte(fresh+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := login(t, fleetURL, fresh); code != http.StatusOK {
		t.Fatalf("rotated token = %d", code)
	}
	if code, _ := login(t, fleetURL, old); code != http.StatusForbidden {
		t.Fatalf("the old token still works: %d", code)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	remade, _ := fleetPage(t, base, h)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the board did not remake a deleted token: %v", err)
	}
	if code, _ := login(t, remade, strings.TrimSpace(string(b))); code != http.StatusOK {
		t.Fatalf("remade token = %d", code)
	}
}

func TestASecondBoardOnTheSameAddressLeavesTheFirstAlone(t *testing.T) {
	h := newHarness(t)
	base, stop := startBoard(t, h, "127.0.0.1")
	if _, _, code := h.run("board", "--addr", strings.TrimPrefix(base, "http://")); code == 0 {
		t.Fatal("a second board bound the same address")
	}
	if b, err := os.ReadFile(addrFile(h)); err != nil || strings.TrimSpace(string(b)) != strings.TrimPrefix(base, "http://") {
		t.Fatalf("board.addr = %q, %v", b, err)
	}
	wantPID(t, filepath.Join(h.vars["SECONDHAND_HOME"], "board.pid"))
	if res, err := http.Get(base + "/"); err != nil {
		t.Fatalf("the first board stopped answering: %v", err)
	} else {
		_ = res.Body.Close()
	}
	stop()
}

func proxiedSend(t *testing.T, method, u string, form url.Values, cookies []*http.Cookie) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("X-Forwarded-For", "100.64.0.7")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(out)
}

func TestARevokeEverywhereReachesEveryFleetThroughTheRealBoard(t *testing.T) {
	a := newHarness(t)
	a.ok("init", "--name", "alpha")
	b := newHarness(t)
	b.vars = a.vars
	b.ok("init", "--name", "beta")
	c := newHarness(t)
	c.vars = a.vars
	c.ok("init", "--name", "gamma")
	d := newHarness(t)
	d.vars = a.vars
	d.ok("init", "--name", "delta")
	base, _ := startBoard(t, a, "127.0.0.1")
	urlA, tokenA := fleetPage(t, base, a)
	urlB, tokenB := fleetPage(t, base, b)
	urlC, tokenC := fleetPage(t, base, c)
	urlD, tokenD := fleetPage(t, base, d)
	idA, idB, idC, idD := path.Base(urlA), path.Base(urlB), path.Base(urlC), path.Base(urlD)
	login := func(id, token string) *http.Cookie { return &http.Cookie{Name: "hand_board_" + id, Value: token} }
	all := []*http.Cookie{login(idA, tokenA), login(idB, tokenB), login(idC, tokenC), login(idD, tokenD)}
	_, page := proxiedSend(t, "POST", urlA+"/connect/code", url.Values{"csrf": {tokenA}, "base": {"https://laptop.example"}, "fleet": {idB, idC}}, all)
	m := regexp.MustCompile(`class="code-big"[^>]*>([0-9A-Z]{5}-[0-9A-Z]{5})<`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no code:\n%s", page)
	}
	res, result := proxiedSend(t, "POST", urlA+"/pair", url.Values{"code": {m[1]}, "name": {"Pixel"}}, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(result, "Paired with alpha, beta and gamma.") {
		t.Fatalf("pair = %d:\n%s", res.StatusCode, result)
	}
	var phone []*http.Cookie
	for _, ck := range res.Cookies() {
		if ck.MaxAge > 0 {
			phone = append(phone, ck)
		}
	}
	if len(phone) != 3 {
		t.Fatalf("cookies = %v", phone)
	}
	opens := func(fleetURL string) int {
		res, _ := proxiedSend(t, "GET", fleetURL+"/", nil, phone)
		return res.StatusCode
	}
	_, list := proxiedSend(t, "GET", urlA+"/connect", nil, all[:3])
	for _, want := range []string{`class="device device-group"`, `aria-label="Revoke Pixel in alpha"`, `aria-label="Revoke Pixel in beta"`, `aria-label="Revoke Pixel in gamma"`, `action="/` + idA + `/connect/revoke/everywhere"`} {
		if !strings.Contains(list, want) {
			t.Fatalf("the Phone page lacks %q:\n%s", want, list)
		}
	}
	if strings.Contains(list, "delta") {
		t.Fatalf("the Phone page names a fleet the phone is not in:\n%s", list)
	}
	label := regexp.MustCompile(`name="phone" value="([0-9a-f]{16})"`).FindStringSubmatch(list)
	if label == nil {
		t.Fatalf("no label in the forms:\n%s", list)
	}
	res, _ = proxiedSend(t, "POST", urlA+"/connect/revoke/in", url.Values{"csrf": {tokenA}, "phone": {label[1]}, "fleet": {idB}}, all[:1])
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("a revoke in beta with no login for beta = %d", res.StatusCode)
	}
	if got := opens(urlB); got != http.StatusOK {
		t.Fatalf("beta after a refused revoke = %d", got)
	}
	res, _ = proxiedSend(t, "POST", urlA+"/connect/revoke/in", url.Values{"csrf": {tokenA}, "phone": {label[1]}, "fleet": {idB}}, all[:2])
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke in beta = %d", res.StatusCode)
	}
	if opens(urlB) != http.StatusForbidden || opens(urlA) != http.StatusOK || opens(urlC) != http.StatusOK {
		t.Fatalf("revoking in beta left alpha %d, beta %d, gamma %d", opens(urlA), opens(urlB), opens(urlC))
	}
	res, _ = proxiedSend(t, "POST", urlA+"/connect/revoke/everywhere", url.Values{"csrf": {tokenA}, "phone": {label[1]}}, []*http.Cookie{all[0], all[2]})
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke everywhere = %d", res.StatusCode)
	}
	for name, fleetURL := range map[string]string{"alpha": urlA, "beta": urlB, "gamma": urlC} {
		if got := opens(fleetURL); got != http.StatusForbidden {
			t.Fatalf("a revoked phone opens %s = %d", name, got)
		}
	}
	_, after := proxiedSend(t, "GET", urlA+"/connect", nil, all[:3])
	if !strings.Contains(after, "No phone is paired.") {
		t.Fatalf("the Phone page after a revoke everywhere:\n%s", after)
	}
}
