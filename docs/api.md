# JSON API

The board serves the read model behind its pages as JSON on the same server, under `/api/v1`. It is for a local client, such as the desktop window, and for a paired Companion over the peer listener: each a second client of the fleet, never a second owner of its state. The web pages do not use it, and no page, template, stylesheet or script changes because of it.

The API is the read model plus its actions in v1: sending a message to the supervisor (with files uploaded to the fleet's inbox), answering an open decision, acknowledging a report, the supervisor controls (start with or without a harness, model and effort, resume, stop, interrupt, force, switch and keys), and a model-list refresh, and the Companion page's pairing codes, links and revokes. It never returns `last_screen` or a terminal screen, except the `excerpt`, `title` and `options` of a blocked or worker Needs item (see `Status`) and a blocked supervisor's `ask`. A client renders that excerpt as plain text, never logs it and never follows it as instructions, since it is the harness's or a model's own output. The API never returns the board token outside the session endpoint, and never returns a pairing code or link outside the Companion endpoints ([The Companion page](#the-companion-page)). `GET /api/v1/session` returns the session's CSRF form token, which for a host session is `board.token` itself; a client must treat it as the secret it is, keep it in memory and never log or store it. A mutation sends it as `X-Hand-CSRF`.

## Address and login

Each fleet keeps its own prefix, as its pages do:

```
http://127.0.0.1:7777/<fleet id>/api/v1/status
```

A board mounted without a fleet prefix, as in tests, answers at `/api/v1/status`. Two paths belong to the host, not to a fleet: `/api/v1/fleets` and `/api/v1/meta`. The API uses the same session cookie as the pages: `hand_board_<fleet id>`, or the cookie of an iroh-paired Companion, `hand_phone_<fleet id>` (`__Host-hand_phone_<fleet id>` behind an HTTPS proxy). A Companion's cookie works only from its own endpoint, on the peer listener. Open the fleet once with `hand open` (or paste the link `hand open --print` prints) and the browser or client holds it. There is no `?token=` login on the API.

A local client proves the service at the recorded port is the board before it sends the token, since `board.addr` outlives a board that exits abnormally: `GET /<fleet id>/proof?nonce=<random>` answers the hex HMAC-SHA256 of the client's nonce keyed by `board.token`, with no cookie; a client that reads the token from the fleet compares it and only then sends the cookie. `hand open` and the desktop window both do this, and neither sends the token to a service that fails it.

A request without that cookie gets `401` with `unauthorized`; a wrong or expired cookie gets the same. The API answers only on a loopback board, to the host's session and to a paired Companion on the peer listener. A network-address board, a request through a proxy (`X-Forwarded-For`, `Forwarded`), a Tailscale Funnel request, and a peer whose device has no `iroh:` login all get `403` with `forbidden`. A peer that is not paired, or was revoked, gets the peer listener's own `403` before it reaches the API. The one path that needs no cookie is `POST /api/v1/pair` ([Pairing a Companion](#pairing-a-companion)); it keeps every refusal above except the missing cookie.

The actions need the session's CSRF token in an `X-Hand-CSRF` header, on top of the cookie. Read it from `GET /api/v1/session`, whose `data` is `{"csrf": "..."}`; a host session's value is `board.token`, the same token the cookie holds, and a paired Companion's is derived from its pairing. A mutation without the header, or with a wrong one, gets `403` with `csrf_required`. `GET` and `HEAD` are the only other methods: a `POST` outside the action paths gets `405` with `method_not_allowed` and `Allow: GET, HEAD`, and `PUT`, `PATCH` and `DELETE` get `405` with an `Allow` that names what the requested path takes (`GET, HEAD`, or `POST` on an action path).

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

`cursor` is opaque: a client stores it and sends it back as `?since=`. For `status`, `timeline`, `tasks`, `projects`, a task, a decision and a report it encodes the fleet and a signed fingerprint of the fleet's name, the path and query, and the board's version: the last event, the pending operator messages, the last alert, the supervisor's pane, transcript and worker signals, the Old files warning, whether each file under the fleet folder that a deliverable links to exists and what kind it is, and the size of each inbox image. After a board restart the first poll with an older cursor answers `200`. The board computes that version once per tick for all its clients and does not build the data to answer a poll, so a change reaches a poll within a couple of seconds (at once after a write made through the board). `phone` fingerprints its data with each code and link replaced by a keyed digest. A session, an attempt, a links answer and an action's answer fingerprint their `data` instead, because an attempt's duration and transcript change without a board version change. A cursor is not a timestamp and not just an event sequence. The signature is keyed by the board, so a cursor it never issued is rejected even when its fleet and fingerprint look well formed.

- No `?since=`: always `200` with the full data.
- `?since=` equal to the cursor the board computes now: `304 Not Modified`, an empty body, and the current value in `X-Hand-Cursor`. Poll that first: for the pages above it costs no build and no body.
- `?since=` from before any change to that data: `200` with the new data and a new cursor. A worker's screen is not in the status cursor, so a conditional read of `status` does not see a worker's screen-only change: a client that refreshes a worker item reads without `since`.
- A cursor this board did not issue (another fleet, a typo, a truncated value): `400` with `bad_cursor`.

Every `200` and `304` of a fleet's endpoints carries `X-Hand-Cursor`; a client may use it instead of reading the envelope. `fleets` and `meta` belong to no fleet, take no `?since=` and carry no cursor.

## Endpoints

| Endpoint | `data` | Page it mirrors |
|---|---|---|
| `GET /api/v1/status` | `Status`: supervisor, agent state, pill, gauge, compactions, Needs summary and items, task counts, project count | the fleet page's status line and Needs |
| `GET /api/v1/models` | `Models`: each harness the menus offer, its model groups and efforts, its stale and missing notes, and the running supervisor's choice; see [Models](#modelsdata) | the model menu and the Start strip |
| `GET /api/v1/timeline[?before=N]` | `Timeline`: `live`, `working`, `entries`, `older`, `note` | the fleet page's Chat; `before` pages like "Older messages" |
| `GET /api/v1/tasks[?all=1]` | `Tasks`: `all`, `groups`, `hidden`, `finished_count` | the fleet page's Tasks |
| `GET /api/v1/tasks/search[?q&status&project&since&until&page]` | `TaskSearch`: the search's echo, `page`, `page_size`, `total`, `pages` and day `groups` of task rows | `/tasks`, the searchable Tasks page |
| `GET /api/v1/history/search?q=WORDS[&who&since&task&limit&context]` | `HistorySearch`: the search's echo, `total`, `more` and the `hits`; see [`HistorySearch.data`](#historysearchdata) | none yet; `hand history search` |
| `GET /api/v1/history/{ref}` | `HistoryEntry`: one operator message, supervisor reply, report, decision or task title whole | none yet; `hand history search --full REF` |
| `GET /api/v1/phone` | `Phone`: the paired Companions, the iroh state, the fleets a code may cover, and the code and app link while one shows; see [The Companion page](#the-companion-page) | `/connect`, the Companion page |
| `GET /api/v1/projects` | `Projects`: the project names in the Tasks filter's order | the Tasks filter's Project list |
| `GET /api/v1/skills[?scope=SCOPE[&project=NAME]]` | `Skills`: each scope's skills with source, ref, commit, latest commit, trust, drift state and the pending diff; see [Skills](#skills) | none yet; `hand skills list` for every scope |
| `GET /api/v1/doc/{path}[?page=N]` | `DocFolder` for a folder (entries and pager) or `DocFile` for a file (name, kind, size, time, neighbours); see [Documents](#documents) | `/doc/{path}`, the document viewer |
| `GET /api/v1/doc/{path}?raw` | not an envelope: the file's bytes, as the page's `?raw` serves them | `/doc/{path}?raw` |
| `GET /api/v1/doc/{path}?from=N` or `?lines=M` (either, or both) | `DocWindow`: those lines of a text file and where the next ones start | the viewer's text lines |
| `GET /api/v1/links?path=PATH[&path=PATH...]` | `Links`: `limit` and `links`, one `{rel, href, dir}` or `null` per `path`, in order; see [Links](#links) | Desktop links the paths in a reply, a Needs card, a decision, a plan and a report with it, when `meta.features` lists `links`; the Board links the paths in its own text from the same check |
| `GET /api/v1/inbox/{name}` | not an envelope: the bytes of a file in the fleet's inbox, with the headers the page's `/inbox/{name}` sets; see [Attachments](#attachments) | `/inbox/{name}`, the images and file cards in the conversation |
| `GET /api/v1/task/{tN}` | `TaskDetail`: task, plan, delivered, waits, open and all decisions, unread and recent reports, attempts, events | `/task/tN` |
| `GET /api/v1/attempt/{aN}` | `AttemptDetail`: attempt, task, state, brief, model_note, duration, reports, events, delivered, transcript | `/attempt/aN` |
| `GET /api/v1/decision/{dN}` | `DecisionDetail`: decision, task, prose body, options | `/decision/dN` |
| `GET /api/v1/report/{rN}` | `ReportDetail`: report, task, delivered | the report's card on `/task/tN` |
| `GET /api/v1/session` | `Session`: `csrf` | none; the form token a mutation sends |
| `GET /api/v1/events` | a Server-Sent Events stream; see [The event stream](#the-event-stream) | the pages' own `/events` stream |
| `GET /api/v1/fleets` | `Fleets`: every fleet this session proves, each with its Needs summary and supervisor pill; host level, see [The fleet list and the features](#the-fleet-list-and-the-features) | the Board's rail order, with each fleet's status line |
| `GET /api/v1/meta` | `Meta`: Hand's version, the `features` list and the sidecar's state; host level | none; what a client checks before it shows a feature |
| `POST /api/v1/inbox` | `InboxFile`: the stored file's inbox `name`, `label`, `kind` and `size`; see [Attachments](#attachments) | the composer's paperclip, drop and paste (`POST /supervisor/image`) |
| `POST /api/v1/message` | the same `Timeline` the GET returns, after the message; takes `attachments`, a list of inbox names | the fleet page's composer |
| `POST /api/v1/decision/{dN}/answer` | the same `DecisionDetail` the GET returns, after the answer | `/decision/dN` |
| `POST /api/v1/report/{rN}/ack` | the same `ReportDetail` the GET returns, after the ack | the report's card on `/task/tN` |
| `POST /api/v1/phone/code` | the same `Phone` the GET returns, with a new pairing code showing | the Companion page's Show pairing code |
| `POST /api/v1/phone/app` | the same `Phone`, with a new app link showing | the Companion page's Pair the Hand app |
| `POST /api/v1/phone/revoke` | the same `Phone`, with `revoke`: the fleets the revoke reached and those it did not | the Companion page's Revoke and Revoke everywhere |
| `POST /api/v1/pair` | `Paired`: the cookie to set for each fleet the code named; no session needed | the pair page's form |
| `POST /api/v1/supervisor/start` | the same `Status` the GET returns, after the start | the console's Start or New session, and the Start strip's harness rows |
| `POST /api/v1/supervisor/resume` | the same `Status` after the resume | the console's Resume |
| `POST /api/v1/supervisor/stop` | the same `Status` after the stop | the console's Stop |
| `POST /api/v1/supervisor/interrupt` | the same `Status` after the interrupt | the console's Interrupt |
| `POST /api/v1/supervisor/force` | the same `Status` after the force | the blocked screen's "Type queued messages anyway" |
| `POST /api/v1/supervisor/switch` | the same `Status` after the switch | the model menu's Switch, Cancel switch and Drop switch |
| `POST /api/v1/supervisor/keys` | the same `Status` after the key press | the supervisor's fixed key buttons |
| `POST /api/v1/skills/sync` | `SkillsRun`: a row per skill, third-party diffs, reconcile commands; see [Skills](#skills) | none yet; `hand skills sync` |
| `POST /api/v1/skills/update` | the same `SkillsRun` | none yet; `hand skills update` |
| `POST /api/v1/skills/propose` | the same `SkillsRun`, with the pull request's address | none yet; `hand skills propose` |
| `POST /api/v1/models/refresh` | the same `Models` the GET returns, after the refresh started | the overflow menu's Refresh models |
| `POST /api/v1/attempt/{aN}/keys` | the same `AttemptDetail` the GET returns, after the key press | `/attempt/aN`'s key buttons |

`{tN}`, `{aN}`, `{dN}` and `{rN}` take the ref or the bare number, as the pages do. An unknown ref is `404`.

`/api/v1/tasks` is the fleet page's list (active and inbox tasks plus the five most recent finished ones), not the searchable Tasks page; `all=1` shows the finished tasks too. `/api/v1/tasks/search` is the searchable Tasks page and its filters.

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

`supervisor` is absent when no supervisor exists; it carries the supervisor's `ref`, `harness`, `model`, `effort`, `status`, `live`, `pending` and `resumable` (`model` and `effort` absent when empty), and `ended_at` (RFC 3339) once it has ended, the time a `resume` item's slug line prints beside its harness and status. A `blocked` or `worker` item carries `deny_at` (RFC 3339) only while its screen shows an automatic-deny countdown of up to 999 minutes; otherwise it is absent. `needs.items` follows the page's order and carries the same `kind`, `ref`, `word` and `tone` (`blocked`, `worker`, `failure`, `limited`, `nosup`, `stalled`, `resume`, `decision`, `report`, `quiet`, `long`, `kept`, `artifacts`). `cleared` and `summary` appear when the page shows "All clear". For a `limited` item, `title` is absent because it comes from a terminal screen; use `ref`, `kind` and the attempt or supervisor detail instead. A `blocked` or `worker` item carries its screen as the Board prints it, only when the Board has read one: `excerpt` is the screen's last 20 lines as the Board shows them, unredacted; `title` is the Board's own title for the item, always present for these two kinds, even when the parser rejects the screen; `options` are the keys the Board offers for the screen (the numbered answers and `esc`, and for a worker also the entries of a `◯`/`◉` list, from the same parser), each a `label` and the `keys` that press it (`["2"]`, or `["down", "enter"]`), a label clipped to 60 characters like `ask`, absent when the Board offers none or when the parser returns more than 8; `deny_at` is as above. An item with a screen to guard carries `keys`: the same buttons under `options` (the parsed options when they fit those bounds, else the Board's fixed seven-key toolbar) plus the guard a press needs, `revision`, `digest` and `pressed`, exactly the three values `supervisor.keys` carries for a blocked supervisor. A `blocked` item always carries it; a `worker` carries it once the Board has read its screen. Those lines can show a command, a path or a prompt's text, as the Board does. The full screen and the supervisor's screen page stay out of the API, and `excerpt` is not `cut`, which belongs to a report. A `stalled` title leaves out how long the supervisor has been idle, so a client does not show a count that a `304` would freeze.

An item the Board draws from an attempt carries that attempt's slug line: `attempt_meta` holds `ref`, `harness`, `model`, `status` and `ended_at`, the same values the Board prints beside the item's ref, with the attempt object's omission rules (`model` and `ended_at` absent when empty). For a `failure`, `kept`, `quiet`, `limited`, `long` or `worker` item these are the words the summary line shows; a `report` item keeps `attempt` as its attempt's ref and carries the report's `status` and `created_at` instead, and has no `attempt_meta`. `reason` is that attempt's reason for every item with one, scrubbed like every attempt reason and absent when it has none, though the Board draws it only for a `failure`, in place of its body. `project` is the item's task project, absent when the item has no task, so it is present on more items than the badge, which the Board draws only on a decision or a report when the fleet has more than one project. All three are absent from a `nosup`, `stalled`, `artifacts` or `resume` item; a `resume` item's supervisor words are `Status.supervisor`'s.

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

### `Models.data`

```json
{
  "harnesses": [
    {"name": "claude",
     "groups": [
       {"label": "built-in", "models": [
         {"name": "opus", "text": "Opus 5.5 · opus", "efforts": ["low", "medium", "high", "xhigh", "max"]},
         {"name": "haiku", "text": "Haiku 5.5"}]},
       {"label": "custom, efforts unverified", "models": [
         {"name": "acme-opus", "text": "Acme Opus · acme-opus", "efforts": ["low"], "unverified": true}]}],
     "efforts": ["low", "medium", "high", "xhigh", "max"], "note": "listed 2 h ago"},
    {"name": "omp", "groups": [], "missing": "omp not found on the board's PATH; restart the board after installing it"}
  ],
  "current": "claude", "model": "opus", "effort": "high", "switchable": true
}
```

`harnesses` is the menu's own order, which is the order the board's harness list keeps: `claude`, `codex`, `opencode`, `agy`, `omp`. `copilot` is left out because it starts workers only. Each harness carries `name`, `groups`, `efforts`, and where they apply `note`, `missing`, `model`, `effort`. `groups` are the menu's `<optgroup>`s in its order, each with the `label` the menu prints (`built-in`, `discovered` or `custom`, with `, efforts unverified` appended when any model in it is unverified) and its `models`. A model has `name`, the `text` the option shows (`Label · name`, or the name when it has no label), its `efforts` as tokens, and `unverified` and `selected` when they apply. `selected` is the model the Start strip would prefill, the last supervisor's, on the harness it ran; the others carry none. `efforts` on the harness is the list its effort select offers, in model order. `note` is the menu's stale line verbatim (`listed 2 h ago`, `refresh failed: …`), empty when the list is fresh, and `missing` is its reason verbatim when the harness binary is not on the board's `PATH`. Both go through the absolute-path scrub. A harness with no model list has empty `groups`, and its model box takes a typed name.

`current` is the running supervisor's harness, with its `model` and `effort` as the menu's "Running now" block shows them; all three are absent when there is no supervisor. `model_note` is that block's warning, verbatim (`Asked for opus high, running sonnet low.`), when the model that was asked for is not the one running. `switching` is the pending switch's `model effort`, the menu's `next turn:` line, and `drop` is the same text when the switch can be dropped on a supervisor that is no longer running; `drop_ref` is that supervisor's `ref`, the one the Drop switch posts back. `switchable` says the menu offers a switch at all, which the board does only while a supervisor is running; `resumable` says the Start strip offers Resume.

### `Timeline.data`

`entries` is the page's conversation, newest first. An entry has `role` (`supervisor`, `operator`, `hand`, `wakes`) plus `at`, `text`, `no`, and where it applies `ref`, `model`, `delivery`, `delivered`, `input`, `queued`, `wakes`, `shots`. An operator entry that carries an `input` also has `status` (`queued`, `delivered` or `answered`), and where it applies `answered_at`, `reason` (`blocked`, `limited`, `lost`, `switching`, `turn`, `no_supervisor` or `held`, only while it is `queued`; a `delivered` or `answered` message may instead carry `waited`, with the `reason_detail` codes `off`, `unknown`, `not_wait`, `typed` or `failed`, or `interrupted`, with an empty `reason_detail` or, when Hand did not type the message again, a sentence beginning `not sent again:`), `reason_detail` and, for `blocked`, `reason_keys` (the keys that clear the screen, as `POST /api/v1/supervisor/keys` takes them). `reason_detail` is Hand's own words and goes through the same absolute-path scrub as the rest of the API: for `turn` the time of the last delivery to the supervisor (the turn began then or earlier), for `switching` the model and effort, for `lost` and `held` the sentence. `blocked` and `limited` carry no detail, since theirs would be a screen line; a client that wants the blocking question reads the supervisor's `ask` from `Status`, and `reason_keys` is the full set of keys Hand can press (`enter`, `esc`, `up`, `down`, `1`, `2`, `3`), not a computed minimum: Hand does not know which one clears a screen. `delivery` keeps its old values (`queued`, `delivered`, `typed anyway`) and stays `delivered` once the input is answered. A message the supervisor's harness has not yet recorded stays in `entries` while it is `delivered` (while the supervisor works, and otherwise until the first render 2 minutes after its delivery: the board renders on a change, not on a timer), with the same `input`, so its key `i<input>` does not change when the harness records it. The same fields ride the `timeline` event and the answer of `POST /api/v1/message`. A `wakes` entry groups several Hand wake lines in `wakes`. An entry for a screen-derived supervisor event (`supervisor.blocked`, `supervisor.limited`), or one whose text is a harness API error, has no `text`: its `role`, `at` and `ref` remain. A Hand wake line keeps its event kinds and refs but drops a screen-derived detail. `working` is the supervisor's current turn (`ref`, `since`, `on`, `from`, `last`, `input`), absent when it is not working. `older` is the `before` value for the next page, or `0`.

### `Tasks.data`

`groups` are the page's groups in order: `name`, `count`, and `tasks`. Rows in the Done and Abandoned groups are newest first by finish time and rows in Active and Inbox are oldest first by id. A task row has `ref`, `title`, `project`, `status`, `state`, and where it applies `agent`, `attempt`, `delivered`, `waits`, `finished_at` (only for a done or abandoned task), `parent` (the parent's `tN`, for a sub-task) and `children` (how many sub-tasks the task has, absent at zero). A sub-task follows its parent inside its group, and a sub-task whose parent is in another group or not on the page is a root there that still carries `parent`. `hidden` and `finished_count` are the page's counts.

### `TaskSearch.data`

`TaskSearch` is the searchable Tasks page. `q` matches every word in a task's title, goal or `tN` ref, case-insensitively, as the page's search does. `status` is one of `finished` (done and abandoned), `all`, `done`, `abandoned`, `active` or `inbox`. `status` in the answer echoes the value as asked and is empty when none was asked; that empty value resolves to `finished` when `q` is empty and to every status when `q` is not, as the page's default form searches. `project` is one project name as `GET /api/v1/projects` returns it. `since` and `until` are `YYYY-MM-DD` UTC days, inclusive, tested against a done or abandoned task's finish time and an unfinished task's `updated_at`.

`groups` are the page's day groups in order, newest first: `day` (the local date as `YYYY-MM-DD`), `name` (`Mon 2 Jan WIB`), `count` and `tasks`. A day is a calendar day in the zone of the machine that runs the Board, so `day` is the group's key and stays one group across a daylight-saving change; `name` ends with the zone's abbreviation as the newest row in the group sees it (`UTC` on a UTC host). `since` and `until` below stay UTC days, not local ones: in a zone east of UTC a task finished just after local midnight is grouped under the new local day but still counts for the earlier UTC day. Paging is by offset over the ordered rows, so a day that straddles a page boundary appears on both pages with the same `day`; a client that merges pages merges by `day`. Rows in a group are ordered newest first by finish time, or by `updated_at` when no finish event exists. A row uses the `Tasks` row's fields and adds `updated_at`, the time an unfinished row is grouped and sorted by, and `open_decisions`, how many decisions are open on the task (absent at zero). `finished_at` is present only for a done or abandoned task. Sub-tasks are not placed under their parent. `q` echoes the value with its surrounding spaces trimmed; `project`, `since` and `until` echo the values as asked, empty when they were not; `status` echoes the same way, as above.

`page` is `1` unless asked, `page_size` is `50`, `total` is every task the query matches, and `pages` is `total` divided by `page_size`, rounded up (`0` when nothing matches). A page past the last one is `200` with no groups. A `status` outside those words, a `since` or `until` that is not a date, and a `page` that is not a number from 1 to 1,000,000 are `400` with `bad_request` and the board's sentence. An empty result is `200` with zero rows. Here `since` is the finished-on-or-after filter, not the change cursor, so this endpoint has no `?since=` poll and always answers `200`. The answer's `X-Hand-Cursor` is the board's change cursor, not a cursor for this path: do not store it and send it back as `since`, where it would be refused as a date.

### `HistorySearch.data`

`HistorySearch` searches what was said in this fleet: the operator's messages (`iN`), the supervisor's prose replies (`yN`), the reports (`rN`), the decisions (`dN`) and the task titles (`tN`); never a worker's conversation or a tool's output. `q` is SQLite FTS5 syntax over words, case and Latin accents ignored: `amber kiln` needs both words, `amber OR kiln`, `"amber kiln"` as a phrase, `kil*` as a prefix; a ref such as `t12` or `y412` is one word. A `q` FTS5 cannot parse is searched as its whitespace-separated words, each quoted and all required, so `"unterminated` still works. A missing, blank or over 1,024-byte `q` is `400`. Search matches the stored text and prints it redacted, so a `q` can confirm that a string occurs in an operator message, report, decision or task title even where `text` shows `[redacted:…]`; a paired Companion can do this too, but the Board already serves operator messages, reports, decisions and task titles as stored, so the oracle gives it nothing it cannot already read; the routes add reach over older entries at most (see History in the [vocabulary](vocabulary.md#history)).

`who` limits the source: `operator`, `supervisor` or `report`. `since` is `7d`, `36h` or a UTC date `YYYY-MM-DD` (here it is a time bound, not the change cursor, so this route takes no `?since=` poll and sends no `304`). `task` is a `tN`: a report of that task, and an operator message or reply that names `tN` as a word. `limit` is 1 to 50 (default 5) and `context` 0 to 5 (default 0). A value outside those, or a `who`, `since` or `task` it cannot read, is `400` with `bad_request` and the board's sentence. The answer echoes `q`, `who`, `since` and `task` as asked, and `limit` and `context` as applied.

`hits` are ordered by relevance (bm25 within each source, so mixing sources is approximate), then newest first, at most `limit`; `total` is every match under the filters and `more` is `total > len(hits)`. A hit has `ref`, `who`, `at`, `text` (a snippet of at most 300 bytes on one line, the match between `«` and `»`, a cut shown as `…`) and `context`; a reply carries `supervisor` (`sN`), a report carries `task`, an operator message carries neither. With `context` above 0, `context` lists up to that many entries before and then after a message or reply hit in the merged conversation of messages and replies, oldest first, each as `ref`, `who`, `at`, `text` (clipped the same way) and `supervisor` for a reply; a report hit has an empty `context`. Every returned text passes through the same redaction as a stored reply (`internal/redact`), because operator messages and reports are stored as written.

`GET /api/v1/history/{ref}` takes `iN`, `yN`, `rN`, `dN` or `tN` and answers `ref`, `who`, `at`, `supervisor`, `task`, `text` and `truncated`. `text` is the whole body, redacted, at most 16 KiB; `truncated` is `true` when it was cut at a character boundary. An unknown or malformed ref is `404`. The answer reads this fleet's database only: there is no fleet or `all` parameter, and the route takes the refusals of every read below. The envelope's `cursor` is a fingerprint of the answer, not a change cursor.

### `Projects.data`

`projects` is every registered project's name, in the order the Tasks filter lists them, which is the board's project order (`ORDER BY name`). The filter itself appears only when the fleet has more than one project; the list does not depend on whether a project has tasks today.

### Documents

`/api/v1/doc/{path}` serves the Board's document viewer from the same code the page runs: the same `Resolve` and `openSame` confinement, the same refusals, the same folder order, page size and Previous and Next, the same file kinds and the same `?raw`. `{path}` is relative to the fleet folder and no answer carries a host path. The viewer refuses a file in the fleet folder itself, a dot name, `backups/`, `cache/`, `luvus/`, `locks/` and `projects/`, as well as `..`, a backslash, a NUL, a regular file with more than one hard link (`403 forbidden`, "this file has more than one hard link, so the Board does not show it; copy it to show it") and a symlink that leaves the fleet folder or points at one of those. The root `/api/v1/doc/` is refused like the page's `/doc/`. A path with `..` or `//` spelled out is redirected by the router (`307`) before it reaches a handler, as the page's is. Percent-encoded forms are decoded first and judged as the page judges them (`%2e%2e` and `%5C` are refused, `%2F` is a slash).

A folder answers `DocFolder`:

```json
{
  "path": "reports/t1", "name": "t1", "kind": "folder", "at": "2026-10-30T08:15:00Z",
  "entries": [
    {"name": "sub", "path": "reports/t1/sub", "kind": "folder", "label": "DIR", "dir": true, "image": false, "at": "2026-10-30T08:15:00Z"},
    {"name": "design.md", "path": "reports/t1/design.md", "kind": "file", "label": "MD", "dir": false, "image": false, "size": 303, "at": "2026-10-30T08:15:00Z"}
  ],
  "page": 1, "page_size": 500, "total": 13, "pages": 1
}
```

`entries` are the Board's listing: folders first, then files, each group ordered by lower-case name and then by name; dot names are left out. `kind` is `folder`, `file` or `link` (a symlink); `label` is the badge the page prints (`DIR`, `LINK`, the extension in capitals, `FILE`); `dir` is true for a folder and for a link that resolves to one; `image` is true for the names the page shows as a thumbnail (`png`, `jpg`, `jpeg`, `webp`, `gif`). `size` (bytes) is present for a regular file only. `at` (RFC 3339 UTC) is the entry's own modification time, absent when it could not be read. A link the Board will not follow carries `refused` with the page's words (`leads outside the fleet folder`, `points at a file the Board never shows`, `points at something that is not a file or folder`, `points at nothing`, `cannot be read`) and no size. `page_size` is `500`, `total` is every entry and `pages` is `total` divided by `page_size`, rounded up, never below `1`; an empty folder is `200` with `"entries": []`. A `page` past the last one answers the last page and `page` says which; a `page` that is not a whole number from 1 is `400` with `bad_request`. An empty value is read as the default, as in the task search: `?page=` is page 1.

A file answers `DocFile`:

```json
{
  "path": "reports/t1/design.md", "name": "design.md", "kind": "markdown", "text": true,
  "size": 303, "at": "2026-10-30T08:15:00Z",
  "prev": {"name": "data.tsv", "path": "reports/t1/data.tsv"},
  "next": {"name": "flow.png", "path": "reports/t1/flow.png"},
  "folder_page": 1
}
```

`kind` is what the viewer would draw, found from the file name and its first 8 KiB, as the page finds it. The page reads the whole file, so it may still show a text or not-text view for a file of that kind: a Markdown file that is not UTF-8 past the first 8 KiB gets the not-text notice, and a CSV or TSV that is empty or that the table refuses gets the text view. The kinds are `markdown` (a `.md` or `.markdown` text file of at most 1 MiB; a larger one is `text`, as the page shows it as source), `text`, `image` (`png`, `jpeg`, `webp` or `gif` bytes under a matching name), `svg`, `html`, `pdf`, `csv`, `tsv`, `video`, `audio` (the page's players) and `binary`. A Markdown file whose bytes past the first 8 KiB are not UTF-8 is still `markdown` here: its `?raw` is the file as it is, and what to show is the client's choice. `text` says the first 8 KiB read as text, so `from` and `lines` apply. `prev` and `next` are the files before and after this one in the folder's order, skipping folders and what the Board refuses; for an `image` they are images only, as the page's picture view has it. `folder_page` is the page of the folder's listing that holds the file.

`?raw` answers the file's bytes with the headers the page's `?raw` sets: `X-Content-Type-Options: nosniff`, `Cache-Control: private, no-cache`, a `sandbox; default-src 'none'` policy (HTML and SVG add the styles they need, HTML adds `frame-ancestors 'self'`), `Content-Disposition: attachment` for bytes that are not text, and the page's own `Content-Type` choice, which is `text/plain` for text with no better type and never the file's claim. It takes `Range` and `HEAD`. It is never the page's inlined `?frame`: `?raw&frame` is the same bytes, and `?frame` alone is `DocFile`. A folder has no bytes: `?raw` on one is `400`.

`?from=N` and `?lines=M` read a text file by line, the way the page's text view reads it. Either one makes the answer a `DocWindow`, also with an empty value (`?from=` is the default window); `from` is a 1-based line number (default `1`) and `lines` the count wanted (default `500`, most `2000`; a larger value is the most). The answer is bounded by size too: the `lines` as JSON strings take at most 4 MiB (a control character or a byte that is not UTF-8 takes up to six bytes there), so a window can hold fewer lines than asked, and `next` says where it ended. A line is never split between two windows.

```json
{"path": "reports/t1/notes.txt", "name": "notes.txt", "from": 2, "lines": ["two"], "next": 3}
```

`lines` are the text as written, one string each, without the line end and with a trailing `\r` removed, so escape sequences, tabs and control characters arrive untouched and a client must draw them inert; bytes that are not UTF-8 arrive as U+FFFD. A line longer than 256 KiB is cut there and listed in `cut` as `{"line", "bytes"}` with the bytes left out, as the page's "more on this line" note says. `next` is the number of the first line after the window and is absent when the window reached the end of the file. `partial` is `true` when the last line has no line end yet, because the file is being written: a client that follows the file asks again from that line, not from `next`. An empty file answers `from: 1` with no lines. A `from` past the last line, a `from` or `lines` that is not a whole number from 1, a file that is not text and a folder are `400` with `bad_request`; the not-text sentence is the page's (`This file is not text, so the Board does not show it; download it to open it.`). `raw` wins when it is sent with a window. The file is read where the line starts, so a window far into a very large file costs a scan up to it.

Refusals use the Board's own sentence in the error's `message`. A hidden, outside or special path is `403` with `forbidden`; a path that does not exist, or goes through a file, is `404` with `not_found`; a file that changed while it was opened is `409` with `conflict`. A link that leaves the fleet is refused as outside, and no message names where it led. The answers take no `?since=` and have no `304`: an answer's `cursor` fingerprints its data.

### Links

`GET /api/v1/links?path=PATH[&path=PATH...]` tells a client which file paths in a text the Board would link to its viewer, so a client can link them without a copy of the Board's rules. Each `path` is as the text shows it: absolute inside the fleet folder, `~/x`, or relative to the fleet folder (`reports/t1/design.md`, `./reports/`). The answer is the Board's own: `DocHref` finds the fleet-relative path and `Resolve` applies the serve policy of `/api/v1/doc/{path}` (see [Documents](#documents)), so nothing links that the viewer would refuse: a file in the fleet folder itself, a dot name, `backups/`, `cache/`, `luvus/`, `locks/`, `projects/`, `..`, a backslash, a symlink that leaves the fleet folder, a file with more than one hard link, a path that does not exist, and an absolute path outside the fleet folder. A path is a file name as written: `:12` or `#L3` after it is part of the name, so it links only if such a file exists. A relative path is read against the fleet folder of the fleet in the URL, never a project or a worktree; a client that links `docs/spec.md` written about a project's repository links the fleet's own `docs/spec.md` when that exists, as the Board's pages do.

```json
{
  "limit": 50,
  "links": [
    {"rel": "reports/standup/2026-11-20.md", "href": "/f00000000e0aa/doc/reports/standup/2026-11-20.md", "dir": false},
    null,
    {"rel": "reports", "href": "/f00000000e0aa/doc/reports", "dir": true}
  ]
}
```

`links` has one element for each `path`, in the order sent, repeats included, and no input is echoed. An element is `null` for every refusal and every miss, with no reason, so the route reveals no more than `/api/v1/doc/{path}` serves. `rel` is the file's or folder's path inside the fleet folder with symlinks inside it resolved (the argument to `GET /api/v1/doc/{rel}`); `href` is the Board's page for the path, the one `DocHref` builds; `dir` is true for a folder. No answer carries a host path. The kind of file is not part of the answer; `GET /api/v1/doc/{rel}` gives it.

A request needs 1 to 50 `path` values, each at most 512 bytes and without a NUL, and a whole query of at most 16 KiB; otherwise it is `400` with `bad_request`, as is a query that is not valid percent-encoding. `limit` says the most paths one request takes. The route answers from the file system when asked, so a file that appears later links on a later request; `?since=` and `304` work on the answer's own `cursor`, which fingerprints its `data`. A session is needed as for `status`, and the usual refusals apply.

### Detail data

`TaskDetail` has `task` (with `parent` for a sub-task), `pill`, `plan`, `delivered`, `waits`, `open_decisions`, `decisions`, `unread`, `reports`, `more_reports`, `attempts`, `events`, `more_events`, `children` (the task's sub-tasks as task rows, absent when it has none) and `attachments` (the files linked with `hand task attach`, in the order linked, each `name`, `label`, `kind`, `size` in bytes and `missing` once the file left `inbox/`; absent when there is none, and never a host path; the API only reads them, as it reads every task field). A `TaskDetail`'s `attempts` rows are attempt objects with an extra `state` (`running`, `failing`, `passing`, `idle`), and each row's `ref` is the attempt. `AttemptDetail` has `attempt`, `task`, `state`, `brief`, `model_note`, `duration`, `reports`, `events`, `delivered`, and `transcript` (`shown`, `more`, `note`, `model`, `answered`, `turns`; a turn is `role`, `text`, `at`, `queued`). `DecisionDetail` has `decision`, `task`, `body` and `options` (`key`, `label`, `note`, `value`, `recommended`, `other`, `long`). `body` is the prose below the options and includes their text, so a client that renders `options` should not render the option lines twice. `ReportDetail` has `report`, `task` and `delivered`, the report's own `Delivered:` items (or its PR and MR links when it has no such line), each with `ref`, `kind` and, for a file inside the fleet, `href`.

Every ref in a detail is the same `tN`, `aN`, `rN`, `dN`, `sN` the pages and the CLI use. A report object carries the full `body` and a decision object the full `question`, as written; an attempt object carries neither. The board does not render Markdown in the API.

An event from a terminal screen (`attempt.blocked`, `attempt.limited`, `supervisor.blocked`, `supervisor.limited`) carries no `detail`, and neither does an event whose `detail` is a harness API error: that field would be screen text. Its `seq`, `at`, `kind` and `task` remain.

The API never returns a host path except the `excerpt` and `options` (the same labels again under the item's `keys`) of a blocked or worker Needs item: they are the Board's own lines and keep a path as the screen printed it, so a client shows them as plain text and never logs them. The `title` keeps the Board's words but goes through the same absolute-path scrub as the rest of the API. An attempt carries its `branch` but not its worktree or launch command; a file deliverable's `ref` is the file's base name, and its `href` is the board's `/doc/...` path when the file is inside the fleet (absent otherwise). Hand-composed text, such as an attempt's or supervisor's `reason` or an event `detail`, keeps its message but drops the directory part of any absolute path. Free text written by you or the agent, such as a report's `body` or a task's `goal`, is returned as written.

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

A blocked screen's countdown does not send an event each second. A `blocked` or `worker` item in `Status.needs.items` carries `deny_at`, the time the harness will deny the request, and the client counts down to it: it takes the time left from `deny_at` and never renders the countdown phrase inside `excerpt`, which is held at its last sent text while only the countdown moves. While a screen counts down, `deny_at`, an item's `keys.revision` and `supervisor.keys.revision` hold their last sent values (within 2 s); a key press with that revision is accepted while the digest matches. An unchanged `excerpt` is not sent again.

The board does not read a worker's screen on each version tick (`TestTheEventLoopLeavesWorkerScreensUnread`), so while the last sent `Status` has a `worker` item the stream rebuilds the status once per beat (15 s), and a changed worker prompt reaches the stream within one beat. A supervisor's excerpt follows its own screen at once. A `status` event over 64 KiB, which many blocked workers can cause, is sent as `resync` with `{"topics": ["status"]}`, and the client re-reads `GET /api/v1/status`, which has no cap.

The stream ends when the client closes, when the session is revoked, and when the board shuts down. A write that takes longer than twice `beat_ms` ends it too, so a client that stops reading cannot hold the board. `HEAD` answers `200` with the stream's headers and no body, and does not start a stream. Where the peer listener serves the API to a paired Companion, a half-close also ends the stream, as it ends `/events`.

## The actions

Each carries the cookie and `X-Hand-CSRF`, and answers one envelope with the updated object. A mutation ignores `?since=` and never answers `304`.

### Receipts and warnings

An action's answer says what the Board's toast would say, in two places: the envelope's `receipt` and `warn` fields, and the `X-Hand-Receipt` and `X-Hand-Warn` headers. The headers hold the same words URL path-escaped, as the Board's own forms send them, so a reader that decodes the header gets the field's string; a field is absent, and its header empty, when the Board has nothing to say. A failed action carries no receipt, because it did not happen, but it carries its `warn` on the header and in the error object's `warn`.

`receipt` is the Board's success words:

| Action | `receipt` |
|---|---|
| `POST /decision/{dN}/answer` | `d1 answered · s1 reads it now`, or `d1 answered · no supervisor is running; it waits` |
| `POST /report/{rN}/ack` | `r1 marked read` |
| `POST /supervisor/start`, `resume` | `Starting the supervisor…` |
| `POST /supervisor/stop` | `s1 stopping` |
| `POST /supervisor/interrupt` | `Interrupt sent to s1` |
| `POST /supervisor/force` | `Typed into s1` |
| `POST /supervisor/switch` | `Switch applied`, `Switch set for after this turn`, `Switch applied, not answered`, `Switching to codex…`, or `Switch canceled` |
| `POST /models/refresh` | `Refreshing models…` |
| `POST /skills/sync`, `update`, `propose` | `Skills sync: 1 current, 2 updated`: the verb, then a count for each state the rows carry, in alphabetical order; `Skills sync: nothing subscribed` for no rows |
| `POST /supervisor/keys` | `Sent down enter to s1` |
| `POST /attempt/{aN}/keys` | `Sent enter to a1` |
| `POST /phone/revoke` | `Revoked in alpha and beta`, absent when nothing was revoked |

The supervisor named is the one the Board's own button would name, read before the control runs. Sending a message carries no receipt, because the Board's composer shows none: the answer's timeline is the confirmation. Pairing carries none either, and a pairing code or an app link is never put in a receipt or a warning: they hold only fleet names.

`warn` is the caveat the Board shows beside the receipt, for 12 seconds rather than 4: the warning the control printed while it succeeded (an unverified model, for one), joined with `; ` when several apply, and the caveat of a write that went through but whose result could not be read, `{"accepted": true}`, which says `the action went through, but the status could not be read: <reason>; refresh to see it` for a control or a key press, and `the message was sent, but the conversation could not be read: <reason>; refresh to see it` for a message. The decision answer, the report ack, a worker's keys and a model refresh say the same in their own words: `the answer was saved, but its decision could not be read: …`, `the report was marked read, but its detail could not be read: …`, `the press went through, but the attempt could not be read: …`, and `the refresh started, but the model lists could not be read: …`. A client shows `receipt` for 4 seconds, `warn` for 12, and both together as `receipt; warn`, as the Board's `app.js` does. A client that shows no toast ignores both.

### Sending a message

`POST /api/v1/message` takes one JSON object:

```json
{"text": "please look at the cookie rename"}
```

It has the fleet page's composer semantics: the board normalizes CRLF to LF and runs the same `CheckMessage` the form does, so whitespace-only text, more than 16 KiB, invalid UTF-8, a control character other than a newline or tab, and text that starts with Hand's own envelope prefix are refused. A JSON decoder would replace an invalid UTF-8 byte or an unpaired surrogate escape with U+FFFD, so the board refuses those before decoding the text. The board then runs the supervisor's send path (`supervisor send --text`) and answers the `Timeline` the GET serves, so a client replaces its conversation with the returned data instead of polling for the message. A queued message has `queued: true` and `delivery: "queued"`; a delivered one carries the delivery the GET reports.

If the board queued the message but cannot read the conversation back, it still answers `200`, with `{"accepted": true}` as its data and the caveat in `warn`. The message is in, so a client must not send it again: it shows the warn, clears its draft and refreshes the timeline instead.

- `400 bad_request`: an empty or invalid message, or a body that is not a JSON object.
- `403 forbidden`: the board has its controls off.
- `500 internal`: the supervisor path failed; the message keeps no path.
- `200` with the updated `Timeline`, or `{"accepted": true}` when the conversation could not be read back.

`text` is free text: a path the operator types stays in it, exactly as the web composer keeps it. Files go in `attachments`, see below.

### Attachments

A file reaches the supervisor in two steps, as it does from the web composer: upload it to the fleet's `inbox/` folder, then name it in a message. The API never carries a host path in either step, only the file's inbox name.

`POST /api/v1/inbox` takes the file as `multipart/form-data` in the field `file`, because the file is the body and the other API actions' JSON cannot carry bytes; a raw body would need its file name in a header or a query. The request carries `X-Hand-CSRF` like every action. The board stores it with the page's own code (`POST /supervisor/image`), so the rules are the page's: a file is at most 10 MiB and a post at most 11 MiB; the name is reduced to its base name and to letters, marks and digits of any script, `.`, `_` and `-` (every other run becomes `_`, so `日本語 メモ.txt` is kept as `日本語_メモ.txt`), at most 80 bytes with a long extension kept, never cut inside a character; Hand keeps the bytes it was given and does not normalise them (see Inbox names in the [vocabulary](vocabulary.md)); the bytes decide whether it is an image, so a PNG, JPEG, WebP or GIF takes the extension its bytes say, and a file named like an image that is not one gets `.bin` added; any other type is kept as it is. The stored name is `YYYYMMDD-HHMMSS-N-label` (the board's local time, and `N` counts from 1 for files stored in the same second), and the folder is private to the board's user.

```json
{"name": "20261101-050000-1-shot.png", "label": "shot.png", "kind": "image", "size": 40}
```

`name` is the file's name in `inbox/` and the only handle to it. `label` is the name the operator sees (`name` without its stamp), `kind` is `image` or `file`, and `size` is the stored bytes, and an empty file is stored as the page stores it, with `size` `0`. The answer is `200`, like every action's, and carries no receipt.

- `400 bad_request`: the body is not `multipart/form-data` or has no file in the field `file`; a part with no file name is a value, not a file.
- `403 csrf_required`: no or a wrong `X-Hand-CSRF`.
- `403 forbidden`: the board has its controls off, or the request is not on a loopback board or a paired Companion.
- `413 too_large`: a file over 10 MiB (`a file can be at most 10 MiB`), a PNG, JPEG, GIF or WebP whose header says more than 36 megapixels, width times height (`an image can be at most 36 megapixels`; refused, never scaled down, and a PNG, JPEG, GIF or WebP whose size cannot be read, such as a PNG whose width times height overflows, is refused the same way with `an image whose size cannot be read is refused`) or a post over 11 MiB (`the request is too large`). Nothing is stored. Every upload also deletes the `.upload-*` files in `inbox/` that no upload has touched for an hour, the leftovers of a crash.
- `507 no_space`: the disk was full.
- `500 internal`: the folder could not be written; the message names no path.

`GET /api/v1/inbox/{name}` answers the file's bytes, not an envelope, with the headers the page's `/inbox/{name}` sets: an image is served with its own type, and every other file as `application/octet-stream` with `Content-Disposition: attachment; filename=<label>` and a `sandbox; default-src 'none'` policy; all carry `X-Content-Type-Options: nosniff` and `Cache-Control: private, max-age=86400`. It takes `Range` and `HEAD`, and a client that restored a draft asks `HEAD` to learn the file is still there and its `Content-Length`. Only a name that `POST /api/v1/inbox` could have answered is served, and only as a regular file inside `inbox/`. A name with a separator, an encoded one, a backslash, a NUL, a leading dot or a `..` is `404`, as is a link that leaves the folder, a folder, and a name that is not there. A raw dot segment (`..`, `.`, `../x`) never reaches the handler: the router answers it with `307`, as it does for `/doc`, so only the encoded forms are `404`. Every refusal is `404` with `not_found`, as the page's is a plain `404`, and names no path.

`POST /api/v1/message` takes an optional `attachments`, a list of inbox names:

```json
{"text": "what is wrong here?", "attachments": ["20261101-050000-1-shot.png"]}
```

`text` may hold `[attachment N]`, where `N` is the 1-based position of a name in `attachments`. The board writes each such token into the message as `[image N: PATH]` for an image and `[file N: PATH]` for any other file, in the same place, where `PATH` is the file in this board's `inbox/`, so the supervisor reads the file where the words put it and can call it "image 2". A token whose `N` is `0`, above the length of `attachments` or not written exactly as `[attachment N]` stays text, and the same `N` may stand in several tokens. A name that no token mentions is written after the text as a line of the same form, with a newline added when the text does not end in one and a newline after every line; the page's composer still writes the older line `[image: PATH]`, which every reader keeps accepting. The whole message then goes through the same CRLF and `CheckMessage` rules, so the longer lines count toward the 16 KiB. `text` may be left out, or empty, when `attachments` is not empty, as the page's composer allows a message of files alone; it is required otherwise. The answer is the `Timeline` as for any message, where the new entry carries the files in `shots`, each with its `n` (the number the supervisor reads), and its `text` with the operator's `[attachment N]` tokens in place and without the appended lines. A client draws shot `n` where `[attachment n]` stands in `text`, and a shot with no token in `text` below it, as it does for a message written on the page or before this grammar (its lines get the next unused numbers in order). A message without `attachments`, or with an empty list or `null`, is sent exactly as before. A name may be listed twice and is then written twice, with two numbers.

Every name is checked before anything is sent, and one bad name refuses the whole message:

- `400 bad_request`: `attachments` is not a list of strings, or a name is not one an upload could have answered (a path, a host path, a `..` or backslash name, an empty name, a name with a newline or NUL).
- `404 not_found`: the name is well formed but not a regular file in `inbox/` (pruned, never uploaded, a link or a folder); the message names the label and no path.

A client that shows the fleet status re-reads `GET /api/v1/status` after a message, an answer or an ack, because a sent message can change the composer hint and Needs. The supervisor controls answer the status itself, so they need no second read.

### Answering a decision

`POST /api/v1/decision/{dN}/answer` takes one JSON object:

```json
{"choice": "1. Yes, keep it", "note": "the docs keep the old name"}
```

`choice` is an option's `value` from the decision detail; `note` is the note the page offers. Either may be empty, though an option the page marks `other` needs a note. Send one JSON object under 64 KiB: a second object or trailing text is refused before the board changes anything. The board trims both and stores `choice`, a blank line, then `note`, exactly as the page's form does. `answered_by` is `operator (desktop)` for the host's session and `phone (<device name>)` for a paired Companion.

- `400 bad_request`: an empty answer, an `other` option with no note, or a body that is not a JSON object.
- `404 not_found`: an unknown ref.
- `409 conflict`: the decision is no longer open.
- `200` with the updated `DecisionDetail`.

### Acknowledging a report

`POST /api/v1/report/{rN}/ack` takes no body. It sets `acked_at` and `acked_by` (`operator (desktop)`, or `phone (<device name>)` for a paired Companion) and answers the updated `ReportDetail`. An unknown ref is `404`; a report already acknowledged is `409 conflict`.

A client that shows the fleet status re-reads `GET /api/v1/status` after either action, because an answer and an ack leave Needs. The action's own data is already the fresh detail, so the detail screen needs no second read. When the answer or the ack went through but the board could not read it back, the answer is `200` with `{"accepted": true}` and the caveat in `warn`: the change is in, so a client does not send it again, shows the warn and refreshes the detail instead.

### Pairing a Companion

`POST /api/v1/pair` is the app's way to redeem a pairing code without the pair page. It needs no cookie and no `X-Hand-CSRF`, and it is the only API path an unpaired peer on the peer listener may reach. It answers only a peer that sent its endpoint ID: on the loopback listener, or on a peer connection with no preface, it is `403 forbidden`, although every other API request on loopback is served with a session. The answer carries the device secret in a readable body, where the form sets an `HttpOnly` cookie, and a device with no `iroh:` login is one the API refuses anyway. Send one `application/json` object under 4 KiB, with exactly the keys `code` and `name`, both strings (a missing, extra, differently cased or `null` key is `bad_request`):

```json
{"code": "K7QM3-XRD2P", "name": "Pixel"}
```

It runs the pair form's code path, so the limits are the form's: a code works once for 5 minutes, 5 wrong codes in a minute from one endpoint block it, and 10 wrong codes from anyone cancel every code. The device is bound to the peer's iroh endpoint ID (`iroh:<endpoint id>`) and recorded as the form records it. `name` may be an empty string, which stores `Companion`. Unlike the form, which cleans a name, the API refuses a name with a control character, even a trailing newline, or over 40 characters once trimmed, before it spends the code. A request through a proxy, through Funnel, to a network-address board, or on the loopback listener is refused as every API request is. A body that is refused as `bad_request` never counts as a wrong code. A fleet that does not open answers `404 not_found` to any method on this path, as the form's page does, not `401`. The peer listener admits any method to the path; on an open fleet only `POST` pairs, and the others get `401 unauthorized` like any API request without a session.

The answer is the usual envelope. `data` is `Paired`:

```json
{"fleets": [{"id": "f00000000e0aa", "name": "hand", "cookie_name": "hand_phone_f00000000e0aa", "cookie_value": "7.4d1c…(64 hex characters)"}], "missed": 0}
```

A code may name several fleets: `fleets` holds each one that paired, and `missed` counts the ones that did not. The client keeps `cookie_value` as the device secret and sends it as the cookie `cookie_name` on that fleet's `/api/v1` requests; it reads the CSRF value from `GET /api/v1/session`, as for any Companion. The secret is in this answer only. The board never logs a code or a secret, and the client must not either.

- `400 bad_request`: a body that is not one JSON object with `code` and `name`, over 4 KiB, or not `application/json`.
- `400 bad_name`: a name with a control character, or over 40 characters once trimmed.
- `403 wrong_code`, `code_expired`, `code_used`: the code is not right, is past 5 minutes, or was already redeemed.
- `403 codes_cancelled`: this was the tenth wrong code, so every code is cancelled.
- `429 rate_limited`: 5 wrong codes from this endpoint in the last minute.
- `200` with `Paired`.

### The Companion page

The Board's Companion page (`/<fleet id>/connect`) is one fleet's page that reads every fleet the host session proves, so its API is a fleet's, at `/<fleet id>/api/v1/phone`, not a host path. It is built from the page's own code, so the rules are the page's: a code works once for 5 minutes, one code shows at a time (a new code or link cancels the unused one before it), and codes live in the board's memory only, so restarting the board cancels them.

`GET /api/v1/phone` answers `Phone`:

```json
{
  "iroh": {"state": "ready", "tone": "pass",
           "text": "Companion access over iroh is online. The Companion can pair with this host.", "id": "9f2c…(64 hex characters)"},
  "fleets": [{"id": "f00000000e0aa", "name": "hand", "current": true, "selected": true},
             {"id": "f00000000e0bb", "name": "api"}],
  "code": {"code": "K7QM3-XRD2P", "link": "https://host.example/f00000000e0aa/pair#K7QM3-XRD2P",
           "url": "https://host.example/f00000000e0aa/pair", "address": "https://host.example",
           "fleets": [{"id": "f00000000e0aa", "name": "hand"}], "expires_at": "2026-11-01T05:05:00Z"},
  "app": {"link": "handboard://c#v=1&e=9f2c…&f=f00000000e0aa&c=K7QM3XRD2P", "expires_at": "2026-11-01T05:05:00Z"},
  "devices": [
    {"phone": "0123456789abcdef", "name": "Pixel 9", "login": "iroh:9f2c…", "paired_at": "2026-11-01T04:40:00Z",
     "seen_at": "2026-11-01T04:59:00Z",
     "fleets": [{"id": "f00000000e0aa", "name": "hand", "current": true}, {"id": "f00000000e0bb", "name": "api", "expired": true}]},
    {"id": 3, "name": "Old phone", "paired_at": "2026-10-01T04:40:00Z", "expired": true}
  ],
  "unreachable": ["web"]
}
```

- `iroh` is the sidecar's state as the page words it, and is absent when the board runs without `--iroh`. `state` is `ready`, `no relay`, `starting` or `failed`, `tone` is the page's badge (`pass`, `wait` or `fail`), and `text` is its sentence. The app link can be made only while `state` is `ready`; the page offers the button on the same condition.
- `fleets` is the page's list of fleets a code may cover: the current fleet first and at most 7 other fleets the session proves, the page's list, so one code names at most 8, each with `current` and `selected` (ticked: the current fleet always, and the fleets of the code that shows). It is empty when the session proves only this fleet, as the page then shows no choice.
- `code` and `app` are present only while one shows: unused and not past its `expires_at`. `code` is the web code: `code` to type, `link` (the page's link with the code in its fragment), `url` and `address` (what to open and type, and what was typed), `plain_http` when the address is plain HTTP on a network, and `fleets` the code pairs. `app` is the Hand app's `handboard://` link, which holds its own code and pairs one Companion with this fleet. A client draws the QR code from `link`. Both stop showing when the Companion redeems, when the time passes, when a new code or link is made, and for `app` when the sidecar is no longer `ready`.
- `devices` are the page's phone list, read from every fleet the session proves, not only the 8 that `fleets` offers, oldest pairing first. `phone` is the label one pairing code gave every fleet it paired, the handle to revoke with. `fleets` lists where the Companion is paired, with `expired` once its 90 days have passed there; it is absent for a Companion paired with this fleet alone, and for a Companion paired before one code could pair several fleets, which has no `phone` and is revoked by its `id` (the number is this fleet's own and means nothing in another). `expired` is then the Companion's own. `login` is the device's login (`iroh:` and its endpoint ID for the app), `seen_at` the last time it read this board since the board started.
- `unreachable` names the fleets the session proves but whose Companions could not be read; their Companions are missing from `devices`, as the page says.

`POST /api/v1/phone/code` takes one JSON object, `{"base": "https://host.example", "fleets": ["f00000000e0bb"]}`: `base` is the address the Companion opens (scheme and host only, as the page's field), and `fleets` the other fleets to pair; the current fleet is always paired, and a repeated id is one. A code names at most 8 fleets, this one and 7 others: a ninth fleet id is refused with `403 forbidden`, as is any fleet the `fleets` list did not offer, and no code is made. It answers `Phone` with `code` showing.

`POST /api/v1/phone/app` takes no body and answers `Phone` with `app` showing.

`POST /api/v1/phone/revoke` takes one JSON object, `{"phone": "0123456789abcdef"}` to revoke that Companion in every fleet the session proves, `{"phone": "...", "fleet": "f00000000e0bb"}` for one fleet, or `{"id": 3}` for a Companion with no label in this fleet. It answers `Phone` as it stands after, with `revoke`:

```json
{"revoke": {"revoked": [{"id": "f00000000e0aa", "name": "hand"}],
            "failed": [{"id": "f00000000e0cc", "name": "web", "reason": "the fleet did not answer"}]}}
```

`revoked` holds the fleets where a record was removed; a fleet that held none is in neither list. `failed` holds the fleets the revoke could not reach, whose record of the Companion, if it has one, is still active: `reason` is `the fleet did not answer`, in fixed words, never the error's own text or a path. The answer is `200` when some or all failed, so a client reads `revoke.failed`; `warn` then carries the page's sentence ("Revoked in hand. web could not be reached: … Press Revoke everywhere again when it is back."), and `receipt` is absent. Press Revoke everywhere again for the same Companion when the fleet is back: the call repeats safely, and a Companion already revoked is an empty report. Revoking an `id` that is not there is an empty report too, as on the page.

A paired Companion is refused on all four paths: it manages no Companions, and the page lets it sign out only itself. The board reads what an answer needs before it acts, so every refusal below leaves everything as it was, and none mints a code or revokes a device. The answer to an action is built from that read, with the revoked records removed, not read again: a Companion that pairs or is seen in this fleet between the read and the answer is missing from that one answer, and the next read has it.

- `400 bad_request`: a body that is not one JSON object as above; for `code`, a `base` that is not `scheme://host` (no path, login, query or fragment); for `revoke`, a body without exactly one of `phone` (16 lowercase hex characters) and `id` (positive), or with `fleet` and no `phone`.
- `403 csrf_required`: no or a wrong `X-Hand-CSRF`.
- `403 forbidden`: a paired Companion; a network board or a proxied request; for `code`, a fleet id the `fleets` list did not offer: one this session does not prove, or one beyond the 7 others a code may name; for `revoke`, a fleet that this session does not prove (revoke reaches every fleet the session proves, with no cap of 8).
- `409 conflict`: `app` while the sidecar is not `ready` with a relay.
- `500 internal`: the Companion list could not be read; the message names no path.

The code and the link are credentials: a Companion that holds either becomes a paired device of this fleet. They are returned only by these endpoints, to the host's session behind the same cookie, CSRF and loopback rules as every action, in `code.code`, `code.link` and `app.link` and nowhere else: not in `receipt`, `warn`, an error, the event stream, another endpoint, a page other than the one that showed it, the database or a log. A client keeps them in memory, never logs or stores them, and stops showing them at `expires_at`. The `cursor` of `phone` fingerprints a keyed digest of the code, not the code, so it is safe to log.

Poll `GET /api/v1/phone` every 2 seconds while a code or link shows, so the page learns when the Companion paired (the code leaves, the device arrives) or the time passed, sending the cursor it last read: an unchanged answer is `304`. Without a code on screen it reads once when the page opens and after each action, which already answers the fresh data.

### The fleet list and the features

`GET /api/v1/fleets` and `GET /api/v1/meta` answer on the host, not under a fleet prefix: `/api/v1/fleets`, never `/<fleet id>/api/v1/fleets`. A Companion paired with several fleets holds one `hand_phone_<fleet id>` cookie for each, and sends all of them in every request, over its one iroh connection; only the host sees them together, as it does for the rail. Their envelope has no `fleet` and no `cursor`, because they belong to no fleet and have no change stream: a client re-reads them when it opens the fleet list and when a fleet's own stream reports a change.

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

`fleets` holds every fleet the request's cookies prove, in the Board's rail order, and nothing else: a fleet it does not prove never appears, not by id, name or count. The rail draws the current fleet plus at most seven others, so the list keeps the rail's order but may carry more fleets than the narrow rail shows. A host session proves each fleet whose `hand_board_<fleet id>` cookie holds that fleet's token, a paired Companion each fleet whose `hand_phone_<fleet id>` cookie proves it from this endpoint. `pill` and `line` are `Status.pill` and `Status.line` of that fleet, and `needs` carries `waiting`, `worst`, `worst_word`, `worst_count` and `summary` as `Status.needs` does; the two are one computation, so a list and a status read agree. A proven fleet that will not open has `unavailable: true` and no `pill` or `needs`. A fleet that has left the Board's list, and a Companion's fleet that will not open, are not listed, as the rail does not list them. The list carries no path, ref or item text.

`GET /api/v1/meta` answers any caller that can read a fleet's `status`:

```json
{
  "version": 1,
  "generated_at": "2026-11-01T05:00:00Z",
  "data": {
    "version": "0.14.0",
    "host": "workstation",
    "features": ["events", "fleets", "history", "host", "links", "pair", "phone", "skills", "skills_sync"],
    "sidecar": {"state": "ready", "id": "9f2c…(64 hex characters)", "relay": "https://relay.example"}
  }
}
```

`version` is what `hand version` prints. `host` is the machine's host name as the operating system reports it (`os.Hostname()`), trimmed of white space, with whatever length the operating system allows, and read when the Board starts: a renamed machine shows its new name after a Board restart. It can contain a person's name (a Mac's default name does), so it goes only to a caller that can already read `meta`, a Companion on the peer listener among them. It is `""` when the machine has none, and a client then names the host by something else. `features` is a sorted list of short names, one for each optional capability a client must check before it uses it. A name is added when its endpoint lands, and it is never renamed or removed inside v1; a client shows a feature only when its name is listed, so an app that updates before the host does hides what the host lacks. `sidecar` is the iroh sidecar's `state` (`starting`, `ready`, `failed` or `stopped`), with `id`, `relay` and a scrubbed `reason` when it has them; it is absent when the Board runs without `--iroh`, and for a caller on the peer listener, which has no use for the host's own sidecar. An unpaired caller cannot read `meta`: the pair path stays the only one that needs no session, so an app pairs first and checks `features` after.

| Feature | Means |
|---|---|
| `events` | `GET /api/v1/events`, the change stream |
| `fleets` | `GET /api/v1/fleets` |
| `history` | `GET /api/v1/history/search` and `GET /api/v1/history/{ref}` |
| `host` | `host` in `GET /api/v1/meta` |
| `links` | `GET /api/v1/links` |
| `pair` | `POST /api/v1/pair` |
| `phone` | `GET /api/v1/phone`, and its `code`, `app` and `revoke` actions |
| `skills` | `GET /api/v1/skills` and its `sync`, `update` and `propose` actions |
| `skills_sync` | a paired Companion may run `POST /api/v1/skills/sync` under the rules in [Skills](#skills) |

Both paths take the refusals of every read: a request through a proxy or Funnel, a network-address board, and a Companion on the peer listener that holds no fleet paired with its own key get `403` with `forbidden`; a caller that proves no fleet gets `401` with `unauthorized`; a method other than `GET` and `HEAD` gets `405` with `Allow: GET, HEAD`.

### The supervisor controls

`POST /api/v1/supervisor/{start,resume,stop,interrupt,force}` run the same path as the console button they mirror and answer the `Status` the GET serves, built after the control ran, so a client replaces its status with the returned data and needs no second read. `POST /api/v1/supervisor/switch` answers the same `Status`; see [Switching the model or harness](#switching-the-model-or-harness).

- **start** takes no body or one JSON object. With no body it runs `supervisor start` with no flags, copying the last supervisor's harness, model and effort, like the console's "same as sN"; there must be an earlier supervisor to copy, else `409 conflict`. With a body it starts the harness the body names, which is how a fleet gets its first supervisor: see [Starting with a harness, model and effort](#starting-with-a-harness-model-and-effort). While one is live the answer is `409 conflict` either way.
- **resume** continues the last session under a new `sN`; it follows the CLI's rules, so a running supervisor or nothing to resume is a conflict or a not-found.
- **stop**, **interrupt** and **force** take one JSON object, the ref the client's `Status` showed: `{"ref": "s1"}`. The board runs `supervisor stop --expect s1`, `supervisor interrupt --expect s1` or `supervisor force --expect s1`, so a control binds to the supervisor on screen and is `409 conflict` when another one runs by then, rather than stopping, interrupting or typing into the replacement.
- **stop** and **interrupt** need a running supervisor; anything else is `409 conflict`.
- **force** is the blocked screen's "Type queued messages anyway": it types the queued messages, or else the wakes, into the prompt. It follows the CLI: a supervisor that is not blocked gets its queued input delivered normally, still only to the named supervisor, and the answer is still `200`.

Resume takes no body; start takes none or a harness, model and effort. When a control ran but the board could not read the status back, the answer is `200` with `{"accepted": true}` instead of a `Status`, and its `warn` says so: the action went through, so a client shows that warning and asks for a refresh rather than reporting a failed action. It keeps the screen it has and needs no second read to report the outcome.

- `400 bad_request`: stop, interrupt or force without one JSON object, or with a ref that is not `sN`.
- `403 forbidden`: the board has its controls off, or the request is not on a loopback board.
- `409 conflict`: the supervisor is in a state the control refuses, or another supervisor is running than the one the request names.
- `404 not_found`: there is nothing to resume.
- `500 internal`: the control failed; the message keeps no path.
- `200` with the updated `Status`, or `{"accepted": true}` when the status could not be read.

### Starting with a harness, model and effort

`POST /api/v1/supervisor/start` takes this instead of no body to choose what starts, which is how a fleet gets its first supervisor:

```json
{"harness": "claude", "model": "opus", "effort": "high"}
```

`harness` is one of the board's: `claude`, `codex`, `opencode`, `agy` or `omp`. `copilot` starts workers only, so it is refused with the board's sentence. `model` is required, except for `opencode`, which starts on its own configuration when the body leaves it out; `effort` may be left out, and then no `--effort` is passed and the harness's own default effort stands, as the Start strip's `default` row does. An effort without a model, or either without a harness, is `400 bad_request`. The board runs `supervisor start --harness H --model M --effort E`, the same path as one of those rows, and answers the `Status` the GET serves with the receipt `Starting the supervisor…`.

The triple goes through the harness's own check before the control runs, the same one the Board uses for its "starting anyway" warning:

- A harness, model or effort the check refuses is `400 bad_request` with the board's own sentence, for one `claude model opus: effort ultra is not one of low, medium, high, xhigh, max`, `harness "gemini" must be one of claude, codex, opencode, agy, copilot, omp`, or `copilot starts workers only; run the supervisor on claude, codex, opencode, agy or omp`. Nothing starts.
- A model the check could not check, because Hand could not read the harness's own model list, starts with that line in the answer's `warn`, exactly as the Board shows it: `claude model gpt-6-luna is not an alias (opus, sonnet, haiku, fable), a claude-* name or in Claude Code's model list; starting anyway` when `~/.secondhand/models/claude.json` is missing, or `codex model gpt-6-luna could not be checked: no model list yet; run \`hand models --refresh codex\`; starting anyway`. A warning with anything else in it, such as an effort the harness's own set refuses, is refused by the next rule.
- A model or effort the harness's list was read for and says is not there is refused with that line minus its `; starting anyway`, rather than started. This is the one place the API is stricter than the Board: its Start strip keeps an "Other model" box for a name the list has not caught up with, and v1 has no field that carries one.

- `400 bad_request`: a body that is not one JSON object, a model or effort without a harness, or a harness, model or effort the check refuses.
- `403 forbidden`: the board has its controls off, or the request is not on a loopback board.
- `409 conflict`: a supervisor is already live, or a `start` with no body had none to copy.
- `500 internal`: the control failed; the message keeps no path.
- `200` with the updated `Status`, or `{"accepted": true}` when the status could not be read.

### Switching the model or harness

`POST /api/v1/supervisor/switch` takes one JSON object:

```json
{"ref": "s1", "model": "opus", "effort": "high"}
```

`ref` is the supervisor the client's `Status` showed, as for stop and interrupt, and it is required: the board runs `supervisor switch --expect s1`, so a switch from a screen showing a replaced supervisor is `409 conflict` with `s2 is on screen, but s3 is running now; refresh and try again`, and nothing is switched.

- `model` and `effort` set the pending switch, as the model menu's Switch does: the running turn finishes on the model it has, and the change lands after it. Either may be left out, and a supervisor that is idle switches at once. With all three fields empty and no `cancel`, the answer is `400 bad_request` with the board's own `give a model or effort`.
- `harness` switches to another harness, which the board does between turns: a working supervisor is `409 conflict` with the CLI's sentence, and an idle one stops and starts the new harness now. Its receipt is `Switching to codex…`.
- `cancel: true` drops a pending switch, or cancels one on a supervisor that is no longer running (the Start strip's Drop switch), and ignores the other fields as the Board's own cancel and drop forms do. Its receipt is `Switch canceled`.
- The confirmations are the Board's: `Switch applied`, `Switch set for after this turn` when the status the board read back still carries the pending switch, `Switch applied, not answered` when the CLI warned that the opencode session did not take the note, and `Switching to H…` above. `Models.switching` carries the pending line.

The answer is the `Status` the GET serves, after the switch, or `{"accepted": true}` with the caveat in `warn` when the switch went through but the status could not be read back: the switch is set, so a client does not send it again, shows the warn and refreshes.

- `400 bad_request`: a body that is not one JSON object, a `ref` that is not `sN`, nothing to switch to and no `cancel`, or a harness the board does not know or cannot supervise.
- `403 forbidden`: the board has its controls off, or the request is not on a loopback board.
- `409 conflict`: another supervisor runs than the `ref` the client showed, a harness switch while the supervisor is working, a model already running, a supervisor with no session yet, or another state the CLI refuses; the message is the CLI's.
- `200` with the updated `Status`, or `{"accepted": true}` when the status could not be read.

### Refreshing the model lists

`POST /api/v1/models/refresh` takes one JSON object, or no body to refresh every list:

```json
{"harness": "claude"}
```

It runs the same refresh as the menu's Refresh models, in the background, one per harness, and answers the `Models` the GET serves with the receipt `Refreshing models…`. The new lists arrive in `GET /api/v1/models` when each refresh finishes, which is a few seconds and no event. The names it takes are `claude`, `codex`, `agy`, `copilot` and `omp`; `opencode` lists its own models when it is asked, so it has none to refresh. A harness that is busy, or was refreshed less than 30 seconds ago, is `429 rate_limited` with the board's sentence, `claude models were refreshed 12 s ago; try again in a moment`, or `claude models are already refreshing`. No body refreshes all five, and is refused only when every one of them is inside its cooldown. A paired Companion cannot refresh a host's lists.

- `400 bad_request`: a body that is not one JSON object, or a harness outside those five.
- `403 forbidden`: the board has its controls off, the request is not on a loopback board, or the session is a paired Companion's.
- `429 rate_limited`: every named list is inside its 30-second cooldown, or already refreshing.
- `200` with the `Models` the GET serves, or `{"accepted": true}` when they could not be read back.

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

### Pressing a worker's keys

`POST /api/v1/attempt/{aN}/keys` takes one JSON object:

```json
{"key": "enter", "revision": 6, "after": 2, "screen": "f8c722e0710879ae811b2fe7ad0c8ba0"}
```

`key` is one button's `keys`, joined with spaces when it holds more than one (`"down enter"`); every key is one of the board's allowed keys, `enter`, `esc`, `up`, `down`, `1`, `2` and `3`, or a single digit from `4` to `9` on a worker's screen. `revision` and `after` are required and are the `revision` and `pressed` of the item's `keys`; `screen` is that `keys` object's `digest`, and an empty one is left out, as the Board's own form does. The board runs the same `attempt keys --revision N --screen DIGEST --after SEQ aN KEY...` path as `/attempt/aN`'s buttons, so a press is refused if the screen moved, another key was pressed since the client read the item, or the attempt is no longer running. The answer is the `AttemptDetail` the GET returns, after the press.

- `400 bad_request`: a body that is not one JSON object, no key, a key outside the set, a negative revision or `after`, or a screen that is not 32 lowercase hex characters.
- `403 forbidden`: the board has its controls off, or the request is not on a loopback board or a paired Companion.
- `404 not_found`: the attempt is not in this fleet.
- `409 conflict`: the screen changed since the given revision or digest, another key was pressed after `after`, or the attempt is not running; nothing was sent.
- `200` with the updated `AttemptDetail`, or `{"accepted": true}` with the caveat in `warn` when the press went through but the attempt could not be read back.

**The keys decision.** The API never returns a screen. While the supervisor is blocked, `Status.supervisor.keys` carries the revision, digest and pressed sequence the guard needs, and `Status.supervisor.ask` carries the question and option labels the Board's Ask card already shows the operator on the same loopback session, so it adds no new disclosure. A client shows one button per option when `ask` is present, and the fixed seven-key toolbar only when it is absent (a screen the parser does not understand). A worker has no `ask`: the Board's Ask card covers the supervisor's screen only. Its item carries the `excerpt`, `title`, `options` and `keys` above, and a press goes to `/api/v1/attempt/{aN}/keys`.

### Skills

The skills endpoints run the code `hand skills` runs, in the Board's process: the same scopes, the same drift states, the same trust levels and the same refusals. A client gets what `hand skills list`, `sync`, `update` and `propose` print as JSON. No endpoint adds, removes or subscribes a skill; `hand skills add` and `remove` stay on the command line.

A scope is `fleet` (the supervisor's own skills in the fleet home), `project` (a project's checkout, named by `project`) or `user` (the machine-wide skills in `$HOME`). There is no working folder to infer one from, so an action names it.

`GET /api/v1/skills` answers `scopes`, in the order `fleet`, then each project in the project list's order, then `user`. A scope is `{scope, project?, error?, skills}`. `error` is a scrubbed sentence when the scope could not be read (a project whose folder is not a git checkout, an unreadable lock); its `skills` is then empty and the other scopes are still answered. A skill is:

| Field | Meaning |
|---|---|
| `name` | the skill's folder name |
| `source` | the source address, with `#PATH` when the skill sits in a folder of it; `hand init` for a skill Hand wrote itself; empty for a folder nothing manages |
| `ref`, `commit` | the subscribed ref and the commit the installed copy came from; empty when not managed |
| `latest` | the source's commit now, read by fetching it; empty when the fetch failed |
| `managed` | `true` when the lock records the skill; the rest of the row is empty when it is `false` |
| `trusted` | `true` for Hand's own repository (`atqamz/skills`) when the scope is subscribed to it, and for a local source subscribed as itself in the fleet or user scope: `sync` applies those without a yes; `false` for every other source, a third-party one among them, which moves only on `yes` |
| `state` | `current`, `behind`, `edited`, `conflict`, or `error` (then `detail` says why); the drift statuses `hand skills outdated` prints |
| `detail` | the reason for `error`, or a diff that could not be built |
| `diff`, `diff_cut` | for `behind`, the change between the installed copy and `latest` as a unified diff, at most 64 KiB, cut at a line with `diff_cut: true`; a diff is third-party file content and is returned as written, not scrubbed |

`edited` is a copy changed here while its source did not move (`propose` sends the edit to the source); `conflict` is changed here and moved at the source (nothing updates, and `sync` and `update` answer a `reconcile` command).

Every read fetches each distinct source once, with the CLI's limits: one git step takes at most 90 seconds, the fetches of one request share 3 minutes, and the whole request is cut at 5 minutes, so it takes seconds on a fast link and is not for polling. `scope` and `project` narrow the read to one scope or one project; `project` goes only with `scope=project`, where it names one project (an unknown one is `404`) and, left out, reads every project. An action needs `project` with `scope: "project"`. A scope that cannot be fetched shows `state: error` per skill instead of failing the read. The envelope's `cursor` is a fingerprint of the answer, and `?since=` with it answers `304` when nothing changed.

`POST /api/v1/skills/sync`, `update` and `propose` take one JSON object: `scope` (required), `project` (with `scope: "project"` only), `name` (the skill; optional for `sync` and `update`, which then take every subscribed skill of the scope, and required for `propose`) and `yes` (`sync` only: `NAME@COMMIT`, the whole commit id of the diff the client showed). They run `hand skills sync`, `update` and `propose` with the matching scope flag and the answer is a `SkillsRun`:

| Field | Meaning |
|---|---|
| `scope`, `project` | as asked |
| `results` | one `{name, state, detail}` per skill, in the order the command prints its rows: `updated`, `bumped` (`detail` is the pull request's address), `pending`, `behind`, `current`, `edited`, `conflict`, `restored`, `proposed` (`detail` is the pull request's address), `removed` or `refused` (`detail` is the reason) |
| `refused` | how many skills the command refused; `hand skills` exits `3` for a non-zero count, and the API answers `200` with the rows instead |
| `diffs` | for `sync`, a `{name, diff, cut}` for each third-party update that waits for a yes; the same 64 KiB cap, and as written like `diff` |
| `reconcile` | for a `conflict`, the `hand task add --goal …` command the CLI prints for a worker, with a login removed from a source address; the API creates no task |

Trust and drift behave as the command does. `sync` applies Hand's own repository and local sources with a backup, never a third-party update without `yes`, never a copy that was edited here, and in a project scope opens a bump pull request for each skill instead of editing the checkout. `update` is the explicit command: it does not need a yes, and it is the one that edits a project checkout in place for a person to commit. `propose` pushes only its own branch and opens a pull request through `gh`, which the Board needs on its `PATH`.

A paired Companion reads `GET /api/v1/skills` like any other read, and may run `POST /api/v1/skills/sync` in the `fleet` and `project` scopes without `yes` (`skills_sync` in `features`). It gets `403 forbidden` for `update` and `propose` ("a paired Companion cannot update or propose skills; use the host's Board"), for `sync` with a `yes` ("a paired Companion cannot apply a third-party update; read the diff, then tell the supervisor or use the host's Board") and for `sync` of the `user` scope ("a paired Companion cannot sync the user scope; use the host's Board"). `update` and `propose` are refused before the body is read; the other two after it is validated, so a malformed `sync` from a Companion answers `400` before `403`. A refusal comes before the one-at-a-time lock and before any fetch. For a Companion, a local source is never applied: its row is `behind` with the detail "local source; apply it from the host's Board or CLI". A third-party source stays `behind` with its diff in `diffs`, as for the host, and the Companion cannot apply it. The reason for the limits: `update` and `propose` and a `yes` run `git` and `gh` with the host's credentials on content nobody on the host read, or publish it, as `models/refresh` and the Companion actions do; `sync` without `yes` moves only what the host already trusts. Every `sync`, from the host or a Companion, records one `skills.synced` event: the scope (and the project, when it ran), `by` the host's session or `phone (NAME)`, and the receipt's counts, or `failed` and the error's kind (`conflict`, `bad_request`, `internal`); never a source address, a diff, a path or an error message. A refusal (`400`, `403`, `409`) writes none. If the event cannot be written, the request answers `500` with that error, after the sync ran. A mutation needs the session's cookie and `X-Hand-CSRF`.

The Board runs one skills request at a time, a read or an action: a second one answers `409 conflict` ("another skills request is running on this board; try again when it ends") instead of queueing another fetch. An action is not cancelled when the client leaves; it runs to its own limits, and it stops when the Board shuts down: the `git` or `gh` step is cancelled, its clone folder is removed and the request ends: a `sync` or `update` answers `200` with a `refused` row for the skill it was on, and a `propose` an error. Error kinds follow the CLI's exit codes: an invalid scope, name or `yes` is `400 bad_request`, a project that is not registered `404 not_found`, a skill the command refuses before it reads any (no lock, a name that is not a skill name) `400` or `409 conflict`, and any other failure `500 internal`. A request-level error carries the command's sentence with no path and no login (a `user:password@` in a URL is removed from it, whatever command or tool printed it).

## Errors

Every error is this shape, not an envelope, including a registered fleet whose store cannot be opened on an API route:

```json
{"error": {"status": 404, "code": "not_found", "message": "not found: task t999"}}
```

`warn` is present only on a failed action that carried a warning, with the same words as the `X-Hand-Warn` header ([Receipts and warnings](#receipts-and-warnings)).

| Status | `code` | When |
|---|---|---|
| `400` | `bad_request` | a query value the endpoint cannot read (`before`, `page`, `status`, `since`, `until`, `from`, `lines`, or a history search's `q`, `who`, `task`, `limit` or `context`, or a links request with no `path`, more than 50, one over 512 bytes or with a NUL, a query that is not valid percent-encoding, or a query over 16 KiB), a window on a file that is not text or past its end, `?raw` or a window on a folder, a mutation body it cannot read, an upload that is not one file in the form field `file`, an attachment that is not an inbox name, or a Phone body the endpoint cannot read, or a skills request with a scope other than `fleet`, `project` or `user`, a project with another scope, an action with `scope: "project"` and no project, a name or `yes` that is over 512 bytes or has a control character, a `propose` with no name, a `yes` on `update` or `propose`, a `name`, `project` or `yes` that starts with a dash, or a `name` that is not a skill name |
| `400` | `bad_cursor` | `since` is not a cursor this board issued |
| `400` | `bad_name` | `pair` with a name with a control character, or over 40 characters once trimmed |
| `401` | `unauthorized` | no valid board session |
| `403` | `forbidden` | a paired Companion on a Companion endpoint, a fleet a Companion action names that the session does not prove, a path the document viewer refuses (hidden, outside the fleet folder, not a file or folder, unreadable), a proxy, Funnel, a peer with no `iroh:` device, v1 on a network-address board, `pair` outside the peer listener, `update`, `propose`, a `yes` or the `user` scope of a skills action from a paired Companion (see [Skills](#skills)), or a control or skills request with the board's controls off |
| `403` | `csrf_required` | a mutation without the session's `X-Hand-CSRF` header, or with a wrong one |
| `403` | `wrong_code` | `pair` with a code the board does not hold |
| `403` | `code_expired` | `pair` with a code older than 5 minutes |
| `403` | `code_used` | `pair` with a code already redeemed |
| `403` | `codes_cancelled` | `pair` was the tenth wrong code, so every code is cancelled |
| `404` | `not_found` | an unknown route or ref, a document path that does not exist, or an inbox name that is not a regular file in `inbox/` (to read it, or to attach it) |
| `405` | `method_not_allowed` | a `POST` outside the action paths, or any other method; the response's `Allow` names what the path takes |
| `409` | `conflict` | a skills request while another one runs, a skills command that refuses before it answers rows, `phone/app` while the sidecar is not ready with a relay, a state conflict while reading or acting, or a document that changed while it was opened |
| `413` | `too_large` | a page form only, answered with the page's HTML error page and not this shape: a `POST` over the board's 11 MiB post limit, refused by the page's guard before the route runs, or a file over 10 MiB or an image over 36 megapixels on `POST /supervisor/image`; a v1 action reads at most 64 KiB of JSON and answers `400 bad_request` above that, except `POST /api/v1/inbox`, which answers this shape with `413` for a file over 10 MiB, an image over 36 megapixels or a post over 11 MiB |
| `429` | `rate_limited` | `pair` after 5 wrong codes from this endpoint in a minute, or `models/refresh` for a list inside its 30-second cooldown |
| `500` | `internal` | any other failure |
| `507` | `no_space` | the disk was full |

Messages are scrubbed like the pages': no path and no secret survives. The `code` is the stable part; clients should branch on it.
