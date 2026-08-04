//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/state"
)

// TestStatusFlagsATerminalReportNoWatcherEverRead drives atqamz/secondhand#70 end
// to end: a worker that finished while no watcher was attached has to be visible
// to the next hand status, and has to stop being flagged once a watcher really
// consumed it. Only real processes prove the second half, because what clears the
// flag is the report_offset a watcher persisted to state/hand.db.
func TestStatusFlagsATerminalReportNoWatcherEverRead(t *testing.T) {
	home := seedOneTaskHome(t)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR up\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fleet := runHand(t, home, "status")
	if !strings.Contains(fleet.stdout, "(reported: done, unacknowledged)") {
		t.Fatalf("hand status stdout %q, want the completion no watcher read flagged unacknowledged", fleet.stdout)
	}
	single := runHand(t, home, "status", "task-1")
	if !strings.Contains(single.stdout, "done: PR up (unacknowledged)") {
		t.Fatalf("hand status task-1 stdout %q, want the same flag in the detail view", single.stdout)
	}

	// Exit 4, not 0: arming consumes the backlog into state/events.log and the
	// notify hook without printing it, and only a later event is fleet news. That
	// consumption is exactly what acknowledges the report.
	armed := runHand(t, home, "watch", "--until-event", "--poll", "30ms", "--timeout", "300ms")
	if armed.code != 4 {
		t.Fatalf("hand watch --until-event: exit %d, want 4 (stderr %q)", armed.code, armed.stderr)
	}

	after := runHand(t, home, "status")
	if !strings.Contains(after.stdout, "reported: done") {
		t.Fatalf("hand status stdout %q, want the reported state still shown", after.stdout)
	}
	if strings.Contains(after.stdout, "unacknowledged") {
		t.Fatalf("hand status stdout %q, want the flag cleared once a watcher consumed the report", after.stdout)
	}
}
