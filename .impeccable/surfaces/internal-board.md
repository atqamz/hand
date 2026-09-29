---
version: 1
slug: "internal-board"
primary_target: "internal/board"
related_targets: []
---

# Surface brief: the Hand board

## Scope and mode

- **Covers:** every board surface, including:
  - the fleet page: the waiting queue, running tasks, supervisor controls, conversation and composer;
  - the task page, the decision page, and the fleet list at `/`;
  - the error page, the read-only network variant, and the first-run empty state.
- **Mode:** Operate.

## Audience, job and constraints

- **Audience and load:** the sole operator, on desktop and phone equally, usually with 7 or more tasks running in one fleet.
- **Job:**
  - first, clear what waits on them in place: answer decisions, press a blocked screen's keys, ack reports, see failures;
  - second, talk to the supervisor.
- **Constraints:**
  - rendered on the server with Go `html/template`, plus small first-party JS embedded in the binary, with no framework and no build step;
  - live updates over Server-Sent Events;
  - controls only on loopback, CSRF on every form, and no paths on error pages;
  - light and dark themes follow the system;
  - supervisor replies and reports render as a small in-house safe markdown subset: bold, italics, strikethrough, lists, inline and block code, links, headings, rules, one level of quotes, and GFM tables with numeric columns right-aligned.
- **Alerts:** a tab-title count, plus opt-in browser notifications for new waiting items.

## Chosen direction and memorable moment

**Review Thread:** GitHub's review conventions, made Hand's own. The memorable moment is the top waiting item with its action inline: a decision's answer box opening in place, or a blocked screen's excerpt with its fenced key buttons.

## Unresolved

- The exact icon set. It must be Hand's own, never Octicons.
- The algorithm that derives the fleet tint: deterministic from the fleet id, with contrast checked in both themes.

## Direction contract

THESIS: Waiting items are the review requests and failing checks of a fleet, read first and acted on in place. The board refuses the generic dashboard (sidebar, KPI tiles, card grid) and the messenger layout that buries state.

OWN-WORLD: GitHub-grade restraint: neutral light and dark grounds, hairline borders, status semantics limited to red for failing or blocked, amber for waiting, green for passing and gray for neutral. The header carries Hand's mark, tinted by the fleet-id accent. Refs are monospace chips. Hand's own icon set.

STORY: The operator opens the board, sees at once what needs them, clears it without leaving the page, glances at the running checks, then talks to the supervisor in a quiet timeline.

FIRST VIEWPORT: At every width, two tabs under the header: "Needs you" with its count, and "Chat" with a dot while the supervisor works. The board opens on Needs you when something waits and on Chat otherwise. Needs you shows the waiting queue with the top item's action open, and below it the checks list, one line per task. Chat shows the supervisor status pill and controls, the timeline, and the composer at the bottom. Both panels are centred, up to 880px wide.

FORM: Review Thread, candidate 1 of the safer-register hand (4th on the original grounded list), seed key b7f9ac93.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
