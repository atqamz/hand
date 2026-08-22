# Private pinned core runtime

Status: accepted

## Context

Hand's control plane depends on Git, Treehouse, and Herdr.

Resolving those commands from machine `PATH` allows unrelated machine updates to change the behavior of an installed Hand release.

Workers also need the same Git implementation that Hand qualified for its control-plane operations.

GitHub, GitLab, delivery tools, Witness, and coding-agent harnesses are not required by every Fleet.

## Decision

Hand owns one exact, source-controlled runtime lock for Git, Treehouse, and Herdr.

`hand runtime ensure` downloads each locked artifact into a private, versioned bundle under `~/.secondhand/runtime/`.

The installer verifies HTTPS responses, complete SHA-256 digests, safe archive paths, expected files, and executable state before publishing a bundle.

`runtime/current.json` is the atomic selection record, so a failed install leaves the previous selected bundle untouched.

Core processes use absolute paths from the verified selected bundle and fail with `hand runtime ensure` when the bundle is absent or invalid.

The process environment prepends only the managed Git directory to child `PATH`.

It preserves the user's home, Git configuration, SSH variables, credential helpers, harness access, and unrelated variables.

The parent process and persistent user or machine `PATH` are unchanged.

Optional provider and delivery tools use a closed first-party capability catalog under `~/.secondhand/integrations/`.

They are selected explicitly and never become core runtime dependencies.

Self-update uses the standard-library HTTPS client so an absent GitHub integration does not prevent Hand from updating itself.

Fleet registry data and Herdr Fleet namespacing remain outside the runtime and integration stores.

## Consequences

Bootstrap can make the machine ready without installing core tools into unrelated machine locations or waiting for PATH propagation.

Runtime qualification must produce real immutable Git artifacts for every supported target before a release can successfully provision that target.

Existing unit tests may use explicit fake executables or a test-only PATH adapter, but production binaries have no core PATH fallback.
