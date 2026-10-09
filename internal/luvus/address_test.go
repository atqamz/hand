package luvus_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/luvus"
)

const sessionList = `{
  "sessions": [
    {
      "default": true,
      "endpoint": {"address": "\\\\.\\pipe\\luvus-5b0c8d2f6e1a4973", "transport": "windows_named_pipe"},
      "name": "default",
      "running": false
    },
    {
      "default": false,
      "endpoint": {"address": "\\\\.\\pipe\\luvus-0a1b2c3d4e5f6071", "transport": "windows_named_pipe"},
      "name": "secondhand-f3",
      "running": true
    },
    {
      "default": false,
      "endpoint": {"address": "\\\\.\\pipe\\luvus-9d3e7a1c4b8f2605", "transport": "windows_named_pipe"},
      "name": "secondhand-f1",
      "running": true,
      "session_dir": "C:\\luvus\\f1"
    }
  ]
}`

func TestAddressFromSessionList(t *testing.T) {
	bin := fakebin.Install(t, t.TempDir(), "luvus", "luvus", map[string]string{"out": sessionList})
	got, dir, err := luvus.Address(context.Background(), bin, "secondhand-f1", os.Environ())
	if want := `\\.\pipe\luvus-9d3e7a1c4b8f2605`; err != nil || got != want {
		t.Fatalf("address = %q, %v; want %q", got, err, want)
	}
	if want := `C:\luvus\f1`; dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	if _, _, err := luvus.Address(context.Background(), bin, "secondhand-f2", os.Environ()); err == nil || !strings.Contains(err.Error(), "secondhand-f2") {
		t.Fatalf("missing session err = %v", err)
	}
	if _, _, err := luvus.Address(context.Background(), bin, "secondhand-f3", os.Environ()); err == nil || !strings.Contains(err.Error(), "session_dir") {
		t.Fatalf("a session without session_dir err = %v", err)
	}
	failing := fakebin.Install(t, t.TempDir(), "luvus", "luvus", map[string]string{"exit": "1"})
	if _, _, err := luvus.Address(context.Background(), failing, "secondhand-f1", os.Environ()); err == nil {
		t.Fatal("a failing luvus session list must fail")
	}
}
