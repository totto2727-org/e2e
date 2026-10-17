# TypeScript CLI E2E

`@totto2727/e2e-cli` is a standalone Node.js library implementing the [shared contract](../README.md) with a native Docker Engine HTTP adapter and a framework-independent scoped lifecycle.
The package is private and is consumed from a local checkout.

## Setup

Use Node.js 24 or newer, pnpm 11.21.0, and a running Linux-container Docker Engine.
Add the local package to your consumer's `package.json`:

```json
{
  "devDependencies": {
    "@totto2727/e2e-cli": "file:/path/to/e2e/typescript"
  }
}
```

The public entrypoint requires generated `dist/index.mjs` and `dist/index.d.mts` files.
Build them from the e2e repository root before installing the local dependency in your consumer:

```sh
pnpm install --frozen-lockfile
pnpm exec vp run @totto2727/e2e-cli#build
```

Install your consumer's dependencies with its package manager.
The usage example also requires Vitest in the consumer's test dependencies.

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

## Development

See [TypeScript maintenance](../AGENTS.md#typescript) for repository maintenance.
