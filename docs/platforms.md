# Platform support

Hand supports Linux, macOS (Apple silicon; Intel builds are released but not tested) and native Windows (amd64, Windows 10 or 11). Supported means the project fixes problems that people report on those systems, not that every step has run on a real machine; the table below says how each part is verified. Known gaps: Windows has run in CI, with the real Luvus, and not yet on a real Windows machine; the real Mac run of 2026-10-17 ([#895](https://github.com/atqamz/hand/issues/895)) used a stand-in `claude`; Windows has no desktop notifications; the macOS LaunchAgent's `RunAtLoad` at a real login is unverified.

Report a problem with the [bug form](https://github.com/atqamz/hand/issues/new?template=bug.yml), and a vulnerability as [`SECURITY.md`](../SECURITY.md) says.

CI runs the tests on GitHub's latest Ubuntu (amd64), macOS (Apple silicon) and Windows Server (amd64) runners. Every job installs the tested Luvus and runs the `TestLive*` tests against it; the other tests use a fake one. The Linux arm64 and Intel macOS builds are released but not run in CI.

In the table:
- **daily**: a real Linux machine runs it every day, with the real Luvus and harnesses;
- **real**: it has run on a real Linux machine, but not every day;
- **CI**: CI runs it on that OS, with the real system calls;
- **CI, fake**: CI runs it on that OS, but a fake stands in for the harness, Luvus, the release server, the browser, the notifier or the watcher and board processes;
- **real Mac (#895)**: it ran on a real Mac (macOS 26.5.1, Apple silicon) in the first dogfood run, the issue linked. *Stand-in claude*: a script stood in for `claude`, so the run covers Hand's own processes and panes but not the real worker path;
- **not run**: the code exists but has never run on that OS;
- **no**: not supported.

| What | Linux | macOS | Windows |
|---|---|---|---|
| Install script | CI, fake | real Mac (#895) | CI, fake, in PowerShell 5.1 and 7 |
| `hand init` and the Luvus pin | daily | real Mac (#895) | CI, real Luvus |
| Download the tested Luvus | CI, real Luvus | CI, real Luvus | CI, real Luvus |
| Luvus server, panes and events | daily | real Mac (#895), stand-in claude | CI, real Luvus over its named pipe |
| Start claude, and accept its trust screen | daily | CI, fake | CI, fake |
| Start codex or agy | real | CI, fake | CI, fake |
| Start opencode | CI, fake | CI, fake | CI, fake |
| Hand refuses a harness found only as a `.cmd` or `.bat`, and prefers a `.exe` anywhere on `PATH` | not needed | not needed | CI |
| Keys reach an agent whose program has an extension (`codex.exe`) after a process scan | CI, real Luvus, fake `codex.exe` | CI, real Luvus, fake `codex.exe` | CI, real Luvus, fake `codex.exe` |
| A worker reports through the footer | daily | CI, fake | CI, fake |
| Stop a worker | daily | real Mac (#895), stand-in claude | CI |
| `hand attach` | CI, fake | real Mac (#895), stand-in claude | not run |
| Start the watcher on demand, and the board keeps watchers alive | daily | real Mac (#895) | CI, fake |
| `hand update` | daily | real Mac (#895), from v0.11.0 | CI, fake |
| Luvus switch on repin: stop the server through UHP and `server.pid` | CI, fake | CI, fake | CI, fake |
| Luvus switch of a quiet fleet: stop the supervisor and the server, start the pin, resume the supervisor | CI, real Luvus, fake claude | CI, real Luvus, fake claude | CI, real Luvus, fake claude |
| `hand board --update-every`, the timer's `hand update` | CI, fake | CI, fake | CI, fake |
| Desktop notifications | daily | real Mac (#895) | no |
| `hand open` starts the board and opens the browser | CI, fake | real Mac (#895) | CI, fake |
| Paths over 260 characters | not needed | not needed | CI for Hand's git; not run for a worker's |
| Start the board at login | daily | real Mac (#895) by bootout and bootstrap; not at a real login | not run |
| The watcher starts a killed Luvus server again | CI, real Luvus, fake claude | real Mac (#895), stand-in claude | CI, real Luvus, fake claude |

The two `.exe` rows prove Hand's side: that the lookup picks a `.exe` over a `.cmd` and names the shim it refuses (temporary `PATH` folders with files of those names), and that real Luvus refuses a key to a fake `codex.exe` until Hand has asked it for a process scan, and accepts it after. They do not prove that the real Codex, Claude Code or an npm shim behave the same, what `cmd.exe` does with a shim, or Codex's trust screens: [`docs/dogfood/windows.md`](dogfood/windows.md) `W4` checks those on a real machine.

On Linux, the setup without a systemd user session (plain SSH, for example) has run only in CI. Hand configures neither commit signing nor ssh on any OS.

## Real-machine checks

To help, run the checks in [`docs/dogfood/`](dogfood/README.md) on a real machine, yourself or with a coding agent: one set of checks, with a throw-away home that never touches your own fleets. The checks live only there, riskiest first, and so do the checks for Linux without a systemd user session and for Linux arm64. Report what fails through the issue form; when a run passes, the row of the table above changes from "CI" to a real-machine result, with a link to the run issue.
