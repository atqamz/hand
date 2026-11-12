# Hand's Android Companion

Reach a Hand Board from an Android phone over iroh.

Status: roadmap steps 1 to 4 are built. The app keeps an iroh key, takes a `handboard://` pairing link from an intent, a paste or a QR scan, asks the user to confirm the laptop, and then shows the Board's pair page through a loopback proxy at `http://localhost:PORT`. The web view loads the bundled page and that proxy origin only; every other address opens in the system browser. The signed release build exists (see Release build) and nothing is published yet. A foreground service polls Hand's alert feed and posts each alert as a notification, with the app open or not; it is built and passes its host and JVM tests, and its device check is still to do (see Alerts on the phone).

## What it is

Hand runs a Board on a laptop. This app keeps its own iroh key, dials the laptop's `hand-iroh` sidecar, and shows the Board in a web view through a loopback proxy. The Board's cookie login, forms and event stream then work unchanged, because the web view sees a normal HTTP origin at `http://localhost:PORT`.

Android only. iOS is deferred and has no code here.

## Layout

- `core/`: `hand-android-core`, plain Rust with no Tauri in it. It owns the key and seals it through a `KeyCipher`, the iroh endpoint, the one connection to a laptop, the loopback proxy, the close and reset handling, the pairing link parser, the pending pairing, the scanner reply mapping, the alert feed client and the supervisor that runs one feed loop per paired fleet. Its tests run on the host.
- `src-tauri/`: the Tauri shell. It starts the core with the app's data directory and the Keystore cipher, takes a link from the Android intent plugin, registers the pairing commands, and keeps the web view on the bundled page and the proxy origin. `src-tauri/src/host.rs` is the JNI entry that starts the core for the service and the Activity alike. Its Kotlin plugins hold the intent bridge and the camera scanner; the Keystore sealing, the cookie read and the notifications are plain Kotlin objects the core calls through JNI. The scanner fills the pair field with the decoded link and sends it through the same `offer_link` command as a paste.
- `web/`: the bundled pairing screen. Static files, no build step, no npm.
- `src-tauri/gen/android/`: the Android project. It was made once with `cargo tauri android init` and is edited by hand; running `init` again overwrites the edits.

## Build a debug APK

Every debug build is signed by the debug key committed at `src-tauri/gen/android/app/debug.keystore` (alias `androiddebugkey`, password `android`, SHA-256 `083a53cf027c8b4ccc94a0ea3894174ffcf8726b0c49d6b3728dc298fbabd6da`), so a new build updates the installed one in place. Debug keys are not secret. The `signingConfigs` block in `app/build.gradle.kts` selects it; `cargo tauri android init` overwrites that file, so redo the block after running it.

An app installed from a build made before this key (CI run 42762177268 and earlier) is signed by a throwaway key: uninstall it and pair again once; later builds update in place.

Prerequisites:

