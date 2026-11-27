//go:build e2e

package main

import (
	"fmt"
	"image/png"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	probeToken  = "PROBETOKEN"
	probeCookie = "hand_board_f0"
	boardPolicy = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'"
	probeSettle = 5 * time.Second
)

type probeHit struct{ server, method, uri, cookie string }

type probeLog struct {
	mu   sync.Mutex
	hits []probeHit
}

func (l *probeLog) add(server string, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits = append(l.hits, probeHit{server, r.Method, r.URL.RequestURI(), r.Header.Get("Cookie")})
}

func (l *probeLog) all() []probeHit {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]probeHit(nil), l.hits...)
}

func (l *probeLog) saw(server, prefix string) bool {
	for _, h := range l.all() {
		if h.server == server && strings.HasPrefix(h.uri, prefix) {
			return true
		}
	}
	return false
}

const hostilePage = `<!doctype html><html><head>
<meta http-equiv="refresh" content="1;url=%[3]s/metarefresh">
<base href="%[1]s/base/">
<link rel="stylesheet" href="%[1]s/css">
</head><body>
<h1>hostile</h1>
<img src="%[1]s/img"><img src="relative-img"><img src="%[2]s/img-other-host">
<iframe src="%[1]s/iframe"></iframe>
<a id=l1 href="%[1]s/link">link</a>
<a id=l2 href="relative-link">relative</a>
<a id=l3 href="javascript:fetch('%[1]s/jsurl')">js</a>
<a id=l4 href="%[1]s/dl" download="x.bin">dl</a>
<a id=l5 target=_blank href="%[1]s/blank">blank</a>
<a id=l6 href="%[2]s/other-host-link">other host</a>
<a id=l7 href="%[3]s/samelink">same origin</a>
<a id=l8 target=_blank href="%[3]s/sameblank">same origin blank</a>
<form id=f method=post action="%[3]s/form"><input name=a value=1></form>
<script>
fetch('%[3]s/self/script-ran');
setTimeout(function(){
 try{window.open('%[3]s/winopen');window.open('%[1]s/winopen-other')}catch(e){}
 try{document.getElementById('f').submit()}catch(e){}
 try{fetch('%[1]s/fetch',{mode:'no-cors',credentials:'include'})}catch(e){}
 try{var x=new XMLHttpRequest();x.open('GET','%[1]s/xhr');x.send()}catch(e){}
 try{navigator.sendBeacon('%[1]s/beacon','x')}catch(e){}
 try{new Image().src='%[1]s/scriptimg'}catch(e){}
 try{new WebSocket('ws://'+'%[1]s'.slice(7)+'/ws')}catch(e){}
 try{navigator.geolocation.getCurrentPosition(function(){fetch('%[3]s/granted/geo')},function(){})}catch(e){}
 try{Notification.requestPermission().then(function(p){if(p==='granted')fetch('%[3]s/granted/notification')})}catch(e){}
 try{navigator.mediaDevices.getUserMedia({audio:true}).then(function(){fetch('%[3]s/granted/mic')}).catch(function(){})}catch(e){}
 try{navigator.clipboard.readText().then(function(){fetch('%[3]s/granted/clipboard')}).catch(function(){})}catch(e){}
 try{window.print()}catch(e){}
 try{location.href='%[3]s/locationhref'}catch(e){}
},500);
setTimeout(function(){for(var i=1;i<=8;i++){document.getElementById('l'+i).click()}},1500);
setTimeout(function(){fetch('%[3]s/self/done')},2500);
</script></body></html>`

const renderPage = `<!doctype html><html><head><style>
body{margin:0;background:#fff}
#a{width:300px;height:200px;background:#ff00ff}
#b{width:300px;height:200px;background:#00ff00}
</style></head><body><div id=a></div><div id=b></div></body></html>`

func probeServers(t *testing.T, policy string) (*probeLog, string, string, string) {
	t.Helper()
	listen := func(addr string) net.Listener {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Skipf("cannot listen on %s: %v", addr, err)
		}
		t.Cleanup(func() { _ = l.Close() })
		return l
	}
	board, otherPort, otherHost := listen("127.0.0.1:0"), listen("127.0.0.1:0"), listen("127.0.0.2:0")
	boardURL, portURL, hostURL := "http://"+board.Addr().String(), "http://"+otherPort.Addr().String(), "http://"+otherHost.Addr().String()
	log := &probeLog{}
	go func() {
		_ = http.Serve(board, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.add("board", r)
			h := w.Header()
			switch {
			case strings.HasPrefix(r.URL.Path, "/page"):
				h.Set("Content-Type", "text/html; charset=utf-8")
				if policy != "" {
					h.Set("Content-Security-Policy", policy)
				}
				h.Set("X-Content-Type-Options", "nosniff")
				fmt.Fprintf(w, hostilePage, portURL, hostURL, boardURL)
			case strings.HasPrefix(r.URL.Path, "/render"):
				h.Set("Content-Type", "text/html; charset=utf-8")
				h.Set("Content-Security-Policy", policy)
				fmt.Fprint(w, renderPage)
			default:
				w.WriteHeader(http.StatusNoContent)
			}
		}))
	}()
	for name, l := range map[string]net.Listener{"other-port": otherPort, "other-host": otherHost} {
		go func() {
			_ = http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				log.add(name, r)
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, "<html><body>attacker</body></html>")
			}))
		}()
	}
	return log, boardURL, portURL, hostURL
}

