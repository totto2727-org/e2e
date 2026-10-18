# Caseyard maintenance

These instructions apply when changing this repository.
For installation, public API behavior, and consumer requirements, use the [root README](./README.md) and its language guides.

## Repository structure

| Path                                             | Maintained content                                                   |
| ------------------------------------------------ | -------------------------------------------------------------------- |
| `go/cli/`                                        | Go library and native tests.                                         |
| `go/cmd/caseyard-driver/`, `go/internal/driver/` | Static Linux helper executable, private transport, and native tests. |
| `go/e2e/`                                        | Go Docker tests, selected with the `e2e` build tag.                  |
| `haskell/src/`, `haskell/test/`, `haskell/e2e/`  | Haskell library, native Hspec specs, and Docker Hspec specs.         |
| `typescript/src/`, `typescript/test/`            | TypeScript library and native, package, and Docker Vitest projects.  |
| `rust/src/`, `rust/tests/`                       | Rust library and native or ignored Docker tests.                     |
| `fixtures/cli/Dockerfile`                        | Shared Linux application image for acceptance tests.                 |
| `.agents/skills/cli-e2e/references/example/`     | Equivalent public-API consumer examples and their sibling documents. |

## Documentation ownership

| Document                                     | Canonical content                                                                                         |
| -------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| [Root README](./README.md)                   | Purpose, language-independent public contract, and links to language guides.                              |
| [Driver setup](./docs/driver.md)             | Optional snapshot/terminal prerequisites, trusted cross-build procedure, and installation routes.         |
| Language READMEs                             | Consumer installation, language-specific public APIs, limitations, and links to relevant examples.        |
| Example sources and sibling Markdown         | Concrete usage and the behavior each example actually verifies.                                           |
| [cli-e2e](./.agents/skills/cli-e2e/SKILL.md) | Language-independent scenario design, implementation, and documentation guidance, with links to examples. |
| This AGENTS.md                               | Maintainer setup, toolchains, architecture constraints, task orchestration, and repository verification.  |

Link to the canonical document instead of repeating its content in another guide or skill.
Language-specific implementation guidance belongs in the language README and examples, not a separate implementation skill.
Do not retain dated verification reports, migration tables, or progress logs in these entry documents.

## Development setup

Run root tasks from the repository root.
Enter `nix develop` for repository tools, or select `nix develop .#go`, `.#haskell`, `.#typescript`, or `.#rust` for standalone native work.
Nix provides hpack and native build dependencies such as zlib; ghcup provides GHC and Cabal.
Cabal owns Haskell library dependencies.
Nix provides `rustup`.
`rust/rust-toolchain.toml` owns the Rust toolchain and components.
The default shell includes Node, pnpm, Vite+, Just, Go, Cargo, linters, and formatters.
Docker must be available for explicit E2E execution.

```sh
nix develop
vp install --frozen-lockfile
just --justfile haskell/Justfile setup
vp run -r ci
```

The root pnpm workspace and `pnpm-lock.yaml` cover the libraries and repository tools only.
Keep documentation examples limited to test source files and their sibling scenario documents.
Do not add package manifests, lockfiles, runner entrypoints, standalone tool configuration, or root task dependencies for them.
Show normal published-package setup in the language READMEs, not checkout-relative dependency overrides.
When manual execution is useful, integrate the source into an existing consumer test project.
Keep `minimumReleaseAge: 1440` and `minimumReleaseAgeStrict: true` without bypasses.
Keep the Vite+ catalog, Vite alias, and bundled Vitest override aligned with the template.
Do not deny Vite+ lifecycle scripts through a package-specific override without evidence.
Review any newly required lifecycle script before granting package-specific permission.

## Tasks and caching

| Task            | Purpose                                                                                   |
| --------------- | ----------------------------------------------------------------------------------------- |
| `fix`           | Formatter and linter autofixes. Cache enabled by default.                                 |
| `check`         | Formatting, linting, and type checks. Compile Docker test sources without executing them. |
| `build`         | Produce the application or library artifacts.                                             |
| `test`          | Lightweight reproducible tests, including controlled transport fixtures. No Docker.       |
| `pre*`, `post*` | Required preparation or follow-up stages, wired with explicit dependencies.               |
| `ci`            | Checks, builds, and native tests without Docker.                                          |
| `e2e`           | Explicit, uncached Docker acceptance for the four implementations.                        |

Use Vite+ for cross-project orchestration and caching.
Use `vp run -r <task>` to select workspace packages instead of listing their names in an aggregate.
Keep caching enabled for `fix`, `check`, `build`, and `test`.
Put a lightweight command directly in its base task.
Add `<category>:<CLI>` tasks and an aggregate only for multiple tools or independently useful stages.
Use `dependsOn` for multi-stage work and local Justfiles for complex options or multi-line procedures.
When a project and all children have no JavaScript dependency or useful cache-control requirement, use native commands for light tasks and Just for heavy procedures.
Do not introduce Node solely for those standalone workflows.

