# Secondhand

Talk to one agent. Ship with a crew.

Secondhand is a Go CLI for orchestrating coding agents.
The binary is named `hand`.

## Status

The repository currently provides runtime initialization and project registry commands.

## Requirements

- Go 1.26.5 or newer

## Build

```sh
make build
```

Run the binary:

```sh
./hand --help
./hand --version
./hand init
./hand init --setup
./hand project add https://github.com/org/repo
./hand project list
./hand project remove repo
```

`hand init` creates the `state/`, `data/`, `projects/`, and `config/` runtime directories,
plus skeleton backlog, project registry, and dashboard files.
The command is idempotent and accepts an optional target path.

`hand init --setup` discovers supported harnesses and tools on `PATH`, then interactively
selects the default worker harness, model, and effort and writes them under `config/`.
`hand project add` clones and registers a repository, `hand project list` displays the
registry (use `--json` for JSON), and `hand project remove` unregisters a project without
deleting its clone.

The build embeds the version supplied through `VERSION`:

```sh
make build VERSION=0.1.0
```

## Development

```sh
make test
make lint
make e2e
```

The E2E target is reserved for the future integration suite.

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for contribution guidelines.
