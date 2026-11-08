# Hand's Android Companion

Reach a Hand Board from an Android phone over iroh.

Status: roadmap steps 1 to 4 are built. The app keeps an iroh key, takes a `handboard://` pairing link from an intent, a paste or a QR scan, asks the user to confirm the laptop, and then shows the Board's pair page through a loopback proxy at `http://localhost:PORT`. The web view loads the bundled page and that proxy origin only; every other address opens in the system browser. Release signing is not built.

## What it is

Hand runs a Board on a laptop. This app keeps its own iroh key, dials the laptop's `hand-iroh` sidecar, and shows the Board in a web view through a loopback proxy. The Board's cookie login, forms and event stream then work unchanged, because the web view sees a normal HTTP origin at `http://localhost:PORT`.

Android only. iOS is deferred and has no code here.

## Layout

- `core/`: `hand-android-core`, plain Rust with no Tauri in it. It owns the key and seals it through a `KeyCipher`, the iroh endpoint, the one connection to a laptop, the loopback proxy, the close and reset handling, the pairing link parser, the pending pairing and the scanner reply mapping. Its tests run on the host.
- `src-tauri/`: the Tauri shell. It starts the core with the app's data directory and the Keystore cipher, takes a link from the Android intent plugin, registers the pairing commands, and keeps the web view on the bundled page and the proxy origin. Its Kotlin plugins hold the Keystore sealing, the intent bridge and the camera scanner. The scanner fills the pair field with the decoded link and sends it through the same `offer_link` command as a paste.
- `web/`: the bundled pairing screen. Static files, no build step, no npm.
- `src-tauri/gen/android/`: the Android project. It was made once with `cargo tauri android init` and is edited by hand; running `init` again overwrites the edits.

## Build a debug APK

Debug-signed only. No release signing key exists in this repository or in CI.

Prerequisites:

- JDK 17 or newer, with `JAVA_HOME` set.
- Android SDK platform 37, build-tools 36.0.0 and the command-line tools.
- NDK 27.3.13750724, with `NDK_HOME` pointing at it.
- Rust 1.98.1, from `rust-toolchain.toml`, with the `aarch64-linux-android` target for a phone and the `x86_64-linux-android` target for an emulator.
- `tauri-cli` 2.12.4.

```sh
rustup target add aarch64-linux-android x86_64-linux-android
cargo install tauri-cli --version 2.12.4 --locked

export JAVA_HOME=/path/to/jdk
export ANDROID_HOME=/path/to/android-sdk
export NDK_HOME="$ANDROID_HOME/ndk/27.3.13750724"

cargo tauri android build --debug --apk --target aarch64
```

The APK lands at `src-tauri/gen/android/app/build/outputs/apk/universal/debug/app-universal-debug.apk`. It is signed with the Android debug key; `apksigner verify --print-certs` reports `CN=Android Debug`. For an emulator, replace `aarch64` with `x86_64` and install the same path with `adb install -r`.

Measured on 2026-11-05 on Linux x86_64 with 22 cores and warm SDK, Gradle and cargo caches: 1m36s for the `aarch64` debug APK with iroh in `target/`, 1m14s for the `x86_64` one, and a 127 MiB APK with debug symbols. The SDK, NDK, JDK, Gradle and Rust caches took about 12 GB.

## Host tests

The unit tests run anywhere. The sidecar tests need a `hand-iroh` binary built from Hand. They start that real sidecar against a fixture Board on loopback; no phone, fleet, Board database or Luvus server is involved, and every key and secret is made by the test.

Build the sidecar at a pinned Hand commit, then run the workspace:

```sh
git clone https://github.com/atqamz/hand.git hand
git -C hand checkout f7c6e1c5c9da9c8d3735c2754fce68df3845ebff
cargo build --locked --manifest-path hand/iroh/Cargo.toml --bin hand-iroh
HAND_IROH_BIN="$PWD/hand/iroh/target/debug/hand-iroh" cargo test --locked --workspace
```

Without `HAND_IROH_BIN` the 13 sidecar tests print a skip and return; CI always sets it. The suite proves a proxied HTTP request and response, half-close in both directions, stream codes 3, 4 and 5, connection codes 1 and 2, the 13th stream waiting, a stream opening only on its first byte, a Board that answers before it closes while the phone still sends, and a wrong secret ending a stream with no byte. It also covers the backoff after a full laptop, the sealed key's lifecycle (one key across runs, concurrent first runs, a missing Keystore key making a new identity, a changed Keystore key and corrupt ciphertext stopping with an error and leaving the file alone, a Keystore error at the probe or at open that is not mistaken for a missing key, a plaintext of the wrong length, an interrupted rotation leaving no blob, ciphertext and folder permissions, and the plugin error codes checked against the Kotlin constants), the port kept across a restart, the relay hint, the pairing link parser over the contract's 13 valid and 73 invalid vectors, the confirmation text, the open URL, the pending link kept against a second one, the iroh key checked before the other fields, the clear-before-forget order of a second laptop, and the Board opened from the saved laptop on a relaunch.