func startProbe(t *testing.T, bin, url string) {
	t.Helper()
	if os.Getenv("DISPLAY") == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("DISPLAY is not set")
		}
		t.Skip("no X display: run under xvfb-run")
	}
	home := t.TempDir()
	cmd := exec.Command(bin)
	var env []string
	for _, kv := range os.Environ() {
		if !denied.MatchString(kv) {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "HOME="+home, "XDG_DATA_HOME="+filepath.Join(home, "data"), "XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"), "XDG_STATE_HOME="+filepath.Join(home, "state"),
		"HAND_WEBPROBE_URL="+url, "HAND_WEBPROBE_COOKIE="+probeCookie, "HAND_WEBPROBE_TOKEN="+probeToken, "HAND_WEBPROBE_DOMAIN=127.0.0.1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out := &strings.Builder{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = stdin.Close()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		t.Logf("probe output: %s", out)
		if strings.Contains(out.String(), "open error: ") && !strings.Contains(out.String(), "open error: <nil>") {
			t.Errorf("the probe could not open the page: %s", out)
		}
	})
}

func probeBinary(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the probe window runs on Linux")
	}
	bin := filepath.Join(t.TempDir(), "probe")
	goBuild(t, ".", bin, "e2e", ".")
	return bin
}

func until(t *testing.T, what string, within time.Duration, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(within); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func settle(t *testing.T, log *probeLog, finish string) bool {
	t.Helper()
	until(t, "the page request", 90*time.Second, func() bool { return log.saw("board", "/page") })
	finished := finish == ""
	for end := time.Now().Add(30 * time.Second); !finished && time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		finished = log.saw("board", finish)
	}
	end := time.Now().Add(probeSettle)
	until(t, "the page to finish", 2*probeSettle, func() bool { return time.Now().After(end) })
	return finished
}

func TestRealWebViewKeepsAHostilePageOnTheBoardsOrigin(t *testing.T) {
	bin := probeBinary(t)
	for name, tc := range map[string]struct{ policy, ran string }{
		"engine alone":       {"", "/self/done"},
		"with the Board CSP": {boardPolicy, ""},
	} {
		t.Run(name, func(t *testing.T) {
			log, boardURL, _, _ := probeServers(t, tc.policy)
			startProbe(t, bin, boardURL+"/page")
			finished := settle(t, log, tc.ran)
			for _, h := range log.all() {
				t.Logf("%s %s %s", h.server, h.method, h.uri)
				if h.server != "board" {
					t.Errorf("%s was reached: %s %s cookie=%q", h.server, h.method, h.uri, h.cookie)
					continue
				}
				if !strings.HasPrefix(h.uri, "/page") && h.uri != "/self/script-ran" && h.uri != "/self/done" {
					t.Errorf("the page reached the Board outside its own load: %s %s", h.method, h.uri)
				}
				if strings.HasPrefix(h.uri, "/page") && !strings.Contains(h.cookie, probeCookie+"="+probeToken) {
					t.Errorf("the page request carries no session cookie: %q", h.cookie)
				}
			}
			if tc.ran != "" && !finished {
				t.Error("the hostile script did not run to its end, so the test proved nothing")
			}
			if tc.ran == "" && log.saw("board", "/self/") {
				t.Error("a script ran under the Board's policy")
			}
		})
	}
}

func TestRealWebViewDrawsAnHTMLPageUnderTheBoardsPolicy(t *testing.T) {
	if _, err := exec.LookPath("import"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("ImageMagick import is not installed")
		}
		t.Skip("ImageMagick import is not installed")
	}
	bin := probeBinary(t)
	log, boardURL, _, _ := probeServers(t, boardPolicy)
	startProbe(t, bin, boardURL+"/render")
	until(t, "the page request", 90*time.Second, func() bool { return log.saw("board", "/render") })
	shot := filepath.Join(t.TempDir(), "screen.png")
	best := [2]int{}
	until(t, "the page drawn in its two colours", 60*time.Second, func() bool {
		if b, err := exec.Command("import", "-window", "root", shot).CombinedOutput(); err != nil {
			t.Fatalf("import: %v\n%s", err, b)
		}
		f, err := os.Open(shot)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		var pink, green int
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y += 2 {
			for x := b.Min.X; x < b.Max.X; x += 2 {
				r, g, bl, _ := img.At(x, y).RGBA()
				switch {
				case r > 0xf000 && g < 0x1000 && bl > 0xf000:
					pink++
				case r < 0x1000 && g > 0xf000 && bl < 0x1000:
					green++
				}
			}
		}
		best = [2]int{max(best[0], pink), max(best[1], green)}
		return pink >= renderMin && green >= renderMin
	})

	t.Logf("pixels sampled every second row and column: pink %d, green %d", best[0], best[1])
}

const renderMin = 8000
