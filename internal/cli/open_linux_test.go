package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenReturnsWhileTheBrowserRuns(t *testing.T) {
	fx := newOpenFixture(t)
	script := "#!/bin/sh\nread -r _ _ _ _ _ sid _ < /proc/$$/stat\nprintf '%s %s\\n' \"$sid\" \"$$\" >> \"$XDG_LOG\"\nexec /usr/bin/sleep 30\n"
	if err := os.WriteFile(filepath.Join(fx.h.vars["PATH"], "xdg-open"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out, errOut, code := fx.h.run("open")
	if code != 0 || time.Since(start) > 5*time.Second {
		t.Fatalf("open with a browser still running: code=%d after %v, stdout=%q stderr=%q", code, time.Since(start), out, errOut)
	}
	line := fx.opened(t)[0]
	var sid, pid int
	if _, err := fmt.Sscan(line, &sid, &pid); err != nil {
		t.Fatalf("xdg-open logged %q", line)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if sid != pid {
		t.Fatalf("xdg-open shares the terminal's session %d, so Ctrl-C there would stop the browser", sid)
	}
}
