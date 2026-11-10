# JSON API

The board serves the read model behind its pages as JSON on the same loopback server, under `/api/v1`. It is for a local client, such as the desktop window: a second window on the fleet, never a second owner of its state. The web pages do not use it, and no page, template, stylesheet or script changes because of it.

The API is the read model plus its actions in v1: sending a message to the supervisor, answering an open decision, acknowledging a report, and the supervisor controls (start, resume, stop, interrupt, force and keys). It never returns `last_screen` or a terminal screen. The one text parsed from a screen it carries is a blocked supervisor's `ask` (see `Status`), and it never returns the board token outside the session endpoint. `GET /api/v1/session` returns the session's CSRF form token, which for a laptop session is `board.token` itself; a client must treat it as the secret it is, keep it in memory and never log or store it. A mutation sends it as `X-Hand-CSRF`.

## Address and login

Each fleet keeps its own prefix, as its pages do:

```
http://127.0.0.1:7777/<fleet id>/api/v1/status
```

A board mounted without a fleet prefix, as in tests, answers at `/api/v1/status`. The API uses the same session cookie as the pages: `hand_board_<fleet id>`, or the cookie of a paired phone. Open the fleet once with `hand open` (or paste the link `hand open --print` prints) and the browser or client holds it. There is no `?token=` login on the API.

A local client proves the service at the recorded port is the board before it sends the token, since `board.addr` outlives a board that exits abnormally: `GET /<fleet id>/proof?nonce=<random>` answers the hex HMAC-SHA256 of the client's nonce keyed by `board.token`, with no cookie; a client that reads the token from the fleet compares it and only then sends the cookie. `hand open` and the desktop window both do this, and neither sends the token to a service that fails it.

A request without that cookie gets `401` with `unauthorized`; a wrong or expired cookie gets the same. The API answers only on a loopback board: a network-address board, a request through a proxy (`X-Forwarded-For`, `Forwarded`), the peer listener, and a Tailscale Funnel request all get `403` with `forbidden`.

The actions need the session's CSRF token in an `X-Hand-CSRF` header, on top of the cookie. Read it from `GET /api/v1/session`, whose `data` is `{"csrf": "..."}`; a laptop session's value is `board.token`, the same token the cookie holds, and a paired phone's is derived from its pairing. A mutation without the header, or with a wrong one, gets `403` with `csrf_required`. `GET` and `HEAD` are the only other methods: a `POST` outside the action paths gets `405` with `method_not_allowed` and `Allow: GET, HEAD`, and `PUT`, `PATCH` and `DELETE` get `405` with an `Allow` that names what the requested path takes (`GET, HEAD`, or `POST` on an action path).

## Envelope

Every success is one envelope:

```json
{
  "version": 1,
  "fleet": {"id": "f00000000e0aa", "name": "hand"},
  "cursor": "ZjAwMDAwMDAwZTBhYQAyMw",
  "generated_at": "2026-11-01T05:00:00Z",
  "data": {}
}
```

| Field | Meaning |
|---|---|
| `version` | `1` for this contract |
| `fleet` | the fleet's id and name, the same on every page |
| `cursor` | an opaque change cursor; see below |
| `generated_at` | the board's clock when it built this response |
| `data` | the endpoint's object |

Responses are `application/json; charset=utf-8`, `Cache-Control: no-store`, and carry the same `Content-Security-Policy`, `X-Frame-Options` and `Referrer-Policy` headers as every board page.

## The change cursor

`cursor` is opaque: a client stores it and sends it back as `?since=`. It encodes the fleet and a signed fingerprint of the fleet's name and that response's `data`, including the facts the board reads outside its database (the harness transcript and the pane), so it is not a timestamp and not just an event sequence. The signature is keyed by the board, so a cursor it never issued is rejected even when its fleet and fingerprint look well formed.

- No `?since=`: always `200` with the full data.
- `?since=` equal to the fingerprint the board computes now: `304 Not Modified`, an empty body, and the current value in `X-Hand-Cursor`. Poll that first; the board rebuilds the page's data to fingerprint it, so a poll costs a read, not a body.
- `?since=` from before any change to that data: `200` with the new data and a new cursor.
- A cursor this board did not issue (another fleet, a typo, a truncated value): `400` with `bad_cursor`.

Every `200` and `304` carries `X-Hand-Cursor`; a client may use it instead of reading the envelope.

## Endpoints

