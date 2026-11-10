//go:build linux

package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/fakebin"
)

const clickHelp = "Usage: notify-send [OPTION…] <SUMMARY> [BODY]\n  -A, --action=[NAME=]Text...\n  -w, --wait\n"

func readLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func installClick(t *testing.T, fx *attemptFixture, params map[string]string) (notifyLog, helpLog, openLog, pidLog string) {
	t.Helper()
	dir := t.TempDir()
	notifyLog = filepath.Join(dir, "notify.log")
	helpLog = filepath.Join(dir, "help.log")
	openLog = filepath.Join(dir, "open.log")
	pidLog = filepath.Join(dir, "pids.log")
	params["log"] = notifyLog
	params["helplog"] = helpLog
	params["pids"] = pidLog
	fakebin.Install(t, fx.h.vars["PATH"], "notify-send", "click", params)
	fakebin.Install(t, fx.h.vars["PATH"], openerName(), "opener", nil)
	fx.h.vars["XDG_LOG"] = openLog
	return notifyLog, helpLog, openLog, pidLog
}

func writeBoardAddr(t *testing.T, fx *attemptFixture, addr string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.addr"), []byte(addr), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setBoardAddr(t *testing.T, fx *attemptFixture) {
	t.Helper()
	writeBoardAddr(t, fx, "127.0.0.1:7777\n")
}

func processRunning(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	i := strings.LastIndexByte(string(b), ')')
	return i < 0 || !strings.HasPrefix(strings.TrimSpace(string(b[i+1:])), "Z")
}

func groupRunning(pgid int) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return syscall.Kill(-pgid, 0) == nil
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 3 || f[0] == "Z" {
			continue
		}
		if f[2] == strconv.Itoa(pgid) {
			return true
		}
	}
	return false
}

