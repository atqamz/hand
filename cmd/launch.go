package cmd

import (
	"fmt"
	"time"

	"github.com/atqamz/secondhand/internal/harness"
	"github.com/atqamz/secondhand/internal/herdr"
)

// launchPoll holds confirmLaunch's timings. They are a package var rather than consts so tests
// can shrink the poll window instead of sleeping through a real one.
type launchPoll struct {
	Interval   time.Duration
	QuietReads int
	Timeout    time.Duration
	KeySettle  time.Duration
	ReadLines  int
}

// QuietReads * Interval is the settle window a pane must stay dialog-free for, sized to the
// slowest gap between frames seen in testing with real claude, and Timeout is the outer bound
// for a pane that never starts or never clears a dialog. KeySettle gives the harness's TUI time
// to re-render a focus change before the next key arrives; sending a multi-key answer (e.g.
// Down, Enter) in one burst can land the confirm key before the focus move is drawn, confirming
// the wrong option.
var launchPolling = launchPoll{
	Interval:   1 * time.Second,
	QuietReads: 6,
	Timeout:    60 * time.Second,
	KeySettle:  300 * time.Millisecond,
	ReadLines:  60,
}

// confirmLaunch waits for a freshly launched pane to hold a live harness with no first-run
// dialog left on it before the spawn/promote is reported successful.
//
// Liveness is herdr's answer, not the screen's: herdr reports an agent on a pane only while a
// harness process runs in it, so a harness that painted a dialog and then exited leaves its text
// in the scrollback but no agent, and can never be mistaken for a started worker. That holds for
// every harness, not just the ones whose screens hand can read. Pane text has exactly one job
// here, spotting dialogs: a known prompt is answered and resets the quiet count, the generic
// unrecognized-dialog fallback resets it without being answered so an uncatalogued dialog fails
// loudly, and a prompt catalogued as refused fails immediately with its own reason. Ready is a
// cheap secondary signal - with the agent already confirmed present, the harness's own paint
// leaves the quiet window nothing to wait for.
func confirmLaunch(client *herdr.Client, paneID, harnessName string) error {
	prompts := harness.FirstRunPromptsFor(harnessName)
	deadline := time.Now().Add(launchPolling.Timeout)
	quiet := 0
	answered := ""
	stall := "the harness never started in the pane"

	for {
		pane, err := client.PaneGet(paneID)
		if err != nil {
			return err
		}
		text, err := client.PaneRead(paneID, launchPolling.ReadLines)
		if err != nil {
			return err
		}

		prompt, known := matchFirstRunPrompt(prompts.Known, text)
		switch {
		case pane.Agent == "":
			quiet = 0
			if answered != "" || known {
				stall = "the harness exited on a first-run dialog instead of starting up"
			}
		case known && prompt.Refuse != "":
			return fmt.Errorf("worker is waiting on the %s prompt: %s", prompt.Name, prompt.Refuse)
		case known:
			// Answering again on a frame byte-identical to the one already answered would inject
			// the keys into whatever the harness moved on to; only a repaint means it is stuck.
			if text != answered {
				if err := answerFirstRunPrompt(client, paneID, prompt); err != nil {
					return err
				}
				answered = text
			}
			quiet = 0
			stall = fmt.Sprintf("answering the %s prompt is not clearing it", prompt.Name)
		case prompts.Unrecognized != nil && prompts.Unrecognized.MatchString(text):
			quiet = 0
			stall = "the pane is showing a dialog no known signature covers"
		case prompts.Ready != nil && prompts.Ready.MatchString(text):
			return nil
		default:
			quiet++
			if quiet >= launchPolling.QuietReads {
				return nil
			}
			stall = "the harness started but its pane never went quiet"
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("not confirmed started within %s: %s:\n%s", launchPolling.Timeout, stall, text)
		}
		time.Sleep(launchPolling.Interval)
	}
}

// matchFirstRunPrompt reports which catalogued dialog the pane text is showing. A refused prompt
// wins over an answerable one wherever either sits in the catalogue, so a screen carrying both
// signatures is never answered into.
func matchFirstRunPrompt(known []harness.FirstRunPrompt, text string) (harness.FirstRunPrompt, bool) {
	var match harness.FirstRunPrompt
	found := false
	for _, prompt := range known {
		if !prompt.Match.MatchString(text) {
			continue
		}
		if prompt.Refuse != "" {
			return prompt, true
		}
		if !found {
			match, found = prompt, true
		}
	}
	return match, found
}

func answerFirstRunPrompt(client *herdr.Client, paneID string, prompt harness.FirstRunPrompt) error {
	for _, key := range prompt.Keys {
		if err := client.PaneSendKeys(paneID, key); err != nil {
			return fmt.Errorf("answer %s prompt: %w", prompt.Name, err)
		}
		time.Sleep(launchPolling.KeySettle)
	}
	return nil
}
