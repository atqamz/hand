# Hand's Android Companion

The phone app of Hand: a Tauri 2 shell around a Rust core, Android only. It dials a laptop's `hand-iroh` sidecar over iroh and shows the Board in a web view. Status: roadmap steps 1 and 2 are built (the iroh dialer, the loopback proxy and the web view); pairing, the QR scanner and release signing are not. See the roadmap in `README.md`.

## Rules

- Smallest correct change. Delete before adding. Follow what the surrounding code does.
- No code comments. Names, types and tests carry what the code does. Keep only lines a tool reads, such as linter directives.
- No dependency, abstraction, compatibility layer or configuration without a concrete need. The pull request that adds a dependency says why the standard library or an existing crate does not do. Pin Rust dependencies exactly (`=x.y.z`) and commit `Cargo.lock`.
- Tests carry behaviour. Docs carry only what code cannot say: why, measurements, external constraints, failure modes, manual steps.
- Before committing, run `cargo fmt --all --check`, `cargo clippy --locked --workspace --all-targets --target aarch64-linux-android -- -D warnings` and `cargo test --locked --workspace` with `HAND_IROH_BIN` set to a sidecar built from the pinned Hand commit, as `README.md` says. A change to `src-tauri/` also builds the debug APK (`README.md`).
- Commits are small, imperative, lowercase-first, without a trailing period. Never bypass hooks or signing.

## Layout

- `core/`: `hand-android-core`, plain Rust with no Tauri in it. The iroh dialer, the loopback proxy and the pairing logic go here, with tests that run on the host. Its sidecar tests need `HAND_IROH_BIN`; they skip without it.
- `src-tauri/`: the Tauri shell. It registers commands that call the core and holds no logic of its own. Its only target is Android, so `cargo clippy` for it runs with `--target aarch64-linux-android`: a host build would need the desktop web view libraries.
- `web/`: the bundled first screen. Static files, no build step, no npm.
- `src-tauri/gen/android/`: made once by `cargo tauri android init`, committed and edited by hand since. Running `init` again overwrites our edits to `AndroidManifest.xml`, `app/build.gradle.kts` and `res/xml/network_security_config.xml`; redo them from the Android rules below.
- Capabilities: the IPC capability, if any is ever added, names the bundled page only, never the Board's origin.

## Android

- Debug-signed APKs only for now. No release signing key, keystore, `keystore.properties` or signing secret exists in the repository, in CI or in a pull request; `.gitignore` keeps `*.jks`, `*.keystore` and `keystore.properties` out, and CI fails when the APK's signer is not the Android debug key.
- Cleartext: the web view opens the Board at `http://localhost:PORT`, by the name `localhost`, never at `http://127.0.0.1:PORT` or `http://[::1]:PORT`: a real phone refused the IP forms. `res/xml/network_security_config.xml` forbids cleartext everywhere and permits it for the host `localhost` without subdomains. Do not set `usesCleartextTraffic`, add `cleartextTrafficPermitted="true"` elsewhere or add another domain.
- iOS is deferred. Add nothing for it.
- Pin the toolchain in one place each: Rust in `rust-toolchain.toml`, `tauri-cli` in `.github/workflows/ci.yaml` and `README.md` (move them together with the `tauri` and `tauri-build` crates), Gradle, the Android Gradle plugin and the AndroidX libraries in `src-tauri/gen/android`.

## Contract with Hand

Hand is the Board on the laptop. This app is the dialer of its `hand-iroh` sidecar, so every fact below is Hand's and changes there first. The contract is `docs/companion.md` with its vectors in `docs/companion-vectors.json`, in https://github.com/atqamz/hand. The app is built against the head of Hand's PR #1135, `f7c6e1c5c9da9c8d3735c2754fce68df3845ebff`; a newer head wins. `docs/phone.md` describes the peer listener and the sidecar. When one of them disagrees with this file, they win; fix this file.

- Transport: iroh, pinned to the sidecar's version (`iroh = "=1.4.1"` there), ALPN `hand-board`. The app dials the laptop's endpoint ID, through a relay or directly. Endpoint IDs are not secrets and iroh admits any dialer that completes the handshake; the Board decides who is paired.
- Streams: one bidirectional QUIC stream per loopback TCP connection, and bidirectional only: the sidecar allows no unidirectional stream. At most 12 streams are open on a connection, and new ones are accepted at 20 a second after a burst of 12. Finishing the send side is the upload's end of file, and the Board's end of file finishes the stream.
- The dialer sends HTTP bytes and nothing else. It never writes the peer preface (`HAND-PEER 1 PEERID SECRET`): the sidecar writes it to the Board, with the secret the phone never holds and the phone's endpoint ID.
- Idle and limits: the sidecar resets a stream that moves no byte for 60 seconds by default and closes a connection on which no stream moved a byte for as long. A connection is closed with code 1 when the sidecar is full and 2 when idle; a stream is reset with code 3 when the Board is unreachable, 4 when idle and 5 when the Board's connection broke. The app reconnects on resume and after code 2, and never retries code 1 in a tight loop.
- Identity: the phone's iroh key is its identity. The Board binds a paired device to `iroh:<endpoint ID>`, so the key stays out of logs and backups. An unpaired key reaches only `/<fleet id>/pair` and the files that page loads, under small byte and request limits.
- Pairing link: it carries the laptop's endpoint ID, a relay URL, the fleet ID and a one-time code, in the URL fragment so that no server sees it (`handboard://c#v1.…` in the design). The contract document is the only source of its grammar. Never log, persist or send the fragment, and never put it in a crash report.