func waitGone(t *testing.T, what string, gone func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !gone() {
		if time.Now().After(deadline) {
			t.Fatalf("%s still running after 10s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func killClickGroup(pidLog string) {
	lines := readLines(pidLog)
	if len(lines) == 0 {
		return
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil {
		return
	}
	alive := processRunning(pid)
	if len(lines) > 1 {
		if child, err := strconv.Atoi(lines[1]); err == nil && processRunning(child) {
			alive = true
		}
	}
	if alive {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

func quietAlert(fx *attemptFixture) {
	fx.rt.srv.Publish("pane.agent_status_changed", map[string]any{"pane": "2", "status": "done", "agent": "claude"})
}

func TestWatchClickOpensTheBoardPage(t *testing.T) {
	fx := newAttemptFixture(t)
	setBoardAddr(t, fx)
	notifyLog, helpLog, openLog, _ := installClick(t, fx, map[string]string{"help": clickHelp, "print": "default\n"})
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	quietAlert(fx)
	eventually(t, func() bool { return len(readLines(notifyLog)) == 1 && readLines(notifyLog)[0] != "" })
	eventually(t, func() bool { return len(readLines(openLog)) == 1 && readLines(openLog)[0] != "" })
	if got := readLines(notifyLog)[0]; !strings.HasPrefix(got, "--app-name=hand|--action=default=Open|--wait|--|") {
		t.Fatalf("notify args = %q", got)
	}
	if got := readLines(openLog)[0]; !strings.HasPrefix(got, "http://127.0.0.1:7777/") || !strings.HasSuffix(got, "/attempt/a1") {
		t.Fatalf("opened %q", got)
	}
	if n := len(readLines(helpLog)); n != 1 {
		t.Fatalf("probe ran %d times, want 1", n)
	}
}

func TestWatchFallsBackWhenNotifySendHasNoAction(t *testing.T) {
	fx := newAttemptFixture(t)
	setBoardAddr(t, fx)
	notifyLog, helpLog, openLog, _ := installClick(t, fx, map[string]string{"help": "Usage: notify-send [OPTION…] <SUMMARY> [BODY]\n  -w, --wait\n", "print": "default\n"})
	fx.start()
	stop := startWatch(t, fx, "--every", "50ms")
	defer stop()
	fx.h.ok("decision", "ask", "t1", "First?")
	fx.h.ok("decision", "ask", "t1", "Second?")
	eventually(t, func() bool { return len(readLines(notifyLog)) == 2 })
	for _, line := range readLines(notifyLog) {
		if strings.Contains(line, "--action") || strings.Contains(line, "--wait") {
			t.Fatalf("fallback carried an action: %q", line)
		}
		if !strings.HasPrefix(line, "--app-name=hand|--|") {
			t.Fatalf("fallback args = %q", line)
		}
	}
	eventually(t, func() bool { return len(readLines(helpLog)) == 1 })
	time.Sleep(100 * time.Millisecond)
	if n := len(readLines(helpLog)); n != 1 {
		t.Fatalf("probe ran %d times, want 1", n)
	}
	if _, err := os.Stat(openLog); !os.IsNotExist(err) {
		t.Fatalf("opener ran without --action support")
	}
}

func TestWatchCapsWaitingClickNotificationsAtEight(t *testing.T) {
	fx := newAttemptFixture(t)
	setBoardAddr(t, fx)
	notifyLog, _, openLog, _ := installClick(t, fx, map[string]string{"help": clickHelp, "clickhold": "30s"})
	fx.start()
	stop := startWatch(t, fx, "--every", "50ms")
	defer stop()
	for i := 1; i <= 9; i++ {
		fx.h.ok("decision", "ask", "t1", fmt.Sprintf("Question %d?", i))
	}
	eventually(t, func() bool { return len(readLines(notifyLog)) == 9 })
	actions, plain := 0, 0
	for _, line := range readLines(notifyLog) {
		if strings.Contains(line, "--action=default=Open") {
			actions++
		} else {
			plain++
		}
	}
	if actions != 8 || plain != 1 {
		t.Fatalf("actions = %d, plain = %d; want 8 and 1", actions, plain)
	}
	if _, err := os.Stat(openLog); !os.IsNotExist(err) {
		t.Fatalf("opener ran for an unclicked notification")
	}
}

func TestWatchFreesAClickSlotWhenAClickEnds(t *testing.T) {
	fx := newAttemptFixture(t)
	setBoardAddr(t, fx)
	releasedir := t.TempDir()
	notifyLog, _, _, pidLog := installClick(t, fx, map[string]string{"help": clickHelp, "releasedir": releasedir})
	fx.start()
	stop := startWatch(t, fx, "--every", "50ms")
	defer stop()
	for i := 1; i <= 8; i++ {
		fx.h.ok("decision", "ask", "t1", fmt.Sprintf("Question %d?", i))
	}
	eventually(t, func() bool { return len(readLines(notifyLog)) == 8 })
	eventually(t, func() bool { return len(readLines(pidLog)) == 8 })
	fx.h.ok("decision", "ask", "t1", "While the cap is held?")
	eventually(t, func() bool { return len(readLines(notifyLog)) == 9 })
	if got := readLines(notifyLog)[8]; strings.Contains(got, "--action") {
		t.Fatalf("ninth click got an action while eight waited: %q", got)
	}
	pids := readLines(pidLog)
	pid, err := strconv.Atoi(pids[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releasedir, pids[0]), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	waitGone(t, "released click", func() bool { return !processRunning(pid) })
	eventually(t, func() bool {
		fx.h.ok("decision", "ask", "t1", "After the release?")
		time.Sleep(100 * time.Millisecond)
		lines := readLines(notifyLog)
		return len(lines) > 9 && strings.Contains(lines[len(lines)-1], "--action=default=Open")
	})
}

func TestWatchFallsBackWhenTheClickFails(t *testing.T) {
	fx := newAttemptFixture(t)
	setBoardAddr(t, fx)
	notifyLog, _, openLog, _ := installClick(t, fx, map[string]string{"help": clickHelp, "exit": "1"})
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	quietAlert(fx)
	eventually(t, func() bool { return len(readLines(notifyLog)) == 2 })
	lines := readLines(notifyLog)
	if !strings.Contains(lines[0], "--action=default=Open") || !strings.HasPrefix(lines[1], "--app-name=hand|--|") || strings.Contains(lines[1], "--action") {
		t.Fatalf("notifications = %q", lines)
	}
	if _, err := os.Stat(openLog); !os.IsNotExist(err) {
		t.Fatalf("opener ran for a failed click")
	}
}

func TestWatchKillsTheClickProcessGroupAtTheTimeout(t *testing.T) {
	old := *cli.ClickWait
	if *cli.ClickWait != 10*time.Minute {
		t.Fatalf("default click wait = %v, want 10m", *cli.ClickWait)
	}
	*cli.ClickWait = 5 * time.Second
	t.Cleanup(func() { *cli.ClickWait = old })
	fx := newAttemptFixture(t)
	setBoardAddr(t, fx)
	notifyLog, _, openLog, pidLog := installClick(t, fx, map[string]string{"help": clickHelp, "sleep": "30s", "child": "30", "print": "default\n"})
	fx.start()
	startWatch(t, fx)
	t.Cleanup(func() { killClickGroup(pidLog) })
	quietAlert(fx)
	eventually(t, func() bool { return len(readLines(pidLog)) == 2 })
	pids := readLines(pidLog)
	parent, err := strconv.Atoi(pids[0])
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(pids[1])
	if err != nil {
		t.Fatal(err)
	}
	if !processRunning(parent) || !processRunning(child) {
		t.Fatalf("click process group exited before the timeout: parent=%v child=%v", processRunning(parent), processRunning(child))
	}
	if len(readLines(notifyLog)) != 1 {
		t.Fatalf("notification did not run: %q", notifyLog)
	}
	waitGone(t, "click process group", func() bool {
		return !processRunning(parent) && !processRunning(child) && !groupRunning(parent)
	})
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(openLog); !os.IsNotExist(err) {
		t.Fatalf("opener ran after the timeout")
	}
	if n := len(readLines(notifyLog)); n != 1 {
		t.Fatalf("a timed-out click sent %d notifications, want 1", n)
	}
}

func TestWatchKeepsAClickAcrossAStreamReconnect(t *testing.T) {
	fx := newAttemptFixture(t)
	setBoardAddr(t, fx)
	notifyLog, _, _, pidLog := installClick(t, fx, map[string]string{"help": clickHelp, "sleep": "30s"})
	fx.start()
	stop := startWatch(t, fx)
	t.Cleanup(func() { killClickGroup(pidLog) })
	quietAlert(fx)
	eventually(t, func() bool {
		lines := readLines(notifyLog)
		return len(lines) == 1 && strings.Contains(lines[0], "--action=default=Open")
	})
	eventually(t, func() bool { return len(readLines(pidLog)) == 1 })
	pid, err := strconv.Atoi(readLines(pidLog)[0])
	if err != nil {
		t.Fatal(err)
	}
	if !processRunning(pid) {
		t.Fatal("click child exited before the reconnect")
	}
	fx.rt.srv.SetGeneration("gen-2")
	fx.rt.srv.DropSubscribers()
	eventually(t, func() bool { return fx.rt.srv.Subscribers() == 1 })
	if !processRunning(pid) {
		t.Fatal("stream reconnect killed the waiting click child")
	}
	if _, code := stop(); code != 0 {
		t.Fatalf("watch exit = %d", code)
	}
	waitGone(t, "click child", func() bool { return !processRunning(pid) })
	if n := len(readLines(notifyLog)); n != 1 {
		t.Fatalf("a cancelled click sent %d notifications, want 1", n)
	}
}

func TestWatchFallsBackWhenTheBoardAddrIsNotAnOpenableURL(t *testing.T) {
	for name, addr := range map[string]string{"empty": "\n", "garbage": "not a host\n", "port only": ":7777\n", "userinfo": "127.0.0.1:7777@evil.example\n", "portless": "127.0.0.1\n", "query": "127.0.0.1:7777?x=1\n", "fragment": "127.0.0.1:7777#x\n", "path": "127.0.0.1:7777/elsewhere\n", "non-loopback": "192.168.1.5:7777\n", "shorthand": "192.168.1:7777\n", "dns": "evil.example:7777\n", "dns port 80": "evil.example\n"} {
		t.Run(name, func(t *testing.T) {
			fx := newAttemptFixture(t)
			writeBoardAddr(t, fx, addr)
			notifyLog, _, openLog, _ := installClick(t, fx, map[string]string{"help": clickHelp, "print": "default\n"})
			fx.start()
			stop := startWatch(t, fx)
			defer stop()
			quietAlert(fx)
			eventually(t, func() bool { return len(readLines(notifyLog)) == 1 })
			if got := readLines(notifyLog)[0]; strings.Contains(got, "--action") || !strings.HasPrefix(got, "--app-name=hand|--|") {
				t.Fatalf("notify args = %q", got)
			}
			time.Sleep(100 * time.Millisecond)
			if _, err := os.Stat(openLog); !os.IsNotExist(err) {
				t.Fatalf("opener ran for board.addr %q", addr)
			}
		})
	}
}

func TestWatchClickOpensALoopbackBoardAddr(t *testing.T) {
	for name, addr := range map[string]string{"localhost": "localhost:7777\n", "ipv6": "[::1]:7777\n"} {
		t.Run(name, func(t *testing.T) {
			fx := newAttemptFixture(t)
			writeBoardAddr(t, fx, addr)
			_, _, openLog, _ := installClick(t, fx, map[string]string{"help": clickHelp, "print": "default\n"})
			fx.start()
			stop := startWatch(t, fx)
			defer stop()
			quietAlert(fx)
			eventually(t, func() bool { return len(readLines(openLog)) == 1 && readLines(openLog)[0] != "" })
			want := "http://" + strings.TrimSpace(addr) + "/"
			if got := readLines(openLog)[0]; !strings.HasPrefix(got, want) || !strings.HasSuffix(got, "/attempt/a1") {
				t.Fatalf("opened %q, want prefix %q", got, want)
			}
		})
	}
}

func TestWatchFallsBackWhenTheBoardIsOnANetworkAddress(t *testing.T) {
	fx := newAttemptFixture(t)
	writeBoardAddr(t, fx, "192.168.1.5:7777\n")
	notifyLog, _, openLog, _ := installClick(t, fx, map[string]string{"help": clickHelp, "print": "default\n"})
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	quietAlert(fx)
	eventually(t, func() bool { return len(readLines(notifyLog)) == 1 })
	if got := readLines(notifyLog)[0]; strings.Contains(got, "--action") || !strings.HasPrefix(got, "--app-name=hand|--|") {
		t.Fatalf("notify args = %q", got)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(openLog); !os.IsNotExist(err) {
		t.Fatalf("opener ran for a board on a network address")
	}
}

func TestWatchClickWithoutABoardURLUsesThePlainNotification(t *testing.T) {
	fx := newAttemptFixture(t)
	notifyLog, _, openLog, _ := installClick(t, fx, map[string]string{"help": clickHelp, "print": "default\n"})
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	quietAlert(fx)
	eventually(t, func() bool { return len(readLines(notifyLog)) == 1 })
	if got := readLines(notifyLog)[0]; strings.Contains(got, "--action") || !strings.HasPrefix(got, "--app-name=hand|--|") {
		t.Fatalf("notify args = %q", got)
	}
	if _, err := os.Stat(openLog); !os.IsNotExist(err) {
		t.Fatalf("opener ran without a board URL")
	}
}

func TestWatchDismissedClickOpensNothing(t *testing.T) {
	fx := newAttemptFixture(t)
	setBoardAddr(t, fx)
	notifyLog, _, openLog, pidLog := installClick(t, fx, map[string]string{"help": clickHelp, "sleep": "50ms"})
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	quietAlert(fx)
	eventually(t, func() bool { return len(readLines(notifyLog)) == 1 && len(readLines(pidLog)) == 1 })
	pid, err := strconv.Atoi(readLines(pidLog)[0])
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return syscall.Kill(pid, 0) != nil })
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(openLog); !os.IsNotExist(err) {
		t.Fatalf("dismissed notification opened a page")
	}
}
