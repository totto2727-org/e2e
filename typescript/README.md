# TypeScript CLI E2E

`@totto2727/e2e-cli` is a standalone Node.js library implementing the [shared contract](../README.md) with a native Docker Engine HTTP adapter and a framework-independent scoped lifecycle.
The package is private in this checkout and publication is not requested.

## Usage

```ts
import { withEnvironment } from '@totto2727/e2e-cli'
import { expect, test } from 'vitest'

test.concurrent('greeting', async (context) => {
  await withEnvironment(
    'my-application:local',
    async (environment) => {
      expect(
        await environment.run({ args: ['my-application', 'hello'], workingDir: '/workspace', env: ['MODE=test'] }),
      ).toEqual({ exitCode: 0, stdout: 'hello\n', stderr: '' })
    },
    context.signal,
  )
}, 125_000)
```

Build the image before starting the tests, and keep its default entrypoint running long enough to receive commands.
Commands preserve argv boundaries, including empty arguments, and environment strings such as `KEY=value`.
Shell syntax requires explicit argv such as `['sh', '-c', 'echo first > /workspace/result.txt']`.
`withEnvironment<T>(image, action, signal?): Promise<T>` returns the awaited callback value after cleanup.
The callback receives `Environment` and returns `Promise<T>`.
Use it inside ordinary `test` or `test.concurrent` callbacks and forward `context.signal`.
Ordinary Vitest `expect` assertions remain available.
The consumer's test framework controls names, scheduling, concurrency, and its enclosing timeout, with no library queue, registration, or suite aggregation.
The package does not depend on a test framework and has no `/vitest` adapter.

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
A callback rejection is rethrown unchanged, including arbitrary non-Error values, unless cleanup also fails.
Cleanup failures reject the invocation, and simultaneous callback/cleanup failures are retained in an `AggregateError`.
A callback that returns after its signal was cancelled still rejects with the cancellation reason.

Each invocation inspects the caller-built local image, resolves its immutable ID, and retains it with a stopped lease container without pulling or removing the image.
It creates and starts one separate fresh container for its callback, then removes the case container and lease on completion or failure.
The fixed 60-second operation budget begins when the helper is invoked, before image inspection, lease creation, container creation, and startup.
Vitest time spent waiting to schedule the test does not consume this budget, because the helper has not yet been called.
The consumer configures a test timeout that allows the operation and cleanup budgets, such as `125_000` milliseconds, and forwards Vitest's cancellation signal.
The exposed `environment.signal` also allows caller code to cooperate with cancellation.
Arbitrary caller code that ignores the signal, never resolves, or blocks the JavaScript event loop is not forcibly terminated by this library.

Cancellation destroys the actual HTTP request and attached response stream.
Returning from a case also closes its signal so an owned connection cannot outlive the case.
Disconnecting exec does not prove the remote process stopped, so removing the case container is the final process boundary.
Each owned resource's cleanup receives a fresh 30-second total budget independent of the operation deadline.
The two sequential cleanup stages can take up to 60 seconds in total, and lease cleanup is still attempted if case cleanup fails.
Random UUID container names and matching ownership labels are acquired before creation, enabling safe recovery after partial creation or startup failure.
Only matching owned containers are removed, and a cleanup failure is reported rather than hidden.
The stopped lease is removed after the callback and case cleanup, leaving the caller's image intact.

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
Packaging retains the template's ESM declaration generation, with the necessary Node platform and a single public entrypoint.
Vitest is used only by consumer tests, not by the reusable library.

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
Use `vp run test:vitest-package` to build and smoke-test the public package entrypoint, or `vp test run --project package` when the build is already available.
Use `vp test run --project docker` for Docker acceptance against the existing caller-built image.
The explicit [Docker tests](test/docker.test.ts) define six ordinary concurrent Vitest tests and invoke `withEnvironment` inside each callback to verify distinct identities, exact file bytes/mode, removal of scenario and lease containers, and preservation of the caller-owned image.
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
The `build` cache uses automatic input tracking and `output: ['dist/**']`. Public build checks confirmed that changing a generated file or moving `dist/` away leaves a cache hit and restores byte-identical output without a manual `!dist/**` input rule.
Formatter and linter blocks are shared from the repository root and derive their exclusions from `.gitignore` with `@totto2727/gitignore-patterns`.
Use `vp run --no-cache ci` for fresh execution.

## Validation status

The scoped API migration passes 52 native tests, two built-package tests, strict library TypeScript checking, and ESM/declaration builds.
Native regression coverage includes arbitrary callback results and rejection identity, unrestricted consumer-owned concurrency, invocation-time deadlines covering image inspection, cancellation, partial output, ownership checks, and independent cleanup budgets.
Built-package checks verify the single public entrypoint and rejection of retired framework and internal transport subpaths.
These native HTTP fixtures do not prove real Docker acceptance of the migrated lifecycle.
The six ordinary Docker tests and the two sample consumer tests remain explicitly selected acceptance workflows, separate from the normal `ci` task.
Real Docker execution of the migrated API passed through the public root `vp run e2e` task on 2026-10-12: six ordinary Vitest tests and the two exact sample tests, with container cleanup and caller-image preservation.
A separate standard `--project docker -t '^greeting$'` invocation passed one test and skipped the other five, including the filter-safe resource audit. Native checks alone are not the basis of this Docker claim.
