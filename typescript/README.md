# TypeScript CLI E2E

`@totto2727/e2e-cli` is a standalone Node.js library implementing the [shared contract](../README.md) with a native Docker Engine HTTP adapter and Vitest integration.
The package is private in this checkout and publication is not requested.

## Usage

```ts
import { registerCases } from '@totto2727/e2e-cli/vitest'

registerCases('my-application:local', [
  {
    name: 'greeting',
    async run(environment) {
      await environment.checkCommand(
        { args: ['my-application', 'hello'], workingDir: '/workspace', env: ['MODE=test'] },
        { exitCode: 0, stdout: 'hello\n', stderr: '' },
      )
    },
  },
])
```

Build the image before starting the tests, and keep its default entrypoint running long enough to receive commands.
Commands preserve argv boundaries, including empty arguments, and environment strings such as `KEY=value`.
Shell syntax requires explicit argv such as `['sh', '-c', 'echo first > /workspace/result.txt']`.
`runCases(image, cases, signal?)` provides the same lifecycle without registering Vitest tests and rejects with an `AggregateError` containing case or cleanup failures.
Case names must be unique and nonempty.

## Values and operations

`Command` has `args: readonly string[]`, optional `workingDir: string`, and optional `env: readonly string[]`.
An empty argv or executable and NUL in any command string are rejected before contacting Docker.
`Result` has `exitCode: number`, `stdout: string`, and `stderr: string`, with no implicit combined output.
Successful command output must be valid UTF-8, and each stream preserves its text exactly, including trailing newlines.
Invalid UTF-8 is an execution error, not a silently repaired successful result.

| Environment operation               | Behavior                                                                                              |
| ----------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `run(command)`                      | Return a result, including a nonzero application exit code.                                           |
| `checkCommand(command, expected)`   | Compare all three result fields exactly; an empty stream asserts absence.                             |
| `writeFile(path, bytes)`            | Transfer a `Uint8Array` through the archive endpoint with mode `0644`; the parent must already exist. |
| `readFile(path)`                    | Return exact bytes as a `Buffer`.                                                                     |
| `checkFile(path, bytes)`            | Compare exact bytes.                                                                                  |
| `checkMissing(path)`                | Reject existing paths and dangling links.                                                             |
| `checkRegularFile(path)`            | Require a physical regular file, not a symbolic link.                                                 |
| `checkDirectory(path)`              | Require a physical directory, not a symbolic link.                                                    |
| `checkSymlink(path, literalTarget)` | Compare the unnormalized literal link target.                                                         |

Path assertions require `test -e/-L/-f/-d` and `readlink --` in the container.
Probe exit codes other than `0` or `1` are infrastructure failures.
Neither a Docker SDK nor a mutable Docker client is exposed through an environment.
Always await operations before returning from a case.

## Errors, deadlines and cleanup

Input validation rejects with `TypeError`.
`ExecutionError` carries `partialResult: { stdout, stderr }` with no exit code and an original `cause`.
Captured partial text uses replacement characters only when an interrupted or invalid UTF-8 sequence cannot be represented as a string.
The cause may be `DockerError`, `DeadlineError`, a cancellation reason, or a native I/O/decode error.
The daemon's diagnostics remain in the infrastructure error and are never appended to child stderr.
An exit code is returned only after successful exec inspection reports `Running: false`; output EOF alone is not process completion.
Cleanup failures reject the case, and simultaneous case/cleanup failures are retained in an `AggregateError`.

The suite inspects the local image once, resolves its immutable ID, and retains it with a stopped lease container without pulling or removing the image.
Every case receives a fresh container and retains its slot through cleanup, with at most two slots held at once.
The fixed 60-second budget starts after acquiring a slot and includes creation, startup, and every environment operation.
Vitest registration disables its enclosing test timeout so time spent in the slot queue does not consume this budget, and forwards Vitest's cancellation signal.
The exposed `environment.signal` also allows caller code to cooperate with cancellation.
Arbitrary caller code that ignores the signal, never resolves, or blocks the JavaScript event loop is not forcibly terminated by this library.

Cancellation destroys the actual HTTP request and attached response stream.
Returning from a case also closes its signal so an owned connection cannot outlive the case.
Disconnecting exec does not prove the remote process stopped, so removing the case container is the final process boundary.
Each cleanup receives a fresh 30-second budget independent of the case deadline.
Random UUID container names and matching ownership labels are acquired before creation, enabling safe recovery after partial creation or startup failure.
Only matching owned containers are removed, and a cleanup failure is reported rather than hidden.
The stopped lease is removed after all cases, leaving the caller's image intact.

## Docker support

