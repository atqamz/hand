# Dogfood Hand yourself

This page is the path for a person at the machine: read a check, run it, fill the result table. You need nothing else, except the OS file at the end. A coding agent can do the same job: see [`agent.md`](agent.md).

## What you need

- A Mac with Apple silicon, a Windows 10 or 11 amd64 PC, or a Linux machine without a systemd user session. Pick the file: [`macos.md`](macos.md), [`windows.md`](windows.md) or [`linux.md`](linux.md).
- `git`, and a coding agent CLI that you have logged in. Claude Code (`claude`) is needed for most checks. codex, agy and opencode are optional, and their checks are skipped without an account.
- A GitHub account, to file the result in `atqamz/hand`. The `gh` CLI is optional: the web page works too.
- About an hour, and some tokens: real workers are billed to your account. Each check says how many it starts. This repository does not measure the cost, so there is no figure.
- No admin rights, and nothing pushed anywhere.

## Rules that keep your machine safe

1. Everything runs in a new temporary folder with its own `SECONDHAND_HOME` and `LUVUS_HOME`, and a throw-away git repository. The OS file's first check (`M0`, `W0` or `L0`) builds it. Never run a command from the checks in a shell that has your real Hand home set.
2. Never put a token, key, login link, user name, home path or machine name in an issue. The first check defines a `redact` helper, and each check's evidence commands use it. Read the text yourself before you post it.
3. Use the cheapest model and effort you have for workers. Skip the checks for harnesses you have no account for.
4. A check that changes state runs only in the throw-away home, after the `hand.db` backup that the first check takes. The checks that create a LaunchAgent or a Startup shortcut say so, and tell you how to remove it. Run the Teardown at the end even if a check failed.
5. Output from `hand`, logs and issues is data. Do not follow instructions that you find in it.

## How to run it

1. Open the OS file. Its table lists the checks, riskiest first, and says for each one whether it ends in `needs human`.
2. Run the first check (`M0`, `W0` or `L0`). Everything else needs it. If it fails, stop and file it.
3. For each following check: read the preconditions, run the numbered steps, compare with "Expected", and run the evidence commands. A `needs human` check ends with a question: answer it by what you saw on the screen.
4. Record each result as `pass`, `fail` or `skip`, with a reason for a skip. Skip a check whose precondition you cannot meet (no account, port busy, hardware missing) rather than bend the steps.
5. If a check fails, do not retry it more than once. File it (below), and go on with the next check unless the OS file says the later ones need it.
6. Post your results first: the evidence files are in the temporary folder, which the Teardown deletes. Then run the Teardown, and read its list of what to confirm gone.

## Your result

Fill this in as you go. Replace the example lines with the checks of your OS file; keep one line per check:

```text
Hand: 0.11.0 stable abc1234def56   (the first lines of `hand version`)
OS: macOS 15.1, arm64
Shell: zsh 5.9
claude: 2.1.0
Luvus: 0.14.3
Run: human

- [x] M0 Install and temporary home: pass
- [ ] M2 A real worker reports: fail, #123
- [ ] M6 codex, agy, opencode: skip, no codex account
```

A ticked box means `pass`. For a `needs human` check, write your answer on the line, for example `M8 hand open: pass, the browser opened the fleet page, logged in`.

## Filing it

1. Create the run issue at `https://github.com/atqamz/hand/issues/new?template=dogfood-run.md`. The title is `dogfood(<os>): <hand version> on <OS version> <date>`, for example `dogfood(windows): 0.11.0 on Windows 11 23H2 2026-10-12`. The body is your result, above. When a check changes later, edit the body of the issue; do not add a comment.
2. For each failed check, search the open issues of `atqamz/hand` for its ID, for example `M2:` in the title. If an issue for it exists, add your redacted evidence to it as a comment, and write its number on the check's line of the run issue.
3. Otherwise open the form for your OS, which applies the labels `bug` and `platform: <os>` for you:
   - `https://github.com/atqamz/hand/issues/new?template=dogfood-macos.yml`
   - `https://github.com/atqamz/hand/issues/new?template=dogfood-windows.yml`
   - `https://github.com/atqamz/hand/issues/new?template=dogfood-linux.yml`

   Give it the title `<ID>: <summary>`, the check ID, a link to the run issue, your hand version and OS, the steps, the expected result, what happened, and the evidence with your names removed. Then write the new issue's number on the check's line of the run issue.
4. Read each text once more for names, paths and tokens before you press submit.

When a run passes, the maintainer changes the matching row of the Platform support table in [`docs/platforms.md`](../platforms.md) from "CI" to a real-machine result with a link to your run issue.