| Endpoint | `data` | Page it mirrors |
|---|---|---|
| `GET /api/v1/status` | `Status`: supervisor, agent state, pill, gauge, compactions, Needs summary and items, task counts, project count | the fleet page's status line and Needs |
| `GET /api/v1/timeline[?before=N]` | `Timeline`: `live`, `working`, `entries`, `older`, `note` | the fleet page's Chat; `before` pages like "Older messages" |
| `GET /api/v1/tasks[?all=1]` | `Tasks`: `all`, `groups`, `hidden`, `finished_count` | the fleet page's Tasks |
| `GET /api/v1/task/{tN}` | `TaskDetail`: task, plan, delivered, waits, open and all decisions, unread and recent reports, attempts, events | `/task/tN` |
| `GET /api/v1/attempt/{aN}` | `AttemptDetail`: attempt, task, state, brief, model_note, duration, reports, events, delivered, transcript | `/attempt/aN` |
| `GET /api/v1/decision/{dN}` | `DecisionDetail`: decision, task, prose body, options | `/decision/dN` |
| `GET /api/v1/report/{rN}` | `ReportDetail`: report, task, delivered | the report's card on `/task/tN` |
| `GET /api/v1/session` | `Session`: `csrf` | none; the form token a mutation sends |
| `POST /api/v1/message` | the same `Timeline` the GET returns, after the message | the fleet page's composer |
| `POST /api/v1/decision/{dN}/answer` | the same `DecisionDetail` the GET returns, after the answer | `/decision/dN` |
| `POST /api/v1/report/{rN}/ack` | the same `ReportDetail` the GET returns, after the ack | the report's card on `/task/tN` |
| `POST /api/v1/supervisor/start` | the same `Status` the GET returns, after the start | the console's Start or New session |
| `POST /api/v1/supervisor/resume` | the same `Status` after the resume | the console's Resume |
| `POST /api/v1/supervisor/stop` | the same `Status` after the stop | the console's Stop |
| `POST /api/v1/supervisor/interrupt` | the same `Status` after the interrupt | the console's Interrupt |
| `POST /api/v1/supervisor/force` | the same `Status` after the force | the blocked screen's "Type queued messages anyway" |
| `POST /api/v1/supervisor/keys` | the same `Status` after the key press | the supervisor's fixed key buttons |

`{tN}`, `{aN}`, `{dN}` and `{rN}` take the ref or the bare number, as the pages do. An unknown ref is `404`.

`/api/v1/tasks` is the fleet page's list (active and inbox tasks plus the five most recent finished ones), not the searchable Tasks page; `all=1` shows the finished tasks too. The archive and its search are not in v1.

`before` must be a non-negative message number; a word, a negative value or any other form is `400` with `bad_request`.

### `Status.data`

```json
{
  "supervisor": {"ref": "s1", "harness": "claude", "model": "sonnet", "effort": "low",
                 "status": "running", "live": true, "pending": 0, "resumable": false},
  "agent_state": "working",
  "composer_hint": "",
  "pill": "working",
  "line": "working",
  "gauge": {"text": "510K / 1M", "level": "", "pct": 51},
  "compactions": 0,
  "needs": {
    "waiting": 2, "more": 0,
    "worst": "decision", "worst_word": "waiting", "worst_count": 1,
    "items": [
      {"kind": "decision", "ref": "d1", "title": "Keep the old cookie name?",
       "word": "DECISION", "tone": "wait", "task": "t1", "decision": "d1",
       "headline": "Keep the old cookie name?", "status": "open",
       "created_at": "2026-11-01T04:40:00Z"}
    ]
  },
  "tasks": {"active": 3, "inbox": 3, "done": 0, "abandoned": 0},
  "projects": 1
}
```

`supervisor` is absent when no supervisor exists. `needs.items` follows the page's order and carries the same `kind`, `ref`, `word` and `tone` (`blocked`, `worker`, `failure`, `limited`, `nosup`, `stalled`, `resume`, `decision`, `report`, `quiet`, `long`, `kept`, `artifacts`). `cleared` and `summary` appear when the page shows "All clear". For a `blocked`, `worker` or `limited` item, `title` is absent because it comes from a terminal screen; use `ref`, `kind` and the attempt or supervisor detail instead.

While the supervisor's pane is blocked, `supervisor` also carries `"blocked": true` and a `keys` object:

