package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/state"
)

func setupSendHome(t *testing.T, herdrScript string) string {
	t.Helper()
	home := t.TempDir()
	t.Chdir(home)

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(herdrScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return home
}

func TestSendHappyPathWhenIdle(t *testing.T) {
	home := setupSendHome(t, `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"idle"}}}'
	;;
"pane send-text")
 printf '{"id":"cli:1","result":{}}'
	;;
"pane send-keys")
 printf '{"id":"cli:1","result":{}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello worker"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestSendFailsWhenPaneNotFound(t *testing.T) {
	home := setupSendHome(t, `#!/bin/sh
printf '{"id":"cli:1","error":{"code":"pane_not_found","message":"no such pane"}}'
exit 1
`)
	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:gone"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got err %v, want pane not found", err)
	}
}

func TestSendWaitsWhileBusyThenSends(t *testing.T) {
	counterFile := filepath.Join(t.TempDir(), "calls")
	home := setupSendHome(t, `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
"pane get")
	n=0
	if [ -f "`+counterFile+`" ]; then n=$(cat "`+counterFile+`"); fi
	n=$((n+1))
	echo "$n" > "`+counterFile+`"
	if [ "$n" -lt 2 ]; then
		printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"working"}}}'
	else
		printf '{"id":"cli:1","result":{"pane":{"pane_id":"wA:pB","agent_status":"idle"}}}'
	fi
	;;
"pane send-text")
 printf '{"id":"cli:1","result":{}}'
	;;
"pane send-keys")
 printf '{"id":"cli:1","result":{}}'
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`)
	if err := state.Write(home, state.Task{ID: "task-1", Herdr: state.Herdr{PaneID: "wA:pB"}}); err != nil {
		t.Fatal(err)
	}

	cmd := newSendCmd()
	cmd.SetArgs([]string{"task-1", "hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestSendFailsForUnknownTask(t *testing.T) {
	setupSendHome(t, "#!/bin/sh\nexit 1\n")

	cmd := newSendCmd()
	cmd.SetArgs([]string{"missing-task", "hello"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got err %v, want not found", err)
	}
}
