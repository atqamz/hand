# Reach the board from a phone without Tailscale

The board listens on `127.0.0.1:7777`, so a phone cannot open it. The shortest way in is `tailscale serve`: see [Pair a phone](board.md#pair-a-phone). It gives an HTTPS address that only your tailnet reaches, and Hand finds the address by itself. This page is for a phone and a laptop that do not use Tailscale. It covers four paths, what each costs and what each exposes.

Hand sets up none of them and changes no setting. The Phone page, `/<fleet id>/connect`, only takes the address your phone opens and turns it into a pairing link and QR code. Never publish the board with `tailscale funnel`, a quick tunnel or any proxy that has no access control of its own: see [Safe use](safe-use.md).

| Path | Chat, files and supervisor control | HTTPS | Pairing and revoke | Alert links open on the phone | Setup | Who can reach the board |
|---|---|---|---|---|---|---|
| [`tailscale serve`](board.md#pair-a-phone) | yes | yes | yes | yes, by itself | Admin console once, one command, the app | Your tailnet's devices, as its access rules allow |
| [SSH tunnel](#ssh-tunnel) | yes | no: plain HTTP on the phone's own loopback | yes | only while the tunnel is up | An SSH server, an SSH app | Whoever can log in over SSH |
| [Cloudflare Tunnel with Access](#cloudflare-tunnel-with-access) | yes | yes | yes | after you rewrite the link | A domain on Cloudflare, `cloudflared`, an Access policy | Whoever the Access policy admits. Cloudflare can read the traffic |
| [WireGuard, behind a TLS proxy](#wireguard) | yes | yes, with a private CA on the phone | yes | after you rewrite the link | WireGuard, a proxy, a CA | Every WireGuard peer |
| [WireGuard, read-only](#wireguard) | no, decisions only | no: plain HTTP inside the tunnel | yes, read-only | yes | WireGuard | Every WireGuard peer |
| [A LAN address](#a-lan-address) | no, decisions only | no: plain HTTP | yes, read-only, over plain HTTP | only with a specific address, not `0.0.0.0` | None | Everyone on the network, who can also sniff the token |

A path that reaches the board over HTTPS through a proxy that sends `X-Forwarded-Proto: https` and `X-Forwarded-For` (`tailscale serve`, a Cloudflare Tunnel, a TLS proxy in front of WireGuard) lets the phone install the Board as an app and shows an offline page when the laptop does not answer: see [Install the Board as an app](board.md#install-the-board-as-an-app). An SSH tunnel, a LAN address and WireGuard over plain HTTP do not get it, because their pages are not secure and carry no proxy header.

"Chat, files and supervisor control" are the composer, attachments and the supervisor's Start, Resume, Stop, model and key controls. The board offers them only while it listens on a loopback address. On any other address they are off, and answering decisions and marking reports read still work.

## What to type in the Phone page

Open `/<fleet id>/connect` on the laptop, type the address the phone opens in **Address the phone opens**, and choose **Show pairing code**. The address is `http://` or `https://`, a host, an optional port, and no path: the page refuses `https://example.test/board`. The pairing link is `ADDRESS/<fleet id>/pair#CODE`, and the QR code holds it. The page warns when the address is plain HTTP on a network, because the code and the phone's session then cross it unencrypted unless the tunnel encrypts them. It gives no warning for `localhost` or a loopback address such as `127.0.0.1`. Revoke and Sign out work the same on every path.

The phone's session is a cookie per fleet it is paired with, `hand_phone_<fleet id>`: one code can pair several fleets ([Several fleets on one phone](board.md#several-fleets-on-one-phone)), and **Revoke everywhere** on the laptop's Phone page signs the phone out of every fleet that browser is signed in to ([Revoke a phone everywhere](board.md#revoke-a-phone-everywhere)). When the request carries `X-Forwarded-Proto: https`, the cookie is `__Host-hand_phone_<fleet id>` and has `Secure`.

## Alerts that open on the phone

Tapping an alert opens the item in the phone's browser with the phone's session, because the phone's cookie is `SameSite=Lax`: a browser sends it when a link from another app opens a page, where `Strict` would be withheld. Lax allows only that page load and withholds the cookie on a form posted from another site, and the form token every change needs, the tailnet login check and the 90-day life of the session still protect the board. A phone paired before this change keeps its `Strict` cookie until it is paired again.

`HAND_NOTIFY_URL` is `http://127.0.0.1:7777/<fleet id>/...`, from the address the board wrote to `board.addr`. When the board listens on `0.0.0.0`, that address is still `127.0.0.1`. Only `tailscale serve` changes it by itself. For a path with its own HTTPS name, rewrite the link in the command in `routing.json` ([Alerts on your phone](alerts.md)). With POSIX `sh`, and `https://board.example.com` as the address the phone opens:

```json
"notify": ["sh", "-c", "exec curl -s -H \"Title: $HAND_NOTIFY_REF\" -H \"Click: https://board.example.com${HAND_NOTIFY_URL#http://127.0.0.1:7777}\" --data-raw \"$HAND_NOTIFY_TEXT\" https://ntfy.sh/CHOOSE-A-LONG-RANDOM-TOPIC"]
```

The recipe needs `sh`, so it is for Linux and macOS. On Windows it is not given here, and the link stays the local one. `${HAND_NOTIFY_URL#http://127.0.0.1:7777}` removes the local prefix and keeps `/<fleet id>/decision/d3`. A board on another port needs that port in the prefix.

## The peer listener

**What it is.** `hand board --peer-addr 127.0.0.1:7778` opens a second loopback listener that serves the same board. A raw forward to the board's own address (`ssh -L`, Wispers, `dumbpipe`) arrives from `127.0.0.1` with whatever headers the phone sent, so the board takes it for the laptop: it lists every fleet at `/`, prints errors in full and shares one pairing rate limit. Point such a forward at the peer listener instead, and the board counts every request as remote, like one behind `tailscale serve`, whatever headers it carries.

**What a peer reaches.**

- Before pairing, only `/<fleet id>/pair` and the files it loads. A connection that asks for more than 32 requests or 64 KiB before it shows a paired phone's cookie is closed, and so is one whose request body stalls for 10 seconds. A connection with no request for 30 seconds is closed, paired or not.
- After pairing, the whole board, with supervisor controls, as a paired phone behind `tailscale serve` does, except `/<fleet id>/proof`, which `hand open` uses on the board's own address. A connection that sends a revoked phone's cookie is capped again. Neither a fleet's token nor its laptop cookie opens anything on this listener, whichever fleet a phone's cookie names, so a tunnel that holds only a token cannot use the board.
- A request that Tailscale marks as Funnel traffic is refused, as on the board's own address.

**What a peer cannot do.** It cannot make the board believe a proxy header: `X-Forwarded-*`, `Forwarded` and `Tailscale-*` are dropped, so a phone cannot claim another login, mark itself HTTPS or pick its own rate-limit key. A connection with no [preface](vocabulary.md#peer-preface) shares one rate limit with every other such connection (five wrong codes make a minute's wait for all of them), and the pairing code's ten-wrong-tries cancel stays global.

**Identity.** A connection that opens with the [peer preface](vocabulary.md#peer-preface) and the secret in `board.peer` has an identity, `iroh:<peer ID>`. A phone paired over it is bound to that ID: its cookie works only from the same ID, and a stolen cookie or a stolen ID alone opens nothing. A device paired over a connection with no preface carries no login, and its cookie works from any connection. The board never reads an `iroh:` login from the `Tailscale-User-Login` header, so no header can claim an identity. Only a program that can read `board.peer` (mode 0600, in the operator's `~/.secondhand`) can claim an identity; the board closes any other connection that claims one.

**What it does not protect against.** Any process of your own user can read `board.peer`, the fleet's `board.token` and `hand.db`, so it can do anything a peer can. On a machine with other users, any of them can connect to the loopback port; without the secret they are anonymous remote, and still need a pairing code that shows only on your laptop. The listener takes loopback addresses only, and only beside a loopback `--addr`, because the preface carries the secret in the clear. A board that `hand open` or `hand update` restarts starts on the default address without it, as it does without any other flag but `--update-every`: put `--peer-addr` in the unit's `ExecStart=`, the plist or the shortcut, as [Update on a timer](update.md#update-on-a-timer) shows for `--update-every`.

## The hand-iroh sidecar

**What it is.** [`hand-iroh`](vocabulary.md#hand-iroh) is a program that lets a phone reach the [peer listener](#the-peer-listener) over [iroh](https://docs.iroh.computer/): the phone dials the laptop's endpoint ID, through a public relay or directly, and the sidecar turns each stream it opens into one connection to the peer listener, with the [peer preface](vocabulary.md#peer-preface) in front. It is a separate Rust program in `iroh/`, so `hand` stays a static Go binary with no new Go dependency.

**Nothing in Hand starts it yet.** The board does not run it, `hand doctor` has no row for it, `install.sh`, `install.ps1` and `hand update` do not fetch it, and no page pairs a phone over it. Today it is for trying the peer listener by hand.

**Where it comes from.** A release cut after 0.13.0 carries one asset per system, each with a `.sha256` file that holds its checksum. 0.13.0 and earlier carry none.

| System | Asset | Build |
|---|---|---|
| Linux amd64, arm64 | `hand-iroh-linux-amd64.tar.gz`, `hand-iroh-linux-arm64.tar.gz` | static, musl |
| macOS Apple silicon, Intel | `hand-iroh-darwin-arm64.tar.gz`, `hand-iroh-darwin-amd64.tar.gz` | |
| Windows amd64 | `hand-iroh-windows-amd64.zip` | MSVC |

The `Iroh` workflow (`.github/workflows/iroh.yaml`) builds them, and the `Release` workflow attaches them to the draft release with their attestations; a failed attestation fails the run after the upload, and running `Release` again overwrites the assets. A change under `iroh/`, or to `internal/board/peer.go` (the preface grammar is written on both sides), runs `cargo fmt --check`, `cargo clippy -D warnings` and `cargo test` on Linux, macOS and Windows, and `cargo test` again for the three targets those do not cover: Linux musl on amd64 and arm64, and macOS Intel (under Rosetta on an Apple silicon runner). It builds all five assets without publishing, and starts each built binary: it must print its ready line, keep its ID across two runs and exit when its input closes. The forwarding tests run in the debug profile; the packed release binary (thin LTO, `panic=abort`, stripped) runs only that start, key and exit check. The `Release` build starts from a clean build folder: it restores no cache. The rolling `edge` release does not carry the sidecar. To build it yourself, run `cargo build --release --locked` in `iroh/`, with the Rust toolchain that `iroh/rust-toolchain.toml` pins.

**Running it by hand.** It exits when its standard input closes, so that the board can own its life; from a shell, keep the input open:

```sh
tail -f /dev/null | hand-iroh --board 127.0.0.1:7778 --secret-file ~/.secondhand/board.peer --key-dir ~/.secondhand/iroh
```

It prints one line, `ready id=ENDPOINTID relay=URL`, once it is online (it waits up to 10 seconds; `relay=none` when it is not online by then, or when it runs without a relay). The ID is the address a phone dials. It writes nothing else to standard output, and its errors go to standard error. It stops cleanly on `SIGTERM`, Ctrl-C or when its input closes, and `hand-iroh --version` prints `hand-iroh VERSION iroh VERSION`. Changing `board.peer` takes a restart of the sidecar as well as of the board.

| Flag | Meaning |
|---|---|
| `--board ADDR` | The peer listener, a loopback IP address and port. Anything else is refused, because the secret crosses it in the clear. |
| `--secret-file PATH` | The file that holds the secret, `board.peer` in `~/.secondhand`: 16 to 128 printable characters without spaces. There is no flag or environment variable for the secret itself. |
| `--key-dir DIR` | Where the endpoint's key lives: `hand-iroh.key`, 32 bytes, created on the first run, with mode 0600 in a folder made with mode 0700 on Linux and macOS. On Linux and macOS the sidecar refuses a key file that group or others can access (`chmod 600`) and a folder that they can write (`chmod go-w`). The folder must be on a filesystem that supports hard links, which the first run uses so that two first runs agree on one key: not FAT or exFAT, and not every network share. A leftover `hand-iroh.key.*.tmp` older than a minute, from a crash in the first run, is deleted at the next start. Its ID stays the same across runs. Delete the file to get a new ID. |
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

## SSH tunnel

**What it is.** The phone is an SSH client. It forwards a port on the phone to the board's port on the laptop, and the phone's browser opens `http://127.0.0.1:7777` on the phone itself. The board stays on loopback, so chat, files and supervisor control work.

**Set up once.**

1. Run an SSH server on the laptop that the phone can reach: on your home network, or from outside through a router port forward or another VPN. A port forward puts the SSH port on the internet: allow keys only.
2. Install an SSH app on the phone that can forward a local port, and give it a key.
3. Open the tunnel from the phone, here as the OpenSSH command line (the app's setting has the same three parts, a local port, the host `127.0.0.1` and the port `7777`):

   ```sh
   ssh -N -L 7777:127.0.0.1:7777 -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 USER@LAPTOP
   ```

   `-L` forwards the phone's port 7777 to `127.0.0.1:7777` as the laptop sees it. `-N` runs no remote command. `ExitOnForwardFailure=yes` ends the session when the phone cannot bind the port. `ServerAliveInterval=30` sends a keep-alive every 30 seconds; the default is none.

**On the Phone page.** Type `http://127.0.0.1:7777`, the forward's port on the phone, then show the code. The QR opens only while the tunnel is up, so start the tunnel first.

**Cost and what goes wrong.**

- The tunnel lives as long as the SSH app does. When the phone suspends or kills the app, the tunnel is gone, and a page that is open shows "Connection lost" ([the board's connection band](board.md)). How long a given app keeps the tunnel up in the background was not verified here.
- The alert link is `http://127.0.0.1:7777/...`. It opens on the phone while the tunnel is up with the same port, and fails otherwise.
- Not HTTPS. Browsers treat a loopback address as a secure context ([MDN](https://developer.mozilla.org/en-US/docs/Web/Security/Secure_Contexts), [W3C](https://w3c.github.io/webappsec-secure-contexts/#is-origin-trustworthy)), so the Clipboard API is available there, subject to the browser's own rules, such as permission or a user action. The board falls back to `execCommand` when a write fails. That is the specification: no phone browser was tried, and Safari on an iPhone is not verified here. A forward that a phone browser reaches by another name, such as the phone's LAN address, is not loopback and is not a secure context.
- SSH encrypts the traffic between the phone and the laptop, so plain HTTP on the phone is not sniffable on the network.

**Security.** The board stays on loopback and opens no new port. The SSH server is the new door: its login is the first fence, and the board's token or a pairing is the second. Anyone who can log in to that account over SSH can also forward to the board, which the token still guards.

## Cloudflare Tunnel with Access

**What it is.** `cloudflared` on the laptop keeps an outbound connection to Cloudflare, and Cloudflare serves a hostname on your domain that reaches `http://localhost:7777` through it. Cloudflare Access sits in front and asks the visitor to log in before anything reaches the board. Chat, files and supervisor control work, because the board sees a loopback connection.

**The Access policy is mandatory.** Without it, anyone on the internet who finds the hostname reaches the board's login page and its unauthenticated routes, and one token guards agents that run with their permission prompts skipped. Cloudflare says so: if no Access application is in place, "the published application will be available to anyone on the Internet", and "All Access applications are deny by default -- a user must match an Allow policy before they are granted access" ([Cloudflare](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/self-hosted-public-app/)). So make the Access application first, then publish the hostname.

**Set up once.** You need a domain on Cloudflare ([Cloudflare](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/get-started/create-remote-tunnel/)). The steps are in the Cloudflare dashboard, so there are no commands to copy here.

1. **Access application first.** Zero Trust, Access controls, Applications, **Create new application**, **Self-hosted and private**, **Add public hostname**. Pick the domain and the subdomain you will use, for example `board.example.com`. Under **Access policies**, add an Allow policy that admits only you, with the identity providers you use, then **Create**. The policy rules are on [Cloudflare's policy page](https://developers.cloudflare.com/cloudflare-one/access-controls/policies/policy-management/), which was not read here.
2. **Tunnel.** Networking, Tunnels, **Create a tunnel**, a name, **Create Tunnel**. Pick your operating system, copy the install command the dashboard shows, and run it on the laptop. Wait until the tunnel connects. Whether that command keeps `cloudflared` running after a reboot is not stated on the page read here: check that the tunnel is `Healthy` after one. The laptop must reach Cloudflare on port 7844 outbound.
3. **Route.** Open the tunnel, **Routes**, **Add route**, **Published application**. Enter the same subdomain and domain. For **Service URL**, enter `http://localhost:7777`, then **Add route**.
4. **Check it from outside.** In a browser that is not signed in to Access, open `https://board.example.com/`. It must show Access's login, never the board's pages. Do not go on until it does.

**On the Phone page.** Type `https://board.example.com`, with no path.

**What the board sees.** `cloudflared` connects to the board from the laptop, so the board sees loopback. Cloudflare adds `X-Forwarded-For` and `X-Forwarded-Proto` ([Cloudflare](https://developers.cloudflare.com/fundamentals/reference/http-headers/)), and Access adds a `Cf-Access-Jwt-Assertion` header ([Cloudflare](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/)). They were not captured at the board here. The board then:

- shows no fleet list at `/` to a request with no valid phone session, because it carries `X-Forwarded-For`; a paired phone sees only the fleets it is paired with ([Several fleets on one phone](board.md#several-fleets-on-one-phone));
- sets the `__Host-` and `Secure` phone cookie when `X-Forwarded-Proto` is `https`;
- uses no `Cf-` header and does not check the Access token. Any process on the laptop can send those headers to `127.0.0.1`, so the board never lets them in. The pairing code, the phone's session or the fleet's token is still needed behind Access.

The board refuses Funnel traffic by its `Tailscale-Funnel-Request` header. It has no such check for Cloudflare: it cannot tell a Cloudflare request from a local one, so the Access policy is the only fence in front of the board's own login.

**Cost and what goes wrong.**

- A domain, a Cloudflare account, and a service to keep running.
- Cloudflare serves the hostname, so its edge holds the TLS keys and can read the chat, decisions and files. Not verified here against Cloudflare's documentation.
- **Never use a quick tunnel.** Cloudflare says that quick tunnels have no uptime guarantee and "do not support Server-Sent Events (SSE)" ([Cloudflare](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/)), which the board uses for its live updates. The only access control that page offers is an emailed one-time PIN (`--allowed-mail`), not an Access policy.
- Whether a named tunnel passes the board's event stream without buffering is not verified here. If it does not, the page shows "Connection lost".
- The alert link needs the rewrite above, with `https://board.example.com`.

**Security.** The hostname is public: scanners find it. The Access policy decides who gets past it, and it is the first fence. Behind it, the pairing code or the token is all that stands between a person Access admits and typing into agents that skip their permission prompts. Keep it to your own identity, check it from outside after every change, and read [Safe use](safe-use.md).

## WireGuard

**What it is.** The laptop and the phone are two peers of a WireGuard network. The phone reaches the laptop by its WireGuard address, here `10.8.0.1`. Your choice is how the board listens on that address.

- **Behind a TLS proxy.** The board stays on loopback, and a proxy on the laptop listens on the WireGuard address with TLS and forwards to `http://127.0.0.1:7777`. The phone keeps chat, files and supervisor control, and gets HTTPS, if it trusts the proxy's certificate. You build and run the proxy and a private certificate authority, and install its root on the phone. Pick this only if you already run such a proxy.
- **Read-only.** The board listens on the WireGuard address itself with `--addr`. That is a network address, so the board turns the supervisor controls off: the login token, or a phone's pairing code and session cookie, crosses plain HTTP there, and supervisor control means typing into an agent that has its permission prompts skipped. WireGuard encrypts the hop, but the board cannot know that, so the rule stays. Decisions and read marks still work.

**Set up once.** The laptop needs a WireGuard endpoint the phone can reach: a UDP port forward on your router, or a server with a public address in between. Neither was run here.

1. On the laptop and on the phone, make a key pair ([WireGuard quick start](https://www.wireguard.com/quickstart/)):

   ```sh
   umask 077
   wg genkey | tee privatekey | wg pubkey > publickey
   ```

2. Write the laptop's `/etc/wireguard/wg0.conf` and the phone's configuration with `Address`, `PrivateKey`, the laptop's `ListenPort`, and for each side a `[Peer]` with the other's `PublicKey`, `AllowedIPs` and, on the phone, the laptop's `Endpoint`. A `PersistentKeepalive` of 25 seconds on the phone is the interval the quick start calls sensible behind NAT. `wg-quick up wg0` brings the interface up ([wg-quick(8)](https://man7.org/linux/man-pages/man8/wg-quick.8.html)). The exact file is in the quick start and the manual page, and was not run here.
3. On the phone, install the official WireGuard app ([Android](https://play.google.com/store/apps/details?id=com.wireguard.android), [iOS](https://itunes.apple.com/us/app/wireguard/id1441195209?ls=1&mt=8), from [wireguard.com/install](https://www.wireguard.com/install/)) and add the tunnel. How the app takes the configuration is not verified here.
4. Start the board for the mode you chose:
   - **Read-only:** `hand board --addr 10.8.0.1:7777`. The WireGuard address must already exist when it starts: with no such address, the board exits with `listen tcp 10.8.0.1:7777: bind: cannot assign requested address`. Never use `0.0.0.0` for this, which also opens the board to every other network the laptop is on. The autostart unit starts `hand board --addr 127.0.0.1:7777`; change `--addr` in it ([Autostart](autostart.md)), and start the unit after the WireGuard interface.
   - **TLS proxy:** keep `127.0.0.1:7777`. Configure the proxy to listen on `10.8.0.1:443` only, to forward to `http://127.0.0.1:7777`, to send `X-Forwarded-For` and `X-Forwarded-Proto: https`, and to pass the event stream through without buffering. A proxy that buffers responses stalls the live updates. How to turn buffering off depends on the proxy and is not covered here. Without `X-Forwarded-For`, `/` lists the fleet names to every peer.

**On the Phone page.**

- **Read-only:** `http://10.8.0.1:7777`. The page shows its plain-HTTP warning, which WireGuard answers.
- **TLS proxy:** `https://NAME`, the name the certificate covers.

**Cost and what goes wrong.**

- Of the four paths, this one has the most to run. Read-only is the short version, and gives up the composer, attachments and supervisor control.
- Read-only is plain HTTP, not HTTPS. The copy buttons use the Clipboard API where the browser allows it, and fall back to `execCommand`.
- In read-only mode the alert link is already `http://10.8.0.1:7777/...`: it is the address the board wrote to `board.addr`. It opens on the phone while WireGuard is up. With a TLS proxy it needs the rewrite above.
- Every peer can open the board's login page. Do not add a peer you do not trust.

**Security.** Every WireGuard peer reaches the board's port. The token or a pairing still guards every page, and in read-only mode a session can answer decisions, which steer the supervisor. A proxy that sends `X-Forwarded-For` hides the fleet list from a request that holds no valid phone session. Do not bind `0.0.0.0`.

## A LAN address

**What it is.** The board listens on a network address, and a phone on the same network opens it directly.

**Set up once.** Start the board with `--addr`:

```sh
hand board --addr 192.168.1.20:7777
```

Use the laptop's address on that network. `0.0.0.0:7777` also works and listens on every interface: Docker, a VPN and a public WiFi included. A DHCP address changes, so the phone's link breaks when it does. The board prints this warning: "plain HTTP on a network address: anyone who can see this traffic can take a fleet's token; prefer a loopback board behind ssh -L or tailscale serve". Make it permanent by changing `--addr` in the autostart unit ([Autostart](autostart.md)).

**On the Phone page.** Type `http://192.168.1.20:7777`. The page warns that the address is plain HTTP on a network.

**What it is for.** Reading Needs, reports and logs from the sofa at home, and answering a decision. It is read-only by design: the composer is off, and the page says "Read-only: this board listens on a network address. Message the supervisor from a board on loopback." A paired phone is read-only too.

**Cost and what goes wrong.**

- Plain HTTP. Anyone on the network who can see the traffic can take the token, the pairing code or a phone's session cookie, and the document viewer reads the fleet folder's files with the token. A decision they answer goes to the supervisor. On a shared or public network, do not use this path.
- `/` shows no fleet list on a network address. The alert link is `http://127.0.0.1:7777/...` when you listen on `0.0.0.0`, and the LAN address only when you name it.
- Not HTTPS. The copy buttons use the Clipboard API where the browser allows it, and fall back to `execCommand`.

**Security.** Everyone on the network reaches the board's port. Use it at home on a network you trust, or not at all.
