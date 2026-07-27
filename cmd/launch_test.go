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
// command test costs milliseconds instead of sleeping through the real settle window. The
// deadline stays generous because a pane that confirms returns on its own poll and never reaches
// it: every poll here is two real subprocess round-trips into the herdr fake, so a deadline sized
// to a few of them is a race against machine speed rather than a test. Tests that assert the
// deadline itself call expectLaunchTimeout to narrow it back down.
func useFastLaunchPolling(t *testing.T) {
	t.Helper()
	previous := launchPolling
	launchPolling = launchPoll{
		Interval:   time.Millisecond,
		QuietReads: 3,
		Timeout:    10 * time.Second,
		KeySettle:  time.Millisecond,
		ReadLines:  60,
	}
	t.Cleanup(func() { launchPolling = previous })
}

// expectLaunchTimeout narrows the deadline set by an earlier useFastLaunchPolling, whose cleanup
// restores it, for a pane that never confirms and so has to run its deadline out.
func expectLaunchTimeout() {
	launchPolling.Timeout = 150 * time.Millisecond
}

// launchFrame is one poll's worth of pane state: what "pane read" shows and what agent, if any,
// "pane get" reports running there. An empty agent is a pane holding no harness process.
type launchFrame struct {
	text  string
	agent string
}

func live(text string) launchFrame   { return launchFrame{text: text, agent: "claude"} }
func exited(text string) launchFrame { return launchFrame{text: text} }

