# JSON API

The board serves the read model behind its pages as JSON on the same loopback server, under `/api/v1`. It is for a local client, such as the desktop window: a second window on the fleet, never a second owner of its state. The web pages do not use it, and no page, template, stylesheet or script changes because of it.

The API is the read model plus two actions in v1: answering an open decision and acknowledging a report. It never returns `last_screen`, a terminal screen, or text parsed from one, and it never returns the board token. The session endpoint returns the session's CSRF form token, which a mutation sends as `X-Hand-CSRF`.

## Address and login

Each fleet keeps its own prefix, as its pages do:

```
http://127.0.0.1:7777/<fleet id>/api/v1/status
```

A board mounted without a fleet prefix, as in tests, answers at `/api/v1/status`. The API uses the same session cookie as the pages: `hand_board_<fleet id>`, or the cookie of a paired phone. Open the fleet once with `hand open` (or paste the link `hand open --print` prints) and the browser or client holds it. There is no `?token=` login on the API.

A local client proves the service at the recorded port is the board before it sends the token, since `board.addr` outlives a board that exits abnormally: `GET /<fleet id>/proof?nonce=<random>` answers the hex HMAC-SHA256 of the client's nonce keyed by `board.token`, with no cookie; a client that reads the token from the fleet compares it and only then sends the cookie. `hand open` and the desktop window both do this, and neither sends the token to a service that fails it.

A request without that cookie gets `401` with `unauthorized`; a wrong or expired cookie gets the same. The API answers only on a loopback board: a network-address board, a request through a proxy (`X-Forwarded-For`, `Forwarded`, `Tailscale-User-Login`), the peer listener, and a Tailscale Funnel request all get `403` with `forbidden`.

The two actions need the session's CSRF token in an `X-Hand-CSRF` header, on top of the cookie. Read it from `GET /api/v1/session`, whose `data` is `{"csrf": "..."}`; a laptop session's value is `board.token`, the same token the cookie holds, and a paired phone's is derived from its pairing. A mutation without the header, or with a wrong one, gets `403` with `csrf_required`. `GET` and `HEAD` are the only other methods: a `POST` outside the two action paths gets `405` with `method_not_allowed` and `Allow: GET, HEAD`, and `PUT`, `PATCH` and `DELETE` get `405` with `Allow: GET, HEAD, POST`.

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
| `GET /api/v1/report/{rN}` | `ReportDetail`: report, task | the report's card on `/task/tN` |
| `GET /api/v1/session` | `Session`: `csrf` | none; the form token a mutation sends |
| `POST /api/v1/decision/{dN}/answer` | the same `DecisionDetail` the GET returns, after the answer | `/decision/dN` |
| `POST /api/v1/report/{rN}/ack` | the same `ReportDetail` the GET returns, after the ack | the report's card on `/task/tN` |

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

### `Timeline.data`

`entries` is the page's conversation, newest first. An entry has `role` (`supervisor`, `operator`, `hand`, `wakes`) plus `at`, `text`, `no`, and where it applies `ref`, `model`, `delivery`, `delivered`, `input`, `queued`, `wakes`, `shots`. A `wakes` entry groups several Hand wake lines in `wakes`. An entry for a screen-derived supervisor event (`supervisor.blocked`, `supervisor.limited`), or one whose text is a harness API error, has no `text`: its `role`, `at` and `ref` remain. A Hand wake line keeps its event kinds and refs but drops a screen-derived detail. `working` is the supervisor's current turn (`ref`, `since`, `on`, `from`, `last`, `input`), absent when it is not working. `older` is the `before` value for the next page, or `0`.

### `Tasks.data`

`groups` are the page's groups in order: `name`, `count`, and `tasks`. A task row has `ref`, `title`, `project`, `status`, `state`, and where it applies `agent`, `attempt`, `delivered`, `waits`, `finished_at` (only for a done or abandoned task). `hidden` and `finished_count` are the page's counts.

### Detail data

`TaskDetail` has `task`, `pill`, `plan`, `delivered`, `waits`, `open_decisions`, `decisions`, `unread`, `reports`, `more_reports`, `attempts`, `events`, `more_events`. A `TaskDetail`'s `attempts` rows are attempt objects with an extra `state` (`running`, `failing`, `passing`, `idle`), and each row's `ref` is the attempt. `AttemptDetail` has `attempt`, `task`, `state`, `brief`, `model_note`, `duration`, `reports`, `events`, `delivered`, and `transcript` (`shown`, `more`, `note`, `model`, `answered`, `turns`; a turn is `role`, `text`, `at`, `queued`). `DecisionDetail` has `decision`, `task`, `body` and `options` (`key`, `label`, `note`, `value`, `recommended`, `other`, `long`). `body` is the prose below the options and includes their text, so a client that renders `options` should not render the option lines twice. `ReportDetail` has `report` and `task`.

Every ref in a detail is the same `tN`, `aN`, `rN`, `dN`, `sN` the pages and the CLI use. A report object carries the full `body` and a decision object the full `question`, as written; an attempt object carries neither. The board does not render Markdown in the API.

An event from a terminal screen (`attempt.blocked`, `attempt.limited`, `supervisor.blocked`, `supervisor.limited`) carries no `detail`, and neither does an event whose `detail` is a harness API error: that field would be screen text. Its `seq`, `at`, `kind` and `task` remain.

The API never returns a host path. An attempt carries its `branch` but not its worktree or launch command; a file deliverable's `ref` is the file's base name, and its `href` is the board's `/doc/...` path when the file is inside the fleet (absent otherwise). Hand-composed text, such as an attempt's or supervisor's `reason` or an event `detail`, keeps its message but drops the directory part of any absolute path. Free text written by you or the agent, such as a report's `body` or a task's `goal`, is returned as written.

## The two actions

Both carry the cookie and `X-Hand-CSRF`, and both answer one envelope with the updated object. A mutation ignores `?since=` and never answers `304`.

### Answering a decision

`POST /api/v1/decision/{dN}/answer` takes one JSON object:

```json
{"choice": "1. Yes, keep it", "note": "the docs keep the old name"}
```

`choice` is an option's `value` from the decision detail; `note` is the note the page offers. Either may be empty, though an option the page marks `other` needs a note. The board trims both and stores `choice`, a blank line, then `note`, exactly as the page's form does. `answered_by` is `operator (desktop)`.

- `400 bad_request`: an empty answer, an `other` option with no note, or a body that is not a JSON object.
- `404 not_found`: an unknown ref.
- `409 conflict`: the decision is no longer open.
- `200` with the updated `DecisionDetail`.

### Acknowledging a report

`POST /api/v1/report/{rN}/ack` takes no body. It sets `acked_at` and `acked_by` (`operator (desktop)`) and answers the updated `ReportDetail`. An unknown ref is `404`; a report already acknowledged is `409 conflict`.

A client that shows the fleet status re-reads `GET /api/v1/status` after either action, because an answer and an ack leave Needs. The action's own data is already the fresh detail, so the detail screen needs no second read.

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
| `403` | `forbidden` | not loopback, a proxy, the peer listener, Funnel, or v1 on a network-address board |
| `403` | `csrf_required` | a mutation without the session's `X-Hand-CSRF` header, or with a wrong one |
| `404` | `not_found` | an unknown route or ref |
| `405` | `method_not_allowed` | a `POST` outside the two actions, or any other method; the response's `Allow` names what the path takes |
| `409` | `conflict` | a state conflict while reading or acting |
| `500` | `internal` | any other failure |
| `507` | `no_space` | the disk was full |

Messages are scrubbed like the pages': no path and no secret survives. The `code` is the stable part; clients should branch on it.
