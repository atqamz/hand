package cmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/atqamz/secondhand/internal/harness"
	"github.com/atqamz/secondhand/internal/herdr"
	"github.com/spf13/cobra"
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

// QuietReads * Interval is the settle window a pane must stay dialog-free for after the harness
// first paints, sized to the slowest gap between frames seen in testing with real claude, and
// Timeout is the outer bound for a pane that never starts or never clears a dialog. KeySettle
// gives the harness's TUI time to re-render a focus change before the next key arrives; sending
// a multi-key answer (e.g. Down, Enter) in one burst can land the confirm key before the focus
// move is drawn, confirming the wrong option.
var launchPolling = launchPoll{
	Interval:   1 * time.Second,
	QuietReads: 6,
	Timeout:    60 * time.Second,
	KeySettle:  300 * time.Millisecond,
	ReadLines:  60,
}

// errLaunchUnconfirmable reports the one outcome that is neither success nor failure: the
// harness has no verified pane signatures, so nothing about its pane can be read as "started".
// confirmLaunchOrWarn downgrades it to a warning; it must never be reported as a confirmation.
var errLaunchUnconfirmable = errors.New("harness has no verified first-run pane signatures")

// confirmLaunch waits for a freshly launched pane to clear any first-run dialog before the
// spawn/promote is reported successful. herdr's agent_status reports "idle" the instant the
// harness process starts, even while it sits unanswered on a dialog, so only the pane's own
// text can tell "stuck" from "started".
//
// Confirmation is two-staged. First the pane must show that the harness itself painted -
// either its readiness signature or a dialog, which is equally proof of a drawn frame. The
// echoed launch command alone does not count, or a cold start slower than the settle window
// would be confirmed moments before the trust dialog appears, which is the exact failure this
// function exists to remove. Only then does the quiet count run: QuietReads consecutive reads
// with neither a known prompt nor the generic unrecognized-dialog fallback matching. A known
// prompt is answered and resets the count; an unrecognized match resets it without being
// answered, so an uncatalogued dialog fails loudly instead of passing as a started worker. A
// prompt catalogued as refused (one hand will not answer for the operator) fails immediately
// with its own reason rather than waiting out the timeout as an unknown dialog.
func confirmLaunch(client *herdr.Client, paneID, harnessName string) error {
	prompts := harness.FirstRunPromptsFor(harnessName)
	if prompts.Ready == nil {
		return errLaunchUnconfirmable
	}

	deadline := time.Now().Add(launchPolling.Timeout)
	ready := false
	quiet := 0
	stall := "the harness never painted a startup frame"

	for {
		text, err := client.PaneRead(paneID, launchPolling.ReadLines)
		if err != nil {
			return err
		}

		prompt, known := matchFirstRunPrompt(prompts.Known, text)
		unrecognized := !known && prompts.Unrecognized != nil && prompts.Unrecognized.MatchString(text)
		if !ready && (known || unrecognized || prompts.Ready.MatchString(text)) {
			ready = true
			stall = "the harness started but its pane never went quiet"
		}

		switch {
		case known && prompt.Refuse != "":
			return fmt.Errorf("worker is waiting on the %s prompt: %s", prompt.Name, prompt.Refuse)
		case known:
			if err := answerFirstRunPrompt(client, paneID, prompt); err != nil {
				return err
			}
			quiet = 0
			stall = fmt.Sprintf("answering the %s prompt is not clearing it", prompt.Name)
		case unrecognized:
			quiet = 0
			stall = "the pane is showing a dialog no known signature covers"
		case ready:
			quiet++
			if quiet >= launchPolling.QuietReads {
				return nil
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("not confirmed started within %s: %s:\n%s", launchPolling.Timeout, stall, text)
		}
		time.Sleep(launchPolling.Interval)
	}
}

// confirmLaunchOrWarn confirms the launch for the spawn-shaped commands, reporting the one case
// hand cannot decide - a harness with no verified pane signatures - as a warning on stderr, so
// the operator is told the worker was launched but never observed instead of being handed a
// success the pane was never checked for.
func confirmLaunchOrWarn(cmd *cobra.Command, client *herdr.Client, paneID, harnessName string) error {
	err := confirmLaunch(client, paneID, harnessName)
	if errors.Is(err, errLaunchUnconfirmable) {
		_, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: cannot confirm the %s worker started: %v\n", harnessName, err)
		return printErr
	}
	return err
}

func matchFirstRunPrompt(known []harness.FirstRunPrompt, text string) (harness.FirstRunPrompt, bool) {
	for _, prompt := range known {
		if prompt.Match.MatchString(text) {
			return prompt, true
		}
	}
	return harness.FirstRunPrompt{}, false
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