The baseline is a Linux-container Docker Engine accessed through `DOCKER_HOST=unix:///path/to/docker.sock`, `http://host:port`, or `tcp://host:port` using explicit unencrypted HTTP.
The default socket is `/var/run/docker.sock`.
Docker CLI contexts are not read, so a nondefault local socket must be supplied with `DOCKER_HOST`.
`DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH`, HTTPS, and credentials in endpoint URLs are rejected rather than ignored.
TLS, Windows named pipes/containers, and HTTP 101 hijacking are not supported.
Exec start uses a normal POST with `{ Detach: false, Tty: false }` and consumes the documented HTTP 200 raw stream, decoding fragmented eight-byte multiplex headers and bodies.
No host `docker exec`, pull, image removal, workspace mount, or application-specific readiness abstraction is used.
Files use Docker's archive endpoint with `tar-stream` rather than command fallback.
The adapter buffers output and archives in memory and is intended for small CLI test fixtures.

Sources: [Docker Engine API](https://docs.docker.com/reference/api/engine/), [Vitest hooks](https://vitest.dev/api/hooks), [Vitest timeout](https://vitest.dev/config/testtimeout), and [tar-stream public API](https://github.com/mafintosh/tar-stream).
The Vitest and archive APIs were consulted through the configured Context7 documentation connection.

## Toolchain and checks

Use Node.js 24+, pnpm, and the local Vite+ CLI.
The repository-root `pnpm-lock.yaml` is authoritative, and its workspace configuration enforces a strict 24-hour minimum release age without exclusions or lifecycle-script grants.
Vite+ requires its bundled Vite alias and exact Vitest version, recorded with official migration/update references in the repository-root `pnpm-workspace.yaml`.
TypeScript configuration follows `template/vite-plus-lib`: `@tsconfig/strictest` and `@tsconfig/node-ts` with only the template's `target`, `module`, `noEmit`, and Node types settings.
Formatter and type-aware lint options match the template, including import/package sorting and preserved Markdown wrapping.
Packaging retains the template's ESM declaration generation, with the necessary Node platform, two public entrypoints, and external Vitest peer.
The `/vitest` integration has an optional Vitest peer dependency so ordinary non-Vitest callers can use the root entrypoint.

From the repository root:

```sh
nix develop .#typescript -c vp install --frozen-lockfile
nix develop .#typescript -c vp run --filter @totto2727/e2e-cli ci
# Explicit Docker acceptance only, after the root builds e2e-example:local:
nix develop .#typescript -c vp run --filter @totto2727/e2e-cli e2e
```

Normal tests use controlled HTTP servers and reproducible local resources, such as temporary directories or in-memory fixtures, and do not require Docker or external services.
Vitest 5.0.3 selects static projects through its standard `--project` option, without environment-variable include toggles.
From `typescript/`, `vp test run` defaults to the `native` project, and `vp test run --project native` makes that selection explicit.
Use `vp run test:vitest-package` to build and smoke-test both exported package entrypoints, or `vp test run --project package` when the build is already available.
Use `vp test run --project docker` for Docker acceptance against the existing caller-built image.
The explicit [Docker tests](test/docker.test.ts) register the six shared cases separately and verify distinct identities, exact file bytes/mode, removal of scenario and lease containers, and preservation of the caller-owned image.
The `e2e` task directly selects the Docker project against the existing image.
It disables Vite+ task caching so every invocation observes the current Docker daemon rather than replaying earlier success output, and it does not build the image itself.
The deadline case takes approximately one minute.

The package task graph uses direct base tasks for single commands and subdivides `test` because native and built-package tests have different prerequisites.
`fix` invokes `vp check --fix`, `check` invokes `vp check`, and `build` invokes `vp pack`; formatting, linting, and type checking remain a single Vite+ operation rather than artificially split tasks.
`test:vitest` selects the native project and `test:vitest-package` selects the built-package project after `build`; `test` runs both without Docker.
`postbuild` explicitly depends on `build` and runs a private-package-safe `vp pm pack -- --dry-run` to validate package contents after generated outputs exist.
`ci` depends on `check`, `test`, and `postbuild`, never Docker acceptance.
`fix` disables caching because it mutates source files, and `e2e` disables caching because it observes Docker.
The remaining tasks retain caching; `check`, `test:vitest`, `test:vitest-package`, and `postbuild` declare `output: []` because checks, tests, and a package dry-run do not produce lasting artifacts.
The `build` cache uses `input: [{ auto: true }, '!dist/**']` and `output: ['dist/**']` so generated output is not an input and can be restored on cache hits.
Use `vp run --no-cache ci` for fresh execution.

## Validation status

The template-aligned toolchain passes formatting, type-aware lint, strict TypeScript checking, ESM/declaration builds, 43 native tests, and the built-public-entrypoint smoke test.
Default `vp test run` executes only the 43 native tests, and explicit `--project docker` file collection selects only the Docker suite without executing it.
The build cache restores byte-identical `dist/` contents after those outputs are moved away.
The earlier implementation also passed real Docker acceptance of the six registered cases against `e2e-example:local`.
Those Docker observations validate the Unix-socket Linux-container baseline; controlled HTTP tests cover TCP transport and cancellation, but real remote TCP or TLS acceptance is not claimed.
Docker acceptance is not rerun as part of toolchain-only validation.