- JDK 21 or newer (Paparazzi's Gradle plugin needs it), with `JAVA_HOME` set.
- Android SDK platform 37, build-tools 36.0.0 and the command-line tools.
- NDK 27.3.13750724, with `NDK_HOME` pointing at it.
- Rust 1.98.1, from `rust-toolchain.toml`, with the `aarch64-linux-android` target for a phone and the `x86_64-linux-android` target for an emulator.
- `tauri-cli` 2.12.4 (also pinned in `.github/workflows/ci.yaml` and `release.yaml`).

```sh
rustup target add aarch64-linux-android x86_64-linux-android
cargo install tauri-cli --version 2.12.4 --locked

export JAVA_HOME=/path/to/jdk
export ANDROID_HOME=/path/to/android-sdk
export NDK_HOME="$ANDROID_HOME/ndk/27.3.13750724"

cargo tauri android build --debug --apk --target aarch64
```

The APK lands at `src-tauri/gen/android/app/build/outputs/apk/universal/debug/app-universal-debug.apk`. `apksigner verify --print-certs` reports the fingerprint above. For an emulator, replace `aarch64` with `x86_64` and install the same path with `adb install -r`.

The build generates the Kotlin activity classes. Once they exist, `cd src-tauri/gen/android && ./gradlew :app:testUniversalDebugUnitTest` runs the JVM tests of the window-inset arithmetic, of the notification inputs (kind to channel, tag, tap URI, lock-screen visibility), of the Board client and of the fleet list. `./gradlew :app:verifyPaparazziUniversalDebug` runs the same tests and also compares the rendered screens with the stored images; CI runs that one.

Measured on 2026-11-05 on Linux x86_64 with 22 cores and warm SDK, Gradle and cargo caches: 1m36s for the `aarch64` debug APK with iroh in `target/`, 1m14s for the `x86_64` one, and a 127 MiB APK with debug symbols. The SDK, NDK, JDK, Gradle and Rust caches took about 12 GB.

## Release build

A tag `vMAJOR.MINOR.PATCH` builds an arm64-v8a APK, signs it and attaches it to a draft GitHub release. The version has one source, `src-tauri/tauri.conf.json`: `version` is `MAJOR.MINOR.PATCH` and `bundle.android.versionCode` is the literal `MAJOR*1000000 + MINOR*1000 + PATCH` (0.2.0 is 2000, and the file stays readable by F-Droid). Set both in the commit that the tag names. The job refuses a tag that disagrees with them, a major of 2100 or more, a minor or patch of 1000 or more, a `release-key.sha256` that is not exactly one line of 64 lowercase hex digits, and a signer that is not the one in `release-key.sha256`. Android refuses an update with a lower `versionCode`.

R8 is on. Rust finds `Core` and `AppHost` and the Tauri plugins by name, so `app/proguard-rules.pro` keeps `Core` and `AppHost` whole (without it R8 strips every `AppHost` method) and `.github/scripts/check-jni-keep.sh` reads `src-tauri/src` for each native, `call_method`, plugin class and plugin command and fails when the APK lacks one, when any of those four lists comes up empty, when `lib/arm64-v8a/libhand_android_lib.so` is missing or another ABI is present, and when the sources use a JNI lookup it does not follow (`find_class`, `new_object`, `get_field`, `get_method_id`, a static call). CI runs it on an unsigned release build of every pull request.

The key is the app's identity for good and one key serves every channel. It lives only in the `release` environment's secrets, so a pull request or another job never sees it, and the build never does either: the APK is built with no secret in the environment, and the last step signs it with `apksigner`, the password in that step's environment and not on its command line. `RELEASE_KEYSTORE_*` are never repository-level or organization-level secrets.

### One-time steps for the maintainer

Make the key on a machine you trust, outside the repository. The password stays in one shell variable (`read` does not echo it, and a variable keeps it off every command line); close the shell when the steps are done.

```sh
mkdir -p ~/hand-android-key && cd ~/hand-android-key
read -rsp 'Release key password: ' RELEASE_KEYSTORE_PASSWORD && export RELEASE_KEYSTORE_PASSWORD
keytool -genkeypair -storetype PKCS12 -keystore release.jks -alias handcompanion \
  -keyalg RSA -keysize 4096 -validity 36500 -dname "CN=Hand's Android Companion" \
  -storepass:env RELEASE_KEYSTORE_PASSWORD
```

Back it up in two places that are not this machine, and keep the password in a password manager. Prove a backup opens:

```sh
cp release.jks /path/to/backup-one/ && cp release.jks /path/to/backup-two/
keytool -list -keystore /path/to/backup-two/release.jks -storepass:env RELEASE_KEYSTORE_PASSWORD
```

Create the environment. Protection rules: the maintainer is a required reviewer, deployment is limited to tags matching `v*`, and "prevent self-review" stays off (the maintainer is the only reviewer). Read the workflow at the tag before approving a run: a tag carries its own copy of it.

```sh
reviewer=$(gh api users/atqamz --jq .id)
gh api -X PUT repos/atqamz/hand-android/environments/release --input - <<EOF
{"reviewers":[{"type":"User","id":$reviewer}],"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}
EOF
gh api -X POST repos/atqamz/hand-android/environments/release/deployment-branch-policies -f name='v*' -f type=tag
```

Store the two secrets in that environment, never at repository or organization level:

```sh
base64 -w0 release.jks | gh secret set RELEASE_KEYSTORE_BASE64 --env release --repo atqamz/hand-android
printf '%s' "$RELEASE_KEYSTORE_PASSWORD" | gh secret set RELEASE_KEYSTORE_PASSWORD --env release --repo atqamz/hand-android
```

To rotate the key, replace the fingerprint in `release-key.sha256` in a pull request. It is a public value:

```sh
keytool -list -v -keystore release.jks -alias handcompanion -storepass:env RELEASE_KEYSTORE_PASSWORD \
  | sed -n 's/^[[:space:]]*SHA256: //p' | tr -d : | tr A-F a-f > /path/to/hand-android/release-key.sha256
[[ "$(cat /path/to/hand-android/release-key.sha256)" =~ ^[0-9a-f]{64}$ ]] && echo ok
```

Print `ok` before you open the pull request: the file must hold one line of 64 lowercase hex digits (trailing newlines are ignored), and the job refuses anything else.

Forget the password in this shell, then tag a commit of `main` whose CI is green, and approve the run in the Actions tab. The run leaves a draft release. Install its APK on the phone, pair, see an alert, and only then publish:

```sh
unset RELEASE_KEYSTORE_PASSWORD
git tag -s v0.1.0 -m v0.1.0 && git push origin v0.1.0
gh release download v0.1.0 --repo atqamz/hand-android --pattern '*.apk' --dir /tmp/hand-android-release
adb install -r /tmp/hand-android-release/hand-android-0.1.0.apk
gh release edit v0.1.0 --repo atqamz/hand-android --draft=false
```

The first release-signed install replaces a debug-signed one only after `adb uninstall io.github.atqamz.handcompanion`: Android refuses a different signer, and the uninstall deletes the Keystore key and the iroh identity, so pair again once.

### A local release build

Gradle makes `app-universal-release-unsigned.apk`. Sign it with the same command as the job, the password taken from the environment (read it with `read -rsp` after the build, so the build never sees it, and `unset` it afterwards):

```sh
cargo tauri android build --apk --target aarch64
read -rsp 'Release key password: ' RELEASE_KEYSTORE_PASSWORD && export RELEASE_KEYSTORE_PASSWORD
apksigner sign --ks /path/to/release.jks --ks-key-alias handcompanion --ks-pass env:RELEASE_KEYSTORE_PASSWORD \
  --out hand-android.apk src-tauri/gen/android/app/build/outputs/apk/universal/release/app-universal-release-unsigned.apk
unset RELEASE_KEYSTORE_PASSWORD
```

## An unreachable laptop

A relaunch dials the saved laptop before it opens the Board. When the dial fails (a sleeping laptop is the daily case), the first screen shows "Can't reach 4d92…6ed6." with Retry and Pair again, never the pairing form: the app keeps no fleet name, only the laptop's short ID that the pairing confirmation showed. It retries on its own after 1 s, doubling to 30 s, only while the app is in the foreground, and once on return to it. A reload asks again, because `open_board` stops answering only after the Board has opened. `node --test "src-tauri/tests/*.test.mjs"` covers the page script; the real Android lifecycle (background, resume, a laptop that wakes) is device-only.

## Window insets

The app draws edge to edge. Tauri hands the web view over before it is attached, so `MainActivity` wraps it in a `FrameLayout` and installs the insets listener one main-loop turn later; the padding follows the status bar or the display cutout at the top and the larger of the navigation bar and the keyboard at the bottom, on every insets change. It zeroes those insets on their way to the web view so the WebView's own safe-area and visual-viewport handling does not pad the Board a second time; the Board's pages carry no safe-area CSS, and the shell owns this. The scanner overlay stays a separate child of the content view and keeps its own bottom inset.

## Session store

`core/src/sessions.rs` keeps what the phone holds per paired fleet: laptops keyed by endpoint ID, each with its fleets (ID, name, device ID, secret, pairing date, and whether it was imported from the web view's jar). The file is `sessions/sessions.sealed` in the app's data directory, sealed by the same Keystore cipher as the iroh key and written to a temp file, synced and renamed, so a crash leaves the old file. The feed and `Core.request` read the cookie header from it (`Sessions::header`), and `Core.paired` asks it whether any fleet is paired. The secret crosses JNI only inside the sealed blob, on its way to and from the Keystore sealer as the iroh key does, and as the jar's cookie header on its way into the store; it is never logged.

- Each `header` call first imports the `laptop` file's endpoint if the store lacks it, then the web view's `hand_phone_<fleet id>` cookies. A fleet the store lacks is added as imported, with an empty name and the import time as its date (the true date is not known). An imported fleet whose jar value has the Board's shape (digits, a dot, 64 hex) and differs from the stored one takes the new secret, because until `web/` goes the Board's pair page is how a phone pairs again after "no longer paired". An imported fleet the jar no longer holds is dropped, so the Board's own sign-out, which expires the jar cookie, still unpairs it. A fleet that is not imported (a native pairing) is never touched by the jar. A jar that cannot be read changes nothing.
- `forget` clears the jar and then the store. A jar that cannot be cleared fails the call and leaves the store alone, so an import never brings a fleet back and the user can try again.
- A failed write does not stop the feed or a request: `header` answers from what it computed, keeps the stored state and tries the write again on the next call. `Sessions::unsaved` is true from a failed write (of `header`, of a pairing or of Forget) until a write succeeds, and `Core.unsaved` hands it to the fleet list, which shows "The phone could not save its pairings" and, as it polls, makes the next write.
- A failed Forget clears the jar, empties the memory, so no feed or request reads a pairing the user forgot, and returns the error; the file is written again by the next `header` call or by Forget again.
- A file that fails authentication or does not parse is renamed to `sessions/sessions.sealed.bad`, replacing an older one, so only the latest bad file is kept, and never deleted. The core starts with an empty store and imports again from the `laptop` file and the jar, and the host posts one notice, "Pairings could not be read; check the app". A store sealed by a Keystore key that is gone is removed, as the iroh key makes a new identity in that case. A store with a field missing opens; a Keystore error that is not corruption fails the start and touches nothing.

## Host tests

The unit tests run anywhere. The sidecar tests need a `hand-iroh` binary built from Hand. They start that real sidecar against a fixture Board on loopback; no phone, fleet, Board database or Luvus server is involved, and every key and secret is made by the test.

Build the sidecar at a pinned Hand commit, then run the workspace:

```sh
git clone https://github.com/atqamz/hand.git hand
git -C hand checkout a1e6d07d4f69653c080414f9b01ac8ed6ad0c335
cargo build --locked --manifest-path hand/iroh/Cargo.toml --bin hand-iroh
HAND_IROH_BIN="$PWD/hand/iroh/target/debug/hand-iroh" cargo test --locked --workspace
```

Without `HAND_IROH_BIN` the 54 sidecar tests print a skip and return; CI always sets it. The suite proves a proxied HTTP request and response, half-close in both directions, stream codes 3, 4 and 5, connection codes 1 and 2, the 13th stream waiting, a stream opening only on its first byte, a Board that answers before it closes while the phone still sends, and a wrong secret ending a stream with no byte, and one HTTP request through the dialer (its single cookie, CSRF token and body, a chunked answer, a 304 and an error answer, a full laptop returning busy at the deadline, and the errors a Board or the sidecar can cause: lost, timed out, too large, malformed, no longer paired and the Board not running). It also covers the backoff after a full laptop, the sealed key's lifecycle (one key across runs with no temp file left, a create over an existing blob replacing it, a missing Keystore key making a new identity, a changed Keystore key and corrupt ciphertext stopping with an error and leaving the file alone, a Keystore error at the probe or at open that is not mistaken for a missing key, a plaintext of the wrong length, an interrupted rotation leaving no blob, ciphertext and folder permissions, and the plugin error codes checked against the Kotlin constants), the port kept across a restart, the relay hint, the pairing link parser over the contract's 13 valid and 73 invalid vectors, the open link parser over its 6 valid and 61 invalid, the confirmation text, the open URL, the pending link kept against a second one, the iroh key checked before the other fields, the clear-before-forget order of a second laptop, and a relaunch with an unreachable saved laptop failing with the laptop kept and named in the message.

The workspace runs 189 tests, 54 of them against the sidecar. Measured on 2026-11-07 on the same host, debug builds through loopback: slowest time to first byte of three proxied requests 70-76 ms; the first stream after a full laptop connected 1.10-1.21 s after the request, which is the 1-second backoff. The scanner tests cover the reply mapping: a code, a refused permission, a scan that is already open, a camera failure, a cancel and an unknown status; a decoded code is returned untouched and checked once by the same parser as a paste. The camera preview, the permission dialog and the native scanner overlay are device-only; the host tests do not open a camera.

The standard checks are:

```sh
cargo fmt --all --check
cargo test --locked --workspace
node --test "src-tauri/tests/*.test.mjs"
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

## Alert feed

`core/src/alerts.rs` polls `GET /<fleet id>/alerts/feed` for one fleet and hands each alert to an `AlertSink`. It knows nothing of Tauri or an Activity: a `CookieSource` supplies the cookie header (the core's `Sessions`), a `Dialer` the connection, a directory the cursor. `paired_fleets` lists the fleets whose `hand_phone_<fleet id>` cookie the header holds; `Feed::run` is one loop per fleet and returns a `Stop` only when the phone is no longer paired or Forget ran. It is no longer paired when the `CookieSource` reports no cookie header at all or the header holds no cookie of that fleet (`CookieGone`; no request is sent, the cursor is deleted and `AlertSink::unpaired` is called with the last fleet name it saw), or when the Board answers 403 with an `X-Hand-Error` that says so (`BoardGone`). The first `BoardGone` keeps the cursor and calls `unpaired`: Hand answers the same 403 for a database error, and a first poll never replays the alerts raised meanwhile. A run started after one (`refused_before`) that is refused again deletes the cursor and stays silent; a 200 clears that memory. A `CookieSource` that fails to read is retried with backoff.

`Feed::forget_cursors` bumps a generation under a lock and deletes every cursor. `run` reads the generation when it starts, and checks it before each `AlertSink::alert`, before the no-cookie notice (the cookie read blocks in JNI) and, under the same lock, before it writes the cursor. A poll in flight when Forget lands (the sink blocks inside JNI and `abort` takes effect only at the next await) therefore posts nothing more and leaves no cursor. The alert being posted at that moment still shows.

- One request per poll, `wait=25`, `Connection: close`, only the fleet's own cookie, no byte of it logged or stored. The request's send side is finished at once; the sidecar half-closes the Board's side and the Board keeps a held request, as its contract says.
- A poll fails, and backs off 1, 2, 4, 8, 16, 30 s with 25 % jitter (the dialer's `Backoff`), on a status other than 200, a 403 or 404 that does not say the phone is no longer paired (an older Hand without the feed, a transient Board error), a reset or close, no complete answer within `wait` plus 10 s, a 200 head that is not HTTP/1.1 with one `Content-Length` of at most 64 KiB and no `Transfer-Encoding` (a refusal may carry one), a body that is short, or a body that is not the contract's answer (another fleet, version other than 1, more than 50 alerts, a replay on a first poll, a seq that does not grow, a cursor that is not the last seq, a cursor above `after` with no alerts). A success resets the backoff. Code 1 takes the dialer's busy path; code 2 closes an idle connection and the next poll dials again.
- Two polls are never less than a second apart, so a Board that ignores `wait` cannot be hammered. An answer with `more` and alerts is followed at once without `wait`.
- The cursor is `<dir>/<fleet id>.cursor`, written by rename after the sink has posted every alert of the answer, so a crash repeats alerts and never loses one; the next request is the Board's receipt. A missing or unreadable file is a first poll, which never replays history. A database the Board recreated answers a lower cursor with no alerts, and the app restarts from it.
- Text is cut to 200 characters with control characters and line and paragraph separators replaced by spaces and format characters (bidirectional controls, zero-width characters) removed; an alert whose text is then blank is skipped, and the cursor still moves past it. A `link` survives only when `parse_open` accepts it and it names the polled fleet; otherwise the alert is shown with no link.

The host tests run the real sidecar against a fixture Board that logs every request and answers a script: alerts and the cursor, `more`, the empty heartbeat with a 1-second wait, 503, 429 and 500 backoff, a connection the sidecar closed with code 2 and dialed again, a 403 that says the phone is no longer paired (with a `Transfer-Encoding`) stopping the fleet with its name, a missing cookie header and a missing own cookie stopping it with no request, an unreadable cookie store retried, other 403 and 404 answers backed off with the cursor kept, a sink failing on the second alert of a batch leaving the cursor and repeating the request, an answer over 64 KiB that is valid JSON refused, the gap after an immediate answer, the cursor across a restart, the supervisor (a fleet starting when its cookie appears with one loop only, forgetting stopping the loops and deleting the cursors with no notification, forgetting while the sink is posting leaving no cursor and no further alert, a refused fleet not started again until its cookie changes or returns, a changed cookie value starting a refused fleet at once, a refused fleet tried again after 30 scans with one notification and its cursor deleted only by the second refusal), and a hostile feed (an oversized body, a short body, another fleet's answer, a pairing link, another fleet's open link, a long text with a line break). The unit tests run the contract's alert exchanges from the vectors, a table of answers that break it, the cookie parsing, the request bytes, the response head and the cursor file.

## Alerts on the phone

`AlertService` is a foreground service of type `specialUse` with one small permanent notification. It starts the core through `Core.start`, which calls one JNI entry (`host.rs`), and asks `Core.watch` to run the supervisor: every 10 seconds it reads the web view's cookie header, runs at most one `Feed` loop per `hand_phone_<fleet id>` cookie, and keeps a loop that ended stopped with the cookie value it ended on. A stopped fleet starts again when its cookie value changes (a new pairing) or goes away and returns; a fleet the Board refused is also tried again after 30 scans (5 minutes) with no second notification; a fleet Forget stopped waits for a new cookie. The Companion is a process-wide singleton: `MainActivity` and the service both call `Core.start`, the second call is a no-op, and the Tauri setup takes the same Companion. A sticky restart after the system killed the process starts the core with no Activity, restores the saved laptop and goes on polling; the A0 spike failed there because only the Activity started the core.

- Notifications: four channels, `service` (low), `decisions` (high: `decision.asked` only), `needs` (high: `attempt.blocked`, `attempt.limited`, `attempt.failed`, `supervisor.blocked`, `supervisor.limited`, `supervisor.stalled`) and `progress` (default: every other kind). A channel's importance is fixed once a phone has created it, so the list was settled before the first install: the user can mute stuck-work noise and keep decisions. A newer alert on the same item sounds again, so a `needs` alert that replaces a `progress` notice for the same attempt is heard; a replay after a crash between the post and the cursor write sounds twice. The text is set as plain text, never through `Html.fromHtml`; the lock screen shows "Hand: alert". The tag is `<fleet id>/<ref>`, so a newer alert on an item replaces the older. A tap opens the `handboard://open/…` link the core accepted, through the existing `open_link` path, or the app when the alert has no link.
- The sink returns after `NotificationManager.notify` has run. When notifications are off for the app it fails, so the cursor stays and the Board's receipt does not advance.
- The permission is asked once, from the visible Activity, when a page of the Board finishes loading and the web view holds a `hand_phone_` cookie. The "asked" flag is written after the dialog's result, so a process that dies mid-dialog asks again. If it is refused, no service starts; allowing it later in Settings starts the service at the next page load. The service is started only while the Activity is at least STARTED and only when it is not already running; `onResume` tries again, so a start skipped while the Activity was in the background happens when it returns, and a refused start (Android 12 and up bars a background start) is logged and not thrown.
- Forget this laptop, and pairing a different laptop, stop the loops and delete every cursor, clear the web view's cookies and stop the foreground service (`AppHost.stopService`) before the user sees the result. No "No longer paired" notification follows. The next Board page load that finds a `hand_phone_` cookie starts the service again.
- No boot start. A reboot, an update or a user Stop leaves alerts off until the app is opened.
- Back with no page to go back to moves the task to the background while the service runs (`moveTaskToBack`), so the Activity and the process stay alive; with no service it finishes the Activity as before. A swipe in Recents still destroys the Activity, and Android ends the whole process, because `tao` calls `process::exit`; the sticky service restarts it about a second later. A swipe is therefore a short alert gap, and a native abort at exit (A0 saw `FORTIFY: pthread_mutex_lock called on a destroyed mutex` on Back) may count toward Android's crash-loop rule: the device run counts tombstones after swipes. `Core.releaseContext` hands the Android context back before the Activity starts, since `tao` sets it once and panics when it is already set.
- After a swipe the restarted process gets `onTaskRemoved` instead of `onStartCommand`, so `begin()` runs from both.
- Not built: the permanent notification's connection states ("Reconnecting…", "Can't reach your laptop"), a battery-optimisation request and a wake lock. Doze behaviour is unmeasured.

The JVM tests cover the kind to channel map, the tag, the title, the tap URI, the visibility and the rule that a notice is acknowledged only after it was posted (`deliver`) as pure functions. The JNI methods of `host.rs` build only for Android and have no host test; the core tests cover what the core does with a sink that fails. The host tests cover the supervisor. Everything that needs the JNI bridge, `CookieManager`, `NotificationManager`, the permission dialog or the service lifecycle is device-only.

Read at the device run, not settled by tests:

- `Core.start` runs on the UI thread (`MainActivity.onCreate` needs the Companion before Tauri's setup does, and a background start in the service would race `unwatch`). It does the Keystore unseal and the iroh and proxy binds: time it and look for ANRs.
- `releaseContext` leaves `ndk_context` empty from `Core.releaseContext` until `tao` sets its own context, tens of milliseconds, because `tao` panics when a context is already set. A dependency thread that reads it then may panic (`netdev` does not guard the read). Look in logcat for `android context was not initialized`.
- `CookieManager.getInstance()` loads the WebView provider in a service-only process just to read one cookie. Measure that process's memory with and without the Activity.
- `CookieManager.getCookie` from a tokio thread with no Activity, the sticky restart then the Activity, tombstones after Back, Recents and SIGKILL, Doze, and notification permission off and on.

## Board client

`Core.request(fleet, post, path, csrf, body)` runs one HTTP exchange through the core's `Companion::request` and blocks, so call it off the main thread. `path` starts at `/api/v1/` and omits the fleet, which the core prefixes. The cookie comes from the session store, as the alert feed's does. It returns one JSON string: `{"status","body"}` for an answer, or `{"failure": name}` for a refusal, with the names of `RequestError::name`. Only a missing cookie for the fleet answers `unpaired`, which the app turns into "Pair again". A fleet, path, CSRF value or body the core refuses answers `malformed`, a panic in the call answers `malformed`, and a web view that cannot give its cookies answers `unreachable`, which the app retries. A null from the JNI call is `malformed` on the Kotlin side.

`BoardClient` (one per fleet, over a `Transport`; `CoreTransport` is the real one) reads Hand's envelope, refuses any `version` but 1 and any answer whose `fleet.id` is not its own. It throws a `BoardError` for the core's failures (busy, no laptop, unreachable, unpaired, lost, Board not running, timed out, too large, malformed) and for an API error, told by its `code` and never by its status. `status()` keeps the envelope's cursor, sends it back as `?since=` and answers `Poll.Unchanged` on a `304`. A poll that fails to reach the Board keeps the cursor; a `bad_cursor` refusal drops it, because the Board signs cursors with its token and a recreated token rejects every old one, so the next poll starts over.

`write` reads the CSRF token from `GET /api/v1/session` once and keeps it in memory. A `csrf_required` refusal means the Board changed nothing, so it reads the token again and sends once more; any other refusal is final and no write is sent twice. A `200` answers `Write.Updated` with the fresh object, or `Write.Accepted` for `{"accepted": true}`, which means the action went in and the read-back failed: the screen refreshes and never sends again. A `200` that cannot be read is `AppliedUnread`, never a "not sent" error. `Lost` and `TimedOut` on a write can follow bytes that reached the Board, so a caller treats them as maybe sent, never as not sent; only an API error (the Board refused), `Busy`, `Unreachable`, `NoLaptop`, `Unpaired` and `BoardNotRunning` say the Board changed nothing, and `Malformed` and `TooLarge` can follow an answer. The models ignore unknown fields.

The JVM tests read Hand's golden answers from `app/src/test/resources/api`, copied from `internal/board/testdata/api` at Hand commit `0063ff18f3fdbdd3b2221e391611dec08937163b`: the four that carry a `Status`. Copy them again when Hand changes them. The JNI method has no host test.

## Native screens

`HandActivity` is the first Compose screen and is not the launcher: `MainActivity` still is, and nothing the user sees changes. It is registered in `src/debug/AndroidManifest.xml`, so release builds do not carry it until it becomes the launcher. Open it in a debug build with `adb shell am start -n io.github.atqamz.handcompanion/.HandActivity`. It shows one row per fleet in the core's session store (`Core.fleets` returns the fleet IDs only, after the store has imported the web view's jar), reads each fleet's `status` through `BoardClient` every 5 seconds while it is in front, and shows the laptop's short ID from the `laptop` file. Nothing is polled in the background; the alert service still is the only thing that runs there.

`FleetsModel` turns each answer into display-ready rows and one band. A `304` keeps the row and moves its time. Busy shows "Your laptop is busy. Trying again."; an unreachable laptop (also lost and timed out) shows the band with Retry and turns the rows to their last state "at HH:mm" with an "unknown" pill; code 3 twice shows "Your laptop answers, but the Board is not running."; an unpaired fleet shows "No longer paired" on its own row. The first failure ends the round, so a dead laptop is asked once and not once per fleet, and "since" counts from the first failure of an outage. An error that is not about the laptop (an answer that cannot be read, an unsupported version, a refusal) shows "The Board's answer could not be read." on that fleet's row only, and the round goes on. The menu is drawn and does nothing yet. Add fleet opens native pairing (see Native pairing).

Tapping a paired fleet opens `FleetScreen`: the top bar (back, the fleet's name and supervisor pill, the fleet's tint rule) and the tabs Needs, Tasks and Chat. Needs is the first tab when anything waits, Chat otherwise, and a tab the user picks stays picked while the screen is open; tapping a fleet in the list picks Needs at once if the tab was Chat and something waits; nothing flips the tab later. With the laptop down on the first open the screen shows the error band and the fleet's name, with Chat's placeholder as its body. The Needs tab is announced as "Needs, N waiting" and Chat as "Chat, supervisor working" while `agent_state` is `working`, and band headers are headings; the label text is tested on the JVM, the semantics themselves are not. `FleetModel` polls `status` every 5 seconds while the screen is in front, with the same bands and errors as the list, and groups adjacent `needs.items` by their word, in the order Hand sends them (blocked and worker share BLOCKED); `needs.more` shows as "N more waiting". The first report is open; a tap opens another in place and closes the first, and when the open one leaves the first report opens. A report opens to its excerpt and Mark read, which sends `POST report/{rN}/ack` and reads `status` again; the button stays off until the report leaves the list. A 409 shows "rN is already read.", a lost or timed-out ack "Not sure rN was marked read.", any other refusal "Could not mark rN read."; the notice shows in a bar above the tabs, whichever card is open, and goes after 4 seconds. An excerpt that Hand cut also offers Open web Board. A failure, a blocked supervisor and a decision show their row and do nothing until their screens exist. Any other kind, and the Tasks and Chat tabs, open the web Board by starting `MainActivity`: that is a stand-in until the fallback screen exists.

`HandTheme` holds the Board's colours (static, light and dark, no dynamic colour), its five type sizes, its radii and the icons as vectors. It sets every field of the Material colour scheme from those colours, and `HandThemeTest` pins each one, so no component can show Material's baseline purple. Each fleet's mark takes the Board's tint, the FNV-1a hash of the fleet ID modulo 12.

A decision row in Needs opens in place to its choice list, and **Open dN** (or a `decision.asked` alert) opens `DecisionScreen`. The options come from the API's `options`; only the question is cut from `body`, which still holds the option lines, by dropping the lines outside code fences whose key the API listed. Sending an answer posts `{"choice": <option value>, "note": <note>}`; an `other` option, and a decision with no options, need a note. The Board's `409` and `AppliedUnread` answers both end in the answered state, and a lost or timed-out answer says it may have been sent and reads the decision again. The note is kept per fleet and decision in the `drafts` preferences file (private to the app, never backed up) and removed after a successful send. In a debug build the alert's tap for a `handboard://open/<fleet>/decision/<dN>` link starts `HandActivity` (`launchMode="singleTask"`, so a tap while it is open arrives in `onNewIntent`); in a release build every tap still opens `MainActivity`, which has no Decision screen, until `HandActivity` becomes the launcher.

`app/src/test/snapshots` holds the stored images of the fleet list, the unreachable band, Decision with a recommended option, an `other` option with its note and its answered state, Needs with a decision open in place, Needs with several bands and one open report, Needs with only rows, a cut report, a tab that has not loaded, Needs all clear and the tabs, light and dark, rendered by Paparazzi on the JVM with no device. `./gradlew :app:recordPaparazziUniversalDebug` replaces them; commit them with the change that made them differ, and look at each before you do.

## Native pairing

Add fleet (screen 2), the scanner (3) and the confirmation (4) are Compose screens of `HandActivity`: `PairScreens.kt` draws them, `PairModel` holds their state, and the core does the pairing. The code, the device secret and the pending link stay in Rust. Kotlin hands the link in (`Core.offer`) and the phone's name (`Core.pair`), and gets back the laptop's short ID and `replaces`, or the fleets' IDs and names, or a failure name with its sentence. `Core.cancel` drops the pending link. Nothing else crosses JNI, and no sentence about the code, the link or a secret is logged.

- `Core.offer` parses the link with `core/src/link.rs`, so a rejected link shows the contract's sentence under the field, and a link that arrives while another is open keeps the first and says so. The confirmation shows the laptop's short ID. When the link names another laptop than `<app data>/laptop` (`Offer.replaces`), it adds the amber notice, built from the fleet names the list already holds, and the button reads **Replace and pair**.
- `Core.pair` first checks the jar (`Sessions::check`: it must be readable and every `hand_phone_` cookie in it must have a place), so a pairing that cannot start has changed and taken nothing. It then takes the pending link out of the core (a cancel or a new offer while it runs cannot redirect it, and a failed request puts the link back only when the pairing is still in flight and no newer link arrived), and sends `POST /<fleet id>/api/v1/pair` with `{code, name}` on a second dialer for the new laptop (`Request::pair`: no cookie, no CSRF). The main dialer, the old fleets, their feeds and their cookies are untouched until the Board has answered. The name is checked as Hand checks it before a byte goes out (no control character, at most 40 characters once trimmed, counted as characters; the trimmed name is sent). The answer is accepted only when `version` is 1, it names at least one fleet once each, each `cookie_name` is `hand_phone_<fleet id>` and each `cookie_value` matches `^[1-9][0-9]*\.[0-9a-f]{64}$`; anything else is "could not be read" and nothing is stored. A fleet's name from the answer loses control characters and is cut to 40. A code for several fleets adds them all. The screen's Cancel, Close and Back are off while the request runs, and the model ignores a cancel or a link meanwhile; a `handboard://c` link that arrives meanwhile is kept by the Activity and offered when the pairing ends (when the pairing failed and its confirmation is still open, the core answers that one is pending).
- The commit (`Sessions::pair`) is one sealed write. The same check runs again inside it as a race guard, so a clear never destroys a login. After the rename it stops the old feeds (`Supervisor::forget_all`) when another laptop replaced the old one, still under the store's lock so no feed sees the new store first and posts "No longer paired", and then, outside the lock, clears the jar. A pairing with the same laptop keeps its other fleets and takes a new secret for a fleet it already held; every entry becomes native, so the jar no longer decides about it, and a stale jar cookie never replaces a native secret. If the write fails the staged file is removed and nothing changed, feeds and cursors included. Once the write is made the pairing is made: a jar that cannot be cleared, a folder that cannot be synced or a `laptop` file that cannot be written mark the pairing unfinished (the fleet list says so, and only Forget or the next pairing that completes clears the mark) and the app still points at the new laptop. The jar cookies a pairing replaced are remembered in the store by fleet and device number (`retired`), so a clear that failed never lets a stale cookie of the old laptop come back as a fleet, and a new cookie of the same fleet is imported as before; the list empties once the jar no longer holds them. The unsaved band after a failed jar clear ends when the app restarts; the retired list is what keeps the stale cookie out after that. Because the jar is cleared as a whole, the web Board in `MainActivity` needs a new pairing of its own until the fallback screen sets its cookie from the store.
- The alert service is not started by `HandActivity`: after a native pairing from an empty state, alerts start when `MainActivity` is next in front, and A6 (the launcher switch) makes `HandActivity` start the service.
- Errors, each with its sentence from the contract: wrong, expired or used code, every code cancelled, too many wrong codes from this phone, the laptop unreachable, the Board not running, and the laptop busy ("Try again in a moment", since the dialer has already retried to the request's deadline, so "Trying again" would be untrue). A connection that broke tells the user the code may be spent. An unreadable answer or a write that failed before the pairing was made says that the laptop may hold an unfinished device to remove on its Phone page. A jar the app could not read or place is its own sentence, with the way out: sign this phone out in that fleet's web Board, until a native Forget exists. Every other refusal gets one general sentence.
- `FLAG_SECURE` is on for the whole flow. The scanner is `PairScreens.kt`'s `Scanner` over ZXing's `BarcodeView`, with the states asking, refused (with Settings), failed and timed out after 15 seconds; `ScannerPlugin` stays for the web view until it goes. The camera, the permission dialog and the intent are device-only.
- A `handboard://c#…` intent reaches `HandActivity` only in debug builds, where `src/debug/AndroidManifest.xml` gives it a filter for host `c`; Android then asks which of the two Activities opens it. Release builds have no such filter and `HandActivity` is not their launcher, so a link still opens `MainActivity` and the web pairing page. The link is read once at launch and on a new intent, and the Activity's intent is replaced so a recreated Activity does not offer it again.

## Pairing

A link arrives from the `handboard://` intent, from the paste field on the first screen, or from the scanner behind **Scan the QR code**. The scanner asks for the camera once on first use, decodes only QR codes, and hands the text to the same `offer_link` command as a paste; a foreign or invalid code gets the same "check the link" message. If the camera is refused or fails to start, the scanner says so and can be opened again. The app trims surrounding whitespace and applies the contract's 15 checks before it accepts anything. It then shows the first four and last four characters of the laptop's endpoint ID and asks for confirmation. A link that arrives while a pairing is open replaces nothing until the user confirms; the app says `Finish or dismiss the current pairing first` and keeps the first link. The code is never shown.

After confirmation the app dials, checks that the Board answers, and loads `http://localhost:PORT/<fleet>/pair#<code>`. The host is the name `localhost`, never an IP. The user types the phone's name on the Board's page and taps its button; the app never submits the form.

Pairing a second laptop first clears the web view's cookies and storage and forgets the first. Wry's `clear_all_browsing_data` does not touch `CookieManager`, so `AppHost.clearCookies` runs `removeAllCookies` and `flush` on the main thread and waits up to 5 seconds for its callback, after the alert loops were told to forget (so no loop reads the cleared cookie before its generation changes and posts a false "No longer paired"). If the callback does not come or the call fails, the app logs it under `HandAlerts` (no cookie value) and Forget, or the clear step of a new pairing, fails with "Can't clear the saved login. Try again."; the loops and the service are already stopped, so a retry is safe. The last endpoint ID is kept in `<app data>/laptop`; it is not a secret. On a later launch the app dials it and opens `http://localhost:PORT/`, where the Board's cookie picks the fleet.

The pairing screen and the pair page set `FLAG_SECURE`, so screenshots, recents thumbnails and screen recording do not capture the code.

If the Board answers with its login page, a pairing page, or a refusal such as "Pair this phone first", the web view shows that page unchanged. The app adds no login screen. A 403 whose `X-Hand-Error` says the phone is no longer paired gets two buttons over the page: **Pair again** returns to the pairing screen and **Forget this laptop** clears the web view's cookies (`AppHost.clearCookies`) and storage and forgets the laptop.

## Open links

`handboard://open/<fleet id>/<page>[#rN]` opens one Board page. A second intent filter takes host `open` beside the pairing host `c`. The page is nothing, `task/tN`, `decision/dN` or `attempt/aN`, with N of 1 to 9 digits and no leading zero, and `#rN` only after `task/tN`. The fleet ID is `f` and 12 lowercase hex digits. The link has no query, user, port, trailing slash, upper case, percent escape or extra segment; anything else gets "This is not a link Hand can open." and nothing is dialed. The link holds no secret.

`Companion::open_link` is the `confirm` path without the pairing steps. It keeps the saved laptop, clears no cookies, dials, checks that the Board answers `/<fleet>/pair`, and returns `http://localhost:PORT/<fleet>/<page>`; the first screen then navigates to it. Every case runs the same way whether the app is running or not.

| Case | The first screen shows |
|---|---|
| A link the app rejects, laptop paired | The Board's first page, as on a relaunch. The rejection is dropped. |
| A link the app rejects, no laptop paired | "This is not a link Hand can open." |
| No laptop paired | "Pair this phone with your laptop first." The link is dropped. |
| A pairing confirmation is open | The pending confirmation and "Finish or dismiss the current pairing first". The link is dropped. |
| Phone not paired for that fleet | The Board's own refusal, unchanged. |
| Phone revoked | The Board's 403 and the **Pair again** and **Forget this laptop** buttons. |
| Laptop or Board unreachable | The dialer's message, for example "Can't reach your laptop. Check that it is on, that Hand's Board is running, and that this phone has internet." Open the link again to retry. |
| A busy laptop | "Your laptop is busy. Tap the link again." |

The host tests cover the parser over the five valid forms and 61 hostile ones, a fleet ID that fails on its tail alone, the `handboard://open` routing prefix, and `open_link` with no laptop, with a pending pairing, with a hostile link, with an unreachable laptop and with a reachable Board. The reachable case runs through a seam that replaces the dial and the probe, and asserts the URL, that the URL passes `may_load`, the unchanged target and the unchanged `laptop` file. The sidecar suite does not cover `open_link`, since the dialer reaches a laptop by endpoint ID alone and the tests need the sidecar's direct address. The `web/main.js` branch that navigates to the returned URL, the Kotlin intent handoff and the fall-through to the Board after a rejected link are device-only.

## Key, port and data

- The phone's iroh identity is 32 random bytes made on first run. The app seals them with AES-256-GCM under a non-exportable Android Keystore key (alias `io.github.atqamz.handcompanion.iroh`, hardware-backed when the phone has a TEE or StrongBox, `setUserAuthenticationRequired(false)`) and stores only the IV, ciphertext and tag at `<app data>/iroh/companion.key`, mode 0600 in a folder of mode 0700. The key is never written in the clear, logged, copied or exported; the core owns the 32 bytes and the endpoint, and the Kotlin `KeystoreSealer` object only asks the Keystore to seal and open bytes, and the core reaches it through JNI. A reinstall removes the file and the Keystore key together, so the Board sees a new phone. If the file survives and the Keystore key is gone or permanently invalidated, the app makes and seals a new identity instead of falling back to raw bytes; a ciphertext that does not authenticate, a plaintext that is not 32 bytes, or a Keystore error at the alias probe or at open that is not a definitively missing key stops with an error on the first screen instead of replacing the identity; the legacy Keystore before Android 12 reports an outage and a missing alias alike, so a startup outage there can still rotate. A rotation unlinks the old blob and syncs the folder before it seals the new one, so an interruption leaves either a blob the current key opens or no blob, never one no key can open; the new blob is written to a `.tmp` file in the same folder and renamed over the path, and the folder is synced again after the rename. The rename is used instead of a hard link because Android denies `linkat` for an app's data directory: on a real phone a hard link failed with `EACCES`, so the key was never created and the app could not start. Tauri's app data directory is private to the app and the manifest sets `android:allowBackup="false"`, so Android's backup does not copy the file; on Android 12 and later some manufacturers still migrate app files device to device, but the Keystore key never leaves the source phone, so a copied blob is unreadable and the app makes a new identity. A pre-PR development install kept a raw 32-byte `companion.key`; the first run under this code cannot authenticate it, unlinks it and makes a new identity, so the Board sees a new phone once and needs a re-pair.
- The Keystore round trip is device-only. CI has no Android Keystore, so the host tests drive the lifecycle with a fake cipher and check the plugin error codes against the Kotlin constants: they do not prove key generation, hardware backing, real AES-GCM sealing and opening, or that a real `KeyStore` probe reports a provider error instead of a missing alias. On a phone, `adb install -r` over the same build must keep the endpoint ID (the identity loads and opens), while `adb uninstall` followed by an install must show a new one (the file and Keystore key are both gone).
- The proxy listens on `127.0.0.1` and, when the phone has it, `[::1]`, because the name `localhost` resolves to either. Its port is chosen once from 49152 to 65535 and kept in `<app data>/proxy.port`; if the port is taken at launch the app picks and keeps another. A fixed port keeps the page's origin and the Board's drafts across a restart.
- The proxy never listens on a network address, adds no headers, and removes none. The proxy never reads the web view's cookies. Hand's `docs/companion.md` says the app must not read them, yet the feed request needs `hand_phone_<fleet id>`; the core reads the cookie header from `CookieManager` only to import it into the session store, which seals it under the Keystore key, and Hand's document allows exactly that.

## Dependencies

- `iroh` 1.4.1 and `tokio` 1.54.1: the dialer and its runtime, pinned to the sidecar's versions.
- `rand` 0.10.3: the proxy port and the backoff jitter; the standard library has no random source.
- `url` 2.5.8: parses and compares the navigation URLs; the standard library has no URL parser, and it is the type Tauri hands the navigation callback.
- `serde` 1.0.229: the alert feed's `Deserialize` derives in the core, the shell's command replies and errors and the derives on the mobile plug-in payloads; `tauri::generate_context!` expands to `serde` paths, so it must be a direct dependency rather than a transitive one.
- `serde_json` 1.0.154: the core reads the alert feed's JSON with it, and its tests read the contract's vectors; `tauri::generate_context!` also expands to code that names it in the shell.
- `socket2` 0.6.5, tests only: sets `SO_LINGER` so the fixture Board can send the reset that becomes code 5.
- `jni` 0.21.1 and `ndk-context` 0.1.1, Android only: the JNI entry that starts the core from the service, the calls back into Kotlin, and the Android context that `tao` otherwise sets only when an Activity exists. Both are already in `Cargo.lock` through `tao`, `wry` and `tauri`, at the same versions.
- `tauri` 2.12.5 and `tauri-build` 2.7.4: the Android shell and its build script, pinned with `tauri-cli` 2.12.4.
- `tauri-plugin-opener` 2.7.2: sends a refused navigation and a clicked `_blank` link or `window.open` call to another origin, or with a `mailto:` or `tel:` scheme, to the Android system browser; neither the standard library nor Tauri starts that intent itself. Its `open_url` command is granted to the bundled page and to `http://localhost:*` by `src-tauri/capabilities/external-links.json`, and its link script is left on.
- `org.jetbrains.kotlinx:kotlinx-serialization-json` 1.9.0 and its compiler plugin, Android: decodes Hand's JSON into typed models that ignore unknown fields. `org.json` is absent from JVM unit tests and gives untyped maps. 1.9.0 because the Kotlin compiler this build runs is 2.2.10, which cannot read the metadata of a later release.
- `androidx.compose:compose-bom` 2026.09.00 with `material3`, `androidx.activity:activity-compose` 1.10.1 and `androidx.lifecycle:lifecycle-runtime-compose` 2.10.0, and the Kotlin Compose compiler plugin 2.4.21, Android: the native screens. They add 8,549,266 B to the debug APK's entries (CI at this PR's head against CI at its base), 8,152,271 B of it dex, since debug builds are not minified. Compose needs JVM target 11, so the app module's `compileOptions` and `jvmTarget` moved from 1.8 to 11.
- `app.cash.paparazzi` 2.0.0-alpha05.1, tests only: renders the screens on the JVM, light and dark, and checks them against stored images, so a screen change is reviewed as a picture without a device. It is an alpha because its release notes name Android Gradle plugin 9 support, which this build needs (9.3.1); it never reaches the APK.
- `com.google.zxing:core` 3.5.4 and `com.journeyapps:zxing-android-embedded` 4.3.0, Android: the QR decoder and the camera preview that feeds it. They add 463,884 B to the debug APK; the two artifacts are 610,364 and 151,839 B, about 0.76 MB together. They need no Google Play services; the embedded library's own `core` is excluded so the decoder is pinned directly. A hand-written camera loop would be more code for the same result.

`Cargo.lock` holds every transitive version.

## Store text

F-Droid reads the store text from `fastlane/metadata/android/en-US/`: `title.txt` (at most 50 characters), `short_description.txt` (at most 80; no trailing dot is this project's rule, not F-Droid's), `full_description.txt` (at most 4000, F-Droid's simple HTML) and `images/icon.png` (512x512). Each release adds `changelogs/<versionCode>.txt` (at most 500 characters), named by the literal version code from `src-tauri/tauri.conf.json`. `.github/scripts/check-store-metadata.sh` checks the limits in CI. There are no screenshots or feature graphic yet.

## Contract with Hand

The wire contract is [Hand's Companion contract](https://github.com/atqamz/hand/blob/main/docs/companion.md), with its test vectors in `docs/companion-vectors.json`. It is built against Hand's `main` at `a1e6d07d4f69653c080414f9b01ac8ed6ad0c335`, which has the alert feed (PR #1203) and `POST /<fleet id>/api/v1/pair` (PR #1248); a newer head wins when they disagree. `core/tests/companion-vectors.json` is that file copied from the same commit, and `core/tests/vectors.rs` runs the parser over all 13 valid and 73 invalid pairing links and all 6 valid and 61 invalid open links as written. In short: iroh 1.4.1, ALPN `hand-board`, one bidirectional QUIC stream per loopback TCP connection, 12 open streams, one connection to a laptop, the phone's endpoint ID as its identity, and the close and reset codes 1 to 5. `AGENTS.md` carries the rules this repository follows.

## Roadmap

1. [x] iroh dialer in `core/`: keep the phone's key, dial the laptop's endpoint ID, and reconnect with the sidecar's documented close and reset codes.
2. [x] Loopback proxy and web view: listen on `127.0.0.1`, open one QUIC stream per TCP connection, and open the Board at `http://localhost:PORT`.
3. [x] Pairing: parse the pairing link, confirm the laptop, and open the Board's pair page that spends the one-time code.
4. [x] QR scanner for the pairing link.
5. [x] A signed release build: tag, version code, R8, a release job with its key in a protected environment.
6. Distribution: an F-Droid repository.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`AGENTS.md`](AGENTS.md). The project is under the [MIT license](LICENSE).
