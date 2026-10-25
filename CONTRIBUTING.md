# Contributing

Hand is a personal tool for one operator, under the [MIT license](LICENSE). Changes from others are welcome, and the maintainer decides every merge.

## Propose a change

- For anything beyond a small fix, open an issue first and say what you want to change. Blank issues are off, so use the [bug form](https://github.com/atqamz/hand/issues/new?template=bug.yml) for a change that is not a bug too. The dogfood forms are only for failed real-machine checks.
- Send one pull request per change. Say in it what changed and why.
- Read [`AGENTS.md`](AGENTS.md) and [`docs/spec.md`](docs/spec.md) first, then run the commands in [Development](README.md#development) before you push: `gofmt -l .`, `go vet ./...` and `go test -race ./...`.
- Report a vulnerability as [`SECURITY.md`](SECURITY.md) says, never in a pull request.

## Sign off your commits

Every commit in an outside pull request carries a `Signed-off-by: Name <email>` line. `git commit -s` adds it. It says you wrote the change, or have the right to send it, under the [Developer Certificate of Origin 1.1](https://developercertificate.org), and that Hand may distribute it under the MIT license in [`LICENSE`](LICENSE). Nothing checks it automatically, and a commit without it needs `git commit --amend -s`, or `git rebase --signoff` for several, before a merge.

The maintainer's own commits are signed with a GPG key, which is a different thing, and do not need the line.

## Review

CI must pass on Linux, macOS and Windows. A pull request from a fork waits for the maintainer to approve its CI run.
