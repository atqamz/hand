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

"Chat, files and supervisor control" are the composer, attachments and the supervisor's Start, Resume, Stop, model and key controls. The board offers them only while it listens on a loopback address. On any other address they are off, and answering decisions and marking reports read still work.

## What to type in the Phone page

Open `/<fleet id>/connect` on the laptop, type the address the phone opens in **Address the phone opens**, and choose **Show pairing code**. The address is `http://` or `https://`, a host, an optional port, and no path: the page refuses `https://example.test/board`. The pairing link is `ADDRESS/<fleet id>/pair#CODE`, and the QR code holds it. The page warns when the address is plain HTTP on a network, because the code and the phone's session then cross it unencrypted unless the tunnel encrypts them. It gives no warning for `localhost` or a loopback address such as `127.0.0.1`. Revoke and Sign out work the same on every path.

The phone's session is a cookie, `hand_phone_<fleet id>`. When the request carries `X-Forwarded-Proto: https`, the cookie is `__Host-hand_phone_<fleet id>` and has `Secure`.

## Alerts that open on the phone

`HAND_NOTIFY_URL` is `http://127.0.0.1:7777/<fleet id>/...`, from the address the board wrote to `board.addr`. When the board listens on `0.0.0.0`, that address is still `127.0.0.1`. Only `tailscale serve` changes it by itself. For a path with its own HTTPS name, rewrite the link in the command in `routing.json` ([Alerts on your phone](alerts.md)). With POSIX `sh`, and `https://board.example.com` as the address the phone opens:

```json
"notify": ["sh", "-c", "exec curl -s -H \"Title: $HAND_NOTIFY_REF\" -H \"Click: https://board.example.com${HAND_NOTIFY_URL#http://127.0.0.1:7777}\" --data-raw \"$HAND_NOTIFY_TEXT\" https://ntfy.sh/CHOOSE-A-LONG-RANDOM-TOPIC"]
```

The recipe needs `sh`, so it is for Linux and macOS. On Windows it is not given here, and the link stays the local one. `${HAND_NOTIFY_URL#http://127.0.0.1:7777}` removes the local prefix and keeps `/<fleet id>/decision/d3`. A board on another port needs that port in the prefix.

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
