# Hand's Android Companion

Reach a Hand Board from an Android phone over iroh.

Status: the app keeps an iroh key, takes a `handboard://` pairing link from an intent, a paste or a QR scan, pairs natively and draws the fleet list, Needs, Tasks, a Task, an Attempt, a Report, a Decision, pairing and the web fallback in Jetpack Compose. A report body is drawn as the Board's Markdown subset: paragraphs, `###` and `####` heads, lists, inline code, fenced code with a copy button, quotes and tables that scroll sideways. A fleet screen follows the Board's `/api/v1/events` stream while it is open and falls back to the 5-second `status` poll when the Board's `meta.features` lacks `events` or the stream keeps failing. An attempt reads its detail every 5 seconds while its state is `running`, because a transcript moves without a board version, and stops once the state is finished. A page with no native screen still opens in a plain `WebView` through a loopback proxy at `http://localhost:PORT`, where the Board's cookie login, forms and event stream work unchanged. The signed release build exists (see Release build) and nothing is published yet. A foreground service polls Hand's alert feed and posts each alert as a notification, with the app open or not; it is built and passes its host and JVM tests, and its device check is still to do (see Alerts on the phone).

## What it is

Hand runs a Board on a laptop. This app keeps its own iroh key and dials the laptop's `hand-iroh` sidecar. Its screens are native (`HandActivity`, Compose over the Rust core through JNI); the fallback screen opens the Board's pages in a plain `WebView` through the loopback proxy.

Android only. iOS is deferred and has no code here.

## Layout

- `core/`: `hand-android-core`, plain Rust. It owns the key and seals it through a `KeyCipher`, the iroh endpoint, the one connection to a laptop, the loopback proxy, the close and reset handling, the pairing link parser, the pending pairing, the sealed session store, the HTTP exchange, the alert feed client, the live event stream for the fleet on screen and the supervisor that runs one feed loop per paired fleet. Its tests run on the host.
- `src-tauri/`: the JNI crate. `src-tauri/src/host.rs` is the JNI entry that starts the core for the service and the Activity alike, calls back into Kotlin (Keystore sealing, cookie read, notifications, live events and the link state), and owns the core's tokio runtime. It holds no UI and no app logic.
- `src-tauri/gen/android/`: the Android project, edited by hand. `app/build.gradle.kts` runs `cargo build --locked --target <abi>` as its `cargoDebug` and `cargoRelease` tasks and gives their output to the variant API as the generated `jniLibs` folder, so `:app:assembleDebug` and `:app:assembleUniversalRelease` build the Rust library too.

## Build a debug APK

Every debug build is signed by the debug key committed at `src-tauri/gen/android/app/debug.keystore` (alias `androiddebugkey`, password `android`, SHA-256 `083a53cf027c8b4ccc94a0ea3894174ffcf8726b0c49d6b3728dc298fbabd6da`), so a new build updates the installed one in place. Debug keys are not secret. The `signingConfigs` block in `app/build.gradle.kts` selects it.

An app installed from a build made before this key (CI run 42762177268 and earlier) is signed by a throwaway key: uninstall it and pair again once; later builds update in place.

Prerequisites:

