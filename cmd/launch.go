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
// on screen but no agent, and can never be mistaken for a started worker. Pane text has
// exactly one job here, spotting dialogs: a known prompt is answered and resets the quiet count,
// the generic unrecognized-dialog fallback resets it without being answered so an uncatalogued
// dialog fails loudly, and a prompt catalogued as refused fails immediately with its own reason.
// The text is recent scrollback rather than the visible viewport because an unattached pane is too
// short to show a whole dialog and would clip the very lines that identify it (see
// herdr.PaneRead), which means a dialog already answered can still be in the read. Each catalogued
// dialog is therefore answered at most once per launch, so lingering text can never make hand type
// into a session that has moved on. Ready is a cheap secondary signal - with the agent already
// confirmed present, the harness's own paint leaves the quiet window nothing to wait for.
//
// The gate is only as good as herdr's labeling of the harness, which is why a pane that is never
// labeled says so by name (see harness.AgentDetectionVerified) instead of failing as a bare
// timeout.
func confirmLaunch(client *herdr.Client, paneID, harnessName string) error {
	prompts := harness.FirstRunPromptsFor(harnessName)
	deadline := time.Now().Add(launchPolling.Timeout)
	quiet := 0
	answered := map[string]bool{}
	sawAgent := false
	detected := harness.AgentDetectionVerified(harnessName)
	noAgent := "the harness never started in the pane"
	if !detected {
		noAgent = fmt.Sprintf("no agent detected in pane; herdr agent detection for harness %s has not been exercised", harnessName)
	}
	stall := noAgent

	for {
		pane, err := client.PaneGet(paneID)
		if err != nil {
			return err
		}
		text, err := client.PaneRead(paneID, launchPolling.ReadLines)
		if err != nil {
			return err
		}

		sawAgent = sawAgent || pane.Agent != ""

		prompt, known := matchFirstRunPrompt(prompts.Known, text, answered)
		switch {
		case pane.Agent == "":
			quiet = 0
			switch {
			case (known || len(answered) > 0) && (sawAgent || detected):
				stall = "the harness exited on a first-run dialog instead of starting up"
			case known:
				stall = fmt.Sprintf("%s, so the %s dialog on screen may be a harness herdr never recognized rather than one that exited", noAgent, prompt.Name)
			case sawAgent:
				stall = "the harness started and then exited"
			default:
				stall = noAgent
			}
		case known && prompt.Refuse != "":
			return fmt.Errorf("worker is waiting on the %s prompt: %s", prompt.Name, prompt.Refuse)
		case known:
			if !answered[prompt.Name] {
				if err := answerFirstRunPrompt(client, paneID, prompt); err != nil {
					return err
				}
				answered[prompt.Name] = true
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

// matchFirstRunPrompt reports which catalogued dialog the pane read is showing, given the dialogs
// this launch has already answered. A refused prompt wins over an answerable one wherever either
// sits in the catalogue, so a read carrying both signatures is never answered into. Among
// answerable ones an unanswered dialog wins over an already-answered one, so the text of a dialog
// just cleared cannot hide the next dialog behind it in the same read; otherwise catalogue order
// decides, which for claude is the order it paints them in.
func matchFirstRunPrompt(known []harness.FirstRunPrompt, text string, answered map[string]bool) (harness.FirstRunPrompt, bool) {
	var match harness.FirstRunPrompt
	found := false
	for _, prompt := range known {
		if !prompt.Match.MatchString(text) {
			continue
		}
		if prompt.Refuse != "" {
			return prompt, true
		}
		if !found || (answered[match.Name] && !answered[prompt.Name]) {
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
