# Secondhand

Talk to one agent. Ship with a crew.

Secondhand is a Go CLI for orchestrating coding agents.
The binary is named `hand`.

## Status

The repository currently contains project scaffolding and the Cobra root command.
No fleet-management subcommands are implemented yet.

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
```

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