- JDK 21 or newer (Paparazzi's Gradle plugin needs it), with `JAVA_HOME` set.
- Android SDK platform 37, build-tools 36.0.0 and the command-line tools.
- NDK 27.3.13750724, with `NDK_HOME` pointing at it. Gradle derives the target's clang, linker and `llvm-ar` paths from it.
- Rust 1.98.1, from `rust-toolchain.toml`, with the `aarch64-linux-android` target for a phone and the `x86_64-linux-android` target for an emulator.

```sh
rustup target add aarch64-linux-android x86_64-linux-android

export JAVA_HOME=/path/to/jdk
export ANDROID_HOME=/path/to/android-sdk
export NDK_HOME="$ANDROID_HOME/ndk/27.3.13750724"

cd src-tauri/gen/android
./gradlew :app:assembleDebug
```

The APK lands at `src-tauri/gen/android/app/build/outputs/apk/universal/debug/app-universal-debug.apk`. `apksigner verify --print-certs` reports the fingerprint above. For an emulator, build with `./gradlew :app:assembleDebug -PrustTarget=x86_64` and install the same path with `adb install -r`.

`cd src-tauri/gen/android && ./gradlew :app:testUniversalDebugUnitTest` runs the JVM tests of the notification inputs (kind to channel, tag, tap URI, lock-screen visibility), of the Board client, of the landing routes, of the pairing model, of the manifest, of the fleet list, of the Tasks list, of the task detail, of the attempt detail and of the report detail. The Markdown parser is tested against the cases of `internal/markdown/markdown_test.go`. `./gradlew :app:verifyPaparazziUniversalDebug` runs the same tests and also compares the rendered screens with the stored images; CI runs that one.

Measured on 2026-11-13 on Linux x86_64 with 22 cores and warm SDK and Gradle caches, a cold Rust target: 1m8s for the `aarch64` debug APK with iroh compiled, and 110,411,370 B. The same build at the base commit, with Tauri, was 152,223,196 B, so removing Tauri moved −41,811,826 B: 40,716,744 B of it from `lib/arm64-v8a/libhand_android_lib.so` (134,457,848 to 93,741,104 B), 2,332,132 B from dex, and 2,370 B of bundled `tauri.conf.json`. The SDK, NDK, JDK, Gradle and Rust caches took about 12 GB.

## Release build

A tag `android-vMAJOR.MINOR.PATCH` builds an arm64-v8a APK, signs it and attaches it to a draft GitHub release with `--latest=false`, so `hand update` keeps resolving Hand's own latest. The version has one source, `android/version.properties`: `version` is `MAJOR.MINOR.PATCH` and `versionCode` is the literal `MAJOR*1000000 + MINOR*1000 + PATCH` (0.2.0 is 2000, and the file stays readable by F-Droid). Set both in the commit that the tag names. The job refuses a tag that disagrees with them, a major of 2100 or more, a minor or patch of 1000 or more, a `release-key.sha256` that is not exactly one line of 64 lowercase hex digits, and a signer that is not the one in `release-key.sha256`. Android refuses an update with a lower `versionCode`.

R8 is on. Rust finds `Core` and `AppHost` by name, so `app/proguard-rules.pro` keeps `Core` and `AppHost` whole (without it R8 strips every `AppHost` method) and `android/scripts/check-jni-keep.sh` reads `src-tauri/src` for each native and `call_method` and fails when the APK lacks one, when either list comes up empty, when `lib/arm64-v8a/libhand_android_lib.so` is missing or another ABI is present, and when the sources use a JNI lookup it does not follow (`find_class`, `new_object`, `get_field`, `get_method_id`, a static call). CI runs it on an unsigned release build whenever a change touches `android/`, `iroh/` or `.github/`.

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

Create the environment. Protection rules: the maintainer is a required reviewer, deployment is limited to tags matching `android-v*` and to the `main` branch (the on-demand `main-apk` job in `android.yaml` runs there), and "prevent self-review" stays off (the maintainer is the only reviewer). Read the workflow at the tag or on `main` before approving a run: that ref carries its own copy of it.

```sh
reviewer=$(gh api users/atqamz --jq .id)
gh api -X PUT repos/atqamz/hand/environments/release --input - <<EOF
{"reviewers":[{"type":"User","id":$reviewer}],"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}
EOF
gh api -X POST repos/atqamz/hand/environments/release/deployment-branch-policies -f name='android-v*' -f type=tag
gh api -X POST repos/atqamz/hand/environments/release/deployment-branch-policies -f name=main -f type=branch
```

Store the two secrets in that environment, never at repository or organization level:

```sh
base64 -w0 release.jks | gh secret set RELEASE_KEYSTORE_BASE64 --env release --repo atqamz/hand
printf '%s' "$RELEASE_KEYSTORE_PASSWORD" | gh secret set RELEASE_KEYSTORE_PASSWORD --env release --repo atqamz/hand
```

To rotate the key, replace the fingerprint in `release-key.sha256` in a pull request. It is a public value:

```sh
keytool -list -v -keystore release.jks -alias handcompanion -storepass:env RELEASE_KEYSTORE_PASSWORD \
  | sed -n 's/^[[:space:]]*SHA256: //p' | tr -d : | tr A-F a-f > /path/to/hand/android/release-key.sha256
[[ "$(cat /path/to/hand/android/release-key.sha256)" =~ ^[0-9a-f]{64}$ ]] && echo ok
```

Print `ok` before you open the pull request: the file must hold one line of 64 lowercase hex digits (trailing newlines are ignored), and the job refuses anything else.

Forget the password in this shell, then tag a commit of `main` whose CI is green, and approve the run in the Actions tab. The run leaves a draft release. Install its APK on the phone, pair, see an alert, and only then publish:

```sh
unset RELEASE_KEYSTORE_PASSWORD
git tag -s android-v0.1.0 -m android-v0.1.0 && git push origin android-v0.1.0
gh release download android-v0.1.0 --repo atqamz/hand --pattern '*.apk' --dir /tmp/hand-android-release
adb install -r /tmp/hand-android-release/hand-android-0.1.0.apk
gh release edit android-v0.1.0 --repo atqamz/hand --draft=false --latest=false
```

The first release-signed install replaces a debug-signed one only after `adb uninstall io.github.atqamz.handcompanion`: Android refuses a different signer, and the uninstall deletes the Keystore key and the iroh identity, so pair again once.

### A local release build

Gradle makes `app-universal-release-unsigned.apk`. Sign it with the same command as the job, the password taken from the environment (read it with `read -rsp` after the build, so the build never sees it, and `unset` it afterwards):

```sh
(cd src-tauri/gen/android && ./gradlew :app:assembleUniversalRelease)
read -rsp 'Release key password: ' RELEASE_KEYSTORE_PASSWORD && export RELEASE_KEYSTORE_PASSWORD
apksigner sign --ks /path/to/release.jks --ks-key-alias handcompanion --ks-pass env:RELEASE_KEYSTORE_PASSWORD \
  --out hand-android.apk src-tauri/gen/android/app/build/outputs/apk/universal/release/app-universal-release-unsigned.apk
unset RELEASE_KEYSTORE_PASSWORD
```

### An APK from `main`

The phone runs a release-signed build, and a debug APK cannot replace it, so testing `main` on the device needs the same key. `main-apk` is a `workflow_dispatch` job in `android.yaml` that builds `main` only, on purpose: it refuses a tag ref, the `release` environment's branch policy matches the dispatch ref, and `./.github/actions/build-signed-apk` resolves from the checked-out tree, so an input ref would hand the release key to that ref's scripts, and a ref older than the action cannot build at all. It signs the same arm64 release APK with the same key, checks the signer, the `versionName`, the `versionCode` and the JNI keep rules, and uploads the artifact `hand-android-main-apk` for 7 days; the run summary carries its `versionName`, `versionCode` and sha256. It refuses a `versionCode` that does not follow from `version` or a `release-key.sha256` that is not 64 lowercase hex digits, both before the build, and attaches no release. `workflow_dispatch` reads the workflow from the default branch, so a change to it runs only after the merge.

```sh
gh workflow run android.yaml --repo atqamz/hand
gh run download --repo atqamz/hand --name hand-android-main-apk --dir /tmp/hand-android-main
adb install -r /tmp/hand-android-main/hand-android-main.apk
```

## Session store

`core/src/sessions.rs` keeps what the phone holds per paired fleet: laptops keyed by endpoint ID, each with its fleets (ID, name, device ID, secret, pairing date). The file is `sessions/sessions.sealed` in the app's data directory, sealed by the same Keystore cipher as the iroh key and written to a temp file, synced and renamed, so a crash leaves the old file. The feed and `Core.request` read the cookie header from it (`Sessions::header`), and `Core.paired` asks it whether any fleet is paired. The secret crosses JNI only inside the sealed blob, on its way to and from the Keystore sealer as the iroh key does, and as the jar's cookie header on its way into the store; it is never logged.

- The jar is read once. When `sessions/sessions.sealed` is absent — a fresh install, a quarantined file, or a store removed with its Keystore key — `Sessions::open` marks one import pending, and the first read — `Sessions::header` (the fleet list, the alert feed, `request`, `open` and `fallback`) or `Sessions::pair`, which calls the import directly before it writes — reads the jar and the `laptop` file once, adds each cookie of the Board's shape (digits, a dot, 64 hex) with an empty name and the import time as its date, writes the store, and clears the mark. A fresh install's jar is empty, so that run adds nothing and the store it writes means a cookie the fallback later puts in the jar can never become a pairing. When the store exists, the jar is not read at all: a Board sign-out in the fallback no longer unpairs the phone, and Forget in the app is the way out. If the jar cannot be read, the store is not written and the next start tries again.
- `forget` clears the jar and then the store. A jar that cannot be cleared fails the call and leaves the store alone, so the user can try again.
- `Sessions::unsaved` is true from a failed import, pairing or Forget until a write succeeds, and `Core.unsaved` hands it to the fleet list, which shows "The phone could not save its pairings". A failed write does not stop the feed or a request: the store in memory serves them, and the next pairing or Forget writes again.
- A failed Forget clears the jar, empties the memory, so no feed or request reads a pairing the user forgot, and returns the error; the file is written again by the next Forget.
- A file that fails authentication or does not parse is renamed to `sessions/sessions.sealed.bad`, replacing an older one, so only the latest bad file is kept, and never deleted. The core starts with an empty store, imports once as above, and the host posts one notice, "Pairings could not be read; check the app". A store sealed by a Keystore key that is gone is removed, as the iroh key makes a new identity in that case. A store with a field missing opens; a Keystore error that is not corruption fails the start and touches nothing.

## Host tests

The unit tests run anywhere. The sidecar tests need a `hand-iroh` binary built from `iroh/` in this checkout. They start that real sidecar against a fixture Board on loopback; no phone, fleet, Board database or Luvus server is involved, and every key and secret is made by the test.

Build the sidecar from `iroh/` beside this folder, then run the workspace:

```sh
(cd ../iroh && cargo build --locked --bin hand-iroh)
HAND_IROH_BIN="$PWD/../iroh/target/debug/hand-iroh" cargo test --locked --workspace
```

Without `HAND_IROH_BIN` the 59 sidecar tests print a skip and return; CI always sets it. The suite proves a proxied HTTP request and response, half-close in both directions, stream codes 3, 4 and 5, connection codes 1 and 2, the 13th stream waiting, a stream opening only on its first byte, a Board that answers before it closes while the phone still sends, and a wrong secret ending a stream with no byte, the live stream (each SSE event crossing with the API's own JSON, the 35-second watchdog reconnecting for a fresh snapshot, and four feeds plus one live holding five of the twelve streams with a thirteenth waiting), and one HTTP request through the dialer (its single cookie, CSRF token and body, a chunked answer, a 304 and an error answer, a full laptop returning busy at the deadline, and the errors a Board or the sidecar can cause: lost, timed out, too large, malformed, no longer paired and the Board not running). It also covers the backoff after a full laptop, the sealed key's lifecycle (one key across runs with no temp file left, a create over an existing blob replacing it, a missing Keystore key making a new identity, a changed Keystore key and corrupt ciphertext stopping with an error and leaving the file alone, a Keystore error at the probe or at open that is not mistaken for a missing key, a plaintext of the wrong length, an interrupted rotation leaving no blob, ciphertext and folder permissions, and the plugin error codes checked against the Kotlin constants), the port kept across a restart, the relay hint, the pairing link parser over the contract's 13 valid and 73 invalid vectors, the open link parser over its 6 valid and 61 invalid, the confirmation text, the open URL, the pending link kept against a second one, the iroh key checked before the other fields, the clear-before-forget order of a second laptop, and a relaunch with an unreachable saved laptop failing with the laptop kept and named in the message.

The workspace runs 206 tests, 59 of them against the sidecar. Measured on 2026-11-07 on the same host, debug builds through loopback: slowest time to first byte of three proxied requests 70-76 ms; the first stream after a full laptop connected 1.10-1.21 s after the request, which is the 1-second backoff. The camera preview, the permission dialog and the native scanner overlay are device-only; the host tests do not open a camera.

The standard checks are:

```sh
cargo fmt --all --check
cargo test --locked --workspace
cargo clippy --locked --workspace --all-targets --target aarch64-linux-android -- -D warnings
actionlint
```

`cargo clippy` for the Android target needs the NDK's C compiler, as ring compiles C:

```sh
toolchain="$NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin"
export CC_aarch64_linux_android="$toolchain/aarch64-linux-android24-clang"
export AR_aarch64_linux_android="$toolchain/llvm-ar"
export CARGO_TARGET_AARCH64_LINUX_ANDROID_LINKER="$toolchain/aarch64-linux-android24-clang"
```

## Alert feed

`core/src/alerts.rs` polls `GET /<fleet id>/alerts/feed` for one fleet and hands each alert to an `AlertSink`. It needs neither an Activity nor Android: a `CookieSource` supplies the cookie header (the core's `Sessions`), a `Dialer` the connection, a directory the cursor. `paired_fleets` lists the fleets whose `hand_phone_<fleet id>` cookie the header holds; `Feed::run` is one loop per fleet and returns a `Stop` only when the phone is no longer paired or Forget ran. It is no longer paired when the `CookieSource` reports no cookie header at all or the header holds no cookie of that fleet (`CookieGone`; no request is sent, the cursor is deleted and `AlertSink::unpaired` is called with the last fleet name it saw), or when the Board answers 403 with an `X-Hand-Error` that says so (`BoardGone`). The first `BoardGone` keeps the cursor and calls `unpaired`: Hand answers the same 403 for a database error, and a first poll never replays the alerts raised meanwhile. A run started after one (`refused_before`) that is refused again deletes the cursor and stays silent; a 200 clears that memory. A `CookieSource` that fails to read is retried with backoff.

`Feed::forget_cursors` bumps a generation under a lock and deletes every cursor. `run` reads the generation when it starts, and checks it before each `AlertSink::alert`, before either unpaired notice (the cookie read and the Board's answer both block) and, under the same lock, before it writes the cursor. A poll in flight when Forget lands (the sink blocks inside JNI and `abort` takes effect only at the next await) therefore posts nothing more and leaves no cursor. The alert being posted at that moment still shows.

- One request per poll, `wait=25`, `Connection: close`, only the fleet's own cookie, no byte of it logged or stored. The request's send side is finished at once; the sidecar half-closes the Board's side and the Board keeps a held request, as its contract says.
- A poll fails, and backs off 1, 2, 4, 8, 16, 30 s with 25 % jitter (the dialer's `Backoff`), on a status other than 200, a 403 or 404 that does not say the phone is no longer paired (an older Hand without the feed, a transient Board error), a reset or close, no complete answer within `wait` plus 10 s, a 200 head that is not HTTP/1.1 with one `Content-Length` of at most 64 KiB and no `Transfer-Encoding` (a refusal may carry one), a body that is short, or a body that is not the contract's answer (another fleet, version other than 1, more than 50 alerts, a replay on a first poll, a seq that does not grow, a cursor that is not the last seq, a cursor above `after` with no alerts). A success resets the backoff. Code 1 takes the dialer's busy path; code 2 closes an idle connection and the next poll dials again.
- Two polls are never less than a second apart, so a Board that ignores `wait` cannot be hammered. An answer with `more` and alerts is followed at once without `wait`.
- The cursor is `<dir>/<fleet id>.cursor`, written by rename after the sink has posted every alert of the answer, so a crash repeats alerts and never loses one; the next request is the Board's receipt. A missing or unreadable file is a first poll, which never replays history. A database the Board recreated answers a lower cursor with no alerts, and the app restarts from it.
- Text is cut to 200 characters with control characters and line and paragraph separators replaced by spaces and format characters (bidirectional controls, zero-width characters) removed; an alert whose text is then blank is skipped, and the cursor still moves past it. A `link` survives only when `parse_open` accepts it and it names the polled fleet; otherwise the alert is shown with no link.

The host tests run the real sidecar against a fixture Board that logs every request and answers a script: alerts and the cursor, `more`, the empty heartbeat with a 1-second wait, 503, 429 and 500 backoff, a connection the sidecar closed with code 2 and dialed again, a 403 that says the phone is no longer paired (with a `Transfer-Encoding`) stopping the fleet with its name, a missing cookie header and a missing own cookie stopping it with no request, an unreadable cookie store retried, other 403 and 404 answers backed off with the cursor kept, a sink failing on the second alert of a batch leaving the cursor and repeating the request, an answer over 64 KiB that is valid JSON refused, the gap after an immediate answer, the cursor across a restart, the supervisor (a fleet starting when its cookie appears with one loop only, forgetting stopping the loops and deleting the cursors with no notification, forgetting while the sink is posting leaving no cursor and no further alert, a refused fleet not started again until its cookie changes or returns, a changed cookie value starting a refused fleet at once, a refused fleet tried again after 30 scans with one notification and its cursor deleted only by the second refusal), and a hostile feed (an oversized body, a short body, another fleet's answer, a pairing link, another fleet's open link, a long text with a line break). The unit tests run the contract's alert exchanges from the vectors, a table of answers that break it, the cookie parsing, the request bytes, the response head and the cursor file.

## Alerts on the phone

`AlertService` is a foreground service of type `specialUse` with one small permanent notification. It starts the core through `Core.start`, which calls one JNI entry (`host.rs`), and asks `Core.watch` to run the supervisor: every 10 seconds it reads the session store's cookie header, runs at most one `Feed` loop per `hand_phone_<fleet id>` cookie, and keeps a loop that ended stopped with the cookie value it ended on. A stopped fleet starts again when its cookie value changes (a new pairing) or goes away and returns; a fleet the Board refused is also tried again after 30 scans (5 minutes) with no second notification; a fleet Forget stopped waits for a new cookie. The Companion is a process-wide singleton: `HandActivity` and the service both call `Core.start` and the second call is a no-op. A sticky restart after the system killed the process starts the core with no Activity, restores the saved laptop and goes on polling; the A0 spike failed there because only the Activity started the core.

- Notifications: four channels, `service` (low), `decisions` (high: `decision.asked` only), `needs` (high: `attempt.blocked`, `attempt.limited`, `attempt.failed`, `supervisor.blocked`, `supervisor.limited`, `supervisor.stalled`) and `progress` (default: every other kind). A channel's importance is fixed once a phone has created it, so the list was settled before the first install: the user can mute stuck-work noise and keep decisions. A newer alert on the same item sounds again, so a `needs` alert that replaces a `progress` notice for the same attempt is heard; a replay after a crash between the post and the cursor write sounds twice. The text is set as plain text, never through `Html.fromHtml`; the lock screen shows "Hand: alert". The tag is `<fleet id>/<ref>`, so a newer alert on an item replaces the older. A tap opens the `handboard://open/…` link the core accepted, through the existing `open_link` path, or the app when the alert has no link.
- The sink returns after `NotificationManager.notify` has run. When notifications are off for the app it fails, so the cursor stays and the Board's receipt does not advance.
- `HandActivity` decides at each `onResume`, and after a native pairing, with `alertStep` (`AlertGate.kt`). Nothing paired: nothing. Permission granted, or Android 12 and older: it starts the service, only while the Activity is at least STARTED and the service is not running (a refused start, which Android 12 and up gives a background caller, is logged and not thrown). Permission missing and never asked: it asks once from the Activity. The "asked" flag is written after the dialog's result, so a process that dies mid-dialog asks again. Asked and still missing: the fleet list shows "Alerts are off. Allow notifications in Settings.", and a tap on it opens the app's notification settings; allowing it there starts the service when the app is next resumed. The flag is `HandActivity`'s own.
- Forget this laptop, and pairing a different laptop, stop the loops and delete every cursor, clear the web view's cookies and stop the foreground service (`AppHost.stopService`) before the user sees the result. No "No longer paired" notification follows. The service starts again the next time `HandActivity` resumes with a paired fleet.
- No boot start. A reboot, an update or a user Stop leaves alerts off until the app is opened.
- Back at the fleet list finishes the Activity and leaves the service and the process running. A swipe in Recents should end only the Activity, not the process (device-only, not measured). `AlertService.onTaskRemoved` and the sticky restart stay as the fallback.
- After a swipe the restarted process gets `onTaskRemoved` instead of `onStartCommand`, so `begin()` runs from both.
- Not built: the permanent notification's connection states ("Reconnecting…", "Can't reach your laptop"), a battery-optimisation request and a wake lock. Doze behaviour is unmeasured.

The JVM tests cover the kind to channel map, the tag, the title, the tap URI, the visibility and the rule that a notice is acknowledged only after it was posted (`deliver`) as pure functions. The JNI methods of `host.rs` build only for Android and have no host test; the core tests cover what the core does with a sink that fails. The host tests cover the supervisor. Everything that needs the JNI bridge, `CookieManager`, `NotificationManager`, the permission dialog or the service lifecycle is device-only.

Read at the device run, not settled by tests:

- `Core.start` runs on the UI thread (`HandActivity.onCreate` needs the Companion before its first poll, and a background start in the service would race `unwatch`). It does the Keystore unseal and the iroh and proxy binds: time it and look for ANRs.
- `CookieManager.getInstance()` loads the WebView provider the first time the jar is read: the first `Sessions::header` after a start with no store, from the fleet list's background poll, the feed or a pairing, never the UI thread. Measure that process's memory with and without the Activity.
- `CookieManager.getCookie` from a tokio thread with no Activity, the sticky restart then the Activity, tombstones after Back, Recents and SIGKILL, Doze, and notification permission off and on.

## Board client

`Core.request(fleet, post, path, csrf, body)` runs one HTTP exchange through the core's `Companion::request` and blocks, so call it off the main thread. `path` starts at `/api/v1/` and omits the fleet, which the core prefixes. The cookie comes from the session store, as the alert feed's does. It returns one JSON string: `{"status","body"}` for an answer, or `{"failure": name}` for a refusal, with the names of `RequestError::name`. Only a missing cookie for the fleet answers `unpaired`, which the app turns into "Pair again". A fleet, path, CSRF value or body the core refuses answers `malformed`, and a panic in the call answers `malformed`. A null from the JNI call is `malformed` on the Kotlin side.

`BoardClient` (one per fleet, over a `Transport`; `CoreTransport` is the real one) reads Hand's envelope, refuses any `version` but 1 and any answer whose `fleet.id` is not its own. It throws a `BoardError` for the core's failures (busy, no laptop, unreachable, unpaired, lost, Board not running, timed out, too large, malformed) and for an API error, told by its `code` and never by its status. `status()` keeps the envelope's cursor, sends it back as `?since=` and answers `Poll.Unchanged` on a `304`. A poll that fails to reach the Board keeps the cursor; a `bad_cursor` refusal drops it, because the Board signs cursors with its token and a recreated token rejects every old one, so the next poll starts over.

`write` reads the CSRF token from `GET /api/v1/session` once and keeps it in memory. A `csrf_required` refusal means the Board changed nothing, so it reads the token again and sends once more; any other refusal is final and no write is sent twice. A `200` answers `Write.Updated` with the fresh object, or `Write.Accepted` for `{"accepted": true}`, which means the action went in and the read-back failed: the screen refreshes and never sends again. A `200` that cannot be read is `AppliedUnread`, never a "not sent" error. `Lost` and `TimedOut` on a write can follow bytes that reached the Board, so a caller treats them as maybe sent, never as not sent; only an API error (the Board refused), `Busy`, `Unreachable`, `NoLaptop`, `Unpaired` and `BoardNotRunning` say the Board changed nothing, and `Malformed` and `TooLarge` can follow an answer. The models ignore unknown fields.

The JVM tests read Hand's golden answers from `app/src/test/resources/api`, copied from `internal/board/testdata/api`: the `Status`, attempt and report answers they decode. Copy them again when Hand changes them; a report answer must keep every field the model requires. The JNI method has no host test.

`Core.live(fleet)` starts one `Live` loop for the fleet on screen (`core/src/live.rs`): `Dialer::open_with` writes `GET /<fleet>/api/v1/events` and keeps the send half open, and the head must be `200` with `Content-Type: text/event-stream`. `core/src/http.rs` decodes the chunked body into server-sent events one at a time (name and data lines, any split, at most 256 KiB an event) and `Live` forwards each as `AppHost.live(fleet, topic, json)` with the API's own JSON untouched; `AppHost.link(fleet, state, since)` carries `live`, `reconnecting`, `stopped` or `unpaired`, with `since` the last healthy byte in epoch milliseconds. No byte for 35 seconds is a lost stream (the pages' own rule), `Dialer::open_with` is bounded by the same watchdog so a stream waiting on the connection's permit cannot hang the loop, and the loop reconnects with the dialer's backoff, 1, 2, 4, 8, 16, 30 seconds with 25 % jitter, reset after a stream lasted 10 seconds. A reconnect sends no cursor: Hand's merged stream has no `?since=`, so the Board answers `hello`, `status` and a snapshot `timeline` again, and a client treats a `timeline` whose `from` is not the `to` it holds as a gap. `Core.unlive` cancels the loop and drops the stream, so leaving the fleet frees its one of the 12 connection streams. A loop that ends emits a terminal `stopped` link, or `unpaired` for a revoked session; the view model falls back to the poll on `stopped` and stops on `unpaired`. `LiveBus` keeps the last 64 events and drops the oldest (a dropped event is a snapshot, a hint, or a delta with a gap check) and tracks each fleet's timeline `to` to flag a gap. `FleetModel` applies a `status` event inline with no request, resets its failure count on a `status` event or a `resync` that names `status` (a successful head alone is not evidence), counts `deny_at` against the stream's `now` offset, re-reads `status` on a `resync` with the `status` topic, and re-reads `tasks` on a `touched` with the `tasks` topic while the Tasks tab is open; the 5-second poll is the fallback when `meta.features` lacks `events`, after three `reconnecting` links without a `status`, or on a `stopped` link. `Core.request` sends `/api/v1/meta` at the host level with one fleet cookie, which is how the feature check works.

## Native screens

`HandActivity` is the launcher. The manifest has one activity, `HandActivity`, with `MAIN`/`LAUNCHER` and a filter for each `handboard` host (`c` and `open`); `ManifestTest` pins that. It shows one row per fleet in the core's session store (`Core.fleets` returns the fleet IDs), reads each fleet's `status` through `BoardClient` every 5 seconds while it is in front, and shows the laptop's short ID from the `laptop` file. Nothing is polled in the background; the alert service still is the only thing that runs there. When `Core.start` fails (a Keystore error, say) the Activity shows "Hand can't start" with the core's message in place of the list.

`FleetsModel` turns each answer into display-ready rows and one band. A `304` keeps the row and moves its time. Busy shows "Your laptop is busy. Trying again."; an unreachable laptop (also lost and timed out) shows the band with Retry and turns the rows to their last state "at HH:mm" with an "unknown" pill; code 3 twice shows "Your laptop answers, but the Board is not running."; an unpaired fleet shows "No longer paired" on its own row. The first failure ends the round, so a dead laptop is asked once and not once per fleet, and "since" counts from the first failure of an outage. An error that is not about the laptop (an answer that cannot be read, an unsupported version, a refusal) shows "The Board's answer could not be read." on that fleet's row only, and the round goes on. Add fleet opens native pairing (see Native pairing). The menu holds **Forget this laptop**, which asks first ("This phone leaves 4d92…6ed6 and its 2 fleets. Alerts stop."), then calls `Core.forget`: the alert loops forget their fleets and delete their cursors, the store clears the web view (`AppHost.clearCookies`: DOM storage, the HTTP cache and the cookie jar, on the main thread) and then writes itself empty, the service stops, and the `laptop` file goes. Pairing a different laptop clears the web view the same way. A failed Forget starts the alert service again, since the pairings are still there. A failed clear also stops the alert supervisor, so a fleet it parked is polled again when the service restarts; a web view that cannot clear its storage fails Forget instead of crashing. A failure shows "Can't clear the saved login. Try again." above the list and the list is read again, so nothing the core still holds is hidden. The full "This phone" screen is A19.

Tapping a paired fleet opens `FleetScreen`: the top bar (back, the fleet's name and supervisor pill, the fleet's tint rule) and the tabs Needs, Tasks and Chat. Needs is the first tab when anything waits, Chat otherwise, and a tab the user picks stays picked while the screen is open; tapping a fleet in the list picks Needs at once if the tab was Chat and something waits; nothing flips the tab later. With the laptop down on the first open the screen shows the error band and the fleet's name, with `Loading…` as its body. The Needs tab is announced as "Needs, N waiting" and Chat as "Chat, supervisor working" while `agent_state` is `working`, and band headers are headings; the label text is tested on the JVM, the semantics themselves are not. `FleetModel` follows the fleet's `/api/v1/events` stream while the screen is in front (needs, pill, working dot and the link band), and falls back to polling `status` every 5 seconds when the host's `meta.features` lacks `events`, when three `reconnecting` links arrive without a `status` event or a status-targeted `resync`, or when a `stopped` link ends the stream. Link events carry their fleet and are filtered by it, so a late callback from a fleet that just closed cannot touch the next fleet's band or state. The poll uses the same bands and errors as the list, and groups adjacent `needs.items` by their word, in the order Hand sends them (blocked and worker share BLOCKED); `needs.more` shows as "N more waiting". The first card is open, a report or the blocked supervisor; a tap opens another in place and closes the first, and when the open one leaves the first card opens. A report opens to its excerpt and Mark read, which sends `POST report/{rN}/ack` and reads `status` again; the button stays off until the report leaves the list. A 409 shows "rN is already read.", a lost or timed-out ack "Not sure rN was marked read.", any other refusal "Could not mark rN read."; the notice shows in a bar above the tabs, whichever card is open, and goes after 4 seconds. An excerpt that Hand cut also offers Open web Board. A failure shows its row and does nothing until its screen exists. Any other kind, the Tasks and Chat tabs and the fleet's menu (**Open web Board**) open the web fallback on that fleet's first page.

`TaskScreen` reads `GET /api/v1/task/{tN}` once when it opens, with the envelope's cursor on later
reads, and shows the Board's phone column: the open decisions and unread reports waiting on the
task first, then Delivered, Sub-tasks, Plan, Reports, Decisions, Attempts and Events. A section the
answer leaves empty has no heading. The head shows the task's title, its `status` word with `pill`'s
tone, and the project, parent, waits and updated time; `goal` is lead text, not a section. A report card shows its ref, summary, "from aN", state and time,
and **Mark read** until `acked_at` is set, when it reads "read by …" from the `ReportDetail` the
ack answered. A second tap while the ack is in flight is ignored; a refusal keeps the button and
says so under the card, and nothing on the screen is cleared. A task, sub-task, decision, attempt or report ref opens
its native screen; a repository path and a delivered file with a `/doc/...` href open the web
fallback (a file outside the fleet stays plain text), and a PR or other link opens the browser. `LiveBus` hints that name this task or one of its sub-tasks, or a
report, decision or attempt the detail holds, re-read it; a hint naming anything else does not, and
a hint during a read queues one more read for when it finishes. A laptop whose `meta.features` lacks
`events` polls the detail every 5 seconds while the screen is shown, and the read stops when the
screen leaves. The fleet route keeps its
saved state, so Back from a task returns to the Tasks tab with its list and scroll where they were. A `404` shows the Board's own message with **Back**; a first failure shows **Retry**;
a failure after the detail was shown keeps it.

`ReportScreen` reads `GET /api/v1/report/{rN}` once when it opens, with the envelope's cursor on
later reads; a `304` keeps what is shown and a `bad_cursor` drops the cursor and reads full. The
head is the summary in the Board's title, the state chip, "from aN" with the attempt's native route,
the time, and the task ref with its title. The body is the Markdown subset below; then Delivered,
where a PR opens the browser and a file opens the web fallback. **Mark read** is docked at the foot
and posts `report/{rN}/ack` under the same single-flight, `AppliedUnread` and refusal rules as a
task's card; once `acked_at` is set the dock reads "read by …" from that answer. `touched` and
`resync` hints naming this report, or `reports` with no refs, re-read it, the fleet filter drops
other fleets' events, and the read stops when the screen leaves. A `404` shows the Board's words
with **Back**; a first failure shows **Retry**.

The Markdown parser follows `internal/markdown`: paragraphs with a line break inside, `#` to `######`
heads drawn at the Board's two levels, unordered and numbered lists with six levels of nesting that
keep their written numbers, inline code, fenced code on the panel fill behind a **Copy code** button,
quotes, tables whose columns align and scroll sideways, emphasis, strikethrough, safe `http`/`https`
links, refs and paths. HTML, images and anything the parser cannot close stay text. A ref opens its
native screen; a path opens the fleet's `/doc/` fallback until N8; a link opens the browser. The
table lives in `refTarget` and `pathTarget`, and the parser's cases are the Board's own test table.

`HandTheme` holds the Board's colours (static, light and dark, no dynamic colour), its five type sizes, its radii and the icons as vectors, including the terminal's own background and ink. It sets every field of the Material colour scheme from those colours, and `HandThemeTest` pins each one, so no component can show Material's baseline purple. Each fleet's mark takes the Board's tint, the FNV-1a hash of the fleet ID modulo 12.

A decision row in Needs opens in place to its choice list, and **Open dN** (or a `decision.asked` alert) opens `DecisionScreen`. The options come from the API's `options`; only the question is cut from `body`, which still holds the option lines, by dropping the lines outside code fences whose key the API listed. Sending an answer posts `{"choice": <option value>, "note": <note>}`; an `other` option, and a decision with no options, need a note. The Board's `409` and `AppliedUnread` answers both end in the answered state, and a lost or timed-out answer says it may have been sent and reads the decision again. The note is kept per fleet and decision in the `drafts` preferences file (private to the app, never backed up) and removed after a successful send. The alert's tap for a `handboard://open/<fleet>/decision/<dN>` link reaches `HandActivity` as a `VIEW` intent (`launchMode="singleTask"`, so a tap while it is open arrives in `onNewIntent`) in debug and release builds alike.

A blocked supervisor's item is a card: its last lines in the terminal box, **Screen misread?** with **Type queued messages anyway**, and one button per option of `supervisor.ask` in the Board's order, or the fixed `enter`, `esc`, `up`, `down`, `1`, `2` and `3` keys when there is no ask. Every press posts `{"keys", "revision", "digest", "after", "ref"}` from the latest `Status.supervisor.keys`; its `Status` answer is applied first, the row reads "Sent 2 · waiting…" and a receipt names the supervisor for 4 seconds. A `409` reopens the card on the refreshed screen and says nothing was sent. A `deny_at` on the blocked item counts down locally, one second at a time, and when it passes the screen says it was denied and refreshes.

`app/src/test/snapshots` holds the stored images of the fleet list and of the fleet list before its first answer (`loadingLight`, `loadingDark`), the unreachable band, Decision with a recommended option, an `other` option with its note and its answered state, Needs with a decision open in place, Needs with several bands and one open report, Needs with only rows, a cut report, the blocked supervisor with its ask options, its fixed keys, its countdown and a stale refusal, a tab that has not loaded (`notLoadedLight`), a first frame after a failed request with the band above `Loading…` (`failedFirstFrameLight`, `failedFirstFrameDark`), the live band while the stream reconnects (`reconnectingLight`, `reconnectingDark`) and after it gives up (`liveLostLight`, `liveLostDark`), a tapped tab before the first answer (`tappedBeforeAnswerLight`, `tappedBeforeAnswerDark`), Task with its waiting rows and every section, the same with nothing waiting, a sub-task, a task with
sub-tasks, an inbox task, the card after Mark read, its loading frame and its error with Retry, each
light and dark, a report with a list, a code block, a table and a quote before and after Mark read,
its long body, its marking frame, its loading frame and its error with Retry, each light and dark, the web Board before its page loads (`webLoadingLight`, `webLoadingDark`), the web Board after its open times out with Retry (`webTimedOutLight`, `webTimedOutDark`), the web Board when the phone is not paired with the fleet (`notPairedHereLight`, `notPairedHereDark`) and when the saved login is refused (`cookieRefusedLight`, `cookieRefusedDark`), Needs all clear and the tabs, light and dark, rendered by Paparazzi on the JVM with no device. `./gradlew :app:recordPaparazziUniversalDebug` replaces them; commit them with the change that made them differ, and look at each before you do.

## Native pairing

Add fleet (screen 2), the scanner (3) and the confirmation (4) are Compose screens of `HandActivity`: `PairScreens.kt` draws them, `PairModel` holds their state, and the core does the pairing. The code, the device secret and the pending link stay in Rust. Kotlin hands the link in (`Core.offer`) and the phone's name (`Core.pair`), and gets back the laptop's short ID and `replaces`, or the fleets' IDs and names, or a failure name with its sentence. `Core.cancel` drops the pending link. Nothing else crosses JNI, and no sentence about the code, the link or a secret is logged.

- `Core.offer` parses the link with `core/src/link.rs`, so a rejected link shows the contract's sentence under the field, and a link that arrives while another is open keeps the first and says so. The confirmation shows the laptop's short ID. When the link names another laptop than `<app data>/laptop` (`Offer.replaces`), it adds the amber notice, built from the fleet names the list already holds, and the button reads **Replace and pair**.
- `Core.pair` takes the pending link out of the core (a cancel or a new offer while it runs cannot redirect it, and a failed request puts the link back only when the pairing is still in flight and no newer link arrived), and sends `POST /<fleet id>/api/v1/pair` with `{code, name}` on a second dialer for the new laptop (`Request::pair`: no cookie, no CSRF). The main dialer, the old fleets, their feeds and their cookies are untouched until the Board has answered. The name is checked as Hand checks it before a byte goes out (no control character, at most 40 characters once trimmed, counted as characters; the trimmed name is sent). The answer is accepted only when `version` is 1, it names at least one fleet once each, each `cookie_name` is `hand_phone_<fleet id>` and each `cookie_value` matches `^[1-9][0-9]*\.[0-9a-f]{64}$`; anything else is "could not be read" and nothing is stored. A fleet's name from the answer loses control characters and is cut to 40. A code for several fleets adds them all. The screen's Cancel, Close and Back are off while the request runs, and the model ignores a cancel or a link meanwhile; a `handboard://c` link that arrives meanwhile is kept by the Activity and offered when the pairing ends (when the pairing failed and its confirmation is still open, the core answers that one is pending).
- The commit (`Sessions::pair`) is one sealed write. After the rename it stops the old feeds (`Supervisor::forget_all`) when another laptop replaced the old one, still under the store's lock so no feed sees the new store first and posts "No longer paired", and then, outside the lock, clears the jar. A pairing with the same laptop keeps its other fleets and takes a new secret for a fleet it already held; no later import runs, so a stale jar cookie never replaces a stored secret. If the write fails the staged file is removed and nothing changed, feeds and cursors included. Once the write is made the pairing is made: a jar that cannot be cleared, a folder that cannot be synced or a `laptop` file that cannot be written mark the pairing unfinished (the fleet list says so, and only Forget or the next pairing that completes clears the mark) and the app still points at the new laptop. A Forget that lands before the commit finishes wins: the pairing is refused with `no_pending`, and neither the store nor the `laptop` file keeps it. Because the jar is cleared as a whole, the web fallback sets the fleet's cookie from the store before it loads (see Web fallback), so a native pairing never asks the web Board to pair again.
- `HandActivity` starts the alert service (see Alerts on the phone), including right after a native pairing from an empty state.
- Errors, each with its sentence from the contract: wrong, expired or used code, every code cancelled, too many wrong codes from this phone, the laptop unreachable, the Board not running, and the laptop busy ("Try again in a moment", since the dialer has already retried to the request's deadline, so "Trying again" would be untrue). A connection that broke tells the user the code may be spent. An unreadable answer or a write that failed before the pairing was made says that the laptop may hold an unfinished device to remove on its Phone page. Every other refusal gets one general sentence.
- `FLAG_SECURE` is on for the whole flow. The scanner is `PairScreens.kt`'s `Scanner` over ZXing's `BarcodeView`, with the states asking, refused (with Settings), failed and timed out after 15 seconds. The camera, the permission dialog and the intent are device-only.
- A `handboard://c#…` intent reaches `HandActivity` in every build type. The link is read once at launch and on a new intent, and the Activity's intent is replaced so a recreated Activity does not offer it again.

## Open links

`handboard://open/<fleet id>/<page>[#rN]` opens one Board page. A second intent filter takes host `open` beside the pairing host `c`. The page is nothing, `task/tN`, `decision/dN` or `attempt/aN`, with N of 1 to 9 digits and no leading zero, and `#rN` only after `task/tN`. The fleet ID is `f` and 12 lowercase hex digits. The link has no query, user, port, trailing slash, upper case, percent escape or extra segment. The link holds no secret.

`HandActivity` takes a link from its launch intent or `onNewIntent`, from a notification tap or from any other app, and `landing` (`Landing.kt`) routes it. A pairing link (host `c`) goes to Confirm. Anything else goes to `Core.open`, which runs `parse_open` and then requires the fleet to be one the store holds (`open_paired`): the same rule as the alert feed, so no link opens a pairing link or another fleet's page, and no link touches a cookie, the laptop or a pending pairing.

| Link | Shows |
|---|---|
| `handboard://c#…` | Add fleet, then Confirm; "Finish or dismiss the current pairing first" when one is pending |
| `decision/dN` of a paired fleet | The Decision screen of that fleet |
| A paired fleet with no page | That fleet's native screen |
| `task/tN[#rN]`, `attempt/aN` of a paired fleet | That fleet, with the web fallback open on the page |
| A link the grammar rejects, or a fleet the phone is not paired with | "This is not a link Hand can open." at the bottom of the screen the user is on, for 4 seconds |
| Nothing paired | "Pair this phone with your laptop first.", the same way |
| Any open link while a pairing confirmation is open | "Finish or dismiss the current pairing first", the same way: the confirmation stays and the link is dropped |
| A busy pairing request | Kept, and applied when it ends |

The host tests cover the parser over the five valid forms and 61 hostile ones, a fleet ID that fails on its tail alone, the `handboard://open` routing prefix and `open_paired` with no fleet, an unpaired fleet and a pairing link. `LandingTest` covers each landing and the refusals on the JVM. The intent handoff and the fall-through are device-only.

## Web fallback

A page with no native screen opens in `FallbackScreen` (screen 16): a bar that says "Web Board" with the fleet's name and the page, a line that says why, and Close, over a plain `WebView` on the proxy's origin. It is temporary: each native screen that lands removes a reason to open it.

- Before the page loads, `openBoard` asks the core for that fleet's proxy port and cookie (`Core.fallback`, from `Sessions::cookie`: one `hand_phone_<fleet id>=<value>`, never another fleet's) and sets it in the web view's jar for `http://localhost/` with `Path=/`, `Max-Age` 90 days, `HttpOnly` and `SameSite=Lax`, the attributes the Board gives it, and waits for the call to finish. So a native pairing, which clears the jar, never asks the web Board to pair again. The cookie goes only to the Board's own pages through the proxy; the contract's other uses of it (the feed, the API) are the core's. A fleet the store lacks shows "This phone is not paired with this fleet." and sets nothing.
- The open is bounded by `OPEN_MILLIS` (30 s), the same deadline as the core's `REQUEST_TIMEOUT`: that is the longest one Board request may take, so a cookie read or a jar write that outlasts it is already a dead path and must not leave `Loading…` up. On the deadline the body shows "The web Board took too long to open. Try again." with Retry; Close stays in the bar. Retry changes the effect's key and runs the whole open again, and the timed-out attempt is cancelled, so a result that arrives later is dropped and no `WebView` exists to attach. The bridge to `CookieManager` is a cancellable suspension, so cancellation releases the worker at once instead of leaving it parked. The flush runs on `Dispatchers.IO` after the answer, before `storeCookie` returns, never on the main thread, so it cannot hold the deadline off. An answer that arrives after the deadline skips that flush, and nothing else: its `setCookie` has already written the cookie to the shared jar, which the platform may persist without a flush.
- The jar is the web view's own. The store reads it once, on the first `Sessions::header` after a start when `sessions/sessions.sealed` is missing; setting this cookie keeps the Board's page logged in across a fallback restart and never becomes a pairing.
- JavaScript and DOM storage are on; file and content access are off; there is no JavaScript bridge. `navigation` decides each navigation: the proxy's origin (scheme `http`, host `localhost` in any case, the proxy's port) stays; `http`, `https`, `mailto` and `tel` elsewhere go to the system browser; every other scheme (`file`, `content`, `intent`, `javascript`, `handboard`) is dropped. A form post, a subresource and the page's own script requests are not seen by `shouldOverrideUrlLoading`.
- A cookie the web view refuses to store gives "Can't set the saved login for the web Board. Close this and try again." and no page. Only a main-frame navigation made by a tap can leave for the browser; a redirect or a frame is dropped. If the web view's renderer dies the screen closes. File attach and JavaScript dialogs (`alert`, `confirm`) do not work in the fallback yet.
- Back goes back in the page's history and then closes the screen. A page that cannot be reached shows the web view's own error page under the bar; there is no native band for it yet.
- Shared with the native side: the loopback proxy and its dialer (`Companion`, started by `Core.start` and pointed at the saved laptop there), the web view's cookie jar, `network_security_config.xml`'s one cleartext host `localhost`, and `Fallback.kt`'s `navigation` rules. The fallback has no bundled page and no JavaScript bridge; a plain `WebView` handles `window.open` and `target=_blank` through `shouldOverrideUrlLoading`.

## Updating from a build before A4

The package, the signing key, the iroh key, the `laptop` file and the web view's jar stay. On the first open after the update `HandActivity` starts, the first fleet-list read finds no `sessions/sessions.sealed`, imports the `laptop` file's laptop and the jar's shaped `hand_phone_` cookies once, writes the store, and the list shows one row per fleet, first by ID and then by the name the Board's first `status` answer gives. The alert service starts when notifications are allowed, as before. Notifications that were refused once are asked once more (the flag is `HandActivity`'s own). A fleet imported that way has its name empty until `status` answers and is not dropped later when the jar changes, because no later open reads the jar; Forget is the way to remove it. The old home-screen icon points at `.MainActivity`, which no longer exists: an icon left over from that build opens nothing and should be removed in favour of the app's own (not tried on a phone).

## Key, port and data

- The phone's iroh identity is 32 random bytes made on first run. The app seals them with AES-256-GCM under a non-exportable Android Keystore key (alias `io.github.atqamz.handcompanion.iroh`, hardware-backed when the phone has a TEE or StrongBox, `setUserAuthenticationRequired(false)`) and stores only the IV, ciphertext and tag at `<app data>/iroh/companion.key`, mode 0600 in a folder of mode 0700. The key is never written in the clear, logged, copied or exported; the core owns the 32 bytes and the endpoint, and the Kotlin `KeystoreSealer` object only asks the Keystore to seal and open bytes, and the core reaches it through JNI. A reinstall removes the file and the Keystore key together, so the Board sees a new phone. If the file survives and the Keystore key is gone or permanently invalidated, the app makes and seals a new identity instead of falling back to raw bytes; a ciphertext that does not authenticate, a plaintext that is not 32 bytes, or a Keystore error at the alias probe or at open that is not a definitively missing key stops with an error on the first screen instead of replacing the identity; the legacy Keystore before Android 12 reports an outage and a missing alias alike, so a startup outage there can still rotate. A rotation unlinks the old blob and syncs the folder before it seals the new one, so an interruption leaves either a blob the current key opens or no blob, never one no key can open; the new blob is written to a `.tmp` file in the same folder and renamed over the path, and the folder is synced again after the rename. The rename is used instead of a hard link because Android denies `linkat` for an app's data directory: on a real phone a hard link failed with `EACCES`, so the key was never created and the app could not start. The app's data directory is private to the app and the manifest sets `android:allowBackup="false"`, so Android's backup does not copy the file; on Android 12 and later some manufacturers still migrate app files device to device, but the Keystore key never leaves the source phone, so a copied blob is unreadable and the app makes a new identity. A pre-PR development install kept a raw 32-byte `companion.key`; the first run under this code cannot authenticate it, unlinks it and makes a new identity, so the Board sees a new phone once and needs a re-pair.
- The Keystore round trip is device-only. CI has no Android Keystore, so the host tests drive the lifecycle with a fake cipher and check the plugin error codes against the Kotlin constants: they do not prove key generation, hardware backing, real AES-GCM sealing and opening, or that a real `KeyStore` probe reports a provider error instead of a missing alias. On a phone, `adb install -r` over the same build must keep the endpoint ID (the identity loads and opens), while `adb uninstall` followed by an install must show a new one (the file and Keystore key are both gone).
- The proxy listens on `127.0.0.1` and, when the phone has it, `[::1]`, because the name `localhost` resolves to either. Its port is chosen once from 49152 to 65535 and kept in `<app data>/proxy.port`; if the port is taken at launch the app picks and keeps another. A fixed port keeps the page's origin and the Board's drafts across a restart.
- The proxy never listens on a network address, adds no headers, and removes none. The proxy never reads the web view's cookies. Hand's `docs/companion.md` says the app must not read the web view's cookies, yet a phone upgrading from before A4 has its pairing only there; the core reads the cookie header from `CookieManager` once, when the store is missing, imports it into the session store, and seals it under the Keystore key, which Hand's document allows.

## Dependencies

- `iroh` 1.4.1 and `tokio` 1.54.1: the dialer and the runtime, pinned to the sidecar's versions. `src-tauri/src/host.rs` builds and owns the one multi-threaded runtime, starts it on the first `Core.start`, and runs every core future on it.
- `rand` 0.10.3: the proxy port and the backoff jitter; the standard library has no random source.
- `serde` 1.0.229: the session store's format and the alert feed's `Deserialize` derives.
- `serde_json` 1.0.154: the core reads the alert feed's and the store's JSON with it, its tests read the contract's vectors, and the JNI replies are JSON.
- `socket2` 0.6.5, tests only: sets `SO_LINGER` so the fixture Board can send the reset that becomes code 5.
- `jni` 0.21.1 and `ndk-context` 0.1.1, Android only: the JNI entry that starts the core from the service, the calls back into Kotlin, and the Android context that `netdev` reads; `nativeStart` initialises it once and nothing releases it.
- `org.jetbrains.kotlinx:kotlinx-serialization-json` 1.9.0 and its compiler plugin, Android: decodes Hand's JSON into typed models that ignore unknown fields. `org.json` is absent from JVM unit tests and gives untyped maps. 1.9.0 because the Kotlin compiler this build runs is 2.2.10, which cannot read the metadata of a later release.
- `androidx.compose:compose-bom` 2026.09.00 with `material3`, `androidx.activity:activity-compose` 1.10.1 and `androidx.lifecycle:lifecycle-runtime-compose` 2.10.0, and the Kotlin Compose compiler plugin 2.4.21, Android: the native screens. They add 8,549,266 B to the debug APK's entries (CI at this PR's head against CI at its base), 8,152,271 B of it dex, since debug builds are not minified. Compose needs JVM target 11, so the app module's `compileOptions` and `jvmTarget` moved from 1.8 to 11.
- `app.cash.paparazzi` 2.0.0-alpha05.1, tests only: renders the screens on the JVM, light and dark, and checks them against stored images, so a screen change is reviewed as a picture without a device. It is an alpha because its release notes name Android Gradle plugin 9 support, which this build needs (9.3.1); it never reaches the APK.
- `com.google.zxing:core` 3.5.4 and `com.journeyapps:zxing-android-embedded` 4.3.0, Android: the QR decoder and the camera preview that feeds it. They add 463,884 B to the debug APK; the two artifacts are 610,364 and 151,839 B, about 0.76 MB together. They need no Google Play services; the embedded library's own `core` is excluded so the decoder is pinned directly. A hand-written camera loop would be more code for the same result.

`Cargo.lock` holds every transitive version.

## Store text

F-Droid reads the store text from `fastlane/metadata/android/en-US/`: `title.txt` (at most 50 characters), `short_description.txt` (at most 80; no trailing dot is this project's rule, not F-Droid's), `full_description.txt` (at most 4000, F-Droid's simple HTML) and `images/icon.png` (512x512). Each release adds `changelogs/<versionCode>.txt` (at most 500 characters), named by the literal version code from `android/version.properties`. `android/scripts/check-store-metadata.sh` checks the limits in CI. There are no screenshots or feature graphic yet.

## Contract with Hand

The wire contract is [Hand's Companion contract](../docs/companion.md), with its test vectors in [`docs/companion-vectors.json`](../docs/companion-vectors.json). The app is in `android/` and the sidecar it dials is in `iroh/`, in one checkout, so a change to either meets the other's tests in the same pull request. `core/` reads that file by path from the same checkout, so `core/` builds only inside this repository, and `core/tests/vectors.rs` runs the parser over all 13 valid and 73 invalid pairing links and all 6 valid and 61 invalid open links as written. In short: iroh 1.4.1, ALPN `hand-board`, one bidirectional QUIC stream per loopback TCP connection, 12 open streams, one connection to a laptop, the phone's endpoint ID as its identity, and the close and reset codes 1 to 5. `AGENTS.md` carries the rules this repository follows.

## Roadmap

1. [x] iroh dialer in `core/`: keep the phone's key, dial the laptop's endpoint ID, and reconnect with the sidecar's documented close and reset codes.
2. [x] Loopback proxy and web view: listen on `127.0.0.1`, open one QUIC stream per TCP connection, and open the Board at `http://localhost:PORT`.
3. [x] Pairing: parse the pairing link, confirm the laptop, and pair natively over `POST /api/v1/pair`.
4. [x] QR scanner for the pairing link.
5. [x] A signed release build: tag, version code, R8, a release job with its key in a protected environment.
6. Distribution: an F-Droid repository.

## Media

Screenshots for issues and pull requests live on the `blob` branch of this repository, at `<PR number>/<file>`, and bodies link them as `https://github.com/atqamz/hand/raw/blob/<PR number>/<file>`. The branch is append-only: it refuses force-pushes and deletion, and it is never merged into `main`, which shares no history with it. It holds images only. Images from before the app moved into `android/` are no longer hosted, so a body that needs one uploads it here first.

A clone without it is lighter: `git clone --single-branch --branch main https://github.com/atqamz/hand.git`, or the same with `--depth 1`.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`AGENTS.md`](AGENTS.md). The project is under the [MIT license](../LICENSE).
