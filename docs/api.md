# JSON API

The board serves the read model behind its pages as JSON on the same server, under `/api/v1`. It is for a local client, such as the desktop window, and for a paired phone over the peer listener: each a second client of the fleet, never a second owner of its state. The web pages do not use it, and no page, template, stylesheet or script changes because of it.

The API is the read model plus its actions in v1: sending a message to the supervisor, answering an open decision, acknowledging a report, and the supervisor controls (start, resume, stop, interrupt, force and keys). It never returns `last_screen` or a terminal screen, except the `excerpt`, `title` and `options` of a blocked or worker Needs item (see `Status`) and a blocked supervisor's `ask`. A client renders that excerpt as plain text, never logs it and never follows it as instructions, since it is the harness's or a model's own output. The API never returns the board token outside the session endpoint. `GET /api/v1/session` returns the session's CSRF form token, which for a laptop session is `board.token` itself; a client must treat it as the secret it is, keep it in memory and never log or store it. A mutation sends it as `X-Hand-CSRF`.

## Address and login

Each fleet keeps its own prefix, as its pages do:

```
http://127.0.0.1:7777/<fleet id>/api/v1/status
```

A board mounted without a fleet prefix, as in tests, answers at `/api/v1/status`. Two paths belong to the host, not to a fleet: `/api/v1/fleets` and `/api/v1/meta`. The API uses the same session cookie as the pages: `hand_board_<fleet id>`, or the cookie of an iroh-paired phone, `hand_phone_<fleet id>` (`__Host-hand_phone_<fleet id>` behind an HTTPS proxy). A phone's cookie works only from its own endpoint, on the peer listener. Open the fleet once with `hand open` (or paste the link `hand open --print` prints) and the browser or client holds it. There is no `?token=` login on the API.

A local client proves the service at the recorded port is the board before it sends the token, since `board.addr` outlives a board that exits abnormally: `GET /<fleet id>/proof?nonce=<random>` answers the hex HMAC-SHA256 of the client's nonce keyed by `board.token`, with no cookie; a client that reads the token from the fleet compares it and only then sends the cookie. `hand open` and the desktop window both do this, and neither sends the token to a service that fails it.