```json
{"blocked": true, "keys": {"revision": 6, "digest": "f8c722e0710879ae811b2fe7ad0c8ba0", "pressed": 2}}
```

`revision` and `digest` are the same guard the Blocked screen page sends, and `pressed` is the sequence of the last key press (`0` when none has been sent). The digest is a non-reversible hash, not screen content.

When the Board's Ask card would show a question for that screen, `supervisor` also carries `ask`, with the Board's own parsed text and nothing else:

```json
{"ask": {"question": "Do you want to proceed?", "options": [{"label": "2 No", "keys": ["2"]}, {"label": "1 Yes", "keys": ["1"]}]}}
```

`question` is the one question line the Board parser picks from the screen. Each option has the `label` the Ask card shows (for a numbered dialog, the number and the option text clipped to 24 characters) and the `keys` that choose it, in the order they are sent: `["1"]`, or `["down", "enter"]` for a `◯`/`◉` list. Both go through the absolute-path scrub the rest of the API uses. `ask` is absent unless `blocked` is true, when the screen is stale or unreadable, when the parser rejects the screen (no option block), when the question line does not end in `?`, when a question or label looks like a secret (a bearer or API token, a `password=` form, a long token), when there are more than 8 options, and when any option would need a key outside the allowed set. The question is clipped to 200 characters and each label to 60. Nothing else from the screen is carried: not the command or file a dialog shows, not its footer or hint lines, and not the raw text. A client posts an option's `keys` as one press with the three guard values above, so a stale option is refused like any stale key.

### `Timeline.data`

`entries` is the page's conversation, newest first. An entry has `role` (`supervisor`, `operator`, `hand`, `wakes`) plus `at`, `text`, `no`, and where it applies `ref`, `model`, `delivery`, `delivered`, `input`, `queued`, `wakes`, `shots`. A `wakes` entry groups several Hand wake lines in `wakes`. An entry for a screen-derived supervisor event (`supervisor.blocked`, `supervisor.limited`), or one whose text is a harness API error, has no `text`: its `role`, `at` and `ref` remain. A Hand wake line keeps its event kinds and refs but drops a screen-derived detail. `working` is the supervisor's current turn (`ref`, `since`, `on`, `from`, `last`, `input`), absent when it is not working. `older` is the `before` value for the next page, or `0`.

### `Tasks.data`

`groups` are the page's groups in order: `name`, `count`, and `tasks`. A task row has `ref`, `title`, `project`, `status`, `state`, and where it applies `agent`, `attempt`, `delivered`, `waits`, `finished_at` (only for a done or abandoned task), `parent` (the parent's `tN`, for a sub-task) and `children` (how many sub-tasks the task has, absent at zero). A sub-task follows its parent inside its group, and a sub-task whose parent is in another group or not on the page is a root there that still carries `parent`. `hidden` and `finished_count` are the page's counts.

### Detail data

