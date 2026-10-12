# e2e

Small libraries for CLI end-to-end tests in disposable Linux containers.
The shared contract is language-independent. Each implementation uses its language's normal values, errors, and test framework.

## Purpose and boundaries

Build the application image outside the test, then exercise the real executable through argv commands and observable output or filesystem state.
Every case owns a fresh container. Multi-command workflows remain in the test, not in the library.
The libraries do not build, pull, or remove the caller's application image, mount the host workspace, run interactive commands, or manage application-specific state.
An implementation may use backend infrastructure images, such as Testcontainers' resource reaper; its language README documents this behavior.
A container is not a shell abstraction: commands are argv entries, and shell syntax requires an explicit shell command.

## Implementations

| Language   | Implementation and API                         | Framework      | Shared API   | Native checks                            | Docker acceptance        |
| ---------- | ---------------------------------------------- | -------------- | ------------ | ---------------------------------------- | ------------------------ |
| Go         | [go/README.md](./go/README.md)                 | `testing`      | Implemented. | Passed, including race tests.            | Six common cases passed. |
| Haskell    | [haskell/README.md](./haskell/README.md)       | Hspec outcomes | Implemented. | Passed, including a Cabal library build. | Six common cases passed. |
| TypeScript | [typescript/README.md](./typescript/README.md) | Vitest         | Implemented. | Passed, including built package imports. | Six common cases passed. |
| Rust       | [rust/README.md](./rust/README.md)             | Tokio tests    | Implemented. | Passed, including Clippy and rustdoc.    | Six common cases passed. |

Each shared API includes command execution, exact assertions, byte file transfer, physical path checks, isolated cases, deadlines, and owned-resource cleanup.
Docker acceptance uses a local Linux-container Engine over a Unix socket and verifies cleanup and image preservation. A controlled transport test is not Docker acceptance.
No package publication or release is implied by this table.

| Language   | Backend                                      | Additional connection behavior                                                                                                                                                |
| ---------- | -------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go         | Testcontainers and the native Moby exec API. | Testcontainers configuration and negotiated or explicit API versions. Configured TLS has a native transport regression test; initial TLS dialing follows the upstream dialer. |
| Haskell    | Native Engine HTTP through `http-client`.    | Unix sockets and HTTP/TCP endpoints. Unversioned Engine endpoints; explicit API-version overrides and TLS are rejected.                                                       |
| TypeScript | Native Engine HTTP through Node.             | Unix sockets and HTTP/TCP endpoints. Unversioned Engine endpoints; explicit API-version overrides and TLS are rejected.                                                       |
| Rust       | Bollard.                                     | Unix sockets and HTTP/TCP endpoints with API negotiation. Explicit API-version overrides and TLS are rejected.                                                                |

Connection support is not a claim that every transport has passed real-daemon acceptance.
Use the language READMEs for cancellation boundaries, non-UTF-8 handling, and concrete argument types.

## Shared values

- **Case**: a unique name and an action that receives an isolated environment.
- **Command**: nonempty argv with a nonempty executable, an optional working directory, and optional environment entries. Preserve argument boundaries, empty argument values, and environment values. Reject NUL in argv, working directory, and environment entries before execution.
- **Result**: exit code, stdout, and stderr. Keep both output streams separate and preserve valid UTF-8 command text unchanged. Preserve ordering within each stream, but do not promise ordering between streams. Languages may expose text or bytes; their READMEs define non-UTF-8 handling. File APIs always transfer exact bytes.
- **Environment**: the case-owned command and file operations. Do not expose a mutable Docker client as a public escape hatch.

A nonzero application exit code is a command result, not an infrastructure error.
Daemon failures, failure to attach, output decode errors, cancellation, and deadline expiry are errors. An exit code is valid only after successful execution and inspection.
Command results and execution errors delivered by the command API retain output already captured. Cancelling or abandoning the whole runner may instead propagate framework cancellation without delivering a command outcome. Each language README explains that boundary and its error representation.
Never infer application exit status solely from the host `docker exec` process: its failure can instead be a Docker error.

## Shared operations

Names below identify behaviors, not mandatory spellings in every language.

| Operation           | Contract                                                                                                                                                              |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Run cases           | Resolve a local prebuilt image once and run each case in a fresh container.                                                                                           |
| Run command         | Execute a `Command` and return a separate-stream `Result`.                                                                                                            |
| Check command       | Execute a command and compare its exit code, stdout, and stderr exactly against an expected `Result`. An empty expected stream asserts absence, not an ignored field. |
| Write file          | Copy exact bytes to a container path with mode `0644`. The parent directory must exist.                                                                               |
| Read file           | Return exact bytes from a container file.                                                                                                                             |
| Check file          | Read a file and compare exact bytes.                                                                                                                                  |
| Check missing       | Reject any existing path, including a dangling symbolic link.                                                                                                         |
| Check regular file  | Require a physical regular file, not a symbolic link to a file.                                                                                                       |
| Check directory     | Require a physical directory, not a symbolic link to a directory.                                                                                                     |
| Check symbolic link | Compare the literal target, without resolving or normalizing it. A dangling target is valid.                                                                          |

