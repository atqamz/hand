# Hand's Android Companion

Reach a Hand Board from an Android phone over iroh.

Status: roadmap steps 1 and 2 are built. The app keeps an iroh key, dials a laptop's endpoint ID with the ALPN `hand-board`, and shows the Board through a loopback proxy at `http://localhost:PORT`. Pairing over iroh, the QR scanner and release signing are not built, so a temporary debug field on the first screen takes the laptop's details by hand.

## What it is

Hand runs a Board on a laptop. This app keeps its own iroh key, dials the laptop's `hand-iroh` sidecar, and shows the Board in a web view through a loopback proxy. The Board's cookie login, forms and event stream then work unchanged, because the web view sees a normal HTTP origin at `http://localhost:PORT`.

Android only. iOS is deferred and has no code here.

## Layout

- `core/`: `hand-android-core`, plain Rust with no Tauri in it. It owns the key, the iroh endpoint, the one connection to a laptop, the loopback proxy and the close and reset handling. Its tests run on the host.
- `src-tauri/`: the Tauri shell. It starts the core with the app's data directory and registers four commands: the proxy URL, this phone's endpoint ID, the last notice, and opening the Board.
- `web/`: the bundled first screen. Static files, no build step, no npm.
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

Measured on 2026-11-05 on Linux x86_64 with 22 cores and warm SDK, Gradle and cargo caches: 1m36s for the `aarch64` debug APK with iroh in `target/`, 1m14s for the `x86_64` one, and a 127 MiB APK with debug symbols. The SDK, NDK, JDK, Gradle and Rust caches took about 12 GB. On an Android 17 x86_64 emulator the app started, the debug screen showed its `http://localhost:PORT/` proxy URL, and after entering the endpoint ID, the relay URL and a throwaway fleet ID it showed that real Board's pair page through the proxy, with the Board printing the phone's `iroh:` endpoint ID.

## Host tests

The unit tests run anywhere. The sidecar tests need a `hand-iroh` binary built from Hand. They start that real sidecar against a fixture Board on loopback; no phone, fleet, Board database or Luvus server is involved, and every key and secret is made by the test.

Build the sidecar at a pinned Hand commit, then run the workspace:

```sh
git clone https://github.com/atqamz/hand.git hand
git -C hand checkout f7c6e1c5c9da9c8d3735c2754fce68df3845ebff
cargo build --locked --manifest-path hand/iroh/Cargo.toml --bin hand-iroh
HAND_IROH_BIN="$PWD/hand/iroh/target/debug/hand-iroh" cargo test --locked --workspace
```

Without `HAND_IROH_BIN` the 13 sidecar tests print a skip and return; CI always sets it. The suite proves a proxied HTTP request and response, half-close in both directions, stream codes 3, 4 and 5, connection codes 1 and 2, the 13th stream waiting, a stream opening only on its first byte, a Board that answers before it closes while the phone still sends, and a wrong secret ending a stream with no byte. It also covers the backoff after a full laptop, the key's file and folder permissions, the port kept across a restart, and the relay hint.

Measured on 2026-11-05 on the same host, debug builds through loopback: 25 tests, 13 of them against the sidecar; slowest time to first byte of three proxied requests 70-76 ms; the first stream after a full laptop connected 1.10-1.21 s after the request, which is the 1-second backoff.

The standard checks are:

```sh
cargo fmt --all --check
cargo test --locked --workspace
cargo clippy --locked --workspace --all-targets --target aarch64-linux-android -- -D warnings
actionlint .github/workflows/ci.yaml
```

`cargo clippy` for the Android target needs the NDK's C compiler, as ring compiles C:

```sh
toolchain="$NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin"
export CC_aarch64_linux_android="$toolchain/aarch64-linux-android24-clang"
export AR_aarch64_linux_android="$toolchain/llvm-ar"
export CARGO_TARGET_AARCH64_LINUX_ANDROID_LINKER="$toolchain/aarch64-linux-android24-clang"
```

## The temporary debug field

The first screen takes the laptop's endpoint ID (64 lowercase hexadecimal characters), an optional relay URL, and an optional fleet ID. It is marked as temporary, stores nothing, and is not the pairing flow. Pairing over iroh replaces it in step 3. With a fleet ID it opens `/<fleet>/pair`; without one it opens `/`.

If the Board answers with its login page, a pairing page, or a refusal such as "Pair this phone first", the web view shows that page unchanged. The app adds no login screen.

## Key, port and data

- The phone's iroh key is made on first run at `<app data>/iroh/companion.key`, 32 bytes, mode 0600 in a folder of mode 0700 on Unix. Tauri's app data directory is private to the app on Android and the manifest sets `android:allowBackup="false"`, so a backup or device transfer does not copy the identity. It is not in the Android Keystore yet; that is still required before release.
- The proxy listens on `127.0.0.1` and, when the phone has it, `[::1]`, because the name `localhost` resolves to either. Its port is chosen once from 49152 to 65535 and kept in `<app data>/proxy.port`; if the port is taken at launch the app picks and keeps another. A fixed port keeps the page's origin and the Board's drafts across a restart.
- The proxy never listens on a network address, adds no headers, and removes none. The web view's cookies stay in the web view.

## Dependencies

- `iroh` 1.4.1 and `tokio` 1.54.1: the dialer and its runtime, pinned to the sidecar's versions.
- `rand` 0.10.3: the proxy port and the backoff jitter; the standard library has no random source.
- `socket2` 0.6.5, tests only: sets `SO_LINGER` so the fixture Board can send the reset that becomes code 5.
- `tauri` 2.12.5 and `tauri-build` 2.7.4: the Android shell and its build script, pinned with `tauri-cli` 2.12.4.

`Cargo.lock` holds every transitive version.

## Contract with Hand

The wire contract is [Hand's Companion contract](https://github.com/atqamz/hand/blob/main/docs/companion.md), with its test vectors in `docs/companion-vectors.json`. It is built against the head of Hand's PR #1135, `f7c6e1c5c9da9c8d3735c2754fce68df3845ebff`, which was open on 2026-11-05; a newer head wins when they disagree. In short: iroh 1.4.1, ALPN `hand-board`, one bidirectional QUIC stream per loopback TCP connection, 12 open streams, one connection to a laptop, the phone's endpoint ID as its identity, and the close and reset codes 1 to 5. `AGENTS.md` carries the rules this repository follows.

## Roadmap

1. [x] iroh dialer in `core/`: keep the phone's key, dial the laptop's endpoint ID, and reconnect with the sidecar's documented close and reset codes.
2. [x] Loopback proxy and web view: listen on `127.0.0.1`, open one QUIC stream per TCP connection, and open the Board at `http://localhost:PORT`.
3. Pairing: parse the pairing link, spend the one-time code, and bind the device to the phone's key.
4. QR scanner for the pairing link.
5. Release signing and distribution.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`AGENTS.md`](AGENTS.md). The project is under the [MIT license](LICENSE).
