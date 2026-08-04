# Launch confirmation trusts herdr for liveness and the pane text only for dialogs

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#28
- PRs: none single

## Context

`hand spawn` sends a launch command to a pane and has to decide whether a worker actually started.

Both available signals are unreliable alone.
The pane's text is written by the harness, so a harness that painted a first-run dialog and then exited leaves convincing output behind and no process.
That is atqamz/secondhand#28: a spawn confirmed against text, with the worker parked on a dialog nobody answered.
Herdr's agent labeling knows whether a harness process is in the pane's foreground, but it says nothing about what that process is waiting on.

## Decision

The two signals are used for the two different questions, and neither is used for the other's.

**Liveness is herdr's answer, never the screen's.**
Herdr reports an agent on a pane only while a harness process is in its foreground, so a harness that painted a dialog and exited is never mistaken for a started worker.
That labeling is verified empirically for `claude` and `opencode`, each run in a real pane and observed being labeled.
For `codex`, `pi` and `grok` it rests on herdr's shipped agent-detection manifests, read but not exercised, because no binary for those is installed on this host.

**Pane text is read only to spot dialogs.**
A known dialog is answered; success needs the pane to hold a live agent and stay free of both known dialogs and the generic unrecognized-dialog fallback for a settle window.
A harness's own readiness signature is a secondary shortcut: on a pane already holding a live agent, the harness's own paint means there is nothing left to settle for.

**The text comes from recent scrollback, not the visible viewport.**
A pane in an unattached herdr session is too short to show a whole dialog - 23 rows against 61 attached - and what it clips is the lower half, where the option and footer lines that identify a dialog live.

**Scrollback rests on a measured premise, and its failure direction is chosen.**
Claude Code erases an answered first-run dialog in place rather than scrolling it away, so a recent-scrollback read does not carry answered dialogs forward.
Measured on 2026-07-26 against a real spawned worker pane on the Claude Code version installed on this host, reading 200 lines of retained scrollback: no trust-dialog, bypass-disclaimer or `Enter to confirm` text remained anywhere in it.
So a read that still matches a catalogued dialog is treated as that dialog still being up, and the launch runs out its poll window rather than being confirmed.
If the premise stops holding on a later version, spawn fails on the deadline instead of confirming a healthy worker.
A wrong deadline failure is loud and fixable; confirming an unread dialog is atqamz/secondhand#28 again.

Independently of the read, each catalogued dialog is answered at most once per launch, so retained text can cost a timeout but can never send a second round of keys into a live agent's composer.

**Two outcomes are not success.**
A pane with no agent, or one still showing a dialog, when the window elapses fails the spawn with the pane content and what held it up.
For a harness whose agent detection has not been exercised, that failure names the unexercised detection first and a harness that exited on a dialog second, since an unrecognized process is the likelier cause.
A recognized-but-refused dialog fails immediately, naming what a human has to accept.

## Rejected alternatives

**Confirm on pane text alone.**
This is what atqamz/secondhand#28 was.
The text a dead harness left behind is indistinguishable from the text a live one is showing.

**Confirm on herdr's agent presence alone.**
An agent parked on a dialog is a live process that will never do the work.
This is exactly the residual gap for a harness with no catalogued signatures, and it is accepted only because there is nothing else to read for one.

**Read the visible viewport, since that is what an operator would see.**
An unattached pane clips the half of the dialog that identifies it, and unattached is the normal state for a fleet.

**Treat retained dialog text as stale and confirm anyway.**
That converts the measured premise from a safety margin into a requirement, and its failure mode is silently confirming a parked worker.

**Re-answer a catalogued dialog on every poll, in case the first answer was lost.**
On a live agent the second round of keys lands in the composer as input the worker did not write.

**Scrape the harness's readiness signature as the primary confirmation.**
It is another tool's UI text with the same staleness problem as the dialog signatures, and it answers "the harness painted something" rather than "a process is running".

## Consequences

Confirmation needs two mechanisms that can each fail, and every failure fails the spawn rather than confirming it.

The scrollback premise is dated and version-specific, so it will eventually be wrong.
It fails toward a deadline error naming the dialog, which is why it is safe to depend on in the meantime.

A harness with no catalogued signatures at all is confirmed on agent presence alone, so an agent parked on an unrecognized dialog reports as started.
That is a known accepted gap, and the reason the signature catalogue matters for every harness added rather than only for `claude`.
