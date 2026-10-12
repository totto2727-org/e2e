# Equivalent CLI scenario examples

These complete consumer source/document pairs exercise the same two cases through the current public APIs:

| Language   | Source                                                                | Sibling document                                                      | Local dependency                                                                                      |
| ---------- | --------------------------------------------------------------------- | --------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Go         | [multiple_scenarios_test.go](./multiple_scenarios_test.go)            | [multiple_scenarios_test.md](./multiple_scenarios_test.md)            | `github.com/totto2727-org/e2e/go/cli`                                                                 |
| Haskell    | [MultipleScenariosSpec.hs](./haskell/MultipleScenariosSpec.hs)        | [MultipleScenariosSpec.md](./haskell/MultipleScenariosSpec.md)        | [Cabal project](./haskell/cabal.project) selects this consumer and the checkout's Haskell library     |
| TypeScript | [multiple_scenarios.test.ts](./typescript/multiple_scenarios.test.ts) | [multiple_scenarios.test.md](./typescript/multiple_scenarios.test.md) | [Private workspace package](./typescript/package.json) depends on the checkout's `@totto2727/e2e-cli` |
| Rust       | [multiple_scenarios.rs](./rust/multiple_scenarios.rs)                 | [multiple_scenarios.md](./rust/multiple_scenarios.md)                 | [Cargo manifest](./rust/Cargo.toml) selects the checkout's Rust library by path                       |

Each runner receives `example-cli:local` and registers separate uniquely named `prints greeting` and `returns expected failure` cases.
The greeting case executes literal argv `echo`, `hello from e2e` and asserts exit `0`, exact stdout `hello from e2e\n`, and empty stderr.
The failure case executes literal argv `false` and asserts exit `1` and two empty streams.
Each case gets a distinct fresh container from its library runner, not a shared application container.
The source assertions observe real library execution. None of the examples supplies fake command output, duplicates a Docker backend, or uses host `docker exec` status.
The libraries own container cleanup and retain the caller's image. These minimal consumers do not independently inspect identities or cleanup and do not assert filesystem state.

## Prerequisites and boundaries

Run commands below from the repository root unless the command explicitly changes directories.
Use the repository's Nix shells for the pinned toolchains and installed Haskell dependencies.
Install TypeScript dependencies through the authoritative root pnpm workspace and lockfile, keeping the root release-age safeguards in force.
The workspace must include `.agents/skills/document-e2e-scenarios/references/example/typescript` before installing. Its package name is `@totto2727/e2e-doc-examples`.
The TypeScript consumer uses real package exports, including public `/vitest`, without source or distribution aliases. Build the local library before checking or collecting the consumer.
Haskell and Rust use their lightweight local consumer manifests without requiring registry publication of either library.
Build artifacts belong under root `tmp/scenario-doc-examples/`. The Rust consumer lockfile is retained so subsequent builds can use `--locked`.

Docker is needed only for the explicit acceptance commands.
The caller must already have built `example-cli:local` with `echo`, `false`, and a long-running default entrypoint. Image preparation remains outside the samples.
Set `DOCKER_HOST` to the actual local Linux-container daemon endpoint if it differs from the language's default. Do not assume the libraries read Docker CLI contexts or offer identical transports.
See the language READMEs for each adapter's supported endpoints and limitations.

## Compile exact consumer sources without Docker

### Haskell

```sh
repo="$PWD"
cd .agents/skills/document-e2e-scenarios/references/example/haskell
nix develop "$repo#haskell" -c just build
```

This compiles and links the exact `MultipleScenariosSpec.hs` with warnings treated as errors, depending on the exposed local `Test.CLI.E2E` module through Cabal rather than copying library sources.
It does not execute `main` or contact Docker.
`main` runs both case actions first and then maps every named `Either SomeException ()` outcome to its own Hspec example. Hspec name filters therefore do not select which case actions execute.
Do not include this consumer suite in a native `cabal test all` task: it is intentionally Docker-bearing when executed.

### TypeScript