Measured on 2026-11-07 on the same host, debug builds through loopback: 64 tests, 13 of them against the sidecar; slowest time to first byte of three proxied requests 70-76 ms; the first stream after a full laptop connected 1.10-1.21 s after the request, which is the 1-second backoff. The scanner tests cover the reply mapping: a code, a refused permission, a scan that is already open, a camera failure, a cancel and an unknown status; a decoded code is returned untouched and checked once by the same parser as a paste. The camera preview, the permission dialog and the native scanner overlay are device-only; the host tests do not open a camera.

The standard checks are:

```sh
cargo fmt --all --check
cargo test --locked --workspace
node --test src-tauri/tests/external_links.test.mjs
cargo clippy --locked --workspace --all-targets --target aarch64-linux-android -- -D warnings
actionlint .github/workflows/ci.yaml
```

The link wrapper test needs Node.js 20 or newer, and no package.

`cargo clippy` for the Android target needs the NDK's C compiler, as ring compiles C:

```sh
toolchain="$NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin"
export CC_aarch64_linux_android="$toolchain/aarch64-linux-android24-clang"
export AR_aarch64_linux_android="$toolchain/llvm-ar"
export CARGO_TARGET_AARCH64_LINUX_ANDROID_LINKER="$toolchain/aarch64-linux-android24-clang"
```

## Pairing

A link arrives from the `handboard://` intent, from the paste field on the first screen, or from the scanner behind **Scan the QR code**. The scanner asks for the camera once on first use, decodes only QR codes, and hands the text to the same `offer_link` command as a paste; a foreign or invalid code gets the same "check the link" message. If the camera is refused or fails to start, the scanner says so and can be opened again. The app trims surrounding whitespace and applies the contract's 15 checks before it accepts anything. It then shows the first four and last four characters of the laptop's endpoint ID and asks for confirmation. A link that arrives while a pairing is open replaces nothing until the user confirms; the app says `Finish or dismiss the current pairing first` and keeps the first link. The code is never shown.

After confirmation the app dials, checks that the Board answers, and loads `http://localhost:PORT/<fleet>/pair#<code>`. The host is the name `localhost`, never an IP. The user types the phone's name on the Board's page and taps its button; the app never submits the form.

Pairing a second laptop first clears the web view's cookies and storage and forgets the first. The last endpoint ID is kept in `<app data>/laptop`; it is not a secret. On a later launch the app dials it and opens `http://localhost:PORT/`, where the Board's cookie picks the fleet.

The pairing screen and the pair page set `FLAG_SECURE`, so screenshots, recents thumbnails and screen recording do not capture the code.

If the Board answers with its login page, a pairing page, or a refusal such as "Pair this phone first", the web view shows that page unchanged. The app adds no login screen. A 403 whose `X-Hand-Error` says the phone is no longer paired gets two buttons over the page: **Pair again** returns to the pairing screen and **Forget this laptop** clears the web view's cookies and storage and forgets the laptop.

## Key, port and data

