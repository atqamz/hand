package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/secondhand/internal/herdr"
)

// useFastLaunchPolling shrinks confirmLaunch's poll window for the rest of the test, so a
// command test costs milliseconds instead of sleeping through the real settle window.
func useFastLaunchPolling(t *testing.T) {
	t.Helper()
	previous := launchPolling
	launchPolling = launchPoll{
		Interval:   time.Millisecond,
		QuietReads: 3,
		Timeout:    150 * time.Millisecond,
		KeySettle:  time.Millisecond,
		ReadLines:  60,
	}
	t.Cleanup(func() { launchPolling = previous })
}

// fakeLaunchPane installs a herdr fake that replays frames as successive "pane read" responses,
// repeating the last one once they run out, and appends every key sent to a log the test reads
// back. That is enough to drive confirmLaunch through a scripted pane lifecycle.
func fakeLaunchPane(t *testing.T, frames ...string) (keyLog string) {
	t.Helper()
	dir := t.TempDir()
	framesDir := filepath.Join(dir, "frames")
	if err := os.MkdirAll(framesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, frame := range frames {
		if err := os.WriteFile(filepath.Join(framesDir, strconv.Itoa(i)), []byte(frame), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keyLog = filepath.Join(dir, "keys.log")
	script := `#!/bin/sh
case "$1 $2" in
"pane read")
	n=$(cat "$LAUNCH_FRAME_COUNTER" 2>/dev/null || echo 0)
	echo $((n + 1)) > "$LAUNCH_FRAME_COUNTER"
	last=$(($LAUNCH_FRAME_COUNT - 1))
	[ "$n" -gt "$last" ] && n=$last
	cat "$LAUNCH_FRAMES_DIR/$n"
	;;
"pane send-keys")
	shift 3
	echo "$@" >> "$LAUNCH_KEY_LOG"
	;;
*)
	echo "unexpected herdr args: $@" >&2
	exit 1
	;;
esac
`
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LAUNCH_FRAMES_DIR", framesDir)
	t.Setenv("LAUNCH_FRAME_COUNT", strconv.Itoa(len(frames)))
	t.Setenv("LAUNCH_FRAME_COUNTER", filepath.Join(dir, "counter"))
	t.Setenv("LAUNCH_KEY_LOG", keyLog)
	return keyLog
}

const (
	launchEchoFrame     = "$ cd '/tmp/wt' && claude --dangerously-skip-permissions 'Read the brief'"
	launchReadyFrame    = "Welcome to Claude Code\n\n> \n  ? for shortcuts"
	launchTrustFrame    = "Do you trust the files in this folder?\n> 1. Yes, I trust this folder\n  2. No\n\nEnter to confirm"
	launchBypassFrame   = "WARNING: Bypass Permissions mode\n  1. Yes, I accept\n> 2. No, exit\n\nEnter to confirm"
	launchSettingsFrame = "Settings requiring approval:\n  hooks\n> 1. Yes, I trust these settings\n  2. No, exit Claude Code\n\nEnter to confirm"
	launchUnknownFrame  = "Some brand new dialog\n> 1. Sure\n  2. Nope\n\nEnter to confirm"
)

func TestConfirmLaunch(t *testing.T) {
	tests := []struct {
		name    string
		harness string
		frames  []string
		wantErr string
		// wantKeys is the whole key log, unless wantKeysRepeat marks a prompt the pane keeps
		// re-showing, where it is only the first answer of however many the loop sent.
		wantKeys       string
		wantKeysRepeat bool
	}{
		{
			name:    "quiet pane after the harness paints",
			harness: "claude",
			frames:  []string{launchReadyFrame},
		},
		{
			// A bare Enter on the bypass dialog lands on "No, exit" and quits claude, so the
			// Down that moves focus to "Yes, I accept" must be sent first and on its own.
			name:     "answers the bypass prompt with Down before Enter",
			harness:  "claude",
			frames:   []string{launchBypassFrame, launchReadyFrame},
			wantKeys: "Down\nEnter\n",
		},
		{
			name:     "answers the workspace trust prompt",
			harness:  "claude",
			frames:   []string{launchTrustFrame, launchReadyFrame},
			wantKeys: "Enter\n",
		},
		{
			name:    "refuses the settings trust prompt instead of answering it",
			harness: "claude",
			frames:  []string{launchSettingsFrame},
			wantErr: "waiting on the settings trust prompt",
		},
		{
			name:    "an unrecognized dialog keeps resetting the quiet count",
			harness: "claude",
			frames:  []string{launchUnknownFrame},
			wantErr: "no known signature covers",
		},
		{
			name:           "an answered prompt that never clears is not blamed on an unknown dialog",
			harness:        "claude",
			frames:         []string{launchBypassFrame},
			wantErr:        "answering the bypass permissions prompt is not clearing it",
			wantKeys:       "Down\nEnter\n",
			wantKeysRepeat: true,
		},
		{
			name:    "the echoed launch command alone is not a started worker",
			harness: "claude",
			frames:  []string{launchEchoFrame},
			wantErr: "never painted a startup frame",
		},
		{
			name:    "a harness with no signatures is unconfirmable, not confirmed",
			harness: "opencode",
			frames:  []string{launchReadyFrame},
			wantErr: errLaunchUnconfirmable.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useFastLaunchPolling(t)
			keyLog := fakeLaunchPane(t, tt.frames...)

			err := confirmLaunch(herdr.NewClient(), "wA:pC", tt.harness)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("confirmLaunch() = %v, want success", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("confirmLaunch() = nil, want error containing %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("confirmLaunch() = %v, want error containing %q", err, tt.wantErr)
			}

			keys, readErr := os.ReadFile(keyLog)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if tt.wantKeysRepeat {
				if !strings.HasPrefix(string(keys), tt.wantKeys) {
					t.Fatalf("keys sent = %q, want it to start with %q", keys, tt.wantKeys)
				}
			} else if string(keys) != tt.wantKeys {
				t.Fatalf("keys sent = %q, want %q", keys, tt.wantKeys)
			}
		})
	}
}