```sh
pnpm exec vp run @totto2727/e2e-cli#build
pnpm exec tsc --project .agents/skills/document-e2e-scenarios/references/example/typescript/tsconfig.json
pnpm exec vp test list --no-staticParse --config .agents/skills/document-e2e-scenarios/references/example/typescript/vite.config.ts --project docker
```

The typecheck consumes the built public declarations. The list command evaluates actual registration without running test bodies or lifecycle hooks.
Vitest 5 defaults to static parsing for `list`, which cannot discover indirect `registerCases` registration. Use `--no-staticParse` for the two case names to appear.
The static `native` project deliberately includes no Docker source. This pure consumer sample has no unit-test suite, so it exposes no native `test` task and does not manufacture a successful test run with `--passWithNoTests`.
The static `docker` project includes only `multiple_scenarios.test.ts`. Passing a filename filter to the default native project does not opt into Docker.
The package provides the `check:tsc` primitive, the `check` aggregate, and Docker-free `ci` depending only on `check`. Checks depend on building the local library.
Root orchestration can select `pnpm exec vp run @totto2727/e2e-doc-examples#ci` after workspace installation.

### Rust

```sh
repo="$PWD"
(cd .agents/skills/document-e2e-scenarios/references/example/rust && nix develop "$repo#rust" -c just build)
```

The local `build` recipe compiles the exact declared integration-test source with `cargo test --offline --locked --test multiple_scenarios --no-run` against the local `cli-e2e` crate.
Its explicit target directory is root `tmp/scenario-doc-examples/rust`.
Ordinary `cargo test --offline --locked --test multiple_scenarios --target-dir ../../../../../../tmp/scenario-doc-examples/rust`, run from the sample directory, leaves the Tokio test ignored. This default exclusion does not prove either Docker case passes.
The case functions use public `Environment`, `Case`, `Command`, and byte-oriented `CommandResult` and return every error to the runner.

## Explicit real Docker acceptance

After the caller prepares the image, execute these separately from native CI and only when Docker acceptance is authorized:

```sh
# Haskell, from the repository root:
repo="$PWD"
(cd .agents/skills/document-e2e-scenarios/references/example/haskell && nix develop "$repo#haskell" -c just e2e)

# TypeScript, after the local library build and workspace install:
pnpm exec vp test run --config .agents/skills/document-e2e-scenarios/references/example/typescript/vite.config.ts --project docker

# Rust, selecting only the ignored consumer test:
(cd .agents/skills/document-e2e-scenarios/references/example/rust && nix develop "$repo#rust" -c just e2e)
```

The Haskell and Rust sample-local Justfiles centralize the long build and acceptance options for each single consumer project.
Haskell `e2e` runs `cabal test --offline` for `scenario-doc-example:test:scenarios` with direct Hspec reporting and the same root temporary build directory as `build`.
Rust `e2e` runs its declared integration test with `--ignored --nocapture`, using the same root target directory as `build`.
The TypeScript package exposes the same selection through `e2e:vitest` and aggregate `e2e`, both uncached and dependent on the local library build.
These commands execute the exact sample files, not copied source or substitute test fixtures. The commands do not build, pull, or delete images.
Root task wiring and image preparation belong to the consuming repository's coordinator, not this documentation skill.

## Documentation validation limits

```sh
PYTHONDONTWRITEBYTECODE=1 python3 .agents/skills/document-e2e-scenarios/scripts/validate_scenario_docs.py .agents/skills/document-e2e-scenarios/references/example
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s .agents/skills/document-e2e-scenarios/scripts -p 'test_*.py'
```

The bundled validator discovers the Go source/document pair only. Its success does not validate Haskell, TypeScript, or Rust coverage and never proves semantic truth.
Review each non-Go document against its exact source and registered functions, checking sibling links, section order, literal argv, expected status, stream bytes/text, fixtures, and stated non-assertions.
Compilation validates public interface compatibility. Collection validates registered names. Only actual Docker execution validates these observable cases against the prepared image.
Report compilation, document review, collection, and acceptance separately rather than claiming that one substitutes for another.
