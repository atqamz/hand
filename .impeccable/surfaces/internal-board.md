---
version: 1
slug: "internal-board"
primary_target: "internal/board"
related_targets: []
---

# Surface brief: the Hand board

## Scope and mode

- **Covers:** every board surface, including:
  - the fleet page: the masthead, the rail, the work pane (Needs in severity bands, then Tasks with its search field), the supervisor pane (its controls, the conversation and the composer) and the separator between the panes;
  - the Tasks page (search over every task), the task page, the decision page, the conversation page and the fleet list at `/`;
  - the error pages, the network variant (no supervisor controls; answering decisions and marking reports read still work), and the first-run empty state.
- **Mode:** Operate.

## Audience, job and constraints

- **Audience and load:** the sole operator, on desktop and phone equally, usually with 7 or more tasks running in one fleet.
- **Job:**
  - first, clear what waits on them in place: press a blocked screen's keys, answer decisions from a choice list, mark reports read, see failures;
  - second, talk to the supervisor and steer it (model, harness, interrupt, stop) from the composer;
  - third, find a task: the Tasks list on the fleet page, and the Tasks page that searches every task.
- **Constraints:**
  - rendered on the server with Go `html/template`, plus small first-party JS embedded in the binary, with no framework and no build step;
  - strict CSP: no inline script, no inline style, no `style` attribute; `app.js` sets `--chat` and the rail tip's `top` through the CSSOM;
  - live updates over Server-Sent Events, through five fleet-page regions (`status`, `console`, `timeline`, `queue`, `tasks`), with the composer form and the task search form outside every region so a live update never takes a draft;
  - supervisor controls only on loopback (on a network address only answering decisions and marking reports read work), CSRF on every form, and no paths on error pages;
  - every page shows one fleet; the rail lists the current fleet and only the fleets this browser holds a valid token for, and never another fleet's counts;
  - light and dark themes follow the system;
  - supervisor replies render as the safe markdown subset, with refs (`t12`, `d3`, `a5`, `r7`, `s2`) linking through `/ref/`;
  - the masthead shows the supervisor's context use against its window.
- **Alerts:** the tab title, the favicon states, the Needs count on the rail, and opt-in browser notifications that name the worst open item.
- **Physical scene:** at a desk by day with the board beside an editor, half a 1920 screen wide, and on a phone in the evening, checking one question: is anything paged? The system theme decides light or dark.

## Chosen direction and memorable moment

**The On-Call Board:** the fleet run as an on-call board. The memorable moment is acknowledging in place: pressing "2 No" on a blocked screen collapses the item to its receipt while a Hand line lands in the conversation, and the masthead pill turns from red back to ready.

## Unresolved

- The claude model-to-context-window values, checked against the models' published limits (ruled in the plan).

## Direction contract

THESIS: The fleet is run from an on-call board. Everything that needs the operator is an open item (a blocked screen, a failure, a decision, an unread report), paged in severity order under one head per severity word, acknowledged in place and resolved into the conversation. The work pane, with what waits and the tasks, is the widest thing on the page, and the supervisor's pane sits beside it at a width the operator drags. It refuses a text sidebar and a third column, and it refuses the green-on-black terminal opposite.

OWN-WORLD: A white ground #ffffff with panels #f6f7f9 and cards #f1f3f5 on hairline #e3e6ea rules and ink #15171c; at night ground #111317 with cards raised to #1b1f25. Accent blue #2457c5 marks the operator's own bubble, focus, links, the picked option and the working pulse. Ink is the one committing button. Fail red is reserved for what blocks or has broken. Severity lives in pale header fills on bordered items, in band heads, and in a word plus an icon shape on every state. System sans at five steps (22, 18, 15, 14, 12px); mono only for refs, keys, terminal text, severity codes and live figures in tabular numerals. Every colour, radius and size is a custom property in `board.css`. The fleet tint touches only the mark and a 3px top rule.

STORY: The operator opens the board and knows at once whether anything is paged: the masthead pill and the Needs count on the rail answer before the page settles. They acknowledge the top item in place (press "2 No", pick option 2 on d5 and send it, mark r7 read) and watch it collapse to its receipt while a Hand line lands in the conversation. Then they read s1's reply, drag the line between the panes to give it more room, and steer it from the composer. A task, a report or a decision opens in a new tab, and the board stays where they left it.

FIRST VIEWPORT: At 1440px: a 56px rail of icons at the left edge (Board with the Needs count, Tasks, Conversation, Phone, then the fleets this browser can open); a 44px masthead with the mark and fleet name on the left and on the right the s1 status pill (working with an accent pulse, ready, blocked in red, stopped) and a 48px context bar reading "510K / 1M"; the work pane, with Needs in bands (Blocked, Failed, Decision, Report) as calm one-line rows and the top item open as a card, then Tasks under Active and Inbox with a search field; and the supervisor pane on the right, 400px by default, under its head (`s1`, the model menu, Interrupt, Stop): replies on the pane, "you" as accent-bg bubbles on the right, Hand events as 12px ringed lines, and a slim composer pinned beneath. From 900px both panes show, the chat never under 360px and the work pane never under 440px. Below 900px the tabs read "Needs (n)", "Tasks" and "Chat", a menu button opens the rail's links as a sheet, and the phone's first row answers "1 blocked". With nothing paged, Needs reads "All clear since 12:41 · 1 running · 6 inbox".

FORM: The On-Call Board. It takes these raises:
- from the teletext service: red is reserved for what blocks the operator now;
- from the type specimen: live figures (the countdown, the context bar, the working timer) update in place in tabular mono without shifting layout;
- from the star atlas: one fixed severity ramp (blocked, failed, limited, no supervisor, stalled, interrupted, decision, report, quiet, long turn) orders Needs into bands, fills each header, and colours the band heads and the Needs counts;
- from the folding sequence: every action leaves a recoverable trace, namely a receipt, a Hand line and a deep link back to the item;
- from the cyclorama: state is never colour alone; every pill carries a word and an icon shape;
Signature interaction: acknowledge in place. A pressed key or a sent answer collapses the item to "Sent 2 · waiting…" with its keys disabled, then resolves it into the conversation as a Hand line. A decision is answered from a choice list (numbered options with their consequences, one marked recommended, and a note), by pointer or by number key. Motion grammar: an arrival wash on new items, one accent working pulse, and the collapse on acknowledge; all of it stops under `prefers-reduced-motion`.
Link rule: things open in a new tab, places navigate in place, and a draft in the composer survives navigation, new tabs and restarts.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