Use Vite+'s automatic input tracking by leaving `cache.input` unset.
Do not enumerate source or configuration paths unless a demonstrated input-tracking gap requires an exception.
Use automatic output tracking unless a demonstrated gap requires explicit outputs.
Cargo and Cabal read and update `target/` and `dist-newstyle/`; exclude those directories from inputs and restore them as outputs.
Disable caching for image creation and Docker execution because daemon state is not a source-file output.
Use each tool's standard output directories and keep generated files out of commits.
Haskell uses `dist-newstyle/`; Rust uses `target/`; TypeScript emits `dist/`.
Go library builds do not need an artificial archive or executable output.

Derive Vite+ formatter and linter exclusions from `.gitignore` through `@totto2727/gitignore-patterns`.
Keep shared formatter and linter blocks in the root configuration.
Add individual exclusions only for a demonstrated helper limitation or cache-input gap.
The TypeScript documentation example is a source snippet for a consumer project, not a root workspace member.
Its tracked source cannot be excluded through Gitignore, so root type-aware lint excludes that snippet because its consumer dependencies are not installed here.
Root formatting still covers it; type checking belongs to the consumer project when the snippet is integrated.
Consumers of built exports must depend on the library build.
A clean `check` or `fix` must not assume pre-existing declarations.

## Validation

From the root Nix shell:

```sh
vp run -r ci
vp run e2e
```

`vp run -r ci` checks and builds all implementations and runs native tests.
It does not execute Docker tests or build documentation examples.
`e2e` builds the static Linux amd64 and arm64 drivers, builds `fixtures/cli` with the `e2e-example:local` tag and its `nonroot` stage with `e2e-example-nonroot:local`, then runs the four language suites sequentially.
Use `vp run @totto2727/caseyard-go#build:driver` to build only the drivers.
Their ignored outputs are `go/target/driver-linux-amd64` and `go/target/driver-linux-arm64`.
Standalone Docker suites require these outputs as well as the fixture image.
Select the binary for the container architecture rather than the test host architecture.
Keep the shared image free of Python and Go so acceptance verifies post-start executable transfer.
Use `vp run @totto2727/caseyard-go#ci` or the corresponding Haskell, TypeScript, or Rust package name to select one graph.
Standalone language Docker commands require the prebuilt fixture image.

| Working directory | Native selection                                                        | Docker selection                                                         |
| ----------------- | ----------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| `go/`             | `go test -race ./...`                                                   | `go test -race -tags=e2e ./e2e`                                          |
| `haskell/`        | `just test`                                                             | `just e2e`                                                               |
| `typescript/`     | `vp test run --project native`; package imports use `--project package` | `vp test run --project docker`                                           |
| `rust/`           | `cargo test --locked --all-features`                                    | `cargo test --locked --all-features --test e2e -- --ignored --nocapture` |

Select cases with Go `-run`, Hspec `--match`, Vitest `-t`, or Cargo's test-name filter.
Do not use environment-dependent discovery or require a full-suite test count for cleanup audits.
Controlled HTTP/socket tests verify transport behavior, not real Docker acceptance.

Each language must verify these public behaviors against the shared image:

| Case             | Required observation                                                                                      |
| ---------------- | --------------------------------------------------------------------------------------------------------- |
| Greeting         | Exact stdout, empty stderr, exit zero.                                                                    |
| File workflow    | Multiple commands share case state; transfers preserve exact bytes and mode `0644`.                       |
| Expected failure | Application exit `7` is a result, not an infrastructure error.                                            |
| Separate streams | Working directory and environment reach the process; stdout and stderr remain separate on failure.        |
| Physical paths   | Regular files, directories, missing paths, literal links, and dangling links follow the public contracts. |
| Deadline         | An indefinite command returns a deadline error and its container is removed.                              |
| Snapshots        | Explicit path kinds, exact bytes, literal links, deterministic differences, and read failures.            |
| Terminal session | Output-driven input, separate stdout, pipe input, resize, signals, raw output, and scope cleanup.         |

Also verify isolation between scopes, owned-container cleanup, and preservation of the caller's image.
Keep assertions inside standard test bodies and use the framework's equality, diff, and snapshot facilities.
Caseyard returns command, path, file, and snapshot observations instead of implementing verdicts or snapshot persistence.
Extract only shared lifecycle and fixtures.
Use controlled native tests for invalid inputs, transport failures, partial output, comparison failures, cleanup failures, and cancellation.

## Implementation constraints

