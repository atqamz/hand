package cli

import (
	"context"
	"testing"
	"time"
)

func TestExitLogIgnoresARecordOlderThanExitKeep(t *testing.T) {
	l := &exitLog{m: map[string]exitRecord{
		"old":   {"terminal exited (code 3)", time.Now().Add(-exitKeep - time.Second)},
		"fresh": {"terminal exited (code 4)", time.Now()},
	}}
	l.budget()
	if got := l.wait(context.Background(), "old"); got != "terminal exited" {
		t.Fatalf("old record reported as %q", got)
	}
	if got := l.wait(context.Background(), "fresh"); got != "terminal exited (code 4)" {
		t.Fatalf("fresh record reported as %q", got)
	}
}
