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
  - the rail: 56px of icons (232px with labels) for Board with its Needs count, toned by the worst item's severity as the Board's is, Tasks, Conversation with its working dot, Screen and an inert Phone (Board returns to the two panes; Tasks, Conversation, Screen and Phone do nothing until the pages they name exist), then a tinted mark for each fleet this machine can open;
  - the two panes: Needs and Tasks on the left, divided by a draggable divider from the supervisor on the right (the chat starts at 38% wide clamped to 400 to 720 px and may be dragged down to 360 px, the work pane never under 440 px, the width kept while the window runs; the window is never narrower than 860 px, so that rule holds until the narrow tabs exist);
  - the supervisor's pane head: its ref and model, the controls that apply (Resume with New session in the menu, or Start new, same as the supervisor; Interrupt; Force, a stand-in for the Board's "Screen misread?" until the blocked item is built; Stop with its second press) and an overflow menu with Refresh now and Open in browser; with no supervisor ever, one Open in browser button and the status's composer hint under the composer; a conversation that cannot be read shows its own error with Retry in the chat pane and keeps what it held; a blocked supervisor's question with one button per option (or the key toolbar) under it;
  - Needs: bands by severity in the page's order, each headed by its word in the band's tone; 44px rows with the kind's icon, refs and time; the top item open as a card with its band's toned header, any row opening in place and closing again, one open at a time; a report opens to its excerpt and Mark read, a failure to its attempt's reason, a decision to its options and note; a kind with nothing to show stays inert; after Mark read or an answer a receipt toast shows for 4 seconds and the next item opens; nothing waiting shows All clear, when it cleared and the summary;
  - the conversation: the entries, the working turn and its queued messages, opened at the newest entry, and the composer pinned below it: a multi-line message field with Send and Clear;
  - Tasks: the grouped task list, and each task's detail;
  - the detail of a task, an attempt, a decision and a report, opened in the left pane with Back;
  - an open decision's options, its note field and Send answer, a report's Ack, a message to the supervisor, and the supervisor controls: start, resume, stop, interrupt, force, and, while it is blocked, the ask's option buttons or the fixed key toolbar;
  - the empty and error states, including no board, no fleet, a token the board refuses and an action the board refuses;
  - auto-refresh: the selected fleet's status and the open screen's data poll while the window is open (see Refresh).
- **Not in v1:** any other write (attachments, because the API sends text only; a model switch, because the API has no route for it), the document viewer, phone pairing, the archive's search, and a server-push channel: updates arrive by polling, never over Server-Sent Events.
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

**The same on-call board, with a composer.** The memorable moment is the ref: the window opens on the fleet's state, and one click on a Needs row opens it in place, and one on a task row puts its detail in place, with Back returning to the list exactly where it was left.

## Direction contract

THESIS: The same on-call board, in a native window. The fleet's state is read; the only changes the window makes are a message to the supervisor, an answer to an open decision, an acknowledgement of a report, and the supervisor controls the console already offers. What waits on the operator, the conversation and the tasks are the same rows as the Board's, and a ref opens its detail in place. It refuses a second owner of state, a second design language and any other write.

OWN-WORLD: The Board's palette with both themes: white ground, `#f6f7f9` panel, `#f1f3f5` card, `#e3e6ea` hairlines and `#15171c` ink; the fail red, wait amber, pass green and review blue, and their pale fills. Five type sizes (22, 18, 15, 14, 12px), system sans, system mono for refs, times and figures. A 56px rail, a 48px masthead with a 3px rule in the fleet's tint and one scrolling content pane; resting surfaces carry a hairline, never a shadow.

STORY: The operator opens the window and knows whether anything is paged: the masthead names the fleet and the supervisor's state, the rail's Board icon counts the Needs, in the tone of the worst. In the pane head they start, resume, stop (two presses) or interrupt the supervisor; when it is blocked, Force and the fixed key toolbar appear, and each press carries the screen's revision, digest and last pressed sequence. They read the toned rows, click the decision, choose an option, add a note and send the answer; a report a step away gets Ack. In the pane beside the work they type a message and press Ctrl+Enter (Cmd+Enter on macOS) to send it, or Enter for a new line; a half-typed draft waits there after a fleet switch or a closed window. Back returns. They close the window; nothing else about the fleet changes.

FIRST VIEWPORT: At 1100×760: the 56px icon rail at the left (Board with its Needs count, Tasks, Conversation, Screen, Phone, then the fleet marks); the masthead with the tint rule, the fleet's mark, name and id, the state pill, the queued count and the context bar and figure; below it, the Needs and Tasks, and the supervisor's conversation with its pane head and its composer.

FORM: It takes these raises:
- from the Board: the severity ramp, the pale tone fills, the word-plus-shape rule and the flat resting surface;
- from a native list: keyboard focus, a hover wash and a pointer on every row, with no page reload between screens;
- from a viewer: a ref opens where it is, Back returns to the exact list, and Refresh now re-reads the screen.

Signature moment: one click from a Needs row to its decision, then Open task to the task, then Back twice; the list is where it was. The only traces are the message, the answer, the acknowledgement and the supervisor action the Board records.

FINISH: unreviewed and undocumented is unfinished; v1 ends with the tester scenes, the fourteen screenshots and this brief.

## Screens and scenes

One screenshot per scene, 1100×760, under `docs/images/desktop/`, written by the tests when `DESKTOP_SHOTS` names their folder:

