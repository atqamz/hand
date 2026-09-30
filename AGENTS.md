# Hand (next)

Personal supervisor layer for coding agents. Read `docs/spec.md` before changing anything.

- Terms follow `docs/vocabulary.md`; update it when a term, state, wake kind or ref changes.
- Standard library first; the only dependency is `modernc.org/sqlite`.
- No code comments unless a hidden constraint needs one (≤3 lines).
- Every state change goes through `internal/state` in one transaction that also appends an event.
- Output is TOON via `internal/toon`; keep `hand orient` within its byte budget.
- Run `gofmt -l .`, `go vet ./...` and `go test -race ./...` before committing.
- Luvus is reached only through `internal/luvus`: UHP over its Unix socket, plus the two commands that package builds, `luvus server start` and the interactive `luvus attach` (UHP has no terminal attach). Never call `luvus worktree`, `task`, `pane send`, or `server restart --all`.
- A fleet home is any folder holding `hand.db`. `~/.secondhand` (`$SECONDHAND_HOME`) holds `fleets/<id>` links and `worktrees/<id>/`. Tests set `SECONDHAND_HOME` to a temp dir and never touch the real one.
- `skills/` holds the Hand-owned files `hand init` writes. `bootstrap.md` becomes a fleet's `AGENTS.md`. The `secondhand` skill is installed in every fleet, and `secondhand-migrate` only beside 0.7 data. The binary's name replaces every `` `hand `` and `` `hand` `` in them, so write commands as `` `hand …` ``.
- Board UI work (`internal/board` templates, CSS and `app.js`) reads `PRODUCT.md`, `.impeccable/surfaces/internal-board.md` and `DESIGN.md` first. It keeps the strict CSP (no inline script or style, no `style=` attribute), the five fleet-page region names (`status`, `console`, `timeline`, `queue`, `tasks`), and `#composer` outside every region; the board tests enforce these.
