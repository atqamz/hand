# Hand's Android Companion

Reach a Hand Board from an Android phone over iroh.

Status: bootstrap, not usable yet. The app builds, installs and shows a placeholder screen. It has no iroh dialer, pairing, QR scanner or loopback proxy yet; see the roadmap. A debug APK installed and started on an x86_64 Android 37 emulator.

## What it is

Hand runs a Board on a laptop. This app will keep its own iroh key, dial the laptop's `hand-iroh` sidecar, and show the Board in a web view through a loopback proxy. The Board's cookie login, forms and event stream then work unchanged, because the web view sees a normal HTTP origin at `http://localhost:PORT`.

Android only. iOS is deferred and has no code here.

## Layout

- `core/`: `hand-android-core`, plain Rust with no Tauri in it. The iroh dialer, the loopback proxy and the pairing logic go here, with tests that run on the host.
- `src-tauri/`: the Tauri shell. It registers commands that call the core and holds no logic of its own.
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

Measured on 2026-11-05 on Linux x86_64 with 22 cores and warm SDK, Gradle and cargo download caches: 4m03s with an empty `target/`, 14s with `target/` warm, and a 51 MiB `aarch64` APK with debug symbols. The SDK, NDK, JDK, Gradle and Rust caches took about 12 GB.

## Dependencies

- `tauri` 2.12.5 and `tauri-build` 2.7.4: the Android shell and its build script, pinned with `tauri-cli` 2.12.4.
- `hand-android-core`: this workspace's core crate, with no third-party dependencies yet. The dialer adds `iroh` 1.4.1 and `tokio` when it lands, matching the sidecar.

`Cargo.lock` holds every transitive version.

## Contract with Hand

The wire contract belongs to Hand and changes there first. Until a dedicated contract document lands, [Hand's phone notes](https://github.com/atqamz/hand/blob/main/docs/phone.md) and the peer preface entry in its vocabulary are the source. In short: iroh 1.4.1, ALPN `hand-board`, one bidirectional QUIC stream per loopback TCP connection, the phone's endpoint ID as its identity, and the pairing link in the URL fragment. `AGENTS.md` carries the rules this repository follows.

## Roadmap

1. iroh dialer in `core/`: keep the phone's key, dial the laptop's endpoint ID, and reconnect with the sidecar's documented close and reset codes.
2. Loopback proxy and web view: listen on `127.0.0.1`, open one QUIC stream per TCP connection, and open the Board at `http://localhost:PORT`.
3. Pairing: parse the pairing link, spend the one-time code, and bind the device to the phone's key.
4. QR scanner for the pairing link.
5. Release signing and distribution.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`AGENTS.md`](AGENTS.md). The project is under the [MIT license](LICENSE).
