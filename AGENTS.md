# e2e maintenance

These instructions apply when changing this repository.
For installation, public API behavior, and consumer requirements, use the [root README](./README.md) and its language guides.

## Repository structure

| Path                                                        | Maintained content                                                   |
| ----------------------------------------------------------- | -------------------------------------------------------------------- |
| `go/cli/`                                                   | Go library and native tests.                                         |
| `go/e2e/`                                                   | Go Docker tests, selected with the `e2e` build tag.                  |
| `haskell/src/`, `haskell/test/`, `haskell/e2e/`             | Haskell library, native Hspec specs, and Docker Hspec specs.         |
| `typescript/src/`, `typescript/test/`                       | TypeScript library and native, package, and Docker Vitest projects.  |
| `rust/src/`, `rust/tests/`                                  | Rust library and native or ignored Docker tests.                     |
| `fixtures/cli/Dockerfile`                                   | Shared Linux application image for acceptance tests.                 |
| `.agents/skills/document-e2e-scenarios/references/example/` | Equivalent public-API consumer examples and their sibling documents. |

## Documentation ownership

| Document                                                                   | Canonical content                                                                                        |
| -------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| [Root README](./README.md)                                                 | Purpose, language-independent public contract, implementation status, and links to language guides.      |
| Language READMEs                                                           | Consumer installation, language-specific public APIs, limitations, and links to relevant examples.       |
| Example sources and sibling Markdown                                       | Concrete usage and the behavior each example actually verifies.                                          |
| [document-e2e-scenarios](./.agents/skills/document-e2e-scenarios/SKILL.md) | Language-independent scenario design and documentation guidance, with links to examples.                 |
| This AGENTS.md                                                             | Maintainer setup, toolchains, architecture constraints, task orchestration, and repository verification. |

Link to the canonical document instead of repeating its content in another guide or skill.
Language-specific implementation guidance belongs in the language README and examples, not a separate implementation skill.
Do not retain dated verification reports, migration tables, or progress logs in these entry documents.

## Development setup

Run root tasks from the repository root.
Enter `nix develop` for repository tools, or select `nix develop .#go`, `.#haskell`, `.#typescript`, or `.#rust` for standalone native work.
Nix provides GHC, Cabal, and native build dependencies such as zlib.
Cabal owns Haskell library dependencies.
Nix provides `rustup`.
`rust/rust-toolchain.toml` owns the Rust toolchain and components.
The default shell includes Node, pnpm, Vite+, Just, Go, GHC, Cabal, Cargo, linters, and formatters.
Docker must be available for explicit E2E execution.

```sh
nix develop
vp install --frozen-lockfile
vp run ci
```

The root pnpm workspace and `pnpm-lock.yaml` cover the libraries and repository tools only.
Keep documentation examples limited to test source files and their sibling scenario documents.
Do not add package manifests, lockfiles, runner entrypoints, standalone tool configuration, or root task dependencies for them.
Show normal published-package setup in the language READMEs, not checkout-relative dependency overrides.
When manual execution is useful, integrate the source into an existing consumer test project.
Registry consumption remains unverified until the required packages are published.
Keep `minimumReleaseAge: 1440` and `minimumReleaseAgeStrict: true` without bypasses.
Keep the Vite+ catalog, Vite alias, and bundled Vitest override aligned with the template.
Keep `allowBuilds.vite-plus: false`: the installed tools are prebuilt and need no dependency lifecycle permission.
Review any newly required lifecycle script before granting package-specific permission.

## Tasks and caching

| Task            | Purpose                                                                                   |
| --------------- | ----------------------------------------------------------------------------------------- |
| `fix`           | Formatter and linter autofixes. Never cache mutations.                                    |
| `check`         | Formatting, linting, and type checks. Compile Docker test sources without executing them. |
| `build`         | Produce the application or library artifacts.                                             |
| `test`          | Lightweight reproducible tests, including controlled transport fixtures. No Docker.       |
| `pre*`, `post*` | Required preparation or follow-up stages, wired with explicit dependencies.               |
| `ci`            | Checks, builds, package validation, and native tests without Docker.                      |
| `e2e`           | Explicit, uncached Docker acceptance for the four implementations.                        |

Use Vite+ for cross-project orchestration and caching.
Put a lightweight command directly in its base task.
Add `<category>:<CLI>` tasks and an aggregate only for multiple tools or independently useful stages.
Use `dependsOn` for multi-stage work and local Justfiles for complex options or multi-line procedures.
When a project and all children have no JavaScript dependency or useful cache-control requirement, use native commands for light tasks and Just for heavy procedures.
Do not introduce Node solely for those standalone workflows.

Use Vite+'s automatic input tracking by leaving `cache.input` unset.
Do not enumerate source or configuration paths unless a demonstrated input-tracking gap requires an exception.
Declare outputs that the cache must restore.
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
vp run --no-cache ci
vp run e2e
```

`ci` checks and builds all implementations and runs native tests.
It does not execute Docker tests or build documentation examples.
`e2e` builds `fixtures/cli` with the `e2e-example:local` tag, then runs the four language suites sequentially.
Use `vp run @totto2727/e2e-go#ci` or the corresponding Haskell, TypeScript, or Rust package name to select one graph.
Standalone language Docker commands require the prebuilt fixture image.

| Working directory | Native selection                                                        | Docker selection                                                         |
| ----------------- | ----------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| `go/`             | `go test -race ./...`                                                   | `go test -race -tags=e2e ./e2e`                                          |
| `haskell/`        | `cabal test native`                                                     | `cabal test -fdocker-e2e docker-e2e`                                     |
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

Also verify isolation between scopes, owned-container cleanup, and preservation of the caller's image.
Keep assertions inside standard test bodies; extract only shared lifecycle, fixtures, or assertions.
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

## Language-specific maintenance

### Go

The module path is `github.com/totto2727-org/e2e/go`.
Use the race detector and check every error; wrap causes with `%w` when adding context.
`just ci` in `go/` checks the library and compiles the tagged Docker suite without running it.
`just e2e` runs the suite with shuffled order and checks residual containers and image preservation.
Go's framework controls parallelism through `t.Parallel` and `-parallel`.

### Haskell

`just ci` in `haskell/` runs Cabal builds, manifest checks, and native Hspec tests.
`cabal.project` supplies warnings-as-errors and direct test reporting.
Each spec module exports `spec :: Spec`; one-line `Spec.hs` entrypoints use the declared `hspec-discover` build tool.
Cabal can download that tool on first build; use `--offline` only with the required dependencies already cached.
The checkout declares no license or release maintainer, so `cabal check` excludes only those publication metadata checks.
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

## Template basis and task-specific guides

Start from the corresponding workspace template and make only necessary library-specific extensions.
TypeScript follows the [Vite+ library template](https://github.com/totto2727-org/template-vite-plus-lib/tree/ef2173ab26407fdc45b4b05106b5eed561062dd1), Go the [Go template](https://github.com/totto2727-org/template-go-simple/tree/b12903f8f8bf458b93538237c2ee2016dd54865e), and Rust the [Rust template](https://github.com/totto2727-org/template-rust-simple/tree/08ffe1d2d6a27e8d0b4c25f98e5a89a96d8ec1c1).
There is no Haskell workspace template; keep its native Cabal/Hspec configuration.

When changing README or AGENTS, follow `share-artifact` and `documentation-principles`.
