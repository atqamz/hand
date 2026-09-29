---
name: Hand board
description: The operator's board for one Hand fleet, laid out as a review thread. What waits on the operator comes first, then the running checks, then the supervisor conversation.
colors:
  bg: "#fcfcfd"
  card: "#f4f6f8"
  header: "#fcfcfd"
  fg: "#1b1f24"
  muted: "#59616c"
  line: "#d5dbe2"
  hover: "#eaeef2"
  fail: "#c4262e"
  wait: "#8f5e00"
  pass: "#19793b"
  neutral: "#59616c"
  accent: "#1d5fc2"
  fail-bg: "#fdecec"
  wait-bg: "#fcf3d9"
  pass-bg: "#e3f5e8"
  neutral-bg: "#eceff3"
  accent-bg: "#e8f0fc"
  screen: "#11151a"
  screen-fg: "#d7dde4"
  primary: "#19793b"
  on-primary: "#ffffff"
  shadow: "rgba(27,31,36,.14)"
  bg-dark: "#0e1116"
  card-dark: "#151a21"
  header-dark: "#0e1116"
  fg-dark: "#e6ebf1"
  muted-dark: "#9aa3ae"
  line-dark: "#2e353e"
  hover-dark: "#1c232c"
  fail-dark: "#ff6a63"
  wait-dark: "#e3a73b"
  pass-dark: "#4fc46c"
  neutral-dark: "#9aa3ae"
  accent-dark: "#6ea6ff"
  fail-bg-dark: "#3a1a1c"
  wait-bg-dark: "#352912"
  pass-bg-dark: "#15301d"
  neutral-bg-dark: "#20262e"
  accent-bg-dark: "#17263d"
  screen-dark: "#07090c"
  screen-fg-dark: "#d7dde4"
  primary-dark: "#1f7f3e"
  on-primary-dark: "#ffffff"
  shadow-dark: "rgba(0,0,0,.5)"
  tint-0: "#407acc"
  tint-1: "#736ad7"
  tint-2: "#9f58d3"
  tint-3: "#ca38be"
  tint-4: "#ce4880"
  tint-5: "#cd4f44"
  tint-6: "#a1702b"
  tint-7: "#777f22"
  tint-8: "#4d8724"
  tint-9: "#258b2d"
  tint-10: "#24895f"
  tint-11: "#25848d"
  tint-0-dark: "#417ed2"
  tint-1-dark: "#7a70de"
  tint-2-dark: "#a55cd9"
  tint-3-dark: "#d13bc4"
  tint-4-dark: "#d54b85"
  tint-5-dark: "#d45347"
  tint-6-dark: "#a97328"
  tint-7-dark: "#7c841f"
  tint-8-dark: "#4f8f21"
  tint-9-dark: "#22912b"
  tint-10-dark: "#218f61"
  tint-11-dark: "#238b95"
typography:
  headline:
    fontFamily: "system-ui, -apple-system, \"Segoe UI\", \"Noto Sans\", Helvetica, Arial, sans-serif"
    fontSize: "22px"
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: "-0.015em"
  title:
    fontFamily: "system-ui, -apple-system, \"Segoe UI\", \"Noto Sans\", Helvetica, Arial, sans-serif"
    fontSize: "16px"
    fontWeight: 600
    lineHeight: "24px"
    letterSpacing: "-0.01em"
  heading-small:
    fontFamily: "system-ui, -apple-system, \"Segoe UI\", \"Noto Sans\", Helvetica, Arial, sans-serif"
    fontSize: "14px"
    fontWeight: 600
    lineHeight: "22px"
    letterSpacing: "-0.01em"
  body:
    fontFamily: "system-ui, -apple-system, \"Segoe UI\", \"Noto Sans\", Helvetica, Arial, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
  control:
    fontFamily: "system-ui, -apple-system, \"Segoe UI\", \"Noto Sans\", Helvetica, Arial, sans-serif"
    fontSize: "13px"
    fontWeight: 500
    lineHeight: "20px"
  meta:
    fontFamily: "system-ui, -apple-system, \"Segoe UI\", \"Noto Sans\", Helvetica, Arial, sans-serif"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.5
  badge:
    fontFamily: "system-ui, -apple-system, \"Segoe UI\", \"Noto Sans\", Helvetica, Arial, sans-serif"
    fontSize: "12px"
    fontWeight: 600
    lineHeight: "18px"
  label:
    fontFamily: "system-ui, -apple-system, \"Segoe UI\", \"Noto Sans\", Helvetica, Arial, sans-serif"
    fontSize: "11px"
    fontWeight: 600
    lineHeight: "18px"
  mono:
    fontFamily: "ui-monospace, SFMono-Regular, \"SF Mono\", Menlo, Consolas, \"Liberation Mono\", monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: "18px"
    fontFeature: "\"tnum\""
  code-block:
    fontFamily: "ui-monospace, SFMono-Regular, \"SF Mono\", Menlo, Consolas, \"Liberation Mono\", monospace"
    fontSize: "12px"
    fontWeight: 400
    lineHeight: 1.45
