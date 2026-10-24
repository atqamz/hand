# Task: issue #41, `tide export` writes a broken CSV when a station name has a comma or a quote, and close it properly

Read the issue first: `gh issue view 41 -R acme/tidepool`. A tester on **v2.3.0** ran `tide export --format csv` for the station "Port-au-Prince, Haïti" and got a file whose second column starts one field too early; a station called `Bob's "Landing"` made the reader stop at the first quote. They did not attach the file. The maintainer's hypothesis: `internal/export/csv.go` joins fields with `strings.Join(fields, ",")` instead of `encoding/csv`, so commas, double quotes, `\r\n` and a leading `=` (a formula in a spreadsheet) are not escaped. The old code path may already be gone on `main` after #38 (`--format tsv`): you must **prove** it and close the remaining gaps.

Read `internal/export` (`Write`, `Row`, `quote`), `internal/station/names.go` (where the display name is built, with `%s (%s)` for the country), `cmd/tide/export.go` (the flag parsing: `--format`, `--out`, `--no-header`), the fixtures in `testdata/stations/*.json`, `docs/export.md` section "Escaping", and the CI job `test-windows` in `.github/workflows/ci.yaml`: the exporter writes `\r\n` on Windows and `\n` elsewhere.

## Do

1. **Reproduce** the failure on current `main` with a test that feeds these names through `Write` and reads the result back with `encoding/csv`: `Port-au-Prince, Haïti`, `Bob's "Landing"`, `Zoë Müller & Sons <harbour>`, `100% Pier | North`, `=SUM(A1:A9)`, `C:\Users\dev\tides\`, `日本語の港 🌊`, a name with a tab, and a name with an embedded newline. State which of them fail today and why.
2. If a name round-trips: say so, with the evidence, and keep the test as a guard. If one does not: fix `quote` in `internal/export/csv.go` once, in the shared code, and delete the second copy in `cmd/tide/export.go` that the issue calls "the hot patch".
3. **Harden** what is still weak: the exporter must refuse a name that holds a NUL or a control character other than tab, newline and carriage return, with a plain message that names the station id and the offending byte offset; it must prefix a leading `=`, `+`, `-` or `@` with a single quote when `--spreadsheet-safe` is set. Keep the default output byte for byte as it is.
4. `docs/export.md` states the escaping rules and the flag. Run `gofmt -l .`, `go vet ./...` for linux, darwin/arm64 and windows/amd64, `go test -race -timeout 20m ./...`, `shellcheck scripts/*.sh`, and `actionlint`.

PR title `fix(export): quote a station name that holds a comma or a quote, and refuse a control character`, body ends with `Closes #41`, assigned to `maintainer`, labels `bug`, `area: export`, `area: docs`. After every push comment `@review-bot run`. The maintainer merges after the gate.

## Background and decisions already made

- **Why `encoding/csv`:** the standard library writer handles `"`, `,`, `\r` and `\n` for us, and the maintainer ruled out a hand-written quoter in #38 ("one quoting rule, written by someone else"). Do not reopen that.
- **Why not a dependency:** the repository has three, and a CSV library would be a fourth. The answer to "can we use `xsv`?" is no.
- **The old hot patch:** `cmd/tide/export.go` wraps every name in double quotes when it contains a comma, and doubles an inner quote with `strings.ReplaceAll(name, "\"", "\"\"")`. It forgets a name that holds only a newline, and it quotes `=SUM(A1:A9)` as a formula. Those are the two bugs the tester's spreadsheet showed.
- **Display names:** `internal/station/names.go` builds `Name (Country)`; a country with a comma, such as "Korea, Republic of", is the reason the name column grew a second comma in v2.3.0.
- **Spreadsheets:** Excel and LibreOffice read a leading `=`, `+`, `-` or `@` as a formula. `--spreadsheet-safe` is opt-in because it changes bytes; the default stays as it is.

Names the maintainers use in their own tests (all invented, keep them as they are):

id	name	note
21	Åse Ørsted	Nordic letters
22	Zoë Müller	diaeresis
23	Łukasz Ñandú	mixed Latin Extended
24	孫文 港	CJK with a space
25	Ωmega 🌊 Pier	Greek and an emoji outside the BMP
26	Quote "Dock" Road	inner double quotes
27	Apostrophe's Dock	single quote
28	Back\slash\ Quay\	backslashes, one trailing

## Acceptance check

`go test -race -timeout 20m ./...` passes on Linux, macOS and Windows; `tide export --format csv --out out.csv` on the fixtures `testdata/stations/hard.json` produces exactly the bytes in `testdata/stations/hard.csv`; and `tide export --spreadsheet-safe` prints no field that starts with `=`, `+`, `-` or `@`. If a check cannot run here, say which one and why.

## How to work

- No code comments. Product words: the app is called "Tidepool", never "Tide Desk". Match the surrounding code's idiom; the smallest correct change; delete before adding; no new dependency.
- Run the gate before every push; local timing flakes under load pass when rerun alone: say so, do not hide them. Tests that touch the filesystem must be Windows-safe (no `/tmp`, no `:` in a name, no trailing `.` or space).
- Commit rules: small commits, imperative lowercase subject, no trailing period, no attribution lines, never `--no-verify`.

## Repo facts

- Go is 1.26.5. Run it as `mise exec go@1.26.5 -- go ...`.
- The gate, which must pass before every push:
  - `gofmt -l .` prints nothing;
  - `go vet ./...`;
  - `env GOOS=darwin GOARCH=arm64 go vet ./...`;
  - `env GOOS=windows GOARCH=amd64 go vet ./...`;
  - `go test -race ./...`.
- Rules from `AGENTS.md`: standard library first; output is plain CSV, exit codes 0–3; a doc change lands with the behaviour it describes.
- The fixture folder on the build machine is `C:\Users\dev\tidepool\testdata\` and, on Linux, `/home/dev/tidepool/testdata/`. A path with a space, such as `C:\Users\Zoë Müller\My Tides\`, must work too.

## Git and PR

1. Push, open the PR against `main` with `gh pr create --repo acme/tidepool`, and assign it to `maintainer`. The body has:
   - `## Summary`, with one to three bullets;
   - the issue line given in the brief;
   - `## Test plan`, listing the gate you ran.
2. **The review gate** is both the bot and a human at the current head.
   - After every push, comment `@review-bot run` on the PR.
   - Exit codes of `scripts/review_state.sh`: 0 means both reviews arrived for the head, 2 means pending, 3 means the bot skipped or paused, and 1 means `gh` failed.
   - Poll in the **foreground**, at most 3 polls of 150 s per command. Never poll in a background shell.
3. **Fix every P1, P2, Major or Critical finding in code.** Reply to each thread, then resolve it. If you disagree, leave it open and say so in your report.
4. **Do not merge.** Report when the bot is clean on the head and every thread is resolved. Report with `--status stuck` if you are blocked.
5. **No comments for corrections.** A correction, status update or finding goes into the issue or PR description with `gh pr edit`; never post it as a comment.

## Examples the exporter must keep byte for byte

```csv
id,name,country,lat,lon
12,"Port-au-Prince, Haïti",HT,18.54,-72.34
13,"Bob's ""Landing""",US,47.60,-122.33
14,Zoë Müller & Sons <harbour>,DE,53.55,9.99
15,100% Pier | North,NO,69.65,18.96
16,"C:\Users\dev\tides\",XX,0,0
```

A shell one-liner that checks the header: `head -n 1 out.csv | grep -qx 'id,name,country,lat,lon' && echo "ok: $(wc -l < out.csv) rows"`

In PowerShell: `(Get-Content .\out.csv -TotalCount 1) -eq 'id,name,country,lat,lon'`. Never use `!` history expansion in a bash example, and never put `^` or `&` outside a quoted string in a `cmd.exe` example.

## Finish what was asked

Keep working until everything the brief asks for is done and its acceptance check passes. Do not stop at a milestone to check in. Stop and report only when the work is done or you are stuck.

---
When you finish, report to the supervisor from inside this worktree, passing the report on stdin so no file is left in it:
'/home/dev/.local/bin/tide-hand' report add --status done --file - <<'HAND_REPORT_7BEQ3DXW'
YOUR REPORT
HAND_REPORT_7BEQ3DXW
Use --status stuck instead if you cannot continue, or --status progress for a milestone. Say what changed and list any commits. End with one line `Delivered: <link or absolute path>` for each thing you delivered: a PR, a document, a file.
