---
name: hand-desktop
description: Use when changing the Hand Desktop window (internal/desktop, internal/desktop/kit, cmd/hand-desktop): a screen, widget, token, scene, test, or anything that touches the MyGo dependency.
---

# Hand Desktop

Desktop is a native MyGo window (package `ui`, no webview) and a client of the running Board. Read the Desktop bullet of `AGENTS.md` first: what it reads, writes and never imports. Docs: `docs/platforms.md` (support), `docs/install.md`, `docs/update.md`, `docs/companion.md` (`handboard://open/` grammar), `docs/vocabulary.md` (terms), `docs/images/desktop/` (one screenshot per scene).

## MyGo pin

- Pinned `github.com/egoist/mygo` v0.7.4; Dependabot ignores it. Never upgrade, bump, fetch the latest or migrate it. A widget or API that v0.7.4 lacks is reported to the operator, not fixed by upgrading.
- Third-party MyGo digests (any `mygo-*` skill, the website) describe newer releases. Trust only the module that compiles:
  `MYGO=$(go list -m -f '{{.Dir}}' github.com/egoist/mygo)`
- Read `$MYGO/cmd/mygo/template/shared/.agents/skills/mygo-maintenance/SKILL.md` once: lifetime, identity, threading and input rules. Widget pages are `$MYGO/docs/ui/<widget>.md` (`README`, `testing`, `list`, `outline`, `tree`, `table`, `sidebar`, `badge`, `toast`, `scroll`, `styling`, `accessibility`, `performance`, `windows`, `views`). Confirm every API against `$MYGO/ui` source before use.
- `docs/ui/migration.md` is for upgrades only; not this skill's job.

## Reconcile before building

1. Name the need: tree, long list, table, badge, toast, tabs, menu.
2. Look it up in `$MYGO/docs/ui/README.md` and the widget's page.
3. Use the widget before `Column` + `Text`: `ui.Outline` for a tree, `ui.List` for a long list (builds only rows in view), `ui.Tree`, `ui.Table`, `ui.Sidebar`, `ui.Badge`, `ui.Toast`.
4. Hand code that rebuilds every row of a list each frame is a defect; measure rows 50, 500, 1500 in the tester.

## Rules

- Colours, lengths and type come from `internal/desktop/kit` only: `kit.ColorsFor(dark)`, `kit.Body`, `kit.Heading`, `kit.Message`. No literal colours or sizes in views.
- `kit` mirrors `internal/board/static/board.css`; `kit/tokens_test.go` reads it by path and fails on a difference. A token change edits both in one PR.
- No code comments unless a hidden constraint needs one (3 lines at most). Keep existing comments.
- Light and dark parity: every new scene and test runs both `tt.SetDark(false)` and `tt.SetDark(true)`; no state is legible in only one theme. Status uses shape or text as well as colour.
- Elements expire each build: store data and `ui.Handle`, never an element. Slow I/O off the UI thread, results through `Window.Update`.
- Desktop writes nothing but the API's actions and its two local stores (drafts, `prefs.json`). A feature lands in `/api/v1` first.
- Never run `hand-desktop` or `go run` a window against a live Board or the operator's fleets. Tests only, with `SECONDHAND_HOME` set to a temp dir.

## Tests

- One `ui.NewTester(app.View, w, h)` per screen; fixtures in `fixture_test.go` and the `*fixture_test.go` files. Window scene size is `windowWidth` x `windowHeight` (1100 x 760); also test a narrow window (`narrow_test.go`) and the minimum size.
- Two clocks: the tester's, moved with `tt.Advance(d)` (never `time.Sleep`), and the real one that `Window.Update` and `time.Now` use. Call `tt.Frame` after changing state from outside the view; `tt.FramePending` says whether the view still asks for frames.
- Drive with `tt.Click`, `tt.Key`, `tt.Type`, `tt.RightClick`, `tt.ChooseMenuItem`; read with `tt.HasText`, `tt.Texts`, `tt.Find`, `tt.Announcements`, `tt.SetPreferences`, `tt.SetSize`, `tt.SetZoom`. Not in v0.7.4: `tt.Access`.
- Scenes are `TestScenes*`. Screenshots: `DESKTOP_SHOTS=<folder> go test ./internal/desktop -run TestScenes`; copy changed ones to `docs/images/desktop/`.
- A UI change that adds a screen or state adds a scene.

## Gate

```sh
gofmt -l .
go vet ./internal/desktop/... ./cmd/hand-desktop/...
for p in ./internal/desktop ./internal/desktop/kit ./cmd/hand-desktop; do go run github.com/egoist/mygo/cmd/mygo vet "$p"; done
go test -race ./internal/desktop/... ./cmd/hand-desktop/...
```

`mygo vet` takes one directory per run and flags stored build values and goroutine captures. `go.mod` and `go.sum` stay unchanged. CI runs the full `go test -race ./...`; do not run `internal/board` or `internal/cli` here unless you touched them.