`TaskDetail` has `task` (with `parent` for a sub-task), `pill`, `plan`, `delivered`, `waits`, `open_decisions`, `decisions`, `unread`, `reports`, `more_reports`, `attempts`, `events`, `more_events`, and `children` (the task's sub-tasks as task rows, absent when it has none). A `TaskDetail`'s `attempts` rows are attempt objects with an extra `state` (`running`, `failing`, `passing`, `idle`), and each row's `ref` is the attempt. `AttemptDetail` has `attempt`, `task`, `state`, `brief`, `model_note`, `duration`, `reports`, `events`, `delivered`, and `transcript` (`shown`, `more`, `note`, `model`, `answered`, `turns`; a turn is `role`, `text`, `at`, `queued`). `DecisionDetail` has `decision`, `task`, `body` and `options` (`key`, `label`, `note`, `value`, `recommended`, `other`, `long`). `body` is the prose below the options and includes their text, so a client that renders `options` should not render the option lines twice. `ReportDetail` has `report`, `task` and `delivered`, the report's own `Delivered:` items (or its PR and MR links when it has no such line), each with `ref`, `kind` and, for a file inside the fleet, `href`.

Every ref in a detail is the same `tN`, `aN`, `rN`, `dN`, `sN` the pages and the CLI use. A report object carries the full `body` and a decision object the full `question`, as written; an attempt object carries neither. The board does not render Markdown in the API.

An event from a terminal screen (`attempt.blocked`, `attempt.limited`, `supervisor.blocked`, `supervisor.limited`) carries no `detail`, and neither does an event whose `detail` is a harness API error: that field would be screen text. Its `seq`, `at`, `kind` and `task` remain.

The API never returns a host path. An attempt carries its `branch` but not its worktree or launch command; a file deliverable's `ref` is the file's base name, and its `href` is the board's `/doc/...` path when the file is inside the fleet (absent otherwise). Hand-composed text, such as an attempt's or supervisor's `reason` or an event `detail`, keeps its message but drops the directory part of any absolute path. Free text written by you or the agent, such as a report's `body` or a task's `goal`, is returned as written.

## The actions

Each carries the cookie and `X-Hand-CSRF`, and answers one envelope with the updated object. A mutation ignores `?since=` and never answers `304`.

### Sending a message

`POST /api/v1/message` takes one JSON object:

```json
{"text": "please look at the cookie rename"}
```

It has the fleet page's composer semantics: the board normalizes CRLF to LF and runs the same `CheckMessage` the form does, so whitespace-only text, more than 16 KiB, invalid UTF-8, a control character other than a newline or tab, and text that starts with Hand's own envelope prefix are refused. A JSON decoder would replace an invalid UTF-8 byte or an unpaired surrogate escape with U+FFFD, so the board refuses those before decoding the text. The board then runs the supervisor's send path (`supervisor send --text`) and answers the `Timeline` the GET serves, so a client replaces its conversation with the returned data instead of polling for the message. A queued message has `queued: true` and `delivery: "queued"`; a delivered one carries the delivery the GET reports.

If the board queued the message but cannot read the conversation back, it still answers `200`, with `{"accepted": true}` as its data. The message is in, so a client must not send it again: it clears its draft and refreshes the timeline instead.

- `400 bad_request`: an empty or invalid message, or a body that is not a JSON object.
- `403 forbidden`: the board has its controls off.
- `500 internal`: the supervisor path failed; the message keeps no path.
- `200` with the updated `Timeline`, or `{"accepted": true}` when the conversation could not be read back.

v1 sends text only. The web composer's attachment upload writes an absolute host path into the message; v1 has no attachment upload contract and no field that carries a host path, so attachments need a contract that carries the inbox file by name, and they are not in this version. The text itself is free text: a path the operator types stays in it, exactly as the web composer keeps it.

A client that shows the fleet status re-reads `GET /api/v1/status` after a message, an answer or an ack, because a sent message can change the composer hint and Needs. The supervisor controls answer the status itself, so they need no second read.

### Answering a decision

`POST /api/v1/decision/{dN}/answer` takes one JSON object:

```json
{"choice": "1. Yes, keep it", "note": "the docs keep the old name"}
```

`choice` is an option's `value` from the decision detail; `note` is the note the page offers. Either may be empty, though an option the page marks `other` needs a note. Send one JSON object under 64 KiB: a second object or trailing text is refused before the board changes anything. The board trims both and stores `choice`, a blank line, then `note`, exactly as the page's form does. `answered_by` is `operator (desktop)`.

- `400 bad_request`: an empty answer, an `other` option with no note, or a body that is not a JSON object.
- `404 not_found`: an unknown ref.
- `409 conflict`: the decision is no longer open.
- `200` with the updated `DecisionDetail`.

### Acknowledging a report

`POST /api/v1/report/{rN}/ack` takes no body. It sets `acked_at` and `acked_by` (`operator (desktop)`) and answers the updated `ReportDetail`. An unknown ref is `404`; a report already acknowledged is `409 conflict`.

A client that shows the fleet status re-reads `GET /api/v1/status` after either action, because an answer and an ack leave Needs. The action's own data is already the fresh detail, so the detail screen needs no second read.

### The supervisor controls

`POST /api/v1/supervisor/{start,resume,stop,interrupt,force}` run the same path as the console button they mirror and answer the `Status` the GET serves, built after the control ran, so a client replaces its status with the returned data and needs no second read.

- **start** runs `supervisor start` with no flags, copying the last supervisor's harness, model and effort, like the console's "same as sN". There must be an earlier supervisor to copy: with none, the answer is `409 conflict`, and while one is live it is `409 conflict` too. Start a first supervisor from the CLI, which can choose the harness.
- **resume** continues the last session under a new `sN`; it follows the CLI's rules, so a running supervisor or nothing to resume is a conflict or a not-found.
- **stop**, **interrupt** and **force** take one JSON object, the ref the client's `Status` showed: `{"ref": "s1"}`. The board runs `supervisor stop --expect s1`, `supervisor interrupt --expect s1` or `supervisor force --expect s1`, so a control binds to the supervisor on screen and is `409 conflict` when another one runs by then, rather than stopping, interrupting or typing into the replacement.
- **stop** and **interrupt** need a running supervisor; anything else is `409 conflict`.
- **force** is the blocked screen's "Type queued messages anyway": it types the queued messages, or else the wakes, into the prompt. It follows the CLI: a supervisor that is not blocked gets its queued input delivered normally, still only to the named supervisor, and the answer is still `200`.

Start and resume take no body. When a control ran but the board could not read the status back, the answer is `200` with `{"accepted": true}` instead of a `Status`: the action went through, so a client warns that the status is stale and asks for a refresh rather than reporting a failed action. It keeps the screen it has and needs no second read to report the outcome.

- `400 bad_request`: stop, interrupt or force without one JSON object, or with a ref that is not `sN`.
- `403 forbidden`: the board has its controls off, or the request is not on a loopback board.
- `409 conflict`: the supervisor is in a state the control refuses, or another supervisor is running than the one the request names.
- `404 not_found`: there is nothing to resume.
- `500 internal`: the control failed; the message keeps no path.
- `200` with the updated `Status`, or `{"accepted": true}` when the status could not be read.

### Pressing keys

`POST /api/v1/supervisor/keys` takes one JSON object:

```json
{"keys": ["down", "enter"], "revision": 6, "digest": "f8c722e0710879ae811b2fe7ad0c8ba0", "after": 2, "ref": "s1"}
```

`keys` is a non-empty sequence of the board's allowed keys, `enter`, `esc`, `up`, `down`, `1`, `2` and `3`; they are sent in order in one press. `revision`, `digest` and `after` are all required and are the three values from the supervisor's `keys` object. `ref` is the supervisor the client's `Status` showed. The board runs the same `supervisor keys --revision N --screen DIGEST --after SEQ --expect sN KEY...` path, so the press is refused if the screen moved, another key was pressed since the client read it, or another supervisor runs by then. The answer is the updated `Status`; apply it before anything else, because its `keys` object carries the new revision, digest and pressed sequence.

- `400 bad_request`: a body that is not one JSON object, an empty key sequence, a key outside the set, a negative revision or `after`, a digest that is not 32 lowercase hex characters, or a ref that is not `sN`.
- `403 forbidden`: the board has its controls off, or the request is not on a loopback board.
- `409 conflict`: the screen changed since the given revision, a key was pressed after the given sequence, or another supervisor runs than the one the request names; nothing was sent.
- `200` with the updated `Status`, or `{"accepted": true}` when the press went through but the status could not be read; then the client warns and refreshes.

**The keys decision.** The API never returns a screen. While the supervisor is blocked, `Status.supervisor.keys` carries the revision, digest and pressed sequence the guard needs, and `Status.supervisor.ask` carries the question and option labels the Board's Ask card already shows the operator on the same loopback session, so it adds no new disclosure. A client shows one button per option when `ask` is present, and the fixed seven-key toolbar only when it is absent (a screen the parser does not understand). A blocked worker has no ask: the Board's Ask card covers the supervisor's screen only, and the API carries no worker screen text.

## Errors

Every error is this shape, not an envelope, including a registered fleet whose store cannot be opened on an API route:

```json
{"error": {"status": 404, "code": "not_found", "message": "not found: task t999"}}
```

| Status | `code` | When |
|---|---|---|
| `400` | `bad_request` | a query value the endpoint cannot read (`before`), or a mutation body it cannot read |
| `400` | `bad_cursor` | `since` is not a cursor this board issued |
| `401` | `unauthorized` | no valid board session |
| `403` | `forbidden` | not loopback, a proxy, the peer listener, Funnel, v1 on a network-address board, or a control with the board's controls off |
| `403` | `csrf_required` | a mutation without the session's `X-Hand-CSRF` header, or with a wrong one |
| `404` | `not_found` | an unknown route or ref |
| `405` | `method_not_allowed` | a `POST` outside the action paths, or any other method; the response's `Allow` names what the path takes |
| `409` | `conflict` | a state conflict while reading or acting |
| `500` | `internal` | any other failure |
| `507` | `no_space` | the disk was full |

Messages are scrubbed like the pages': no path and no secret survives. The `code` is the stable part; clients should branch on it.
