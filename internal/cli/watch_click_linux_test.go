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

func setBoardAddr(t *testing.T, fx *attemptFixture) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fx.h.vars["SECONDHAND_HOME"], "board.addr"), []byte("127.0.0.1:7777\n"), 0o644); err != nil {
		t.Fatal(err)
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
	notifyLog, _, openLog, _ := installClick(t, fx, map[string]string{"help": clickHelp, "sleep": "500ms"})
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

func TestWatchKillsAnUnclickedNotificationAtTheTimeout(t *testing.T) {
	old := *cli.ClickWait
	*cli.ClickWait = 300 * time.Millisecond
	t.Cleanup(func() { *cli.ClickWait = old })
	fx := newAttemptFixture(t)
	setBoardAddr(t, fx)
	notifyLog, _, openLog, pidLog := installClick(t, fx, map[string]string{"help": clickHelp, "sleep": "5s", "print": "default\n"})
	fx.start()
	stop := startWatch(t, fx)
	defer stop()
	quietAlert(fx)
	eventually(t, func() bool { return len(readLines(pidLog)) == 1 && readLines(pidLog)[0] != "" })
	pid, err := strconv.Atoi(readLines(pidLog)[0])
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return syscall.Kill(pid, 0) != nil })
	if len(readLines(notifyLog)) != 1 {
		t.Fatalf("notification did not run: %q", notifyLog)
	}
	if _, err := os.Stat(openLog); !os.IsNotExist(err) {
		t.Fatalf("opener ran after the timeout")
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