- The phone's iroh identity is 32 random bytes made on first run. The app seals them with AES-256-GCM under a non-exportable Android Keystore key (alias `io.github.atqamz.handcompanion.iroh`, hardware-backed when the phone has a TEE or StrongBox, `setUserAuthenticationRequired(false)`) and stores only the IV, ciphertext and tag at `<app data>/iroh/companion.key`, mode 0600 in a folder of mode 0700. The key is never written in the clear, logged, copied or exported; the core owns the 32 bytes and the endpoint, and the Kotlin plugin only asks the Keystore to seal and open bytes. A reinstall removes the file and the Keystore key together, so the Board sees a new phone. If the file survives and the Keystore key is gone or permanently invalidated, the app makes and seals a new identity instead of falling back to raw bytes; a ciphertext that does not authenticate, a plaintext that is not 32 bytes, or a Keystore error at the alias probe or at open that is not a definitively missing key stops with an error on the first screen instead of replacing the identity; the legacy Keystore before Android 12 reports an outage and a missing alias alike, so a startup outage there can still rotate. A rotation unlinks the old blob and syncs the folder before it seals the new one, so an interruption leaves either a blob the current key opens or no blob, never one no key can open; the folder is synced again after the hard link. Tauri's app data directory is private to the app and the manifest sets `android:allowBackup="false"`, so Android's backup does not copy the file; on Android 12 and later some manufacturers still migrate app files device to device, but the Keystore key never leaves the source phone, so a copied blob is unreadable and the app makes a new identity. A pre-PR development install kept a raw 32-byte `companion.key`; the first run under this code cannot authenticate it, unlinks it and makes a new identity, so the Board sees a new phone once and needs a re-pair.
- The Keystore round trip is device-only. CI has no Android Keystore, so the host tests drive the lifecycle with a fake cipher and check the plugin error codes against the Kotlin constants: they do not prove key generation, hardware backing, real AES-GCM sealing and opening, or that a real `KeyStore` probe reports a provider error instead of a missing alias. On a phone, `adb install -r` over the same build must keep the endpoint ID (the identity loads and opens), while `adb uninstall` followed by an install must show a new one (the file and Keystore key are both gone).
- The proxy listens on `127.0.0.1` and, when the phone has it, `[::1]`, because the name `localhost` resolves to either. Its port is chosen once from 49152 to 65535 and kept in `<app data>/proxy.port`; if the port is taken at launch the app picks and keeps another. A fixed port keeps the page's origin and the Board's drafts across a restart.
- The proxy never listens on a network address, adds no headers, and removes none. The web view's cookies stay in the web view.

## Dependencies

- `iroh` 1.4.1 and `tokio` 1.54.1: the dialer and its runtime, pinned to the sidecar's versions.
- `rand` 0.10.3: the proxy port and the backoff jitter; the standard library has no random source.
- `url` 2.5.8: parses and compares the navigation URLs; the standard library has no URL parser, and it is the type Tauri hands the navigation callback.
- `serde` 1.0.229: the shell's command replies and errors and the `Serialize`/`Deserialize` derives on the mobile plug-in payloads; `tauri::generate_context!` expands to `serde` paths, so it must be a direct dependency rather than a transitive one.
- `serde_json` 1.0.154: `tauri::generate_context!` expands to code that names it in the shell; the core's tests also read the contract's vectors with it.
- `socket2` 0.6.5, tests only: sets `SO_LINGER` so the fixture Board can send the reset that becomes code 5.
- `tauri` 2.12.5 and `tauri-build` 2.7.4: the Android shell and its build script, pinned with `tauri-cli` 2.12.4.
- `tauri-plugin-opener` 2.7.2: sends a refused navigation and a clicked `_blank` link or `window.open` call to another origin, or with a `mailto:` or `tel:` scheme, to the Android system browser; neither the standard library nor Tauri starts that intent itself. Its `open_url` command is granted to the bundled page and to `http://localhost:*` by `src-tauri/capabilities/external-links.json`, and its link script is left on.
- `com.google.zxing:core` 3.5.4 and `com.journeyapps:zxing-android-embedded` 4.3.0, Android: the QR decoder and the camera preview that feeds it. They add 463,884 B to the debug APK; the two artifacts are 610,364 and 151,839 B, about 0.76 MB together. They need no Google Play services; the embedded library's own `core` is excluded so the decoder is pinned directly. A hand-written camera loop would be more code for the same result.

`Cargo.lock` holds every transitive version.

## Contract with Hand

The wire contract is [Hand's Companion contract](https://github.com/atqamz/hand/blob/main/docs/companion.md), with its test vectors in `docs/companion-vectors.json`. It is built against the head of Hand's PR #1135, `f7c6e1c5c9da9c8d3735c2754fce68df3845ebff`, which was open on 2026-11-05; a newer head wins when they disagree. `core/tests/companion-vectors.json` is that file copied from the same commit, and `core/tests/vectors.rs` runs the parser over all 13 valid and 73 invalid links as written. In short: iroh 1.4.1, ALPN `hand-board`, one bidirectional QUIC stream per loopback TCP connection, 12 open streams, one connection to a laptop, the phone's endpoint ID as its identity, and the close and reset codes 1 to 5. `AGENTS.md` carries the rules this repository follows.

## Roadmap

1. [x] iroh dialer in `core/`: keep the phone's key, dial the laptop's endpoint ID, and reconnect with the sidecar's documented close and reset codes.
2. [x] Loopback proxy and web view: listen on `127.0.0.1`, open one QUIC stream per TCP connection, and open the Board at `http://localhost:PORT`.
3. [x] Pairing: parse the pairing link, confirm the laptop, and open the Board's pair page that spends the one-time code.
4. [x] QR scanner for the pairing link.
5. Release signing and distribution.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`AGENTS.md`](AGENTS.md). The project is under the [MIT license](LICENSE).