Path assertions require `test` with `-e`, `-L`, `-f`, and `-d`, and `readlink` with `--` support in the image.
Probe exit codes other than `0` or `1` are infrastructure errors, not successful absence checks.

## Container lifecycle and deadlines

1. Inspect the caller's local image. Fail if it is absent, without pulling it.
2. Resolve the immutable image ID and create a stopped lease container from that ID. Do not delete the image.
3. Acquire a case slot. At most two cases may own slots concurrently; queued cases have not started their execution budget.
4. Start a one-minute case budget, including container creation and startup. Create a fresh container from the resolved image ID and preserve its image entrypoint.
5. Register cleanup immediately when ownership is acquired, including partial startup failures. Do not let a command connection outlive its case.
6. Execute all case operations against that case's deadline. Deadline expiry or cancellation must unblock output reads and release their owned connections.
7. On completion, failure, or cancellation, remove the case container using a fresh 30-second cleanup budget independent of the case deadline. Release its slot only after cleanup.
8. After all cases finish, remove the stopped lease with its own fresh 30-second budget. Preserve the caller-owned image.

The supplied image must remain running long enough to receive commands; the acceptance fixture uses `sleep infinity`.
There is no generic application-readiness check.
Closing an exec connection does not prove that the remote process stopped. Container cleanup is the required final process boundary.
Report cleanup failure instead of silently treating a leaked resource as success.
Use unpredictable names and ownership labels to recover owned resources when creation fails before returning an ID.
Cleanup cannot guarantee recovery if the daemon commits a creation only after a failed request and the subsequent cleanup lookup have both completed; daemon outages and unresolved creation outcomes must be reported rather than hidden.
These budgets bound library operations, not arbitrary caller code. Each language documents the effect of its framework's cancellation mechanism.

The baseline is a local Linux-container Docker Engine. Backend choice is not part of API equivalence: Testcontainers, a native Docker client, or a small Engine API adapter must satisfy the same observable contract.
A backend must not silently pull images or merge infrastructure diagnostics into command stderr.
Additional remote-daemon, TLS, or platform support must be documented and tested per implementation, not assumed from another language.

## Shared acceptance behavior

Each language must exercise its public interface against the same caller-built image and report these observations:

| Case             | Observable behavior                                                                                                                           |
| ---------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| Greeting         | Exact stdout, empty stderr, and exit code zero.                                                                                               |
| File workflow    | Multiple argv commands share one case container; copied bytes equal the persisted file.                                                       |
| Expected failure | Exit code `7` is a successful execution result, not an infrastructure error.                                                                  |
| Separate streams | Working directory and environment reach the process; stdout and stderr remain distinct on a nonzero result.                                   |
| Physical paths   | Regular files, directories, relative and dangling links, and missing paths follow the exact assertion contracts; invalid states are rejected. |
| Deadline         | An indefinitely running command returns a deadline error; cleanup removes the container.                                                      |

Also verify distinct container identities for separate cases, no scenario or lease containers left behind, and preservation of the caller-owned image.
Native tests should cover invalid input, infrastructure errors, exact comparison failures, cleanup failures, and cancellation at controlled transport boundaries without requiring Docker.
Keep Docker execution behind an explicit E2E task. Normal build and unit-test tasks must not require Docker.

## Repository and tasks

`go/`, `haskell/`, `typescript/`, and `rust/` contain independently usable libraries and their language-specific API documentation.
This mixed-language workspace uses Vite+ for task dependencies, caching, and cross-project execution.
Language-local Justfiles retain their template commands and complex scripts; there is no root Justfile that orchestrates projects.
The root `pnpm-lock.yaml` is authoritative for the workspace.

```sh
nix develop -c vp install --frozen-lockfile
nix develop -c vp run ci
nix develop -c vp run e2e
```

`ci` checks, builds, and runs native tests without Docker.
`e2e` first builds the shared image, then runs the language acceptance tasks and the [equivalent documentation samples](./.agents/skills/document-e2e-scenarios/references/example/README.md) sequentially.
`ci` also compiles those exact sample sources through their real local package dependencies without executing Docker cases.
Docker image creation and acceptance are not cached because daemon state is not a source-file output.
Pure checks and builds are cached with declared source inputs and recoverable output paths.
Formatter and linter exclusions come from the repository's `.gitignore` files through [@totto2727/gitignore-patterns](https://github.com/totto2727-org/gitignore-patterns).
The root configuration owns both blocks, so commands from the TypeScript package use the same exclusions without duplicate lists.
The helper's root-anchored literals are not cache-input globs, and Vite+ cache inputs do not automatically exclude Gitignored files.
The TypeScript build uses automatic input tracking and declares `dist/**` as its restorable output; changing or removing those generated files does not require a parallel manual exclusion list.
Tests that import the built package still track `dist/` as an input through their build dependency; do not exclude it from those consumers.
Individual exclusions require evidence of a helper limitation or missing cache-input coverage.
For example, `nix develop -c vp run @totto2727/e2e-rust#ci` selects only the Rust task graph.
The native libraries remain usable without JavaScript: `nix develop .#rust -c bash -c 'cd rust && just ci'` runs the standalone Rust template commands.