// fakeLaunchPane installs a herdr fake that replays frames as successive polls, repeating the
// last one once they run out, and appends every key sent to a log the test reads back. A poll is
// a "pane get" followed by a "pane read", so the get arm advances the frame and the read arm
// serves whatever the get arm landed on.
func fakeLaunchPane(t *testing.T, frames ...launchFrame) (keyLog string) {
	t.Helper()
	dir := t.TempDir()
	framesDir := filepath.Join(dir, "frames")
	if err := os.MkdirAll(framesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, frame := range frames {
		base := filepath.Join(framesDir, strconv.Itoa(i))
		if err := os.WriteFile(base, []byte(frame.text), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+".agent", []byte(frame.agent), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keyLog = filepath.Join(dir, "keys.log")
	script := `#!/bin/sh
last=$(($LAUNCH_FRAME_COUNT - 1))
case "$1 $2" in
"pane get")
	n=$(cat "$LAUNCH_FRAME_COUNTER" 2>/dev/null || echo 0)
	echo $((n + 1)) > "$LAUNCH_FRAME_COUNTER"
	[ "$n" -gt "$last" ] && n=$last
	echo "$n" > "$LAUNCH_FRAME_CURRENT"
	agent=$(cat "$LAUNCH_FRAMES_DIR/$n.agent")
	printf '{"result":{"pane":{"pane_id":"%s","tab_id":"wA:tB","workspace_id":"wA","agent":"%s","agent_status":"idle"}}}' "$3" "$agent"
	;;
"pane read")
	cat "$LAUNCH_FRAMES_DIR/$(cat "$LAUNCH_FRAME_CURRENT")"
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
	t.Setenv("LAUNCH_FRAME_CURRENT", filepath.Join(dir, "current"))
	t.Setenv("LAUNCH_KEY_LOG", keyLog)
	return keyLog
}

const (
	launchEchoFrame     = "$ cd '/tmp/wt' && claude --dangerously-skip-permissions 'Read the brief'"
	launchReadyFrame    = "Welcome to Claude Code\n\n> \n  ? for shortcuts"
	launchBypassOnFrame = "> \n  secondhand (fm/x)\n  bypass permissions on (shift+tab to cycle)"
	launchTrustFrame    = "Do you trust the files in this folder?\n> 1. Yes, I trust this folder\n  2. No\n\nEnter to confirm"
	launchBypassFrame   = "WARNING: Bypass Permissions mode\n  1. Yes, I accept\n> 2. No, exit\n\nEnter to confirm"
	launchSettingsFrame = "Managed settings require approval\n\nSettings requiring approval:\n  hooks\n> 1. Yes, I trust these settings\n  2. No, exit Claude Code\n\nEnter to confirm"
	launchUnknownFrame  = "Some brand new dialog\n> 1. Sure\n  2. Nope\n\nEnter to confirm"
)

func TestConfirmLaunch(t *testing.T) {
	tests := []struct {
		name     string
		harness  string
		frames   []launchFrame
		wantErr  string
		wantKeys string
	}{
		{
			name:    "a live agent on a quiet pane is confirmed",
			harness: "claude",
			frames:  []launchFrame{live(launchReadyFrame)},
		},
		{
			name:    "the bypass-mode footer also reads as claude having started",
			harness: "claude",
			frames:  []launchFrame{live(launchBypassOnFrame)},
		},
		{
			// The point of taking liveness from herdr: nothing on this screen is recognizable,
			// but herdr reports a harness running on the pane, so the worker did start.
			name:    "a live agent whose screen text is unrecognizable is still confirmed",
			harness: "claude",
			frames:  []launchFrame{live(launchEchoFrame)},
		},
		{
			// A harness with no catalogued signatures at all is confirmed the same way, rather
			// than being reported as unconfirmable.
			name:    "a harness with no signatures is confirmed on agent presence",
			harness: "opencode",
			frames:  []launchFrame{{text: "opencode ready", agent: "opencode"}},
		},
		{
			// The failure this whole function exists for: the dialog text is still on screen but
			// the harness behind it is gone, so there is no worker to confirm.
			name:    "a harness that exited on a dialog is not confirmed",
			harness: "claude",
			frames:  []launchFrame{exited(launchBypassFrame)},
			wantErr: "the harness exited on a first-run dialog instead of starting up",
		},
		{
			name:    "leftover startup text from an exited harness is not confirmed",
			harness: "claude",
			frames:  []launchFrame{exited(launchReadyFrame)},
			wantErr: "the harness never started in the pane",
		},
		{
			// A bare Enter on the bypass dialog lands on "No, exit" and quits claude, so the
			// Down that moves focus to "Yes, I accept" must be sent first and on its own.
			name:     "answers the bypass prompt with Down before Enter",
			harness:  "claude",
			frames:   []launchFrame{live(launchBypassFrame), live(launchReadyFrame)},
			wantKeys: "Down\nEnter\n",
		},
		{
			name:     "answers the workspace trust prompt",
			harness:  "claude",
			frames:   []launchFrame{live(launchTrustFrame), live(launchReadyFrame)},
			wantKeys: "Enter\n",
		},
		{
			name:    "refuses the managed settings prompt instead of answering it",
			harness: "claude",
			frames:  []launchFrame{live(launchSettingsFrame)},
			wantErr: "waiting on the managed settings prompt",
		},
		{
			// The refused signature is catalogued after the answerable one, so answering by list
			// order would send keys into a dialog hand has decided not to answer.
			name:    "a refused prompt wins over an answerable one on the same screen",
			harness: "claude",
			frames:  []launchFrame{live(launchTrustFrame + "\n" + launchSettingsFrame)},
			wantErr: "waiting on the managed settings prompt",
		},
		{
			name:    "an unrecognized dialog keeps resetting the quiet count",
			harness: "claude",
			frames:  []launchFrame{live(launchUnknownFrame)},
			wantErr: "no known signature covers",
		},
		{
			// claude erases an answered dialog in place, so a read that still matches one means the
			// dialog is still up. Keys go out once either way, and the launch fails on the deadline
			// rather than being confirmed.
			name:     "a dialog still matching after its answer fails instead of confirming",
			harness:  "claude",
			frames:   []launchFrame{live(launchBypassFrame)},
			wantErr:  "the bypass permissions prompt was answered, its text is still in the pane, and the harness never became ready",
			wantKeys: "Down\nEnter\n",
		},
		{
			// claude's real first run: trust, then the bypass disclaimer, then the REPL with both
			// dialogs erased. Each is answered once, in the order the catalogue lists them, and the
			// worker is confirmed.
			name:     "both first-run dialogs are answered in order and the launch is confirmed",
			harness:  "claude",
			frames:   []launchFrame{live(launchTrustFrame), live(launchBypassFrame), live(launchReadyFrame)},
			wantKeys: "Enter\nDown\nEnter\n",
		},
		{
			// Issue #28's actual timeline: the pane starts as a bash prompt echoing the launch
			// command, claude comes up a beat later on the trust dialog, then settles. pane.Agent
			// is tested before the answer branch, so a regression there stops dialog-answering
			// while every single-frame case above still passes.
			name:     "a dialog that appears after a cold start is still answered",
			harness:  "claude",
			frames:   []launchFrame{exited(launchEchoFrame), live(launchTrustFrame), live(launchReadyFrame)},
			wantKeys: "Enter\n",
		},
		{
			// codex has no verified agent detection, so "no agent" cannot be blamed on the harness
			// without also naming the thing hand has not checked.
			name:    "an unverified harness that is never labeled names the unexercised detection",
			harness: "codex",
			frames:  []launchFrame{exited("$ cd '/tmp/wt' && codex --file '/tmp/brief.md'")},
			wantErr: "no agent detected in pane; herdr agent detection for harness codex has not been exercised",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useFastLaunchPolling(t)
			if tt.wantErr != "" {
				expectLaunchTimeout()
			}
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
			if string(keys) != tt.wantKeys {
				t.Fatalf("keys sent = %q, want %q", keys, tt.wantKeys)
			}
		})
	}
}
