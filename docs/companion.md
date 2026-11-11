# The Android Companion contract

The Companion is a phone app that holds an [iroh](https://docs.iroh.computer/) connection to your laptop and shows the Board in a web view. It lives outside this repository. This page is the contract it is built against, and the Board's side of it is built to the same words: it fixes what the app dials, what it reads from a pairing link, what it may rely on, and what it must never do. [`companion-vectors.json`](companion-vectors.json) holds the test vectors; the app's tests copy it.

**Status.** Three kinds of statement share this page, and each says which it is:

- **Today** describes code in `main`: the [peer listener](phone.md#the-peer-listener), the [`hand-iroh` sidecar](phone.md#the-hand-iroh-sidecar), the Phone page's "Pair the Hand app" link, and what revoking does to a live connection. A test in `internal/companion` reads the numbers of this page from the vectors file and checks them against that code, so a change to one without the other fails CI.
- **The alert feed** is [its own section](#the-alert-feed), also in `main`.
- **Step 3** describes what pairing over iroh still has to build: telling the sidecar which IDs are paired, so it can keep connection slots for them.

The words *must*, *must not* and *may* bind the app or the Board as the sentence says. The contract is OS-neutral, but only Android is in scope: [iOS is deferred](#ios-is-deferred).

## The wire

### What the app dials

The app is an iroh endpoint with a key of its own, made on first run. Its public key is the identity the Board sees. It dials the laptop's endpoint ID from the link, 32 bytes written as 64 lowercase hexadecimal characters, with the ALPN `hand-board` (ten bytes, no version suffix). If the link carries a relay URL, the app gives it to iroh as a relay hint for that endpoint. The link never carries IP addresses, and the app must not store or send any it learns.

The app holds **one connection to a laptop at a time** and opens every stream on it. A laptop admits 16 connections by default and 4 from one source (one direct IPv4 address or IPv6 /64, or one relayed endpoint ID), so a second connection from the same phone is a cost, not a speed-up.

### Streams

- Every TCP connection that the web view makes to the app's loopback proxy becomes **one bidirectional QUIC stream**, opened on the connection above. Its bytes are the TCP bytes: no framing, no header, no length. The app must not open unidirectional streams: the sidecar grants none.
- The sidecar learns of a stream when its first byte (or its end) arrives, not when it is opened. The app must open the stream when the first bytes of the TCP connection are ready to send, and not earlier. A stream that holds no byte still counts toward the 12 open streams.
- The app speaks first. The bytes it sends are HTTP/1.1 requests. It must not send the [peer preface](vocabulary.md#peer-preface), and a line that looks like one is only malformed HTTP to the Board.

### What the sidecar writes toward the Board

For each accepted stream, the sidecar opens one TCP connection to the peer listener and writes one line before any byte of the app:

```
HAND-PEER 1 <endpoint id of the app> <secret from board.peer>\n
```

It does not wait for an answer, and the Board never sends one: the first byte the app receives on a stream is the first byte of the Board's HTTP response. The app never sees the preface or the secret. After the line, the sidecar copies bytes both ways. It writes nothing else of its own to a stream.

If the sidecar cannot connect to the Board, or cannot do it within the idle time, it resets the stream with code 3. If the Board refuses the preface (a wrong secret, which only a misconfigured laptop causes), it closes without an answer: the app sees the stream end with no byte, or a reset with code 5.

### Ending a stream

| Event | What the sidecar does | What the app must do |
|---|---|---|
| The app ends its send side (the web view half-closed) | Shuts down the write half of the Board connection and keeps copying the answer; the Board answers the request as it would with the send side open, a held request such as a long poll included. The `/events` stream, and `/api/v1/events` where the peer listener serves the API, are the exception: it has no answer to wait for, so the Board ends it | Keep reading until the stream ends |
| The Board closes its write half | Ends its send side after the last byte | Deliver every byte, then shut down the write half of the web view's socket |
| The Board closes the connection while the app is still sending | Delivers the answer, then stops the app's send side with code 5 | Stop sending without an error if an answer arrived; the web view sees a normal response |
| The web view closes its socket at once | Nothing: the app resets its send side with any code, and the sidecar closes the Board connection. The Board cancels the request when that close is a reset or a read error, and cancels an `/events` stream, or an `/api/v1/events` stream where the peer listener serves the API, on any close, plain or half; any other request that sees a close that looks like a half-close finishes and ends the connection at the answer | Reset the stream, and do not retry it |

### Close and reset codes

Codes 1 to 5 exist today. Every other code is unassigned.

| Code | Name | Where | When the sidecar sends it | What the app does | What the user sees |
|---|---|---|---|---|---|
| 1 | `full` | Connection close | The handshake finished and no connection slot was free | Waits and dials again with backoff (below) | "Your laptop is busy. Trying again." |
| 2 | `idle` | Connection close | No byte moved on any stream for the idle time | Nothing at once. Dials again when the web view next opens a stream | Nothing |
| 3 | `board_unreachable` | Stream reset and stop | The sidecar could not reach the peer listener | Closes the web view's socket. The connection stays open | After 2 in a row: "Your laptop answers, but the Board is not running. Start it on the laptop." |
| 4 | `stream_idle` | Stream reset and stop | No byte moved on this stream for the idle time | Closes the web view's socket | Nothing: the page reconnects |
| 5 | `board_error` | Stream reset or stop | The Board's TCP connection broke | Closes the web view's socket. Keeps any answer already delivered | Nothing |
| other | n/a | Either | Never today | Treats it as a lost connection: backoff, three tries, then the message of a lost connection | "Your laptop closed the connection. If you removed this phone there, pair it again." |

**Refusals.** A dialer is also refused before the handshake finishes, with no application code, when 16 connections are open, when 8 handshakes are running, or when 4 connections from its source are open. A refusal arrives at once, before any handshake, and the app treats it as code 1. A handshake that does not finish in 3 seconds is dropped with no code at all: the app cannot tell that drop from a laptop that is off, so it keeps dialing until its own 20-second deadline and then says the laptop does not answer, as below.

**Backoff.** Start at 1 second, double each time up to 30 seconds, add up to 25 percent of random jitter, and reset after a connection has carried a stream for 10 seconds. The app must not retry faster, because every attempt costs the laptop a handshake.

### Caps the app must respect

| Cap | Value | What happens past it |
|---|---|---|
| Open streams per connection | 12 | A 13th `open_bi` waits until one ends |
| New streams per second per connection | 20, after a burst of 12 | The sidecar accepts them later, at that rate: they are delayed, not refused |
| Unidirectional streams | 0 | They cannot be opened |
| Connections per laptop | 16 by default; the operator may set 1 to 1024 | Refused, or closed with code 1 |
| Connections from one source | 4 | Refused |
| Handshakes at once | 8, each dropped after 3 seconds | Refused |
| Idle, stream | 60 seconds by default; the operator may set 1 to 86400 | Reset with code 4 |
| Idle, connection | The same | Closed with code 2 |

Only a byte moves the idle clock. QUIC keepalive packets, opening a stream and ending one do not. The app cannot learn the laptop's setting, so it must assume 60 seconds and treat any connection or stream as one that may end. The Board's live pages send a heartbeat every 15 seconds, which keeps a page's event stream under the default.

The Board also bounds what an unpaired connection may do; see [What the Board does with a paired phone](#what-the-board-does-with-a-paired-phone).

## The pairing link

The link is what the laptop's QR code and its text form carry. It is not the Phone page's `ADDRESS/<fleet id>/pair#CODE` link, which a browser opens.

### Grammar

```
link     = "handboard://c#" fragment
fragment = "v=1" *( "&" key "=" value )
key      = %x61-7A *15( %x61-7A / DIGIT / "_" )
value    = 1*( ALPHA / DIGIT / "." / "-" / "_" / "~" / "%" )
```

The link is printable ASCII (bytes 0x21 to 0x7E) and at most **213 bytes**, the most the Board's QR encoder holds. The scheme and host are lowercase and compared exactly. Keys may come in any order after `v`, and none may appear twice.

| Key | Required | Value |
|---|---|---|
| `v` | yes, and first | The contract version, the text `1` |
| `e` | yes | The laptop's endpoint ID: 64 lowercase hexadecimal characters |
| `f` | yes | The fleet ID the code pairs with: `f` and 12 lowercase hexadecimal characters |
| `c` | yes | The one-time code: 10 characters of `0123456789ABCDEFGHJKMNPQRSTVWXYZ`, uppercase, no hyphen |
| `r` | no | The laptop's relay URL, percent-encoded as below. Absent when the laptop uses the public relays of the iroh project, which the app finds through its own defaults |

Any other key is ignored, so that a later version can add one.

A canonical link is `v`, `e`, `f`, `c`, then `r`: `handboard://c#v=1&e=ID&f=FLEET&c=CODE&r=RELAY`. The Board emits only that order. The app accepts any order.

**Why a fragment, and what must not be in the query.** Everything after `#` stays on the phone: an operating system hands a custom-scheme link to the app whole, and no browser or server that a link might pass through reads it. The code is a credential and must be in the fragment. A link that has a `?` before its `#` is rejected, so that nothing secret is ever put where a log or a preview could keep it, and a link with no `#` is rejected even if its query carries every field.

**The relay URL.** It is `https://`, a lowercase DNS host (an optional final dot is allowed), an optional port from 1 to 65535 with no leading zero, and an optional single `/`. A host whose last label is a number, decimal or `0x` hexadecimal, is read as an IPv4 address by URL parsers, not as a DNS host, and is rejected, so no IP address is ever in a link. There is no user information, path, query or fragment. It is percent-encoded byte by byte: `A-Z a-z 0-9 - . _ ~` stay as they are and every other byte becomes `%` and two uppercase hexadecimal digits. A decoder rejects a lowercase escape, a truncated escape, and an escape of a byte that needed none, so that each URL has one spelling. Example: `https://relay.example.test:7842/` is `https%3A%2F%2Frelay.example.test%3A7842%2F`. The longest relay the 213 bytes allow is 97 encoded characters.

### How the app checks it

The app trims surrounding whitespace that a scanner or a paste adds, and then parses the exact text. It applies these checks in this order and stops at the first that fails. The reason is a stable name that the vectors use and the app may log (never the link).

| # | Reason | The link is rejected when |
|---|---|---|
| 1 | `too_long` | It is over 213 bytes |
| 2 | `characters` | A byte is outside 0x21 to 0x7E, which includes a space, a line break and any non-ASCII character |
| 3 | `scheme` | It does not start with `handboard://` |
| 4 | `no_fragment` | It has no `#` |
| 5 | `query` | The text before the first `#` has a `?` |
| 6 | `target` | The text before the first `#` is not exactly `handboard://c`: another host, a path, a port, user information |
| 7 | `version_missing` | The fragment does not start with `v=` |
| 8 | `version_unsupported` | The value of `v` is not exactly `1` |
| 9 | `fragment` | Any pair is not `key=value`, a key or a value breaks the grammar above (including a second `#`, an empty value, a key in capitals, a relay with a raw `:` or `/`) |
| 10 | `duplicate_key` | A key appears twice |
| 11 | `missing_key` | `e`, `f` or `c` is absent |
| 12 | `endpoint_id` | `e` is not 64 lowercase hexadecimal characters |
| 13 | `fleet` | `f` is not `f` and 12 lowercase hexadecimal characters |
| 14 | `code` | `c` is not 10 characters of the alphabet above |
| 15 | `relay` | `r` is present and is not a relay URL in its one spelling |

Check 8 comes before every check of a field, so that a later version may change anything after `v=` and an old app still says "needs a newer app" and not "invalid". An app also rejects, as `endpoint_id`, an ID its iroh library will not turn into a public key: the reference parser checks only the shape.

The app shows the user the first four and last four characters of the laptop's ID and asks for confirmation before it dials: any other app may open a custom-scheme link, and a link must not pair the phone with a stranger's laptop on one tap. A link that arrives while a pairing is open replaces nothing until the user confirms.

### What the app opens

After the user confirms, the app dials, starts its [loopback proxy](#the-loopback-proxy), and loads this in its web view:

```
http://localhost:PORT/<fleet id>/pair#<code>
```

with `PORT` the proxy's port. **The host is the name `localhost`, never an IP address.** On a real phone, an Android web view refused cleartext HTTP to `http://127.0.0.1:PORT`, and loaded the same page from `http://localhost:PORT` once the app's network security config allowed cleartext to `localhost`.

The fragment keeps the code out of the request: the Board's pair page reads it with a script, puts it in the form's code field, and removes it from the address bar. The user then types a name for the phone and taps the button. So the web view must have JavaScript on, and the app must not submit the form for the user.

### The loopback proxy

- It listens on `127.0.0.1` and `[::1]`, never on any other address, because the name `localhost` resolves to either one.
- Its port is chosen once at install from 49152 to 65535 and kept, so that the page's origin, and the drafts the Board keeps for it, survive a restart. If the port is taken at launch, the app picks and keeps another one.
- It adds no header and removes none. The Board drops every proxy header itself, and reads the identity from the connection.
- Any app on the phone can reach the port while the proxy runs. It gets what an unpaired phone gets, the pair page, unless it also holds the web view's cookie. The proxy therefore must not be on the LAN, and the app must use the cookie only as the rules for the app allow.

## The open link

An alert can open an item in the app. The link is `handboard://open/<fleet id>/<page>[#<report ref>]`, and Hand puts it in `HAND_NOTIFY_APP_URL` for the `notify` command ([Alerts that open the app](phone.md#alerts-that-open-the-app)) and prints it as `app` with `hand open --print`. `<page>` is the Board's own path for the item.

| Item | Link |
|---|---|
| task | `handboard://open/f0123456789ab/task/t3` |
| decision | `handboard://open/f0123456789ab/decision/d3` |
| attempt | `handboard://open/f0123456789ab/attempt/a7` |
| report | `handboard://open/f0123456789ab/task/t3#r5` |
| supervisor, fleet page | `handboard://open/f0123456789ab/` |

**Grammar.** The scheme `handboard` and the host `open`, so it never collides with the pairing link, whose host is `c`. Then the fleet, which matches `^f[0-9a-f]{12}$`; then `/` and nothing, `task/tN`, `decision/dN` or `attempt/aN`, with N one to nine digits and no leading zero; then optionally `#rN`, only after `task/tN`, with the same rule for N. Everything is lowercase ASCII. No query, userinfo, port, percent escape or trailing slash is valid. Any other text is not an open link, and the parser gives no reason: it rejects.

**The laptop.** The desktop window takes the same link as `hand-desktop --open LINK`, and the Linux alert's **Open** runs it. `go test ./internal/desktop` runs its parser over the same vectors, and a link with a `#rN` opens the report `rN` there.

**What it holds.** No token, no pairing code and no endpoint ID, because an alert service reads it and the app has one laptop per install. It names the fleet and an item, nothing more.

**What the app does.** The app must treat it as a request to show one read page through the proxy it already has, never as a pairing. It must not clear cookies, change the laptop, replace a pending pairing or pair a phone. With a laptop paired, it dials, sets up the loopback proxy and loads `http://localhost:PORT/<fleet id>/<page>` (with `#rN` kept as the fragment). With no laptop it shows "Pair this phone with your laptop first." and drops the link. When the laptop is unreachable it shows the unreachable screen, and tapping the alert again retries. A phone that is not paired for the fleet gets the Board's own refusal. A hostile link from another app can open one read page of an already paired laptop, and a change on the Board needs a POST with the form token, so the link grants nothing.

**Vectors.** [`companion-vectors.json`](companion-vectors.json) holds 6 valid and 61 invalid open links under the key `open`. Valid entries carry the parsed `fleet`, `page`, `report` and the `path` to open, and every one is a link Hand emits (`canonical`); an emitter must reproduce its `link` from `fleet`, `page` and `report`. Invalid entries carry only the `link`; they are the app's main reject table, and the reference parser's anchored fleet check also rejects the app's extra right-length fleet cases, which only `internal/companion/companion_test.go` (the reference parser test) holds, not the vectors file. `go test ./internal/companion` runs the reference parser over both lists, and `go test ./internal/cli` checks that Hand's emitter reproduces every valid link. The app runs its parser over them as written.

**Versions.** A new link with a new host needs no new version: an old app has no filter for the host `open`, so it never receives the link. The vectors are new entries, and no earlier vector changes.

## What the Board does with a paired phone

### Today

- **Where it arrives.** The sidecar's streams arrive at the [peer listener](phone.md#the-peer-listener) (`hand board --peer-addr 127.0.0.1:7778`), where every request counts as remote and proxy headers are dropped.
- **Before pairing.** The connection may ask only for `/<fleet id>/pair` and `/static/…`. It is closed after 32 requests or 64 KiB without a paired phone's cookie, after a request body that takes over 10 seconds, and after 30 seconds with no request, paired or not. Each stream is one such connection, so a page that opens many streams starts each one capped.
- **Pairing.** The pair page takes the code and a name of up to 40 characters. A code is 10 characters from the alphabet above, works once for 5 minutes, and a new code cancels the one before it. A wrong code counts against the phone's endpoint ID: 5 wrong codes in a minute block that ID for a minute (the page answers 429), and 10 wrong codes from anyone cancel every code. A key costs nothing to make, so the second limit is the one that stops a guesser. The laptop shows the code only to its own login. The pair page's own texts, which the web view shows as they are, are in [Errors](#errors-and-what-the-user-should-see).
- **The device.** A good code creates a device whose login is `iroh:<endpoint id>`, the name the user typed, and a hash of a secret. The page sets the cookie `hand_phone_<fleet id>`: `HttpOnly`, `SameSite=Lax`, path `/`, 90 days. It has neither `Secure` nor the `__Host-` prefix, because the Board dropped `X-Forwarded-Proto` and the origin is plain HTTP. Then it redirects to `/<fleet id>/`.
- **The binding.** The cookie works only on a connection whose peer is the one in the device's login. From any other endpoint ID it is refused with a 403 that says so, so a stolen cookie opens nothing without the phone's key, and the key opens nothing without the cookie. Cookies are scoped to a host, not a port: every port of `localhost` in one web view sends the same cookie.
- **After pairing.** The whole Board, with the supervisor's controls, except `/<fleet id>/proof`. A request that carries a cookie that no longer proves puts the connection's caps back.
- **The app link.** The Phone page's **Pair the Hand app** shows a `handboard://c#…` link, as a QR code and as text, for this laptop's endpoint ID, its relay when it is not a public one, the fleet whose page shows it, and a new code under the rules above. The link is at most 213 bytes. The page names the device that paired ("paired: NAME") and says a leaked QR is worth revoking after use.
- **Revoking.** On the Phone page, **Revoke** deletes the device and **Revoke everywhere** deletes the phone's devices in every fleet. The cookie fails at once and the page answers 403 "This phone is no longer paired with this fleet". That endpoint ID's live connections are closed, and the laptop then admits that ID only to the pair page until it pairs again. The ID itself is not blocked.
- **What does not exist.** The sidecar does not know who is paired, so it cannot keep connection slots for a paired phone. A key that is not paired may still dial and reaches only the pair page.

### Step 3

- The sidecar may be told which IDs are paired, to keep connection slots for them. When that exists, a connection to a key that is not paired may close with a new code. The app already treats an unassigned code as a lost connection with the message above.

## The alert feed

**Today**, from schema 22. The app reads the alerts the watcher raises from the Board, over the connection it already holds, instead of from a push service. The route is on the fleet's prefix, so a phone reads only the fleets whose `hand_phone_<fleet id>` cookie the web view holds. [`companion-vectors.json`](companion-vectors.json) holds the numbers, the link grammar and five exchanges under the key `alerts`; `go test ./internal/board` runs every exchange against the Board's handler, and `go test ./internal/companion` reads the numbers back out of the code.

### The request

```
GET /<fleet id>/alerts/feed?after=<seq>&wait=<seconds>
```

The app sends it as any other request through the sidecar: `Host: localhost`, the cookie `hand_phone_<fleet id>`, and `Connection: close`, so the answer ends at the end of its body. The answer always has a `Content-Length`, never chunked encoding, so the app reads the headers, then that many bytes, then stops. It is a GET: nothing in it is a form, so it carries no CSRF token.

- `after` is the `cursor` of the last answer the app acted on. Leave it out on a first call.
- `wait` is how many seconds the Board may hold the request when nothing is new. The Board holds it at most 25 seconds, whatever the app asks, because the sidecar resets a stream that moves no byte for 60 seconds ([Caps the app must respect](#caps-the-app-must-respect)). A missing, negative or unreadable `wait` is 0: the answer is immediate. A held request takes one of the connection's 12 streams for as long as it is held, so an app that polls several fleets holds one stream for each. The Board checks for new alerts once a second, so an alert reaches a held request within about a second.

### The answer

`200`, `application/json; charset=utf-8`, `Cache-Control: no-store`, always under 64 KiB:

```json
{"version":1,"fleet":{"id":"f0123456789ab","name":"hand"},"cursor":57,"more":false,
 "alerts":[{"seq":57,"at":"2026-10-22T12:00:00Z","kind":"decision.asked","ref":"d3",
  "text":"t3 decision: Keep the old cookie name?","link":"handboard://open/f0123456789ab/decision/d3"}]}
```

| Field | Meaning |
|---|---|
| `version` | `1` for this contract |
| `fleet` | the fleet's `id` and `name` |
| `cursor` | the number to send as `after` next: the last `seq` in `alerts`, or `after` itself when there are none |
| `more` | `true` when more than 50 alerts were waiting: ask again at once, without `wait` |
| `alerts` | at most 50, oldest first, each with `seq` (a number that only grows and is never reused), `at` (UTC, to the second), `kind`, `ref`, `text` and `link` |

- **No `after`, or one the Board has not reached.** `cursor` is the latest `seq` and `alerts` is empty, at once, without holding. A first call therefore never replays history, and a database that was recreated (its latest `seq` below the app's cursor) tells the app to start again from the answer's `cursor`.
- **Which alerts.** Those of the last 24 hours that come after `after`. The Board keeps 7 days of alerts and answers only the last 24 hours, so a phone that was off for two days is not buried, and a cursor older than the window simply skips what has aged out.
- **Empty.** An answer with `alerts: []` at the end of the wait is the heartbeat. Send the next request at once.
- **`kind`.** One of the kinds in [Alerts on your phone](alerts.md), as `kinds` in the vectors lists. The set can grow inside this version: treat a kind you do not know as an ordinary alert, never as an error.
- **`ref`.** `tN`, `dN`, `aN` or `supervisor`.
- **`text`.** What `notify` gets as `HAND_NOTIFY_TEXT`, with control characters replaced by spaces and cut to 200 characters. It is plain text; show it as text. It can hold a line from a worker's screen, as the ntfy recipe already sends.
- **`link`.** A `handboard://open/<fleet id>/<page>` link for the app's own open-link entry point: `<page>` is `task/tN`, `decision/dN`, `attempt/aN`, or empty for the fleet's root (`supervisor` alerts). The numbers have 1 to 9 digits and no leading zero. The app must check the link with its own parser, and that its fleet is the one it polled, before it opens anything; a link that fails the check is no link, and the alert is still shown. `link` is empty when Hand cannot place the ref. The `open` vectors are the grammar. The link holds no secret.
- **Nothing else.** No token, cookie, pairing code, endpoint ID or path is in any field.

### Receipts

The Board records two times per alert, for the reliability trial: `fetched_at`, when the alert is written to an answer, and `acked_at`, when the next request arrives with `after` at or above its `seq`. That next request is the acknowledgment: the app sends `after` only after it has posted its notifications, which makes delivery at-least-once, and a repeat is harmless when the app tags a notification by `<fleet id>/<ref>`. The receipts are per fleet, not per phone. Each request also counts as the phone being seen on the Phone page. The Board lists the week, with the delay from raised to acknowledged, at `/<fleet id>/alerts`.

### Refusals

The route answers as every other Board page does. An unpaired phone, a cookie of another fleet and a cookie of another endpoint ID get `403`. A phone revoked while its request is held gets `403` with the `X-Hand-Error` of ["no longer paired"](#errors-and-what-the-user-should-see) at the next check, within about a second, and the app stops polling that fleet. A `429` or a `5xx`, a reset with code 3, 4 or 5, or no answer within 35 seconds is a failed poll: the app backs off as it does for any lost connection and tries again.

## Errors and what the user should see

The app's own screens say the left column. A page the Board serves appears in the web view as it is, and the app does not read or rewrite it.

| Case | How the app knows | What the user sees |
|---|---|---|
| The link is rejected | A reason from the table above | For `version_unsupported`: "This code needs a newer Hand app. Update the app." For every other reason: "This is not a Hand pairing code. Show a new one on your laptop." The app never shows the reason code to the user, the link or the code |
| The camera is refused | The OS | "Allow the camera to scan the code, or paste the link." |
| The laptop does not answer | No handshake within the app's 20-second deadline | "Can't reach your laptop. Check that it is on, that Hand's Board is running, and that this phone has internet." |
| The laptop is full or refuses | Code 1, or a refusal | "Your laptop is busy. Trying again." |
| The Board is not running | Code 3 twice in a row | "Your laptop answers, but the Board is not running. Start it on the laptop." |
| The connection is lost | Any other close | "Reconnecting…", and after three tries "Can't reach your laptop." |
| A wrong code | The pair page's 403 | "That code is not right. Check it against the one on your laptop, or show a new code there." |
| An expired code | The pair page's 403 | "That code has expired. A code works for 5 minutes. Show a new code on your laptop." |
| A used code | The pair page's 403 | "That code was already used. A code works once. Show a new code on your laptop." |
| Too many wrong codes (all cancelled) | The pair page's 403 | "Too many wrong codes. Every code was cancelled. Show a new code on your laptop." |
| Too many wrong codes from this phone | The pair page's 429 | "Too many wrong codes from this phone. Wait a minute, then try again." |
| The phone is still paired in another fleet, and this fleet's pairing was removed on the laptop | The Board's 403 "no longer paired", with `X-Hand-Error` | The page, and the app adds two actions: **Pair again** and **Forget this laptop** |
| The phone's only paired fleet was removed on the laptop | The Board's 403 "Pair this phone first", with no `X-Hand-Error` | The page, which says how to pair again. The app adds no actions |
| Too much asked before pairing | The Board's 429 | The page; the web view reconnects, which opens a new stream |

The pair page's texts on the right are the Board's; the app may show them in its own words but must keep their meaning, and must not add the code.

## Rules for the app

- **Never log the code, the link or a cookie**, in the app's logs, crash reports, analytics, clipboard, notifications, intents it sends, or the URL it passes to an error screen. The reason code of a rejected link is the most a log may hold.
- **The app never holds the sidecar's secret.** It is in `board.peer` on the laptop and the app is never given it. The device's secret is in an `HttpOnly` cookie that the web view sends. The app reads only the `hand_phone_<fleet id>` cookie, and only to send it on that fleet's own `/<fleet id>/alerts/feed` request. It never stores the cookie elsewhere, never logs it, and never sends it to any other path or fleet. It must not read, copy or export any other cookie of the web view.
- **Block screenshots, recents thumbnails and screen recording of the pairing screen and of the pair page** where the OS can (on Android, `FLAG_SECURE` on that screen), because the code is on it in clear.
- **Keep the app's iroh key in the keystore.** Make 32 random bytes on first run and keep them encrypted under a non-exportable key of the Android Keystore (hardware-backed when the phone has it), in storage private to the app and excluded from backups and device-to-device transfer, so that a copy of the phone does not copy the identity. A reinstall makes a new key, which the Board treats as a new phone.
- **One laptop per install.** Cookies are scoped to the host `localhost`, not a port, so a second laptop's Board would be sent the first one's cookies. Pairing a second laptop first clears the web view's cookies and storage and forgets the first.
- **Load only the proxy's origin** in the web view. Every other address opens in the system browser. Do not enable file access, content access, or a JavaScript bridge to the web view.
- **Cleartext only to `localhost`.** The network security config allows cleartext to that name and to nothing else.
- **Pace itself.** Follow the caps and the backoff above.
- **Prefer the scanner to the link.** Another app can register the scheme `handboard`, and a link opened from outside the app cannot prove where it came from. The code is worth little after 5 minutes or one use, and the confirmation step above covers the rest.

## Test vectors

[`companion-vectors.json`](companion-vectors.json) holds 13 valid and 73 invalid links, each with the result a conforming parser gives: the fields and the path to open, or the reason. It also holds the numbers of the wire and of the Board's pairing rules, under the keys `wire` and `board`. `go test ./internal/companion` runs a reference parser against the file, and reads the numbers back out of `iroh/src/` and `internal/board/`, so a change of code and a stale file cannot both pass.

The app copies the file into its own tests and runs its parser over every vector as written: trimming whitespace is the caller's job and happens before the parser, so a vector with a leading space is rejected as `characters`. A valid vector whose `canonical` is true is also what a Board emits: an emitter must reproduce its `link` from its `parsed` fields. The reference parser checks shape only: an ID of 64 hexadecimal characters is valid in it even where the app's iroh library refuses it as a key.

## Versions

The contract has one whole-number version, in the link as `v` and on the wire as the ALPN: version 1 is `v=1` and `hand-board`. The link, the ALPN and the rules on this page change together.

- **A change that needs no new version.** A new optional link key (old apps ignore it), a new route such as the alert feed, a new close or reset code (an old app treats an unassigned code as a lost connection), a new vector, a clearer sentence. Add them here and in the vectors file in one change. A vector is never changed or removed inside a version.
- **A change that needs a new version.** Any change to the meaning, spelling or limits of a key that exists, to the code's alphabet or length, to what a stream carries, or to what the app must do for the link to work. It becomes version 2 with the ALPN `hand-board/2`, a vectors file with `"contract": 2`, and a new section here.
- **Old apps keep working.** The sidecar serves the ALPNs of every version a Hand release lists as supported, and a version is dropped only in a release whose notes say so. The Board emits links of the lowest version still supported, so an old app can still pair. An app accepts every link version up to the one it was built for.
- **An old app meets a newer link.** It rejects it as `version_unsupported` and tells the user to update, because check 8 runs before any other field is read.

## iOS is deferred

There is no iOS app and none is planned until the Android app has been used on a real phone. Nothing on this page is a promise about iOS. Open facts about it are untested: whether `WKWebView` keeps an `HttpOnly` cookie set from `http://localhost`, what App Transport Security allows for `localhost`, how long a backgrounded app keeps its sockets, and how an iOS app claims a link. The link grammar and the wire do not depend on any of them.
