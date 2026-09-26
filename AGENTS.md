# Hand (next)

Personal supervisor layer for coding agents. Read `docs/spec.md` before changing anything.

- Standard library first; the only dependency is `modernc.org/sqlite`.
- No code comments unless a hidden constraint needs one (≤3 lines).
- Every state change goes through `internal/state` in one transaction that also appends an event.
- Output is TOON via `internal/toon`; keep `hand orient` within its byte budget.
- Run `gofmt -l .`, `go vet ./...` and `go test -race ./...` before committing.
- Luvus is reached only through `internal/luvus` (UHP over its Unix socket). Never call `luvus worktree`, `task`, `pane send`, or `server restart --all`.
