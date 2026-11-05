# Contributing

Hand's Android Companion is a personal project under the [MIT license](LICENSE). Changes from others are welcome, and the maintainer decides every merge.

- For anything beyond a small fix, open an issue first and say what you want to change.
- Send one pull request per change, and say in it what changed and why.
- Read [`AGENTS.md`](AGENTS.md), then run the three `cargo` commands it lists before you push. A change to `src-tauri/` or `web/` also builds the debug APK: see [Build a debug APK](README.md#build-a-debug-apk).
- Never commit a signing key, a keystore or a secret. Release builds are not made here yet.

## Sign off your commits

Every commit in an outside pull request carries a `Signed-off-by: Name <email>` line. `git commit -s` adds it. It says you wrote the change, or have the right to send it, under the [Developer Certificate of Origin 1.1](https://developercertificate.org), and that it may be distributed under the MIT license in [`LICENSE`](LICENSE).

## Review

CI must pass. A pull request from a fork waits for the maintainer to approve its CI run.