Follow the shared public contract without adding a library-specific runner, case registry, fixed concurrency gate, or mutable Docker-client escape hatch.
Keep application workflows in consumer tests.
Register ownership and cleanup immediately, including partial creation failures.
Use unpredictable names and ownership labels when recovering a resource whose creation response was lost.
Remove the inspected immutable resource ID only after verifying ownership.
Each resource's cleanup has a fresh thirty-second total budget, including retries.
Own attached stream connections and close them on cancellation.
Do not infer application exit status from the host `docker exec` status or mix infrastructure diagnostics into command stderr.
Keep transport support and cancellation limits explicit; do not infer parity from another backend.

### Static driver

When changing terminal or snapshot transport, read [the private wire contract](./fixtures/pty/protocol.md).
Keep the helper a static Linux executable built with `CGO_ENABLED=0` for amd64 and arm64.
The caller supplies trusted matching binary bytes through each language's public install operation.
Do not embed binaries, run an implicit download, or require an interpreter in the target image.
Keep generated executables in ignored build output directories and out of commits and library archives.
Register session ownership before returning its start response, bound all waits, preserve raw streams, and complete close cleanup before acknowledging it.
Native driver tests verify controlled errors and transport behavior.
Each language's real Docker tests verify its public adapter against the shared helper.
Do not infer full PTY coverage or consumer-migration success from compilation alone.

## Language-specific maintenance

### Go

The module path is `github.com/totto2727-org/e2e/go`.
Use the race detector and check every error; wrap causes with `%w` when adding context.
`just ci` in `go/` checks the library and compiles the tagged Docker suite without running it.
`just e2e` runs the suite with shuffled order and checks residual containers and image preservation.
Go's framework controls parallelism through `t.Parallel` and `-parallel`.

### Haskell

Install [ghcup](https://www.haskell.org/ghcup/install/) and put its `bin` directory on `PATH` before running `just setup` in `haskell/`.
The Haskell Justfile pins GHC and Cabal and invokes them through `ghcup run` without changing global tool selections.
Cabal owns package dependencies and hpack generates package metadata.
Edit `haskell/hpack.yaml` and run `just generate` in `haskell/` to regenerate `caseyard.cabal` with hpack.
The explicit filename avoids pnpm treating hpack's dependencies as a Node package manifest.
Commit both manifests so Cabal consumers do not need hpack.
`just ci` in `haskell/` runs Cabal builds, manifest checks, and native Hspec tests.
`cabal.project` supplies warnings-as-errors and direct test reporting.
Each spec module exports `spec :: Spec`; one-line `Spec.hs` entrypoints use the declared `hspec-discover` build tool.
Cabal can download that tool on first build; use `--offline` only with the required dependencies already cached.
No release maintainer is declared, so `cabal check` excludes only that publication metadata check.
Do not weaken dependency bounds or introduce `allow-newer` for local tooling.

### TypeScript

`check` uses Vite+'s type-aware checks, `build` emits bundles and declarations, and package tests import the built public entrypoint.
Vitest remains a development dependency; the library is framework-independent.
Keep static native, package, and Docker projects, with native selected by default.
Use the template's TypeScript presets without enumerating configuration files individually.

### Rust

`just ci` in `rust/` runs rustfmt, Clippy with denied warnings, builds, native tests, and rustdoc.
Retain locked dependencies, the existing Rust 1.88 minimum, and template lint policies, including forbidden unsafe code and denied unwrap/expect use.
Keep Docker tests ignored by default.

## Publishing

The [publish workflow](./.github/workflows/publish.yml) runs on pushes to `main`.
It builds the npm package and calls the shared npm and crates.io publish actions.
Run CI separately before publishing.

| Registry  | Package               | Required configuration                                                                 |
| --------- | --------------------- | -------------------------------------------------------------------------------------- |
| npm       | `@totto2727/caseyard` | Package ownership and trusted publishing for this repository's `publish.yml` workflow. |
| crates.io | `caseyard`            | Crate ownership and the `CARGO_REGISTRY_TOKEN` repository secret.                      |

Confirm registry name availability and complete any first-publication setup before starting the workflow.
Use the MIT license for all language packages and include its text in published artifacts.
Do not run the publish workflow during validation.
The private workspace packages are orchestration wrappers, not publication targets.

## Template basis and task-specific guides

Start from the corresponding workspace template and make only necessary library-specific extensions.
TypeScript follows the [Vite+ library template](https://github.com/totto2727-org/template-vite-plus-lib/tree/ef2173ab26407fdc45b4b05106b5eed561062dd1), Go the [Go template](https://github.com/totto2727-org/template-go-simple/tree/b12903f8f8bf458b93538237c2ee2016dd54865e), and Rust the [Rust template](https://github.com/totto2727-org/template-rust-simple/tree/08ffe1d2d6a27e8d0b4c25f98e5a89a96d8ec1c1).
There is no Haskell workspace template; keep its native Cabal/Hspec configuration.

When changing README or AGENTS, follow `share-artifact` and `documentation-principles`.
