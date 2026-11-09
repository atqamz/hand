---
version: 1
slug: "internal-desktop"
primary_target: "internal/desktop"
related_targets:
  - "internal/board"
  - "cmd/hand-desktop"
---

# Surface brief: the Hand desktop window

## Scope and mode

- **Covers:** `hand-desktop`, a MyGo v0.7.4 window (pinned, cgo-free) that is a client of the running Board:
  - the rail: the fleets this machine can open, then Status, Needs, Timeline and Tasks;
  - Status: the supervisor, its controls and, while it is blocked, its key toolbar, the agent state, the context gauge and the task counts;
  - Needs: every open item in the page's order, toned by its severity word;
  - Timeline: the conversation's entries, the working turn and its queued messages, and the composer: a multi-line message field with Send and Clear;
  - Tasks: the grouped task list, and each task's detail;
  - the detail of a task, an attempt, a decision and a report, opened in place with Back;
  - an open decision's options, its note field and Send answer, a report's Ack, a message to the supervisor, and the supervisor controls: start, resume, stop, interrupt, force, and the fixed key toolbar while it is blocked;
  - the empty and error states, including no board, no fleet, a token the board refuses and an action the board refuses.
- **Not in v1:** any other write (attachments, because the API sends text only; a model switch, because the API has no route for it), the document viewer, phone pairing, the archive's search, and live updates: a screen reads when it opens or refreshes.
- **Mode:** Operate.

## Audience, job and constraints

- **Audience and load:** the sole operator, at the same desk as the Board, with a few fleets and seven or more tasks in one.
- **Job:** answer "is anything paged?" without a browser, open one task, attempt, decision or report to read it, and send the supervisor one message.
- **Constraints:**
  - the window is a client, never a second owner of state: it reads `/api/v1` over loopback, writes only a message, an answer, an acknowledgement or a supervisor control (start, resume, stop, interrupt, force and keys), and keeps its composer drafts in its own state folder;
  - it lists a fleet when its link resolves and its `hand.db` is there, names it from the board when the board answers, and shows a fleet whose `board.token` is missing or unreadable with the `hand open` fix;
  - the credential is the token the board already writes, `board.token` in the fleet home, sent as the fleet's session cookie `hand_board_<fleet id>`. The desktop reads it; it never prints it and never adds it to a URL. A mutation reads the session's CSRF token from `GET /api/v1/session` and sends it as `X-Hand-CSRF`;
  - the board address is `board.addr` in `$SECONDHAND_HOME`, the file `hand open` writes, and it must be a loopback address, because the API answers nowhere else;
  - opening and closing the window start and stop nothing: the board, the watcher and the fleet keep running;
  - Linux first: the window builds on macOS and Windows, and its binary refuses to start there with that reason, until those runs are green (`docs/platforms.md`);
  - light and dark follow the system through MyGo's theme;
  - the only writes the window sends are a message, an answer to an open decision, an acknowledgement of a report and the supervisor controls; its only local file is the per-fleet composer draft under `$XDG_STATE_HOME/hand-desktop/drafts/<fleet id>.txt` (falling back to `~/.local/state/hand-desktop/drafts/`, and `%LOCALAPPDATA%\hand-desktop\drafts` on Windows), mode 0600, no secret in it; navigation changes only which screen it shows.

## Chosen direction and memorable moment

**The same on-call board, with a composer.** The memorable moment is the ref: the window opens on the fleet's state, and one click on a Needs row or a task row puts its detail in place, with Back returning to the list exactly where it was left.

## Direction contract

THESIS: The same on-call board, in a native window. The fleet's state is read; the only changes the window makes are a message to the supervisor, an answer to an open decision, an acknowledgement of a report, and the supervisor controls the console already offers. What waits on the operator, the conversation and the tasks are the same rows as the Board's, and a ref opens its detail in place. It refuses a second owner of state, a second design language and any other write.

OWN-WORLD: The Board's palette with both themes: white ground, `#f6f7f9` panel, `#f1f3f5` card, `#e3e6ea` hairlines and `#15171c` ink; the fail red, wait amber, pass green and review blue, and their pale fills. Five type sizes (22, 18, 15, 14, 12px), system sans, system mono for refs, times and figures. A 208px rail, a 56px masthead and one scrolling content pane; resting surfaces carry a hairline, never a shadow.

