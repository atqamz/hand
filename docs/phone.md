# Reach the board from a phone

The board listens on `127.0.0.1:7777`, so a phone cannot open it. The way in is the Hand app: an Android app that dials your laptop over [iroh](https://docs.iroh.computer/), with no address to type, no account and no public name. The app, the [peer listener](#the-peer-listener) and the [`hand-iroh` sidecar](#the-hand-iroh-sidecar) are all of it, and the app's contract is [The Android Companion contract](companion.md). Hand's iOS Companion comes later.

Any other phone, browser or second computer needs a tunnel you set up yourself: see [Other paths](#other-paths). Hand sets up none of them and changes no setting. Never publish the board with a quick tunnel, Tailscale Funnel or any proxy that has no access control of its own: see [Safe use](safe-use.md).

**Pair the app.** Start the board with `--iroh` ([Turn it on](#the-hand-iroh-sidecar)), open the Phone page, `/<fleet id>/connect`, on the laptop and choose **Pair the Hand app**. Scan the QR code in the app or open the link on the phone. The code lasts 5 minutes and works once. **Revoke** and **Revoke everywhere** on the Phone page sign the phone out and close its live connections ([Revoke a phone everywhere](board.md#revoke-a-phone-everywhere)).

**A phone you paired through Tailscale.** Hand no longer runs `tailscale`, looks for `tailscale serve` or trusts the `Tailscale-User-Login` header, so such a phone cannot sign in again. Its row stays on the Phone page until its 90 days end. Revoke it there, then pair the app.

## Alerts that open the app

Hand never sends an alert itself: the `notify` command in `routing.json` does ([Alerts on your phone](alerts.md)). Hand puts two links in its environment:

- `HAND_NOTIFY_APP_URL` is `handboard://open/<fleet id>/<page>`, the link the Hand app opens ([The open link](companion.md#the-open-link)): `task/t3`, `decision/d3`, `attempt/a7`, `task/t3#r5` for a report, or the fleet page. It holds no token, no code and no endpoint ID, so it is safe to send through a service such as ntfy.sh, and it is set whether or not the board runs with `--iroh`.
- `HAND_NOTIFY_URL` is the page on the laptop, `http://127.0.0.1:7777/<fleet id>/...`, from the address the board wrote to `board.addr`. It is empty when the board listens on a network address. A phone cannot open it.

With ntfy, put the app link in the `Click` header and fall back to the laptop's page: `Click: ${HAND_NOTIFY_APP_URL:-$HAND_NOTIFY_URL}`, as the command in [Alerts on your phone](alerts.md) does. Tapping the alert opens the item in the app, which is already paired. If the app cannot reach the laptop, it says so; tap the alert again once the laptop is back. `hand open --print REF` prints the same link as `app`, beside the `url` that holds the fleet's token.

ntfy documents a custom scheme in `Click` as "the responsible app may open". If your phone does not open the app from the tap, send the link as a button instead: `-H "Actions: view, Open in Hand, $HAND_NOTIFY_APP_URL"`.

## The peer listener

**What it is.** `hand board --peer-addr 127.0.0.1:7778` opens a second loopback listener that serves the same board. A raw forward to the board's own address (`ssh -L`, Wispers, `dumbpipe`) arrives from `127.0.0.1` with whatever headers the phone sent, so the board takes it for the laptop: it lists every fleet at `/`, prints errors in full and shares one pairing rate limit. Point such a forward at the peer listener instead, and the board counts every request as remote, whatever headers it carries.

**What a peer reaches.**

- Before pairing, only `/<fleet id>/pair` and the files it loads. A connection that asks for more than 32 requests or 64 KiB before it shows a paired phone's cookie is closed, and so is one whose request body stalls for 10 seconds. A connection with no request for 30 seconds is closed, paired or not.
- After pairing, the whole board, with supervisor controls, except `/<fleet id>/proof`, which `hand open` uses on the board's own address. A connection that sends a revoked phone's cookie is capped again. Neither a fleet's token nor its laptop cookie opens anything on this listener, whichever fleet a phone's cookie names, so a tunnel that holds only a token cannot use the board.
- A request that carries `Tailscale-Funnel-Request`, the header Tailscale adds to Funnel traffic, is refused, as on the board's own address.

A phone's own **Sign out this phone** writes the signed-out page and flushes it before the board closes its connections, so the app reads the acknowledgement instead of a reset.

**What a peer cannot do.** It cannot make the board believe a proxy header: `X-Forwarded-*`, `Forwarded` and `Tailscale-*` are dropped, so a phone cannot claim a login, mark itself HTTPS or pick its own rate-limit key. A connection with no [preface](vocabulary.md#peer-preface) shares one rate limit with every other such connection (five wrong codes make a minute's wait for all of them), and the pairing code's ten-wrong-tries cancel stays global.

**Identity.** A connection that opens with the [peer preface](vocabulary.md#peer-preface) and the secret in `board.peer` has an identity, `iroh:<peer ID>`. A phone paired over it is bound to that ID: its cookie works only from the same ID, and a stolen cookie or a stolen ID alone opens nothing. A device paired over a connection with no preface carries no login, and its cookie works from any connection. The board reads no identity from any header, `Tailscale-User-Login` included, so no header can claim one. Only a program that can read `board.peer` (mode 0600, in the operator's `~/.secondhand`) can claim an identity; the board closes any other connection that claims one.

**What it does not protect against.** Any process of your own user can read `board.peer`, the fleet's `board.token` and `hand.db`, so it can do anything a peer can. On a machine with other users, any of them can connect to the loopback port; without the secret they are anonymous remote, and still need a pairing code that shows only on your laptop. The listener takes loopback addresses only, and only beside a loopback `--addr`, because the preface carries the secret in the clear. A board that `hand open` or `hand update` restarts starts on the default address without it, as it does without any other flag but `--update-every`: put `--peer-addr` in the unit's `ExecStart=`, the plist or the shortcut, as [Update on a timer](update.md#update-on-a-timer) shows for `--update-every`.

## The hand-iroh sidecar

**What it is.** [`hand-iroh`](vocabulary.md#hand-iroh) is a program that lets a phone reach the [peer listener](#the-peer-listener) over [iroh](https://docs.iroh.computer/): the phone dials the laptop's endpoint ID, through a public relay or directly, and the sidecar turns each stream it opens into one connection to the peer listener, with the [peer preface](vocabulary.md#peer-preface) in front. It is a separate Rust program in `iroh/`, so `hand` stays a static Go binary with no new Go dependency. The phone side is [Hand's Android Companion](vocabulary.md#hands-android-companion), an app for Android; Hand's iOS Companion comes later, and nothing here targets iOS.

**What works today, and what does not.** The Board can start the sidecar, find it, check its version and show its state, and the installers and `hand update` fetch it. A phone pairs through it from the Phone page's **Pair the Hand app** link, and Revoke closes that phone's live iroh connections. The sidecar still does not know who is paired, so it cannot keep connection slots for a paired phone; a stranger who dials the endpoint ID reaches only the pairing page, under the peer listener's limits.

What an app may rely on when it dials the sidecar (the protocol name, the streams, the codes, the caps) and the link it reads to pair are fixed in [The Android Companion contract](companion.md).

**Turn it on.** Start the board with `--iroh`:

```sh
hand board --iroh
```

The board opens the peer listener on a free loopback port (`--peer-addr` picks the address instead), starts `hand-iroh` as its child, and passes it the peer listener's address, the path of `board.peer` and the key folder `~/.secondhand/iroh`. The secret never travels in an argument or in the environment, and the child gets an empty environment: no proxy variable reaches it, and `HOME`, `PATH` and the rest do not either (on Windows it also gets `SystemRoot`, which Go adds for DLL loading). It needs a loopback `--addr`.

The setting is the board's own, like `--update-every`: `board.iroh` in `~/.secondhand` records it, so a board that `hand open` or `hand update` restarts keeps it, and the last board started wins: a board started without `--iroh` clears it. A board that a systemd unit, a LaunchAgent or a shortcut starts needs `--iroh` in its own command, as [Update on a timer](update.md#update-on-a-timer) shows for `--update-every`.

**Where Hand looks for it.** Next to `hand` (the folder of the running binary, symlinks followed), then on `PATH`. Its version must be the one this `hand` pins, which `hand version` prints as `iroh:`; there is one pin, in `internal/sidecar`, and a test keeps it equal to the crate's version. A missing or different sidecar is not a reason to keep the board down, so the board starts anyway and shows the sidecar as failed, with the reason.

**What it shows.**

- The Phone page, in its Network section on the laptop (a paired phone never sees it): `starting`, `ready` with the endpoint ID, `no relay` when the sidecar is up but no relay answered within 10 seconds, or `failed` with the reason. The endpoint ID is an identifier, not a secret: the iroh project's public DNS publishes the endpoint's relay address under its ID, so anyone can look it up, and a stranger who knows it may dial the laptop. The pairing code and the peer listener's limits are what keep a stranger out, so treat a pairing code as the secret and the ID as an address.
- `hand doctor`, in its `iroh` row, with the installed version and path in the `iroh` field and the endpoint ID in `iroh_id` while the sidecar is ready. The row is `info` while the sidecar is off or the board is not running, `ok` while it is ready, and `warn` for a recorded `--iroh` with no sidecar installed, a different version, a failed sidecar or no relay. It never fails the checks.
- The board's output: a line `board: iroh: ready, endpoint ID …` on its standard error (in `board.log` for a board that `hand open` started), and one for each restart and for a failure.
- `board.iroh` in `~/.secondhand`: the setting and the last state on one line, `starting`, `ready ID RELAY`, `failed REASON` or `stopped`. Only a running board keeps it true: `hand doctor` reads it only while a board runs.

**How to find the endpoint ID.** Any of the three: the Phone page, `hand doctor` (`iroh_id`), or the `ready` line above. It is 64 hexadecimal characters, and it stays the same across restarts, because the sidecar keeps its key in `~/.secondhand/iroh/hand-iroh.key`. Deleting that file gives a new ID. The key stays in `~/.secondhand/iroh/` and does not move into a fleet folder: one board and one sidecar serve every fleet on the machine, so the endpoint ID belongs to the machine, and a key per fleet would give a paired phone another ID, or another sidecar, depending on which fleet's folder is mounted.

**Restarts and failures.** The board owns the sidecar's life: it keeps the sidecar's standard input open, closes it when the board stops, and restarts a sidecar that exits after 1 second, then 2, 4 and 8 seconds. A run that lasts a minute resets the count. After 5 exits in a row that each came within a minute, it stops trying and shows `failed` with `gave up after 5 quick exits` and the last line the sidecar wrote to its standard error (cut to 200 characters, control characters removed). Restart the board to try again; a sidecar the board cannot find, or whose version differs, is not retried at all. A sidecar that neither exits nor prints its `ready` line within 30 seconds is killed and counted as an exit. A crashed, silent or hostile sidecar cannot stop the board: everything it prints is read through a bounded buffer, only the first `ready` line counts, and the last line of a failure or restart shows on the Phone page escaped, with paths cut to file names.

| The page or `hand doctor` says | What happened | What to do |
|---|---|---|
| `hand-iroh not found: it is not next to hand (…) or on PATH` | No sidecar was installed, or it is not in either place | Run `hand update`, or install a release that carries it, or put `hand-iroh` next to `hand` |
| `hand-iroh X is Y, but this hand needs X` | The sidecar and `hand` come from different releases | Run `hand update`, or install `hand-iroh` of the release `hand` came from |
| `restarting after: exit status N: …` | The sidecar exited and Hand is waiting to start it again | Read the reason; it stops by itself after 5 quick exits |
| `gave up after 5 quick exits: …` | Every start ended within a minute | Fix what the reason names (a key folder that others can write, a secret file that is unreadable), then `hand board stop` and `hand open` |
| `no relay` | The sidecar is up, but no relay answered within 10 seconds, so a phone elsewhere cannot dial it | Check that the machine reaches the internet, then restart the board; the sidecar does not look again by itself |

**Windows.** Windows has no `SIGTERM`, so Hand never signals the sidecar. It closes the pipe it holds as the sidecar's standard input, which ends the sidecar, and the same pipe closes when the board is killed, as `hand board stop` and `hand update` do there with `TerminateProcess`: a sidecar cannot outlive its board on any system. A sidecar that does not leave within 5 seconds of its input closing is killed.

**Where it comes from.** A release cut after 0.13.0 carries one asset per system, each with a `.sha256` file that holds its checksum. 0.13.0 and earlier carry none. `install.sh`, `install.ps1` and `hand update` fetch it from the same release as `hand`, as `hand update` fetches Luvus: the asset and its `.sha256`, checked before anything is installed, and the same folder as `hand`, replaced atomically. If the asset, its checksum or its version is wrong (the version must be the one the new `hand` reports as `iroh:`), they install nothing and keep the `hand` and the sidecar you have. If the release carries no sidecar (0.13.0 and earlier, and `edge`), they say so and go on, and `hand doctor` shows `not installed`. A `hand` that reports no `iroh:` line never asks for one. `hand update` fetches it together with a new `hand`, and an update that finds `hand` already current leaves the hand alone but still installs the sidecar when it is missing or is not the version that hand needs. An update run by 0.13.0 installs the new `hand` without a sidecar, because that updater does not know about it; the new hand's next `hand update`, or `install.sh`/`install.ps1`, places it.

**Running it by hand.** It exits when its standard input closes, so that the board can own its life (the board does this for you with `--iroh`); from a shell, keep the input open:

```sh
tail -f /dev/null | hand-iroh --board 127.0.0.1:7778 --secret-file ~/.secondhand/board.peer --key-dir ~/.secondhand/iroh
```

It prints one line, `ready id=ENDPOINTID relay=URL`, once it is online (it waits up to 10 seconds; `relay=none` when it is not online by then, or when it runs without a relay). The ID is the address a phone dials. It writes nothing else to standard output, and its errors go to standard error. It stops cleanly on `SIGTERM`, Ctrl-C or when its input closes, and `hand-iroh --version` prints `hand-iroh VERSION iroh VERSION`. Changing `board.peer` takes a restart of the sidecar as well as of the board.

| Flag | Meaning |
|---|---|
| `--board ADDR` | The peer listener, a loopback IP address and port. Anything else is refused, because the secret crosses it in the clear. |
| `--secret-file PATH` | The file that holds the secret, `board.peer` in `~/.secondhand`: 16 to 128 printable characters without spaces. There is no flag or environment variable for the secret itself. |
| `--key-dir DIR` | Where the endpoint's key lives: `hand-iroh.key`, 32 bytes, created on the first run, with mode 0600 in a folder made with mode 0700 on Linux and macOS. On Linux and macOS the sidecar refuses a key file that group or others can access (`chmod 600`) and a folder that they can write (`chmod go-w`). The first run creates the key exclusively, so two first runs agree on the one that wins, and it needs no hard links. The mode checks above still need a filesystem that reports POSIX modes. A leftover `hand-iroh.key.*.tmp` older than a minute, from a crash in an earlier first run, is deleted at the next start, and an incomplete `hand-iroh.key` is replaced at the next start: the start waits a second for a concurrent writer to finish and then makes a new key. Its ID stays the same across runs after that. Only a file shorter than 32 bytes is replaced; a longer one is kept and refused. Two starts that both find the same incomplete key at the same moment can each make a key, and the one that wrote last stays on disk, so the other run's ID is not kept; this needs a crash and an overlap of about 10 ms, and the next start settles on the key on disk. Delete the file to get a new ID. |
| `--relay default\|none\|URL` | `default` uses the public relays the iroh project runs and publishes the endpoint's address to its DNS. A URL uses that relay instead of the public ones and publishes nowhere: no DNS record, no lookup, so a dialer needs the relay URL, which the ready line prints. `none` is for tests and trials on one machine: no relay, no DNS, no port mapping, and the sockets are bound to `127.0.0.1` only, so a phone cannot reach it. |
| `--max-connections N` | Connections served at once, 1 to 1024. Default 16. Past that, dialers are refused before the handshake until one leaves. Handshakes in flight are counted apart, below. |
| `--idle SECONDS` | 1 to 86400. Default 60. A stream that moves no byte for this long is reset, and a connection on which no byte has moved on any stream for this long is closed: opening or ending a stream moves no byte. |

**What it decides, and what it does not.** It does not know who is paired. iroh admits any dialer that completes the handshake, and endpoint IDs are not secret, so every dialer reaches the peer listener, which serves an unpaired peer only `/<fleet id>/pair` and the files it loads, under its own byte, request and idle limits. The sidecar's limits bound what it spends. They do not tell a stranger from a paired phone. iroh 1.4.1 has no limiter of its own (no per-address or per-handshake cap, no rate limit), so these are the sidecar's:

- **Handshakes.** A dialer whose address has not been proved gets a retry packet and costs the sidecar no state, so a spoofed address ends there; a real dialer pays one more round trip (about 27 ms measured on loopback). Then at most 8 handshakes run at once, each given up after 3 seconds, and at most 4 per source: a direct dialer's IPv4 address or IPv6 /64, or a relayed dialer's endpoint ID. A handshake holds no connection slot.
- **Connections.** After the handshake a connection takes one of the `--max-connections` slots and counts against its source's 4. It closes when no byte has moved on any stream for `--idle` seconds.
- **Streams.** At most 12 open at once per connection, and new ones accepted at 20 a second after a burst of 12, so one dialer cannot spend the Board's loopback accept rate. Each stream is one loopback connection to the board, and one descriptor: with the defaults that is at most 16 by 12 and about 11 for the process, 203 measured, under the 256 that a macOS shell allows. If you raise `--max-connections`, raise `ulimit -n` by 12 for each. A sidecar out of descriptors resets the stream that cannot reach the board and stays up.
- **Codes.** A connection that completes its handshake with no slot free is closed with code 1, and one with no byte for `--idle` with code 2. A stream is reset with code 3 when the board cannot be reached, 4 when no byte moved for `--idle` seconds and 5 when the board's connection broke. A board that answers and closes at once still gets its answer to the phone; the phone's upload is stopped with code 5.

What this does not defend is availability. A stranger who moves one byte on every connection each `--idle` seconds, from enough addresses (direct) or made-up endpoint IDs (through a relay, where a new ID costs nothing), holds every slot, and a paired phone is refused until one frees. Behind one NAT address a stranger shares a source, and so its 4 connections, with your phone. The real fix is for the board to tell the sidecar which IDs are paired, so that slots are kept for them; that is a later step.

**Known limits.** On Windows the sidecar neither sets nor checks permissions on the key file or its folder, so choose a folder only you can open. It handles Ctrl-C, not a console close or a shutdown, and has no `SIGTERM`: closing its input stops it on every system. The build pulls in `paste` 1.0.15 through iroh's `netwatch`: RUSTSEC-2024-0436, "unmaintained", informational. An OSV query of `iroh/Cargo.lock` on 2026-11-05 found no vulnerability advisory on the other crates.


## Other paths

These need setup that Hand does not do. A phone that uses them opens the Board in its browser and pairs on the Board's pair page; the Phone page's **Address the phone opens** field takes the address and turns it into a pairing link and QR code. It is `http://` or `https://`, a host, an optional port and no path. The page warns when the address is plain HTTP on a network.

| Path | Chat, files and supervisor control | HTTPS | Alert links | Who can reach the board |
|---|---|---|---|---|
| [SSH tunnel](#ssh-tunnel) | yes | no: plain HTTP on the phone's own loopback | `http://127.0.0.1:7777/...` while the tunnel is up | Whoever can log in over SSH |
| [Cloudflare Tunnel with Access](#cloudflare-tunnel-with-access) | yes | yes | after you rewrite the link | Whoever the Access policy admits. Cloudflare can read the traffic |
| [WireGuard, behind a TLS proxy](#wireguard-and-a-lan-address) | yes | yes, with a private CA on the phone | after you rewrite the link | Every WireGuard peer |
| [WireGuard or a LAN address, read-only](#wireguard-and-a-lan-address) | no, decisions only | no: plain HTTP | none: the board listens on a network address | Every peer, or everyone on the network, who can also read the token |

"Chat, files and supervisor control" are the composer, attachments and the supervisor's Start, Resume, Stop, model and key controls. The board offers them only while it listens on a loopback address. On any other address they are off, and answering decisions and marking reports read still work.

The phone's session is a cookie per fleet it is paired with, `hand_phone_<fleet id>`; behind a proxy that sends `X-Forwarded-Proto: https` it is `__Host-hand_phone_<fleet id>` and has `Secure`. A proxy that sends `X-Forwarded-For` hides the fleet list at `/` from a request that holds no valid phone session. To open an alert link through a path with its own HTTPS name, rewrite the link in the `notify` command. With POSIX `sh`, and `https://board.example.com` as the address the phone opens:

```json
"notify": ["sh", "-c", "exec curl -s -H \"Title: $HAND_NOTIFY_REF\" -H \"Click: https://board.example.com${HAND_NOTIFY_URL#http://127.0.0.1:7777}\" --data-raw \"$HAND_NOTIFY_TEXT\" https://ntfy.sh/CHOOSE-A-LONG-RANDOM-TOPIC"]
```

The recipe needs `sh`, so it is for Linux and macOS. A board on another port needs that port in the prefix.

### SSH tunnel

The phone is an SSH client that forwards a port on the phone to the board's port on the laptop, and the phone's browser opens `http://127.0.0.1:7777` on the phone itself. The board stays on loopback. On the laptop, run an SSH server the phone can reach and allow keys only. On the phone, install an SSH app that forwards a local port and open the tunnel, here as the OpenSSH command line:

```sh
ssh -N -L 7777:127.0.0.1:7777 -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 USER@LAPTOP
```

On the Phone page type `http://127.0.0.1:7777`, with the tunnel already up. The tunnel lives as long as the SSH app does; when the phone suspends the app, an open page shows "Connection lost". SSH encrypts the hop, so plain HTTP on the phone is not sniffable. The SSH server is the new door: its login is the first fence, and the board's token or a pairing is the second. To point a raw forward such as this at the board with the right limits, use the [peer listener](#the-peer-listener).

### Cloudflare Tunnel with Access

`cloudflared` on the laptop keeps an outbound connection to Cloudflare, and Cloudflare serves a hostname on your domain that reaches `http://localhost:7777`. Cloudflare Access sits in front and asks the visitor to log in first.

**The Access policy is mandatory.** Without it, anyone on the internet who finds the hostname reaches the board's login page, and one token guards agents that run with their permission prompts skipped. Cloudflare says so: if no Access application is in place, "the published application will be available to anyone on the Internet" ([Cloudflare](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/)). Make the Access application first, with an Allow policy that admits only you, then publish the hostname. Check it from a browser that is not signed in to Access: it must show Access's login, never the board's pages.

Then create the tunnel and a published-application route to `http://localhost:7777` in the Cloudflare dashboard, and type `https://board.example.com`, with no path, on the Phone page. The board sees a loopback connection with `X-Forwarded-For` and `X-Forwarded-Proto` added. It uses no `Cf-` header and does not check the Access token, because any process on the laptop could send them. The board cannot tell a Cloudflare request from a local one, so the Access policy is the only fence in front of the board's own login.

**Never use a quick tunnel.** Cloudflare says quick tunnels have no uptime guarantee and "do not support Server-Sent Events (SSE)" ([Cloudflare](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/)), which the board uses for live updates, and they offer no Access policy. Cloudflare's edge holds the TLS keys and can read the chat, decisions and files. The hostname is public and scanners find it: keep the policy to your own identity and check it from outside after every change.

### WireGuard and a LAN address

The laptop and the phone are two peers of a WireGuard network, or on one network. There are two ways to listen:

- **Behind a TLS proxy.** The board stays on loopback. A proxy on the laptop listens on the WireGuard address with TLS, forwards to `http://127.0.0.1:7777`, sends `X-Forwarded-For` and `X-Forwarded-Proto: https`, and passes the event stream through without buffering. You run the proxy and a private certificate authority whose root the phone trusts. Type `https://NAME` on the Phone page.
- **Read-only.** Start the board with `--addr` on the WireGuard or LAN address, for example `hand board --addr 10.8.0.1:7777`. That is a network address, so the board turns the supervisor controls off: the token, or a pairing code and a session cookie, crosses plain HTTP, and supervisor control means typing into an agent that skips its permission prompts. WireGuard encrypts the hop, but the board cannot know that. Decisions and read marks still work. The board prints "plain HTTP on a network address: anyone who can see this traffic can take a fleet's token; prefer a loopback board behind ssh -L". Never use `0.0.0.0`, which also opens the board to every other network the laptop is on. Change `--addr` in the autostart unit to make it permanent ([Autostart](autostart.md)), and start it after the WireGuard interface exists.

On a LAN address with no encryption, anyone who can see the traffic can take the token, the pairing code or a phone's session cookie, and the document viewer reads the fleet folder's files with the token. On a shared or public network, do not use it. Every WireGuard peer reaches the board's port: do not add a peer you do not trust.