rounded:
  sm: "4px"
  md: "6px"
  pill: "999px"
  round: "50%"
spacing:
  2xs: "4px"
  xs: "6px"
  sm: "8px"
  md: "10px"
  lg: "12px"
  xl: "16px"
  2xl: "20px"
  3xl: "24px"
components:
  button:
    backgroundColor: "{colors.card}"
    textColor: "{colors.fg}"
    typography: "{typography.control}"
    rounded: "{rounded.md}"
    padding: "4px 12px"
  button-hover:
    backgroundColor: "{colors.hover}"
  button-primary:
    backgroundColor: "{colors.primary}"
    textColor: "{colors.on-primary}"
    typography: "{typography.control}"
    rounded: "{rounded.md}"
    padding: "4px 12px"
  button-quiet:
    textColor: "{colors.muted}"
    typography: "{typography.control}"
    rounded: "{rounded.md}"
    padding: "4px 12px"
  button-quiet-hover:
    textColor: "{colors.fg}"
  key-button:
    backgroundColor: "{colors.card}"
    textColor: "{colors.fg}"
    rounded: "{rounded.md}"
    padding: "4px 12px"
    width: "2.6em"
  input:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.fg}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: "5px 8px"
  ref:
    backgroundColor: "{colors.card}"
    textColor: "{colors.fg}"
    typography: "{typography.mono}"
    rounded: "{rounded.md}"
    padding: "0 6px"
  chip:
    textColor: "{colors.accent}"
    typography: "{typography.badge}"
    rounded: "{rounded.pill}"
    padding: "0 7px"
  count:
    backgroundColor: "{colors.neutral-bg}"
    textColor: "{colors.fg}"
    typography: "{typography.badge}"
    rounded: "{rounded.pill}"
    padding: "0 7px"
  count-waiting:
    backgroundColor: "{colors.wait-bg}"
    textColor: "{colors.wait}"
  pill-neutral:
    backgroundColor: "{colors.neutral-bg}"
    textColor: "{colors.neutral}"
    typography: "{typography.badge}"
    rounded: "{rounded.pill}"
    padding: "0 10px"
  pill-running:
    backgroundColor: "{colors.pass-bg}"
    textColor: "{colors.pass}"
  pill-waiting:
    backgroundColor: "{colors.wait-bg}"
    textColor: "{colors.wait}"
  pill-failing:
    backgroundColor: "{colors.fail-bg}"
    textColor: "{colors.fail}"
  pill-ready:
    backgroundColor: "{colors.pass-bg}"
    textColor: "{colors.pass}"
  pill-working:
    backgroundColor: "{colors.accent-bg}"
    textColor: "{colors.accent}"
  label-queued:
    textColor: "{colors.wait}"
    typography: "{typography.label}"
    rounded: "{rounded.pill}"
    padding: "0 7px"
  wait-summary:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.fg}"
    typography: "{typography.body}"
    rounded: "{rounded.md}"
    padding: "8px 12px"
  wait-summary-hover:
    backgroundColor: "{colors.hover}"
  wait-summary-open:
    backgroundColor: "{colors.card}"
  wait-body:
    padding: "12px"
  check-row:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.fg}"
    typography: "{typography.body}"
    padding: "7px 12px"
  screen:
    backgroundColor: "{colors.screen}"
    textColor: "{colors.screen-fg}"
    typography: "{typography.code-block}"
    rounded: "{rounded.md}"
    padding: "10px 0 10px 12px"
  panel:
    backgroundColor: "{colors.bg}"
    rounded: "{rounded.md}"
    padding: "10px 12px"
  comment-header:
    backgroundColor: "{colors.card}"
    textColor: "{colors.muted}"
    typography: "{typography.meta}"
    padding: "6px 12px"
  comment-header-operator:
    backgroundColor: "{colors.accent-bg}"
  comment-body:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.fg}"
    typography: "{typography.body}"
    padding: "8px 12px"
  event-working:
    textColor: "{colors.accent}"
    rounded: "{rounded.round}"
  md-table:
    rounded: "{rounded.md}"
  md-table-head:
    backgroundColor: "{colors.card}"
    textColor: "{colors.muted}"
    typography: "{typography.badge}"
    padding: "6px 10px"
  md-table-cell:
    textColor: "{colors.fg}"
    typography: "{typography.body}"
    padding: "6px 10px"
  md-table-sticky:
    backgroundColor: "{colors.bg}"
  composer:
    backgroundColor: "{colors.card}"
    rounded: "{rounded.md}"
    padding: "10px"
  kbd:
    textColor: "{colors.muted}"
    rounded: "{rounded.sm}"
    padding: "0 5px"
  switch-summary:
    textColor: "{colors.muted}"
    typography: "{typography.control}"
    padding: "5px 4px"
  switch-summary-hover:
    textColor: "{colors.fg}"
  tab:
    backgroundColor: "{colors.header}"
    textColor: "{colors.muted}"
    padding: "10px 12px"
  tab-wide:
    padding: "10px 14px"
  tab-active:
    textColor: "{colors.fg}"
  tab-dot:
    backgroundColor: "{colors.accent}"
    rounded: "{rounded.round}"
    size: "6px"
  needs-here:
    backgroundColor: "{colors.wait-bg}"
    rounded: "{rounded.md}"
  card:
    backgroundColor: "{colors.bg}"
    rounded: "{rounded.md}"
    padding: "16px"
  toast:
    backgroundColor: "{colors.bg}"
    textColor: "{colors.fg}"
    rounded: "{rounded.md}"
    padding: "10px 14px"
  header-mark:
    size: "22px"
