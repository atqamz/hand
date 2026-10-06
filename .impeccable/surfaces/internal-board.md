---
version: 1
slug: "internal-board"
primary_target: "internal/board"
related_targets: []
---

# Surface brief: the Hand board

## Scope and mode

- **Covers:** every board surface, including:
  - the fleet page: the masthead, Needs (the open items, then Tasks), Chat (the supervisor thread) and the composer;
  - the task page, the decision page, and the fleet list at `/`;
  - the error pages, the network variant (no supervisor controls; answering decisions and marking reports read still work), and the first-run empty state.
- **Mode:** Operate.

## Audience, job and constraints

- **Audience and load:** the sole operator, on desktop and phone equally, usually with 7 or more tasks running in one fleet.
- **Job:**
  - first, clear what waits on them in place: press a blocked screen's keys, answer decisions, mark reports read, see failures;
  - second, talk to the supervisor and steer it (model, harness, interrupt, stop) from the composer.
- **Constraints:**
  - rendered on the server with Go `html/template`, plus small first-party JS embedded in the binary, with no framework and no build step;
  - live updates over Server-Sent Events, through five fleet-page regions;
  - supervisor controls only on loopback (on a network address only answering decisions and marking reports read work), CSRF on every form, and no paths on error pages;
  - light and dark themes follow the system;
  - supervisor replies render as the safe markdown subset, with refs (`t12`, `d3`, `a5`, `r7`, `s2`) linking through `/ref/`;
  - the masthead shows the supervisor's context use against its window.
- **Alerts:** the tab title, the favicon states, and opt-in browser notifications that name the worst open item.
- **Physical scene:** at a desk by day with the board beside an editor, and on a phone in the evening, checking one question: is anything paged? The system theme decides light or dark.

## Chosen direction and memorable moment

**The On-Call Desk:** an on-call incident console. The memorable moment is acknowledging in place: pressing "2 No" on a blocked screen collapses the item to its receipt while a Hand line lands in the timeline, and the masthead pill turns from red back to ready.

## Unresolved

- The claude model-to-context-window values, checked against the models' published limits (ruled in the plan).
- Whether a grouped run of wakes expands to each wake's detail or links to its task (ruled in the plan).

## Direction contract

THESIS: The fleet is an on-call desk. Everything that needs the operator is an open item (a blocked screen, a failure, a decision, an unread report), paged in severity order, acknowledged in place and resolved into the timeline. It refuses the chat-ops shell of sidebar, chat and inspector, and it refuses the green-on-black terminal opposite.

OWN-WORLD: The Plan 14 review palette: ground #fcfcfd, cards #f4f6f8 on hairline #d5dbe2 rules, ink #1b1f24; at night ground #0d1117 with cards raised to #161b22. Accent blue #1d5fc2 marks the operator's own card, focus and the working pulse. Fail red is reserved for what blocks now. Severity lives in pale header fills on bordered items, and in a word plus an icon shape on every pill. System sans at five steps; mono only for refs, keys, terminal text and live figures in tabular numerals. The fleet tint touches only the mark and a 3px top rule.

STORY: The operator opens the board and knows at once whether anything is paged: the masthead pill and the Needs count answer before the page settles. They acknowledge the top item in place (press "2 No", answer d5, mark r7 read) and watch it collapse to its receipt while a Hand line lands in the timeline. Then they read s1's cards and steer it from the composer.

FIRST VIEWPORT: A 44px masthead: the mark and fleet name on the left; on the right the s1 status pill (working with an accent pulse, ready, blocked in red, stopped), `s1 · opus high`, and a 48px context bar reading "510K / 1M". At 1024px and up, Chat is the 760px main column of GitHub-thread cards (s1 on the left at up to 90%, "you" on the right at up to 75% on accent-bg, Hand events as 12px ringed lines), with the composer card pinned beneath. The Needs rail, 360 to 400px, sits on the right: open items most severe first with the top one open, then Tasks under Active and Inbox. Below 1024px the tabs read "Needs (n)" and "Chat", and the phone's first row answers "1 blocked". With nothing paged, Needs reads "All clear since 12:41 · 1 running · 6 inbox".

FORM: The On-Call Desk, position 3 of 7 on the ranked grounded list, seed key 14c264fa. It takes these raises:
- from the teletext service: red is reserved for what blocks the operator now;
- from the type specimen: live figures (the countdown, the context bar, the working timer) update in place in tabular mono without shifting layout;
- from the star atlas: one fixed severity ramp (blocked, failure, decision, report, quiet) orders Needs, fills each header, and colours the tab count and the favicon;
- from the folding sequence: every action leaves a recoverable trace, namely a receipt, a timeline line and a deep link back to the item;
- from the cyclorama: state is never colour alone; every pill carries a word and an icon shape.
Signature interaction: acknowledge in place. A pressed key or a sent answer collapses the item to "Sent 2 · waiting…" with its keys disabled, then resolves it into the timeline as a Hand line. Motion grammar: an arrival wash on new items, one accent working pulse, and the collapse on acknowledge; all of it stops under `prefers-reduced-motion`.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
