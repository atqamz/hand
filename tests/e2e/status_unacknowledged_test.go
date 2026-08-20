//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/state"
)

// Drives atqamz/hand#70 end to end: a report written without a watcher remains unannounced until a
// real watcher consumes it. Announcement is separate from acknowledgement (atqamz/hand#267).
func TestStatusFlagsATerminalReportNoWatcherEverRead(t *testing.T) {
	home := seedOneTaskHome(t)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR up\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fleet := runHand(t, home, "status")
	if !strings.Contains(fleet.stdout, ",done,") || !strings.Contains(fleet.stdout, "unannounced\n") {
		t.Fatalf("hand status stdout %q, want the completion no watcher read flagged unannounced", fleet.stdout)
	}

	// Exit 0: arming observes the backlog and delivers it (atqamz/hand#252), because a report written
	// while nothing was watching has no later transition to be announced by. Consuming it is what
	// announces it, whether or not it also woke someone.
	armed := runHand(t, home, "watch", "--until-event", "--poll", "30ms", "--timeout", "300ms")
	if armed.code != 0 {
		t.Fatalf("hand watch --until-event: exit %d, want 0 (stderr %q)", armed.code, armed.stderr)
	}
	if !strings.Contains(armed.stdout, "reported-done task-1: PR up") {
		t.Fatalf("hand watch --until-event stdout %q, want the unread completion delivered", armed.stdout)
	}

	after := runHand(t, home, "status")
	if !strings.Contains(after.stdout, ",done,") {
		t.Fatalf("hand status stdout %q, want the reported state still shown", after.stdout)
	}
	if strings.Contains(after.stdout, "unannounced") {
		t.Fatalf("hand status stdout %q, want the flag cleared once a watcher consumed the report", after.stdout)
	}
}

// The acknowledgement half atqamz/hand#267 added: hand ack is what clears unacknowledged, not a
// watcher, not hand status itself, and not the passage of time.
func TestHandAckClearsUnacknowledgedEndToEnd(t *testing.T) {
	home := seedOneTaskHome(t)

	if err := os.WriteFile(state.ReportPath(home, "task-1"), []byte("done: PR up\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	before := runHand(t, home, "status")
	if !strings.Contains(before.stdout, ",done,") || !strings.Contains(before.stdout, "unacknowledged") {
		t.Fatalf("hand status stdout %q, want the completion flagged unacknowledged before hand ack runs", before.stdout)
	}
	single := runHand(t, home, "status", "task-1")
	if !strings.Contains(single.stdout, "done: PR up (unacknowledged)") {
		t.Fatalf("hand status task-1 stdout %q, want the same flag in the detail view", single.stdout)
	}

	ack := runHand(t, home, "ack", "task-1", "--reason", "reviewed and merging myself")
	if ack.code != 0 {
		t.Fatalf("hand ack task-1: exit %d, want 0 (stderr %q)", ack.code, ack.stderr)
	}
	if !strings.Contains(ack.stdout, "result: acknowledged") {
		t.Fatalf("hand ack task-1 stdout %q, want an acknowledged confirmation", ack.stdout)
	}

	after := runHand(t, home, "status")
	if !strings.Contains(after.stdout, ",done,") {
		t.Fatalf("hand status stdout %q, want the reported state still shown", after.stdout)
	}
	if strings.Contains(after.stdout, "unacknowledged") {
		t.Fatalf("hand status stdout %q, want the flag cleared once hand ack has run", after.stdout)
	}
}