---

# Design System: Hand board

## Overview

**Creative North Star: "The Review Thread"**

The board borrows the conventions of a code review page and makes them Hand's own. Things that wait on the operator read like review requests and failing checks. The top waiting item opens with its action in place, so the operator can answer a decision, press a blocked screen's keys or mark a report read without leaving the page. Running tasks read as a list of checks, one line each. The supervisor conversation reads as a quiet comment thread with Hand's own events as single muted lines between the comments, and while the supervisor works, one more line at the foot of the thread says for how long and on what.

The mood is restrained and dense. Grounds are neutral in both themes. Structure comes from 1px hairline borders and one tonal step between the page ground and the card fill, not from shadows or colour. Colour carries meaning: four status colours, one blue accent for links, focus and the supervisor at work, one green for the committing button, and one per-fleet tint that only identifies the fleet. Type is the system UI stack at a 14px base, with monospace kept for refs, keys and terminal output.

The direction refuses the generic dashboard (a sidebar, KPI tiles, a grid of cards) and the messenger layout that buries state under chat. Every page shows one fleet. The fleet page splits into two tabs at every width, "Needs you" and "Chat", so the queue and the conversation each get one centred column instead of sharing a squeezed one.

**Key Characteristics:**
- Waiting items first, each with its action inline; the top item is open by default.
- Hairline borders at 1px, a 6px corner on every box, and pills only for small labels.
- Four status colours with fixed meanings, tested for contrast in both themes.
- A per-fleet tint on the header mark and the top rule, and nowhere that carries status.
- Hand's own inline SVG icons on a 16px grid.
- Light and dark themes from `prefers-color-scheme`, with no toggle.
- Two tabs at every width over panels centred at up to 880px, and a no-scroll app shell when JavaScript runs in a window at least 600px tall.
- The supervisor's working state in the blue accent: the pill, a dot on the Chat tab, a working line in the timeline and the favicon.
- A strict CSP: one stylesheet, one script, no inline code, no web fonts.

## Colors

A neutral, low-chroma ground with a small set of semantic colours that each mean exactly one thing.

Every colour is a custom property on `:root` in `internal/board/static/board.css`, which is the single source of the tokens. The token keys above are the property names without `--`. The dark theme redefines the same properties inside `@media (prefers-color-scheme:dark)`; the `-dark` keys record those values. `:root` sets `color-scheme: light dark` so native controls and scrollbars follow the theme. Two places hold literal colours instead: the terminal block's scrollbar and edge fade, which stay dark in both themes, and the three favicons (see Components), because a static SVG cannot read the page's properties.