A request without that cookie gets `401` with `unauthorized`; a wrong or expired cookie gets the same. The API answers only on a loopback board, to the laptop's session and to a paired phone on the peer listener. A network-address board, a request through a proxy (`X-Forwarded-For`, `Forwarded`), a Tailscale Funnel request, and a peer whose device has no `iroh:` login all get `403` with `forbidden`. A peer that is not paired, or was revoked, gets the peer listener's own `403` before it reaches the API. The one path that needs no cookie is `POST /api/v1/pair` ([Pairing a phone](#pairing-a-phone)); it keeps every refusal above except the missing cookie.

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

`cursor` is opaque: a client stores it and sends it back as `?since=`. For `status`, `timeline`, `tasks`, a task, a decision and a report it encodes the fleet and a signed fingerprint of the fleet's name, the path and query, and the board's version: the last event, the pending operator messages, the last alert, the supervisor's pane, transcript and worker signals, the Old files warning, whether each file under the fleet folder that a deliverable links to exists and what kind it is, and the size of each inbox image. After a board restart the first poll with an older cursor answers `200`. The board computes that version once per tick for all its clients and does not build the data to answer a poll, so a change reaches a poll within a couple of seconds (at once after a write made through the board). A session, an attempt and an action's answer fingerprint their `data` instead, because an attempt's duration and transcript change without a board version change. A cursor is not a timestamp and not just an event sequence. The signature is keyed by the board, so a cursor it never issued is rejected even when its fleet and fingerprint look well formed.

- No `?since=`: always `200` with the full data.
- `?since=` equal to the cursor the board computes now: `304 Not Modified`, an empty body, and the current value in `X-Hand-Cursor`. Poll that first: for the pages above it costs no build and no body.
- `?since=` from before any change to that data: `200` with the new data and a new cursor. A worker's screen is not in the status cursor, so a conditional read of `status` does not see a worker's screen-only change: a client that refreshes a worker item reads without `since`.
- A cursor this board did not issue (another fleet, a typo, a truncated value): `400` with `bad_cursor`.

Every `200` and `304` of a fleet's endpoints carries `X-Hand-Cursor`; a client may use it instead of reading the envelope. `fleets` and `meta` belong to no fleet, take no `?since=` and carry no cursor.

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
| `GET /api/v1/events` | a Server-Sent Events stream; see [The event stream](#the-event-stream) | the pages' own `/events` stream |
| `GET /api/v1/fleets` | `Fleets`: the fleets this session proves, each with its Needs summary and supervisor pill; host level, see [The fleet list and the features](#the-fleet-list-and-the-features) | the Board's rail, with each fleet's status line |
| `GET /api/v1/meta` | `Meta`: Hand's version, the `features` list and the sidecar's state; host level | none; what a client checks before it shows a feature |
| `POST /api/v1/message` | the same `Timeline` the GET returns, after the message | the fleet page's composer |
| `POST /api/v1/decision/{dN}/answer` | the same `DecisionDetail` the GET returns, after the answer | `/decision/dN` |
| `POST /api/v1/report/{rN}/ack` | the same `ReportDetail` the GET returns, after the ack | the report's card on `/task/tN` |
| `POST /api/v1/pair` | `Paired`: the cookie to set for each fleet the code named; no session needed | the pair page's form |
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

`supervisor` is absent when no supervisor exists. A `blocked` or `worker` item carries `deny_at` (RFC 3339) only while its screen shows an automatic-deny countdown of up to 999 minutes; otherwise it is absent. `needs.items` follows the page's order and carries the same `kind`, `ref`, `word` and `tone` (`blocked`, `worker`, `failure`, `limited`, `nosup`, `stalled`, `resume`, `decision`, `report`, `quiet`, `long`, `kept`, `artifacts`). `cleared` and `summary` appear when the page shows "All clear". For a `limited` item, `title` is absent because it comes from a terminal screen; use `ref`, `kind` and the attempt or supervisor detail instead. A `blocked` or `worker` item carries its screen as the Board prints it, only when the Board has read one: `excerpt` is the screen's last 20 lines as the Board shows them, unredacted; `title` is the headline the Board builds from them (the kind, the command and the question), absent when the parser finds no question; `options` are the keys the Board offers for the screen (the numbered answers and `esc`, where `supervisor.ask.options` are the numbered answers only), each a `label` and the `keys` that press it (`["2"]`, `["esc"]`), absent when the Board offers none; `deny_at` is as above. Those lines can show a command, a path or a prompt's text, as the Board does. The full screen and the supervisor's screen page stay out of the API, and `excerpt` is not `cut`, which belongs to a report. A worker's `options` are for display: its keys action is not in v1. A `stalled` title leaves out how long the supervisor has been idle, so a client does not show a count that a `304` would freeze.

While the supervisor's pane is blocked, `supervisor` also carries `"blocked": true` and a `keys` object:

```json
{"blocked": true, "keys": {"revision": 6, "digest": "f8c722e0710879ae811b2fe7ad0c8ba0", "pressed": 2}}
```

`revision` and `digest` are the same guard the Blocked screen page sends, and `pressed` is the sequence of the last key press (`0` when none has been sent). The digest is a non-reversible hash, not screen content.

When the Board's Ask card would show a question for that screen, `supervisor` also carries `ask`, with the Board's own parsed text and nothing else:

```json
{"ask": {"question": "Do you want to proceed?", "options": [{"label": "2 No", "keys": ["2"]}, {"label": "1 Yes", "keys": ["1"]}]}}
```

`question` is the one question line the Board parser picks from the screen. Each option has the `label` the Ask card shows (for a numbered dialog, the number and the option text clipped to 24 characters) and the `keys` that choose it, in the order they are sent: `["1"]`, or `["down", "enter"]` for a `◯`/`◉` list. Both go through the absolute-path scrub the rest of the API uses. `ask` is absent unless `blocked` is true, when the screen is stale or unreadable, when the parser rejects the screen (no option block), when the question line does not end in `?`, when a question or label looks like a secret (a bearer or API token, a `password=` form, a long token), when there are more than 8 options, and when any option would need a key outside the allowed set. The question is clipped to 200 characters and each label to 60. Nothing else from the screen is carried except the `excerpt`, `title` and `options` of a blocked or worker Needs item, which show the command, the file and the footer as the Board does. A client posts an option's `keys` as one press with the three guard values above, so a stale option is refused like any stale key.

### `Timeline.data`

`entries` is the page's conversation, newest first. An entry has `role` (`supervisor`, `operator`, `hand`, `wakes`) plus `at`, `text`, `no`, and where it applies `ref`, `model`, `delivery`, `delivered`, `input`, `queued`, `wakes`, `shots`. A `wakes` entry groups several Hand wake lines in `wakes`. An entry for a screen-derived supervisor event (`supervisor.blocked`, `supervisor.limited`), or one whose text is a harness API error, has no `text`: its `role`, `at` and `ref` remain. A Hand wake line keeps its event kinds and refs but drops a screen-derived detail. `working` is the supervisor's current turn (`ref`, `since`, `on`, `from`, `last`, `input`), absent when it is not working. `older` is the `before` value for the next page, or `0`.

### `Tasks.data`

`groups` are the page's groups in order: `name`, `count`, and `tasks`. A task row has `ref`, `title`, `project`, `status`, `state`, and where it applies `agent`, `attempt`, `delivered`, `waits`, `finished_at` (only for a done or abandoned task), `parent` (the parent's `tN`, for a sub-task) and `children` (how many sub-tasks the task has, absent at zero). A sub-task follows its parent inside its group, and a sub-task whose parent is in another group or not on the page is a root there that still carries `parent`. `hidden` and `finished_count` are the page's counts.

### Detail data

`TaskDetail` has `task` (with `parent` for a sub-task), `pill`, `plan`, `delivered`, `waits`, `open_decisions`, `decisions`, `unread`, `reports`, `more_reports`, `attempts`, `events`, `more_events`, and `children` (the task's sub-tasks as task rows, absent when it has none). A `TaskDetail`'s `attempts` rows are attempt objects with an extra `state` (`running`, `failing`, `passing`, `idle`), and each row's `ref` is the attempt. `AttemptDetail` has `attempt`, `task`, `state`, `brief`, `model_note`, `duration`, `reports`, `events`, `delivered`, and `transcript` (`shown`, `more`, `note`, `model`, `answered`, `turns`; a turn is `role`, `text`, `at`, `queued`). `DecisionDetail` has `decision`, `task`, `body` and `options` (`key`, `label`, `note`, `value`, `recommended`, `other`, `long`). `body` is the prose below the options and includes their text, so a client that renders `options` should not render the option lines twice. `ReportDetail` has `report`, `task` and `delivered`, the report's own `Delivered:` items (or its PR and MR links when it has no such line), each with `ref`, `kind` and, for a file inside the fleet, `href`.

Every ref in a detail is the same `tN`, `aN`, `rN`, `dN`, `sN` the pages and the CLI use. A report object carries the full `body` and a decision object the full `question`, as written; an attempt object carries neither. The board does not render Markdown in the API.

An event from a terminal screen (`attempt.blocked`, `attempt.limited`, `supervisor.blocked`, `supervisor.limited`) carries no `detail`, and neither does an event whose `detail` is a harness API error: that field would be screen text. Its `seq`, `at`, `kind` and `task` remain.

The API never returns a host path except the `excerpt`, `title` and `options` of a blocked or worker Needs item: they are the Board's own lines and keep a path as the screen printed it, so a client shows them as plain text and never logs them. An attempt carries its `branch` but not its worktree or launch command; a file deliverable's `ref` is the file's base name, and its `href` is the board's `/doc/...` path when the file is inside the fleet (absent otherwise). Hand-composed text, such as an attempt's or supervisor's `reason` or an event `detail`, keeps its message but drops the directory part of any absolute path. Free text written by you or the agent, such as a report's `body` or a task's `goal`, is returned as written.

## The event stream

`GET /api/v1/events` is a Server-Sent Events stream with the same cookie and access rules as the other reads. It carries no envelope and no `?since=`: every event's `data` is one JSON object on one line. The stream follows the board's own tick (`tick_ms`, 1 s) and its version, so a change reaches a client within about a second. A client that loses the stream reconnects and gets a full snapshot again.

| Event | `data` |
|---|---|
| `hello` | first on every connect, flushed at once: `version`, `fleet`, `tick_ms`, `beat_ms`, `now` (the board's clock, RFC 3339) |
| `status` | the whole `Status` object plus `now`, sent on connect and whenever it changed. A client computes its clock offset from `now` and counts a `deny_at` against its own clock plus that offset |
| `timeline` | an upsert of the latest page: `entries` new or changed since the last `timeline` event, each an entry plus a `key`; `drop`, the keys of entries that left the page's range (a wake line folded into a group); `working` (`null` when the supervisor is not working), `live` and `note`; the range `from` and `to`, where `to` is `older` plus the page's entry count and `from` is the previous event's `to`; `snapshot: true` on the first one, which replaces what the client holds |
| `touched` | topics that moved (`tasks`) and the refs that events since the last one name: `tasks`, `attempts`, `decisions`, `reports`. The client re-reads what it shows. A `touched` that would exceed 64 KiB is sent as `{"topics": ["tasks"]}`, and the client re-reads the task list and the detail it shows. At most 10,000 events are read per tick; a larger burst is named over the next ticks |
| `beat` | no data, every `beat_ms` (15 s) of silence |
| `resync` | `{"topics": ["status"]}`: a `status` or `timeline` event would exceed 64 KiB, so the client re-reads that topic with a plain `GET` |

On connect the stream sends `hello`, `status` and the latest `timeline` page, and after that only what differs from what this connection was last sent: a quiet fleet gets only beats. A client treats a `timeline` whose `from` is not the `to` it holds as a gap and reads the page again. An entry's `key` is `i<input>` for a message the operator sent (queued and delivered are the same key), and otherwise its role and `at`, with `#2`, `#3` for a repeat, counted from the oldest entry of the page; when the page's lower edge cuts such a group, the survivors take the lower numbers and `drop` names the old trailing keys (a group that shrinks by k entries drops k keys). A key the page no longer holds and that is not older than the page's oldest entry is dropped whatever its suffix.

A client merges a `timeline` upsert by `key`: an entry it holds keeps its place and takes the new fields; a new one goes in by `at`, newest first, and entries with the same `at` keep the order the event gave them. `drop` removes the keys it names.

A blocked screen's countdown does not send an event each second. A `blocked` or `worker` item in `Status.needs.items` carries `deny_at`, the time the harness will deny the request, and the client counts down to it: it takes the time left from `deny_at` and never renders the countdown phrase inside `excerpt`, which is held at its last sent text while only the countdown moves. While a screen counts down, `deny_at` and `supervisor.keys.revision` hold their last sent values (within 2 s); a key press with that revision is accepted while the digest matches. An unchanged `excerpt` is not sent again.

The board does not read a worker's screen on each version tick (`TestTheEventLoopLeavesWorkerScreensUnread`), so while the last sent `Status` has a `worker` item the stream rebuilds the status once per beat (15 s), and a changed worker prompt reaches the stream within one beat. A supervisor's excerpt follows its own screen at once. A `status` event over 64 KiB, which many blocked workers can cause, is sent as `resync` with `{"topics": ["status"]}`, and the client re-reads `GET /api/v1/status`, which has no cap.

The stream ends when the client closes, when the session is revoked, and when the board shuts down. A write that takes longer than twice `beat_ms` ends it too, so a client that stops reading cannot hold the board. `HEAD` answers `200` with the stream's headers and no body, and does not start a stream. Where the peer listener serves the API to a paired phone, a half-close also ends the stream, as it ends `/events`.

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

`choice` is an option's `value` from the decision detail; `note` is the note the page offers. Either may be empty, though an option the page marks `other` needs a note. Send one JSON object under 64 KiB: a second object or trailing text is refused before the board changes anything. The board trims both and stores `choice`, a blank line, then `note`, exactly as the page's form does. `answered_by` is `operator (desktop)` for the laptop's session and `phone (<device name>)` for a paired phone.

- `400 bad_request`: an empty answer, an `other` option with no note, or a body that is not a JSON object.
- `404 not_found`: an unknown ref.
- `409 conflict`: the decision is no longer open.
- `200` with the updated `DecisionDetail`.

### Acknowledging a report

`POST /api/v1/report/{rN}/ack` takes no body. It sets `acked_at` and `acked_by` (`operator (desktop)`, or `phone (<device name>)` for a paired phone) and answers the updated `ReportDetail`. An unknown ref is `404`; a report already acknowledged is `409 conflict`.

A client that shows the fleet status re-reads `GET /api/v1/status` after either action, because an answer and an ack leave Needs. The action's own data is already the fresh detail, so the detail screen needs no second read.

### Pairing a phone

`POST /api/v1/pair` is the app's way to redeem a pairing code without the pair page. It needs no cookie and no `X-Hand-CSRF`, and it is the only API path an unpaired peer on the peer listener may reach. It answers only a peer that sent its endpoint ID: on the loopback listener, or on a peer connection with no preface, it is `403 forbidden`, although every other API request on loopback is served with a session. The answer carries the device secret in a readable body, where the form sets an `HttpOnly` cookie, and a device with no `iroh:` login is one the API refuses anyway. Send one `application/json` object under 4 KiB, with exactly the keys `code` and `name`, both strings (a missing, extra, differently cased or `null` key is `bad_request`):

```json
{"code": "K7QM3-XRD2P", "name": "Pixel"}
```

It runs the pair form's code path, so the limits are the form's: a code works once for 5 minutes, 5 wrong codes in a minute from one endpoint block it, and 10 wrong codes from anyone cancel every code. The device is bound to the peer's iroh endpoint ID (`iroh:<endpoint id>`) and recorded as the form records it. `name` may be an empty string, which stores `Phone`. Unlike the form, which cleans a name, the API refuses a name with a control character, even a trailing newline, or over 40 characters once trimmed, before it spends the code. A request through a proxy, through Funnel, to a network-address board, or on the loopback listener is refused as every API request is. A body that is refused as `bad_request` never counts as a wrong code. A fleet that does not open answers `404 not_found` to any method on this path, as the form's page does, not `401`. The peer listener admits any method to the path; on an open fleet only `POST` pairs, and the others get `401 unauthorized` like any API request without a session.

The answer is the usual envelope. `data` is `Paired`:

```json
{"fleets": [{"id": "f00000000e0aa", "name": "hand", "cookie_name": "hand_phone_f00000000e0aa", "cookie_value": "7.4d1c…(64 hex characters)"}], "missed": 0}
```

A code may name several fleets: `fleets` holds each one that paired, and `missed` counts the ones that did not. The client keeps `cookie_value` as the device secret and sends it as the cookie `cookie_name` on that fleet's `/api/v1` requests; it reads the CSRF value from `GET /api/v1/session`, as for any phone. The secret is in this answer only. The board never logs a code or a secret, and the client must not either.

- `400 bad_request`: a body that is not one JSON object with `code` and `name`, over 4 KiB, or not `application/json`.
- `400 bad_name`: a name with a control character, or over 40 characters once trimmed.
- `403 wrong_code`, `code_expired`, `code_used`: the code is not right, is past 5 minutes, or was already redeemed.
- `403 codes_cancelled`: this was the tenth wrong code, so every code is cancelled.
- `429 rate_limited`: 5 wrong codes from this endpoint in the last minute.
- `200` with `Paired`.

### The fleet list and the features

`GET /api/v1/fleets` and `GET /api/v1/meta` answer on the host, not under a fleet prefix: `/api/v1/fleets`, never `/<fleet id>/api/v1/fleets`. A phone paired with several fleets holds one `hand_phone_<fleet id>` cookie for each, and sends all of them in every request, over its one iroh connection; only the host sees them together, as it does for the rail. Their envelope has no `fleet` and no `cursor`, because they belong to no fleet and have no change stream: a client re-reads them when it opens the fleet list and when a fleet's own stream reports a change.

```json
{
  "version": 1,
  "generated_at": "2026-11-01T05:00:00Z",
  "data": {
    "fleets": [
      {"id": "f00000000e0aa", "name": "hand", "pill": "working", "line": "working",
       "needs": {"waiting": 2, "worst": "decision", "worst_word": "waiting", "worst_count": 1}},
      {"id": "f00000000e0bb", "name": "api", "unavailable": true}
    ]
  }
}
```

`fleets` holds the fleets the request's cookies prove, in the Board's rail order, and nothing else: a fleet it does not prove never appears, not by id, name or count. A laptop session proves each fleet whose `hand_board_<fleet id>` cookie holds that fleet's token, a paired phone each fleet whose `hand_phone_<fleet id>` cookie proves it from this endpoint, as the rail lists them. `pill` and `line` are `Status.pill` and `Status.line` of that fleet, and `needs` carries `waiting`, `worst`, `worst_word`, `worst_count` and `summary` as `Status.needs` does; the two are one computation, so a list and a status read agree. A fleet the rail lists but that will not open has `unavailable: true` and no `pill` or `needs`. A fleet that has left the Board's list, and a phone's fleet that will not open, are not listed, as the rail does not list them. The list carries no path, ref or item text.

`GET /api/v1/meta` answers any caller that can read a fleet's `status`:

```json
{
  "version": 1,
  "generated_at": "2026-11-01T05:00:00Z",
  "data": {
    "version": "0.14.0",
    "features": ["events", "fleets", "pair"],
    "sidecar": {"state": "ready", "id": "9f2c…(64 hex characters)", "relay": "https://relay.example"}
  }
}
```

`version` is what `hand version` prints. `features` is a sorted list of short names, one for each optional capability a client must check before it uses it. A name is added when its endpoint lands, and it is never renamed or removed inside v1; a client shows a feature only when its name is listed, so an app that updates before the laptop does hides what the laptop lacks. `sidecar` is the iroh sidecar's `state` (`starting`, `ready`, `failed` or `stopped`), with `id`, `relay` and a scrubbed `reason` when it has them; it is absent when the Board runs without `--iroh`, and for a caller on the peer listener, which has no use for the laptop's own sidecar. An unpaired caller cannot read `meta`: the pair path stays the only one that needs no session, so an app pairs first and checks `features` after.

| Feature | Means |
|---|---|
| `events` | `GET /api/v1/events`, the change stream |
| `fleets` | `GET /api/v1/fleets` |
| `pair` | `POST /api/v1/pair` |

Both paths take the refusals of every read: a request through a proxy or Funnel, a network-address board, and a phone on the peer listener that holds no fleet paired with its own key get `403` with `forbidden`; a caller that proves no fleet gets `401` with `unauthorized`; a method other than `GET` and `HEAD` gets `405` with `Allow: GET, HEAD`.

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

**The keys decision.** The API never returns a screen. While the supervisor is blocked, `Status.supervisor.keys` carries the revision, digest and pressed sequence the guard needs, and `Status.supervisor.ask` carries the question and option labels the Board's Ask card already shows the operator on the same loopback session, so it adds no new disclosure. A client shows one button per option when `ask` is present, and the fixed seven-key toolbar only when it is absent (a screen the parser does not understand). A blocked worker has no ask: the Board's Ask card covers the supervisor's screen only. Its item carries the `excerpt`, `title` and `options` above.

## Errors

Every error is this shape, not an envelope, including a registered fleet whose store cannot be opened on an API route:

```json
{"error": {"status": 404, "code": "not_found", "message": "not found: task t999"}}
```

| Status | `code` | When |
|---|---|---|
| `400` | `bad_request` | a query value the endpoint cannot read (`before`), or a mutation body it cannot read |
| `400` | `bad_cursor` | `since` is not a cursor this board issued |
| `400` | `bad_name` | `pair` with a name with a control character, or over 40 characters once trimmed |
| `401` | `unauthorized` | no valid board session |
| `403` | `forbidden` | a proxy, Funnel, a peer with no `iroh:` device, v1 on a network-address board, `pair` outside the peer listener, or a control with the board's controls off |
| `403` | `csrf_required` | a mutation without the session's `X-Hand-CSRF` header, or with a wrong one |
| `403` | `wrong_code` | `pair` with a code the board does not hold |
| `403` | `code_expired` | `pair` with a code older than 5 minutes |
| `403` | `code_used` | `pair` with a code already redeemed |
| `403` | `codes_cancelled` | `pair` was the tenth wrong code, so every code is cancelled |
| `404` | `not_found` | an unknown route or ref |
| `405` | `method_not_allowed` | a `POST` outside the action paths, or any other method; the response's `Allow` names what the path takes |
| `409` | `conflict` | a state conflict while reading or acting |
| `413` | `too_large` | a request too large for the board; no v1 endpoint answers it yet, because those that take a body answer `bad_request` |
| `429` | `rate_limited` | `pair` after 5 wrong codes from this endpoint in a minute |
| `500` | `internal` | any other failure |
| `507` | `no_space` | the disk was full |

Messages are scrubbed like the pages': no path and no secret survives. The `code` is the stable part; clients should branch on it.
