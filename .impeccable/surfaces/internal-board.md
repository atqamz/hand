---
version: 1
slug: "internal-board"
primary_target: "internal/board"
related_targets: []
---

# Surface brief: the Hand board

## Scope and mode

- **Covers:** every board surface, including:
  - the fleet page: the masthead live strip, the tray (waiting items), the news budget (tasks), the wire (conversation) and the send console;
  - the task page, the decision page, the conversation page, and the fleet list at `/`;
  - the error page, the read-only network variant, and the first-run empty state.
- **Mode:** Operate.

## Audience, job and constraints

- **Audience and load:** the sole operator, on desktop and phone equally, usually with 7 or more tasks running in one fleet.
- **Job:**
  - first, clear what waits on them in place: answer decisions, press a blocked screen's keys, ack reports, see failures;
  - second, talk to the supervisor and steer it (model, interrupt, stop) from the console under the message box.
- **Constraints:**
  - rendered on the server with Go `html/template`, plus small first-party JS embedded in the binary, with no framework and no build step;
  - live updates over Server-Sent Events, through five fleet-page regions;
  - controls only on loopback, CSRF on every form, and no paths on error pages;
  - light and dark themes follow the system;
  - supervisor replies render as the safe markdown subset, with refs (`t12`, `d3`, `a5`, `r7`) linking through `/ref/`;
  - the masthead shows the supervisor's context use and compactions.
- **Alerts:** a tab-title count, the favicon states, and opt-in browser notifications for new waiting items.

## Chosen direction and memorable moment

**The Wire Desk:** a news agency wire desk and its teleprinter copy. The memorable moment is the print head running along the wire's last line while the supervisor works, with the gauge in the masthead counting its context.

## Unresolved

- The exact canary and carbon values, tuned under the craft floor to 4.5:1 for every text pair.

## Direction contract

THESIS: The fleet runs like a news agency wire desk. Every event is a dispatch ranked by wire priority, and the operator edits the desk. It refuses chat bubbles in a SaaS shell, and it refuses the green-on-black terminal opposite.

OWN-WORLD: Canary teleprinter copy on the day desk and carbon black on the night desk. Ink is near-black, FLASH red is the one saturated signal, and the operator's own marks are blue editor's pencil. Fixed-pitch caps slug lines (priority · ref · slug · dateline) sit on one baseline grid. There is one dispatch module under a slug rule. Rank comes from scale and caps, never from boxes, and there are no cards and no glass.

STORY: The operator sees what needs them in the tray, FLASH first, and acts in place. They read s1's dispatches like wire copy and send notes from the console. They always know whether the line is live (the print head) and how full s1's context is.

FIRST VIEWPORT: The masthead carries the fleet's wire name in its tint, the line state, the context gauge ("CTX 508K") and a clock. Below it sit the tabs Needs you (n) · Chat. Chat is the wire feed, centred at about 880px, newest at the bottom, with a print head running along the last line while s1 works. The send console is pinned at the bottom, holding `opus · high ▾`, the Interrupt key and Send. Needs you is the tray, FLASH to ROUTINE, with the top item open in place, and the news budget below it.

FORM: The Wire Desk, position 7 of 7 on the ranked grounded list, seed key 2455c3da. It takes these raises:
- from the type specimen: rank by scale contrast on one baseline grid;
- from the CD-ROM console: honest keys, with Interrupt depressed while s1 works;
- from Dumbar: one dispatch module that every item snaps to;
- from the sneaker boxes: one slug grid rules every item;
- from the tensegrity column: every dispatch traces to its task and attempt along a visible leader.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