| File | Scene |
|---|---|
| `01-fleet-switch.png` | the rail after a switch, the second fleet's masthead |
| `02-panes.png` | the two panes: Needs and Tasks, the supervisor's head, its conversation and its composer |
| `06-detail.png` | a task's plan, delivered PR, decisions, reports, attempts and events |
| `07-empty.png` | All clear, no tasks, and no supervisor: the head offers Open in browser |
| `08-error.png` | a token the board refuses, with Retry |
| `09-decision-answer.png` | an open decision with an option chosen and the note field |
| `10-report-ack.png` | a report with its Ack |
| `11-composer.png` | a typed message in the composer, with Send and Clear |
| `12-composer-error.png` | a failed send, its error and the text it kept |
| `13-supervisor-controls.png` | a blocked supervisor with its controls, Force and the fixed key toolbar |
| `14-open-error.png` | Open in browser with no `hand` to run, and its inline error |
| `15-delivered.png` | a task's Delivered list: a PR and a link that open in the browser, a fleet file that opens in the Board's viewer, and a file outside the fleet as plain text |
| `16-blocked-ask.png` | a blocked supervisor's question with one button per option, in place of the key toolbar |
| `18-overflow-menu.png` | the pane head's menu: Refresh now and Open in browser |
| `19-stop-armed.png` | Stop after its first press: "Press again to stop" |
| `20-panes-<width>x<height>-<light or dark>.png` | the two panes at 1100×760 and 1440×900 in both themes |
| `21-blocked-<width>x<height>-<light or dark>.png` | a blocked supervisor's ask in the chat pane, at 860×700 and 1100×760 in both themes |
| `22-resumable-1100x760-<light or dark>.png` | the head of a supervisor that can be resumed: Resume, with New session in the menu |
| `23-conversation-<light or dark>.png` | the conversation as the Board draws it: the supervisor's and the operator's bubbles, the hand lines, a folded wake group, a day divider and the working line |
| `24-conversation-unfolded-<light or dark>.png` | the same with the wake group unfolded |
| `25-conversation-scrolled-<light or dark>.png` | scrolled up, with the button that returns to the newest entry |
| `26-tasks-1100x760-<light or dark>.png` | a task list as the Board draws it: 48 px rows with a state icon, the meta line, project and deliverable badges, a sub-task indented under its parent, and the group heads |

The day divider prints the day in the entries' own clock (`Sat, Oct 31`), not the Board's server-side UTC badge (`Sat 31 Oct UTC`): every entry prints a local time, so a UTC divider would draw the day a whole offset away from the entries under it. A deliberate deviation from the Board.

## Auth decision

The desktop reads the fleet's `board.token` and sends it as the cookie the board already accepts for that fleet, the same value `hand open` exchanges for a browser. A fleet without a token, or whose token the board refuses, is named in the error with the fix (`hand open` in the fleet); the token never appears in a URL, an error or a log. The API adds the actions and a read-only `GET /api/v1/session` that answers the session's CSRF token; a mutation sends it as `X-Hand-CSRF`, and the board refuses one without it with `403 csrf_required`. There is no local-trust bypass.

## Keys decision

The window sends each press with the status's `revision`, `digest` and `pressed` sequence. While the supervisor is blocked and the status carries an `ask` (the question and option labels the Board's Ask card already parses, never the raw screen), it shows the question and one button per option, each sending that option's keys as one press. Only without an `ask`, for a screen the parser does not understand, does it show the fixed key toolbar. The Needs row of a blocked supervisor opens to the same question and buttons, beside the ones in the pane head's pane.

## Rules

**The Actions Rule.** The only forms v1 renders are the composer, an open decision's answer and a report's Ack; the supervisor controls are buttons in the pane head, and the ask's option buttons and key toolbar press keys, with no fields. Every other click only changes which screen is shown, except Open in browser in the overflow menu, which runs `hand open` for the screen's object in the fleet's folder and writes nothing itself. The composer sends text only; attachments wait for an API contract that carries an inbox file by name.

**The One Owner Rule.** The board owns state. The window reads `board.token`, `board.addr` and the fleet links, opens no fleet database, writes only its composer draft file, and does not import the CLI, the board service or the state store.

**The Loopback Rule.** The window opens a board only on a loopback address; a network board is refused with the reason, because the API refuses it too.

**The Same Palette Rule.** Every colour, size and tone comes from the board's own values through `paletteFor` and `boardTheme`; a new state takes the existing ramp, never a new colour.

**The Closing Changes Nothing Rule.** The window is a process of its own: its close and its crash leave the board, the watcher and every fleet as they were.

## Refresh

The selected fleet refreshes itself, off the render path: a timer posts a tick to the window's loop, which reads in a goroutine and applies the answer on the window's thread. It asks only for the status, the conversation and the open screen's data (the tasks list, or the opened task, attempt, decision or report), never every fleet and never a write, and sends each endpoint's last change cursor as `?since=`.

| Window state | Interval |
|---|---|
| focused | 1 s |
| unfocused | 5 s |
| minimised | paused |

The loop pauses while no fleet is selected, is cancelled on a fleet switch and on close, and starts again clean for the fleet just selected. A `304` changes nothing; a `200` applies the status always and the screen's data only while that screen and ref are still the open one, so a poll never clobbers a screen the operator navigated to meanwhile. A tick is bounded at 5 s; a failure leaves the content in place, shows `Stale · retrying` in the masthead (the error is in its tooltip), and backs off that read alone, doubling its wait up to 60 s and resetting on its next answer, while the other reads go on every tick. A poll clears a read error only when it refreshed every half the error covers: a fresh status does not hide a failed screen's read, nor a fresh screen a failed status. A read that succeeds clears `Stale · retrying`; Refresh now re-arms the loop at the base interval. A refresh never touches the composer draft: the timeline may be replaced while the text stays typed.
