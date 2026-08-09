# Harnesses launch interactively, with liveness owned by herdr

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/hand#28, atqamz/hand#152
- PRs: none single

## Context

Workers must survive multiple steers and gate turns. Interactive launches provide that resident process but expose first-run dialogs. Pane text can outlive a dead process, while herdr can identify a live harness without knowing what dialog blocks it.

## Decision

Every harness template launches a resident interactive process with its autonomy flag. Herdr alone answers whether that process is live. Pane text is used only to detect dialogs, and a launch is confirmed only when both signals are clear.

Exact commands and dialog signatures belong to [`internal/harness`](../../internal/harness); confirmation belongs to [`cmd/launch.go`](../../cmd/launch.go). Their tests own supported flags, prompt handling, and failure behavior.

## Rejected alternatives

- Headless reinvocation has no resident session for `hand send`, watching, or multi-turn gates.
- A wrapper around headless runs would reimplement harness continuity and make pane state describe the wrapper.
- Screen text cannot prove liveness because a dead harness leaves text behind.
- Agent presence alone can confirm a process parked forever on a dialog.

## Consequences

Dialog wording is an external compatibility surface and may need maintenance as harnesses change. Unknown dialogs fail toward an unconfirmed launch where the harness has a catalogued prompt surface.
