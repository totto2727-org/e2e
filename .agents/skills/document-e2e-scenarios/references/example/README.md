# CLI scenario examples

These optional examples use the public APIs to test the same two behaviors.
Each example is a separate consumer that you run manually from its own directory.
They are not members of the root pnpm workspace and are not dependencies of root CI or E2E tasks.

| Language   | Source                                                              | Scenario document                                                   | Local SDK dependency                                    |
| ---------- | ------------------------------------------------------------------- | ------------------------------------------------------------------- | ------------------------------------------------------- |
| Go         | [multiple_scenarios_test.go](multiple_scenarios_test.go)            | [multiple_scenarios_test.md](multiple_scenarios_test.md)            | [go.mod](go.mod) replacement                            |
| Haskell    | [MultipleScenariosSpec.hs](haskell/MultipleScenariosSpec.hs)        | [MultipleScenariosSpec.md](haskell/MultipleScenariosSpec.md)        | [cabal.project](haskell/cabal.project) package          |
| TypeScript | [multiple_scenarios.test.ts](typescript/multiple_scenarios.test.ts) | [multiple_scenarios.test.md](typescript/multiple_scenarios.test.md) | [package.json](typescript/package.json) file dependency |
| Rust       | [multiple_scenarios.rs](rust/multiple_scenarios.rs)                 | [multiple_scenarios.md](rust/multiple_scenarios.md)                 | [Cargo.toml](rust/Cargo.toml) path dependency           |

Each test defines its operations and assertions directly inside the standard framework's test body.
The greeting test runs `echo 'hello from e2e'` as literal argv and checks exit code `0`, stdout `hello from e2e\n`, and empty stderr.
The failure test runs `false` and checks exit code `1` and two empty streams.
Each scoped helper creates and cleans up its own container.
These small examples do not independently audit container identities or filesystem state.

## Preparation

See the [library requirements](../../../../../README.md#prerequisites-and-setup) and [development environment](../../../../../AGENTS.md#development-setup).
The development shell supplies GHC and Cabal, not preinstalled Haskell library dependencies.
Cabal resolves dependencies from the manifests.
Rustup selects the Rust toolchain.

Docker is required only when you execute the tests, not when you compile or list them.
From the repository root, prepare the caller-owned image once:

```sh
docker build --tag example-cli:local fixtures/cli
```

Set `DOCKER_HOST` if required by your daemon setup.
Consult each language's README for supported transports.
The examples do not prepare, pull, or delete application images.

## Go

From this directory:

```sh
# Compile without running Docker tests.
go test -race -run '^$' ./...

# Execute both tests against Docker.
go test -race ./...
```

The local module and checksums make this directory independently executable.

## Haskell

From `haskell/`:

```sh
# Compile without executing examples.
cabal build

# Execute both Hspec examples against Docker.
cabal test
```

The sample exports `spec` with direct `it` bodies.
The one-line `Spec.hs` uses `hspec-discover` to generate the executable entry point.
The local Cabal project enables the sample suite and direct reporting, so repeated selection and output-directory options are unnecessary.

## TypeScript

Build the local SDK first, from the repository's `typescript/` directory:

```sh
pnpm exec vp pack
```

Then run these commands from this example's `typescript/` directory:

```sh
pnpm install
pnpm exec tsc --noEmit

# Collect ordinary Vitest test definitions without running Docker operations.
pnpm exec vitest list

# Execute both tests against Docker.
pnpm exec vitest run
```

The local pnpm configuration isolates installation from the root workspace and retains the 24-hour dependency release-age policy.
The file dependency consumes the SDK's built public export without source aliases.
There is no sample task graph or artificial native test suite.

## Rust

From `rust/`:

```sh
# Compile without executing Docker tests.
cargo test --locked --no-run

# Execute both ignored tests against Docker.
cargo test --locked -- --ignored --nocapture
```

The committed consumer lockfile makes `--locked` usable on a clean machine.
Ordinary `cargo test --locked` leaves both Docker-bearing tests ignored.
Build outputs stay in each sample's standard local output directory and are not committed.

## Scenario document validation

From the repository root:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 .agents/skills/document-e2e-scenarios/scripts/validate_scenario_docs.py .agents/skills/document-e2e-scenarios/references/example
```

The bundled validator checks the Go pair only.
Review the other documents against their source, including test names, order, argv, status, streams, and stated non-assertions.
Compilation checks public API compatibility, collection checks names, and Docker execution checks the actual behavior.
None substitutes for the others.