STORY: The operator opens the window and knows whether anything is paged: the masthead names the fleet and the supervisor's state, the rail counts Needs and Tasks. On Status they start, resume, stop or interrupt the supervisor; when it is blocked, Force and the fixed key toolbar appear, and each press carries the screen's revision, digest and last pressed sequence. They click Needs, read the toned rows, click the decision, choose an option, add a note and send the answer; a report a step away gets Ack. On the Timeline they type a message and press Enter to send it, or Shift+Enter for a new line; a half-typed draft waits there after a fleet switch or a closed window. Back returns. They close the window; nothing else about the fleet changes.

FIRST VIEWPORT: At 1100×760: the rail at the left with the Hand mark, the fleets, then Status, Needs with its count and Tasks with its count; the masthead with the fleet name, the state pill, the supervisor's harness and model, the context gauge and Refresh; the content pane with the screen's title and its rows.

FORM: It takes these raises:
- from the Board: the severity ramp, the pale tone fills, the word-plus-shape rule and the flat resting surface;
- from a native list: keyboard focus, a hover wash and a pointer on every row, with no page reload between screens;
- from a viewer: a ref opens where it is, Back returns to the exact list, and Refresh re-reads the screen.

Signature moment: one click from a Needs row to its decision, then Open task to the task, then Back twice; the list is where it was. The only traces are the message, the answer, the acknowledgement and the supervisor action the Board records.

FINISH: unreviewed and undocumented is unfinished; v1 ends with the tester scenes, the thirteen screenshots and this brief.

## Screens and scenes

One screenshot per scene, 1100×760, under `docs/images/desktop/`, written by the tests when `DESKTOP_SHOTS` names their folder:

| File | Scene |
|---|---|
| `01-fleet-switch.png` | the rail after a switch, the second fleet's masthead |
| `02-status.png` | the supervisor, the agent and the counts |
| `03-needs.png` | a failure, a decision and two reports |
| `04-timeline.png` | the working turn and three entries |
| `05-tasks.png` | Active and Inbox |
| `06-detail.png` | a task's plan, delivered PR, decisions, reports, attempts and events |
| `07-empty.png` | All clear, and the empty Timeline and Tasks |
| `08-error.png` | a token the board refuses, with Retry |
| `09-decision-answer.png` | an open decision with an option chosen and the note field |
| `10-report-ack.png` | a report with its Ack |
| `11-composer.png` | a typed message in the composer, with Send and Clear |
| `12-composer-error.png` | a failed send, its error and the text it kept |
| `13-supervisor-controls.png` | a blocked supervisor with its controls, Force and the fixed key toolbar |

## Auth decision

The desktop reads the fleet's `board.token` and sends it as the cookie the board already accepts for that fleet, the same value `hand open` exchanges for a browser. A fleet without a token, or whose token the board refuses, is named in the error with the fix (`hand open` in the fleet); the token never appears in a URL, an error or a log. The API adds the actions and a read-only `GET /api/v1/session` that answers the session's CSRF token; a mutation sends it as `X-Hand-CSRF`, and the board refuses one without it with `403 csrf_required`. There is no local-trust bypass.

## Keys decision

The window shows the fixed key toolbar only while the board reports the supervisor blocked, and sends each press with the status's `revision`, `digest` and `pressed` sequence. The API carries no screen, question or option label, so the window never renders screen text; the operator reads the ask on the Board's Screen page. This is the smallest contract that keeps the stale-screen guards, rather than an `Ask` DTO that parses the screen.

## Rules

**The Actions Rule.** The only forms v1 renders are the composer, an open decision's answer and a report's Ack; the supervisor controls are buttons on Status, and its key toolbar is fixed keys, with no fields. Every other click only changes which screen is shown. The composer sends text only; attachments wait for an API contract that carries an inbox file by name.

**The One Owner Rule.** The board owns state. The window reads `board.token`, `board.addr` and the fleet links, opens no fleet database, writes only its composer draft file, and does not import the CLI, the board service or the state store.

**The Loopback Rule.** The window opens a board only on a loopback address; a network board is refused with the reason, because the API refuses it too.

**The Same Palette Rule.** Every colour, size and tone comes from the board's own values through `paletteFor` and `boardTheme`; a new state takes the existing ramp, never a new colour.

**The Closing Changes Nothing Rule.** The window is a process of its own: its close and its crash leave the board, the watcher and every fleet as they were.