### Task categories

| Aggregate       | Scope                                                                                                                                                                       |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `fix`           | Automatic formatter and linter corrections. Do not cache mutations.                                                                                                         |
| `check`         | All static checks: formatting, linting, and type checking.                                                                                                                  |
| `build`         | Produce the final application or library artifacts.                                                                                                                         |
| `test`          | Lightweight, reproducible tests. Controlled transport fixtures, in-memory databases, and temporary directories are permitted; external production systems are not required. |
| `pre*`, `post*` | Named prerequisite or follow-up stages, wired through explicit dependencies. Type generation can be a `prebuild` dependency shared by `check` and `test`.                   |
| `e2e`           | Explicitly selected heavyweight or multi-system acceptance tests, excluded from normal `test` and never cached against source files alone.                                  |

Put a single command directly in its base task, such as `build: { command: 'cargo build' }`, without an empty aggregate and a lone executing leaf.
When a category needs multiple tools or independently useful stages, use `<category>:<CLI>` leaves, such as `check:vp`, `check:clippy`, and `test:vitest`, and aggregate them through `dependsOn`.
A case-specific suffix can distinguish consumers that use the same CLI when that subdivision is needed.
Cross-project orchestration uses named dependencies, not a sequence of hidden lifecycle hooks.
`ci` is a justified convenience aggregate for checks, artifacts, and native tests.
`pree2e` prepares the caller-owned fixture and its sample image tag; it is not part of native CI.
Package inspection is a build follow-up, not an extra test category. Other proposed categories need a concrete purpose that does not fit these stages.

### Task-runner patterns

| Project tree and task                                                                         | Recommended pattern                                                                                                                               |
| --------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| JavaScript or mixed-language projects, especially with shared dependencies or useful caching. | Use Vite+ tasks and `dependsOn` to orchestrate projects. Put small commands directly in `vite.config.ts`.                                         |
| Complex options, multi-line scripts, or repeatable project-local procedures.                  | Keep the procedure in a local Justfile and register `just <recipe>` as a Vite+ task when the project participates in a Vite+ workspace.           |
| No JavaScript in the project or any child, and no useful cache-control requirement.           | Use native language commands or task aliases for lightweight tasks and Just for heavy procedures. Do not introduce Node solely for orchestration. |

Select tests through the runner's standard command options, not an environment-dependent file list:

| Language   | Native selection                                                              | Explicit Docker selection                                                |
| ---------- | ----------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| Go         | Run `go test -race ./...` in `go/`.                                           | Run `go test -race ./...` in the separate `go/example/` module.          |
| Haskell    | `cabal test native`                                                           | `cabal test docker-e2e -f docker-e2e`                                    |
| TypeScript | `vp test run --project native`; built package smoke uses `--project package`. | `vp test run --project docker`                                           |
| Rust       | `cargo test --locked --all-features` leaves ignored Docker tests unexecuted.  | `cargo test --locked --all-features --test e2e -- --ignored --nocapture` |

Standalone Docker tasks require a caller-built `e2e-example:local` image.
The root E2E task supplies that acceptance fixture, not the libraries themselves.
See [AGENTS.md](./AGENTS.md) for contributor instructions and each language README for full toolchain commands.

### Template basis

TypeScript settings extend the [Vite+ library template](https://github.com/totto2727-org/template-vite-plus-lib/tree/ef2173ab26407fdc45b4b05106b5eed561062dd1), including its strict TypeScript presets, formatter, lint settings, package-manager safeguards, and Nix Vite+ overlay.
Go and Rust use the [Go template](https://github.com/totto2727-org/template-go-simple/tree/b12903f8f8bf458b93538237c2ee2016dd54865e) and [Rust template](https://github.com/totto2727-org/template-rust-simple/tree/08ffe1d2d6a27e8d0b4c25f98e5a89a96d8ec1c1) settings, with library-specific build targets, locked dependencies, and explicit Docker tasks.
The workspace has no Haskell template; Haskell retains its Cabal/Hspec configuration rather than claiming template equivalence.
Library exports, Node's Docker transport, Vitest projects, and acceptance fixtures are necessary extensions, not replacement toolchains.

## License

No license has been declared for this repository.