### Primary
- **Review Green** (`primary`, #19793b; dark #1f7f3e): the fill of the one committing button in a form (Answer, Send, Resume), with white text (`on-primary`). In light theme it equals `pass`; in dark theme it stays deep so white text keeps its contrast, while `pass` brightens for text use.
- **Link Blue** (`accent`, #1d5fc2; dark #6ea6ff): links, the focus ring, the caret, the current tab's underline and PR chips. It also marks the supervisor at work: the text and pulsing dot of the working pill, the dot on the Chat tab and the ring of the working line's icon. Its pale pair `accent-bg` fills text selection, the operator's comment header and the working pill.

### Tertiary
- **Fleet Tint** (`tint-0` to `tint-11`): twelve hues spread around the wheel at a similar mid lightness, each with a dark-theme value. `tint()` in `internal/board/board.go` picks one per fleet as FNV-1a 32 of the fleet id, mod 12, and the page sets it as `class="tint-N"` on `body`. A page with no fleet id gets `tint-0`.

### Neutral
- **Page Ground** (`bg`, #fcfcfd; dark #0e1116): the page, list boxes, comment bodies, inputs, the toast and the sticky first column of a reply table.
- **Card Fill** (`card`, #f4f6f8; dark #151a21): one step off the ground, for buttons, ref chips, comment headers, the open waiting item's header, inline code, reply table heads and the composer.
- **Header Ground** (`header`): equal to `bg` today, kept separate for the top bar and the tab bar.
- **Ink** (`fg`, #1b1f24; dark #e6ebf1): body text.
- **Muted Ink** (`muted`, #59616c; dark #9aa3ae): meta lines, times, hints, event lines, inactive tabs, placeholders, the "Switch model" disclosure, the keyboard hint, reply table heads, quotes and struck text.
- **Hairline** (`line`, #d5dbe2; dark #2e353e): every border and divider, the timeline connector, the scrollbars, and the rules, quote edge and table lines in replies.
- **Hover Wash** (`hover`): the hover fill of buttons and waiting-item headers.
- **Terminal** (`screen` and `screen-fg`): the dark block that shows a blocked supervisor screen. It stays dark in both themes.

### Status
- **Fail Red** (`fail`, pale `fail-bg`): failing checks, failed or interrupted attempts, blocked screens, a supervisor that is blocked, unreachable or stopped unexpectedly, the error card, the toast border and "stuck" reports.
- **Wait Amber** (`wait`, pale `wait-bg`): decisions and unread reports, the non-zero waiting count, the "Queued" and "switching to" labels, stale warnings and "progress" reports.
- **Pass Green** (`pass`, pale `pass-bg`): running and passing checks, the ready supervisor pill and "done" reports.
- **Neutral Gray** (`neutral`, pale `neutral-bg`): idle checks, inbox and abandoned tasks, withdrawn decisions, a launching or stopped supervisor, and a fleet with no supervisor yet.

### Named Rules
**The Four Signals Rule.** Status uses only `fail`, `wait`, `pass` and `neutral`, and each keeps one meaning: red is failing or blocked, amber is waiting on the operator, green is running, passing or ready, gray is idle. Besides links and focus, the blue accent marks one state: the supervisor at work, on its pill, the Chat tab dot, the working line and the favicon. It never marks a check, a waiting item or an outcome. The fleet tint never carries state, and there is no fifth status colour.

**The Contrast Floor Rule.** In both themes, `fail`, `wait`, `pass`, `neutral`, `fg` and `muted` reach 4.5:1 on `bg` and on `card`, and every tint reaches 3:1 on `header`. `TestColoursMeetContrast` in `internal/board/identity_test.go` enforces this, so a new status or tint value must pass it.

**The Fleet Signature Rule.** The tint only identifies a fleet: the 3px rule across the top of every page, the 22px mark in the header, and the round swatch beside each fleet on the fleet list. It never fills a button, a status or body text.

## Typography

**Body Font:** the system UI stack (`system-ui`, then `-apple-system`, "Segoe UI", "Noto Sans", Helvetica, Arial, sans-serif)
**Mono Font:** the system monospace stack (`ui-monospace`, then SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace)

**Character:** the operating system's own UI face, set small and tight like a review tool, with monospace reserved for what the operator could type: refs, keys and terminal text. No font is downloaded; the CSP has no `font-src` and `TestNoWebFonts` rejects `@font-face` and `url(` in the stylesheet.

### Hierarchy
- **Headline** (600, 22px, 1.3, -0.015em): the title of a task or decision page. It is the largest text on the board; there is no display size.
- **Title** (600, 16px, 24px): the fleet name in the header, the "Checks" section head and a reply's top-level heading. The "Needs you" section head shares it but stays hidden, because the tab carries the count.
- **Heading Small** (600, 14px, 22px): page headings on the fleet list and error page, and a reply's lower headings. The "Attempts", "Reports" and "Events" heads and the tab labels use the same size and weight on the body line height.
- **Body** (400, 14px, 1.5): everything else, including waiting-item titles (600 when open or on top, 400 when collapsed in the list) and reply table cells.
- **Control** (500, 13px, 20px): buttons and the "Switch model" disclosure. Comment author names (bold), crumbs (regular) and the supervisor panel's selects and inputs share the 13px size.
- **Meta** (400, 12px): times, meta lines, hints, event lines and comment headers.
- **Badge** (600, 12px, 18px): PR chips and reply table heads. The waiting count sits on a 20px line and status pills on a 22px line.
- **Label** (600, 11px, 18px): the outlined "Queued" label on a message the supervisor has not read yet, and the "switching to" label while a model switch is pending. The keyboard hint uses the mono family at this size and line, at weight 400.
- **Mono** (12px, 18px): ref chips such as `t3`, `a2`, `d1` and the fleet id. Key buttons use the mono family at the control size.
- **Code Block** (12px, 1.45): the blocked-screen excerpt and other preformatted text. Markdown inline code sits at 85% of its line.

Reply headings take a 12px gap above and 4px below, none when they open the reply, and wrap balanced. The top-level reply heading is 16/24 rather than the 15/22 the Plan 14 spec proposed: 15px is not a step on the board's ramp, so the heading reuses the Title step.

### Named Rules
**The Ref Chip Rule.** A Hand ref that stands on its own (`tN`, `aN`, `rN`, `dN`, `sN`, or a fleet id) renders as a monospace chip with a hairline border and a 6px corner, and it is a link when a page exists for it. Inside a sentence or an action label ("Open d2", "View a2") the ref stays plain text.

**The Tabular Rule.** Times, counts, meta lines, refs, the tab count and reply tables use tabular figures so columns of numbers do not jitter as they update.

## Layout

The fleet page shows the header, a sticky tab bar, then one of two panels: `#needs`, holding the waiting queue, the checks and the finished-tasks link, or `#chat`, holding the supervisor panel, the timeline and the composer. The tabs show at every width. There is no two-column grid and no width breakpoint between the panels.

- **Tabs:** "Needs you" with its waiting-count pill, and "Chat" with an accent dot while the supervisor works. At 600px wide and above, the tabs take their content width and start at the panels' left edge, because the tab bar pads its sides by `max(16px, (100% - 880px) / 2)`. Below 600px wide they split the bar into equal halves. The "Needs you" section head is hidden at every width, because the tab carries the count.
- **Panels:** every block in `#needs` and `#chat` is full width up to 880px, centred. The fleet page's board has no maximum width, so `#needs` scrolls from the window's gutter. The timeline spans the board too and pads its sides by `max(2px, (100% - 880px) / 2)`, so its entries line up with the other blocks while its scrollbar sits at the gutter.
- **App shell,** with JavaScript (`app.js` sets `body[data-shell]`) at `(min-height:600px)`: the body fills `100dvh` as a column and the page itself never scrolls. `#needs` scrolls on its own. In `#chat` the blocks sit 12px apart; the timeline takes the remaining height and scrolls, and the composer sits below it.
- **Flowing page,** below 600px tall or without JavaScript: the page scrolls, and the composer sticks to the bottom of the viewport. With JavaScript the timeline stops scrolling on its own, so the page scrolls as one. Without JavaScript the timeline keeps its own scroll between 12rem and 65vh, both panels show, and the tabs are plain anchors to `#needs` and `#chat`.
- **Narrow,** below 600px wide: board padding drops from 16px to 12px, and check meta and the header's fleet-id chip are hidden.

`TestTheAppShellNeedsHeight` pins the `(min-height:600px)` and `(max-height:599px)` queries and `[data-shell]`, and fails if `min-width:900px` returns. `TestTabsShowAtEveryWidth` pins the tabs and the 880px panels.

Task, decision and conversation pages are single-column threads, and the fleet list and error page are single-column lists, each up to 980px wide, padded 20px at the top, 16px at the sides and 48px at the bottom, with a 14px gap. The header's inner row stays at most 1440px wide.

Spacing steps are 4, 6, 8, 10, 12, 16, 20 and 24px. List rows use 8px by 12px (7px by 12px for check rows), boxes pad 10 to 12px, the page gutter is 16px, blocks in a panel sit 20px apart (12px in the app shell's Chat panel), and the two panels, when both show without JavaScript, sit 24px apart. Gaps inside a row are 6 to 8px.

### Named Rules
**The Region Rule.** Live content lives in exactly four regions, `status`, `timeline`, `queue` and `tasks`, each a `section` with `data-region`. The server sends each region's HTML over Server-Sent Events and `app.js` swaps the region whole, keeping typed drafts, open or closed waiting items, focus and a timeline pinned to its newest entry. `#composer` and `#toast` stay outside every region. `internal/board/page_test.go` enforces the names and the composer's place, and `TestFirstViewportOrder` enforces the order needs, queue, tasks, chat, status, timeline, composer.

**The Clear-In-Place Rule.** The first waiting item stands alone and open, with its action in its body. The rest share one bordered list, collapsed to one line each, and open in place.

**The Hands-Off Tabs Rule.** The board opens on the tab the URL hash names, else on Needs you when something waits, else on Chat. A click on a tab writes its hash with `history.replaceState`. Nothing else changes the tab: a new waiting item raises the count, the title and the favicon, and never pulls the operator out of Chat.

## Elevation & Depth

The board is flat. Depth comes from hairline borders and one tonal step: `card` sits one step off `bg`, and an open waiting item or comment shows its header on `card` above a hairline. Shadows appear only where something floats over content.

### Shadow Vocabulary
- **Toast lift** (`box-shadow: 0 8px 24px var(--shadow)`): the error toast fixed at the bottom right.
- **Sticky composer** (`box-shadow: 0 -8px 16px var(--shadow)`): the composer when it sticks to the bottom of the flowing page, so the timeline reads as passing under it.
- **Keycap edge** (`box-shadow: inset 0 -1px 0 var(--line)`): the bottom edge of a key button, so it reads as a key rather than a command. The keyboard hint beside Send draws its edge as a 2px bottom border instead.
- **Terminal scroll fade**: two background gradients on the blocked-screen block fade its right edge while it can still scroll sideways.

Layers are few and fixed: a reply table's sticky first column at 1, the sticky composer at 2 so it covers those cells, the tab bar at 3 and the toast at 5.

### Named Rules
**The Hairline Rule.** Resting surfaces carry a 1px `line` border and no shadow. A shadow means the element floats over the page.

## Shapes

Every box, button, input, ref chip, terminal block, reply table and waiting item has the same gently rounded 6px corner (`rounded.md`). The focus ring, markdown inline code and the keyboard hint use 4px. Pills (999px) are reserved for small inline labels: PR chips, the waiting count, status pills and the "Queued" and "switching to" labels. Circles appear on the fleet swatch, on the ring behind event icons, and on the 6px activity dots of the working pill and the Chat tab. Lists of rows share one outer border and radius, with hairlines between rows and no radius on the rows themselves; a reply table follows the same rule. The only dashed border is the fence around a blocked screen's key buttons.

### Icons
Icons are Hand's own inline SVG in `internal/board/templates/icons.html`. Each uses a 16 by 16 viewBox, `stroke="currentColor"` at 1.5 width with round caps and joins, no fill except small solid dots, `aria-hidden="true"`, and one to four paths. They render at 16px, except the mark in the header at 22px. They take their colour from the surrounding status. `TestIconsAreOwnInlineSVG` checks the viewBox, `aria-hidden`, the path count and that none names Octicons.

The circle is the shared base: failure is a circle with an X, passing a circle with a tick, waiting a circle with three dots, running an open arc around a solid dot, idle a dashed circle, and resume a circle with a play triangle. Blocked is an octagon with a bar, decision a speech box with a question mark, report a page with lines. The mark is a dial with a needle, the bell beside "Notify me" is a plain bell, and the chevron beside "Switch model" is one downward stroke that turns 180° when the disclosure opens.

## Components

### Buttons
Compact and quiet, closer to a review tool's toolbar than to a call to action.
- **Shape:** gently rounded (6px), 1px hairline border.
- **Default:** card fill, ink text, 13px medium, 4px by 12px. Hover takes the hover wash. Disabled drops to 55% opacity.
- **Primary:** review-green fill, white text, no visible border. Hover brightens it by 8%. One per form, on the committing action: Answer, Send, and Resume in the waiting queue. The supervisor panel's controls stay default.
- **Quiet:** no fill or border, muted text with an icon, turning to ink on hover. Used for "Notify me" in the header, which shows only while notification permission is undecided. The "Switch model" disclosure gives its summary the same treatment.
- **Focus:** every focusable element shows a 2px accent outline, 2px out, with a 4px corner.

### Key fence
A blocked screen's keys sit in a dashed-border box with 6px gaps. Each key is a default button in monospace, at least 2.6em wide, with the keycap edge. Its label is the key's own legend (Enter, Esc, ↑, ↓, digits). Each key is its own form that carries the screen revision, so a stale screen refuses the key.

### Chips and pills
- **Ref chip:** monospace 12px on card fill, hairline border, 6px corner, 0 by 6px. As a link it takes an accent border and text on hover.
- **PR chip:** a pill with a hairline border, 12px semibold accent text.
- **Waiting count:** a pill at least 22px wide, neutral when zero and amber when anything waits. It sits in the Needs you tab.
- **Status pill:** a pill with the pale status fill and the status text colour, 0 by 10px. The stylesheet styles the states neutral, running, passing, ready, working, waiting and failing; running, passing and ready share the green fill. A supervisor's pill reads working, ready, blocked, launching, unreachable or its ended status. The working pill takes the pale accent fill and accent text, with a 6px dot 6px before the label that fades to 30% and back on a 2s ease-in-out loop, and holds still under `prefers-reduced-motion`.
- **Queued label:** an 11px amber outline pill. The same label reads "switching to M E" in the status line while a model switch is pending.
- **Keyboard hint:** a `kbd` keycap in 11px muted monospace, 0 by 5px, with a hairline border, a 2px bottom edge and a 4px corner. It reads "Ctrl Enter", or "⌘ Enter" on Apple platforms, and hides on coarse pointers.

### Inputs
- **Style:** page-ground fill, hairline border, 6px corner, 5px by 8px, inheriting the body face. Text areas are full width, at least 4.5em tall, and resize vertically, except the composer's message box where the browser supports `field-sizing` (see Composer). Placeholders take muted ink.
- **Focus:** the border goes transparent and a 2px accent outline sits just inside it.

### Waiting item
The signature component, a `details` element per item in the queue.
- **Header row:** status icon, ref chip, title, then the task ref, meta and time, at 8px by 12px. It wraps on narrow screens. The icon is red for blocked, failure and resume, amber for decision and report.
- **Open:** the header takes the card fill above a hairline; the body pads 12px with 10px gaps and holds the action: an answer box, a terminal excerpt with its key fence, a report excerpt with "Mark read", or a failure reason.
- **On a task page:** what waits on that task sits in a pale-amber box of rows above the goal, each with its icon, ref, title and action.
- **Arrival:** a new item flashes from the pale amber fill to its ground over 1.6s on `cubic-bezier(.16,1,.3,1)`. The flash is off under `prefers-reduced-motion`.

### Terminal excerpt
The last lines of a blocked supervisor screen, in the code-block style on the terminal colours, 6px corner. Lines never wrap; the block scrolls sideways with a thin scrollbar and a fade on the right edge.

### Checks list
An ordered list in one bordered box, one row per task at 7px by 12px, never wrapping: status icon, ref chip, title (truncated with an ellipsis), meta, and a PR chip when a report links a pull request. The icon is red for failing, amber for waiting, green for running or passing, gray for idle. The same row shape lists attempts on the task page.

### Supervisor panel
A bordered box at 10px by 12px. Its status line holds the status pill, the supervisor ref, the harness and model in semibold, the reason as muted meta, the "switching to" label while a switch is pending, and the count of queued messages. A stale-server warning follows in amber. Below it sits one row of controls, which appear only on a loopback listener: Interrupt (while Luvus reports the agent and it is not blocked), Stop, and either "Cancel switch" or the "Switch model" disclosure. An ended supervisor offers Resume, New session and "other harness…" instead.
- **Switch model:** a disclosure for a running claude or codex supervisor. Its summary is a quiet 13px muted label with the chevron, turning to ink on hover. Open, it takes its own line and holds one row: a profile select limited to profiles with the running harness, then "or", model and effort inputs whose placeholders show the current values, and a default button that reads "Switch now" when the supervisor is ready and "Switch after this turn" otherwise. The CLI validates the fields, and a refusal reaches the toast.

### Timeline
- **Comment:** a bordered box with a header strip on card fill (author in 13px bold ink, time in 12px muted) above a body at 8px by 12px. The operator's header takes the pale accent fill. Supervisor bodies render the safe markdown subset (see Reply markdown); operator bodies stay plain text with line breaks kept.
- **Event:** one muted 12px line with the event dot in a small ringed circle.
- **Working line:** while the supervisor works, the newest entry is an event line whose icon ring and dot take the accent: "sN is working · 4m, on your message from 08:48", "… on a wake" or "… since it started". `app.js` relabels the elapsed time every 30 seconds as `<1m`, `4m` or `1h 12m`, and shows the message's time as a local clock time.
- **Thread line:** a 2px hairline connector runs between entries at the icon column. The newest entry sits at the bottom, and the timeline stays pinned there while the operator is within 48px of it. Its scrollbar is thin, with a stable gutter on both edges so the entries stay centred.

### Reply markdown
Supervisor replies, plans and reports render an in-house safe subset from `internal/markdown`: bold, italics, strikethrough, lists, inline and block code, links, headings, rules, one level of quotes and GFM tables. Blocks sit 8px apart.
- **Headings:** one or two `#` render on the Title step (16/24), three to six on Heading Small (14/22), both at 600.
- **Rule:** a 1px hairline with 12px above and below.
- **Quote:** muted text, 12px in from a 1px hairline at its left edge. The spec proposed 2px; at 1px the edge reads as one more hairline rather than a coloured side stripe.
- **Struck text:** muted.
- **Code:** inline code on card fill at 85% with a 4px corner; code blocks on card fill at 10px by 12px with a 6px corner, lines kept, scrolling sideways.
- **Table:** a focusable scroll wrapper, a region labelled "Table", as wide as its table and never wider than the reply, with a hairline border and a 6px corner. Hairlines split the rows; there is no zebra and no other vertical line. Cells pad 6px by 10px, align to the top and are at least 10ch wide. The head is 12px muted semibold on card fill and does not wrap. Numbers are tabular. A column whose non-empty body cells are all numeric is right-aligned and does not wrap, and explicit `:-`, `-:` and `:-:` delimiters set left, right or centre through classes, since the CSP forbids `style=`. The first column sticks to the left edge on the page ground, unwrapped, with one hairline after it, so row labels stay in view while the table scrolls sideways.

### Composer
A card-fill box at 10px with 8px gaps, holding the message box, then one row with the hint at the left and the keyboard hint and the primary Send button at the right. Send stays at the right edge when the row wraps. The composer is never inside a region, so a region swap cannot erase a draft.
- **Growth:** the message box starts at three lines. Where the browser supports `field-sizing: content`, it grows with its text and has no resize handle; elsewhere it resizes vertically. It stops at 40dvh in the app shell and 30dvh on the flowing page, then scrolls inside.
- **Newest message in view:** when the composer changes height and the operator was within 48px of the newest entry, the board pins the timeline again, in the shell's timeline or on the flowing page's own scroll.
- **Sending:** Ctrl+Enter, or Cmd+Enter, sends the composer or an inline answer box. Enter stays a newline.
- **Hint:** the muted line follows the supervisor: "sN is ready; it reads your message now.", "sN is working; your message goes to it right away.", "sN is waiting on a screen in Needs you; your message waits too.", "sN switches to M E after this turn; your message waits for the new session.", "sN is starting; your message waits.", "No supervisor is running; your message waits until one starts." or "Luvus restarted; your message waits until hand watch settles it." A pending switch outranks working and ready.
- **On the flowing page:** it sticks to the bottom of the viewport, above reply-table cells, with the sticky composer shadow.

### Navigation
- **Header:** a 3px tint rule, then the tinted mark, the fleet name as the title, the fleet-id ref chip, and the quiet "Notify me" button at the far end.
- **Tabs:** a sticky bar on the header ground with a hairline below. Each tab is an anchor in semibold muted text at 10px by 12px (10px by 14px at 600px wide and above), with its count pill or dot 6px after the label, turning to ink on hover. The current tab takes ink text and a 2px accent underline. The Chat dot is a static 6px accent circle, shown only while the supervisor works.
- **Crumbs:** thread pages open with 13px muted crumbs of fleet and ref chips above the headline.

### Favicon and tab title
The tab shows one of three hashed SVG favicons: the mark alone; the mark with an accent dot while the supervisor works; or the mark with an amber dot when something waits or the supervisor is blocked, which outranks working. The mark strokes in a fixed mid-tone gray (#6e7681) and the dots are #3d7fe0 and #a87400, all as presentation attributes, because a static asset served under the strict CSP cannot read the page's properties or follow its theme. The tab title is the fleet name, prefixed with "(N) " when something waits or "● " while the supervisor works.

### Toast
A fixed box at the bottom right, at most 28em wide, with a red hairline border, page-ground fill and the toast lift. It holds the server's error text for 8 seconds, as a polite live region.

## Do's and Don'ts

### Do:
- **Do** take every colour from a custom property on `:root` in `board.css`, and give each new property a value in the `prefers-color-scheme:dark` block too. The favicons are the one exception: a static SVG takes fixed mid-tone presentation attributes.
- **Do** keep status colours at 4.5:1 on `bg` and `card`, and tints at 3:1 on `header`, in both themes; `TestColoursMeetContrast` checks both.
- **Do** draw new icons as Hand's own inline SVG in `icons.html`: a 16px viewBox, `currentColor` stroke at 1.5, round caps and joins, `aria-hidden="true"`, one to four paths.
- **Do** put live content in one of the four regions, `status`, `timeline`, `queue` or `tasks`, and keep `#composer` and `#toast` outside every region.
- **Do** use the 1px `line` hairline and the `bg`-to-`card` step for structure, the 6px corner on every box, and the 999px pill only for small inline labels.
- **Do** show a standalone ref as a monospace ref chip, linked when a page exists for it.
- **Do** lay out for both scenes with the paired height queries: the app shell at `(min-height:600px)` under `body[data-shell]`, and the flowing page at `(max-height:599px)` or without `data-shell`. The tabs show at every width.
- **Do** keep the primary green button for the single committing action of a form.
- **Do** stop every animation under `prefers-reduced-motion`: the arrival flash and the working dot's pulse.

### Don't:
- **Don't** add inline script, a `<style>` element, a `style=` attribute, `@font-face` or `url(` in the stylesheet; the CSP is `default-src 'none'` with `script-src 'self'` and `style-src 'self'`, and the board tests reject each of these.
- **Don't** use Octicons, an icon package or an icon font.
- **Don't** use the fleet tint anywhere but the top rule, the header mark and the fleet-list swatch.
- **Don't** let the blue accent mark a check, a waiting item or an outcome; it marks links, focus and the supervisor at work. Don't let the fleet tint carry any state, and don't add a fifth status colour.
- **Don't** put a shadow on a resting surface; shadows belong to the toast, the sticky composer on the flowing page and the keycap edge.
- **Don't** add a theme toggle or a second font; themes follow `prefers-color-scheme` and type is the system stack.
- **Don't** build the generic dashboard: no sidebar, no KPI tiles, no grid of cards.
- **Don't** bring back the two-column grid or a `min-width:900px` split; `TestTheAppShellNeedsHeight` rejects it.
- **Don't** switch tabs for the operator; only the URL hash, the opening rule and a click choose the panel.
- **Don't** remove `[hidden]{display:none!important}`; the tabs rely on it to hide the other panel.
