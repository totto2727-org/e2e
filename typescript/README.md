# TypeScript CLI E2E

`@totto2727/e2e-cli` provides scoped container environments for TypeScript users writing CLI tests.
It implements the [shared contract](../README.md) without a test-framework dependency.

## Setup

> [!IMPORTANT]
> The package is not yet published.
> The declaration below uses the current metadata version, not an available npm release.

| Requirement                    | Consumer setup                                                                                      |
| ------------------------------ | --------------------------------------------------------------------------------------------------- |
| Runtime                        | Node.js 24 or newer.                                                                                |
| Container image and Engine     | [Shared prerequisites](../README.md#prerequisites-and-setup) and [Docker support](#docker-support). |
| Test framework for the example | Vitest in your test dependencies.                                                                   |

After publication, add the dependency to your `package.json` and install it with your package manager:

```json
{
  "devDependencies": {
    "@totto2727/e2e-cli": "^0.0.0"
  }
}
```

The package exports ESM JavaScript and TypeScript declarations.
Consumers do not build this repository.

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

Use ordinary Vitest test names and assertions.
Vitest controls scheduling, concurrency, and timeouts.
There is no `/vitest` adapter.

> [!IMPORTANT]
> Await all environment operations before the callback returns.
> Forward `context.signal` to cancel library operations when Vitest cancels the test.
> The library cannot terminate caller code that ignores cancellation or blocks the JavaScript event loop.

## Values and operations

See the [shared values](../README.md#shared-values) for command validation and output rules.
The [shared operations](../README.md#shared-operations) define comparisons, file modes, parent-directory requirements, and symbolic-link behavior.

```ts
declare function withEnvironment<T>(
  image: string,
  action: (environment: Environment) => Promise<T>,
  signal?: AbortSignal,
): Promise<T>
```

The promise returns the callback value after cleanup succeeds.

| Exported type   | Fields                                                                      |
| --------------- | --------------------------------------------------------------------------- |
| `Command`       | `args: readonly string[]`, `workingDir?: string`, `env?: readonly string[]` |
| `Result`        | `exitCode: number`, `stdout: string`, `stderr: string`                      |
| `PartialResult` | `stdout: string`, `stderr: string`, `exitCode?: never`                      |

| `Environment` member                                | Type              |
| --------------------------------------------------- | ----------------- |
| `readonly signal`                                   | `AbortSignal`     |
| `run(command: Command)`                             | `Promise<Result>` |
| `checkCommand(command: Command, expected: Result)`  | `Promise<void>`   |
| `writeFile(path: string, bytes: Uint8Array)`        | `Promise<void>`   |
| `readFile(path: string)`                            | `Promise<Buffer>` |
| `checkFile(path: string, expected: Uint8Array)`     | `Promise<void>`   |
| `checkMissing(path: string)`                        | `Promise<void>`   |
| `checkRegularFile(path: string)`                    | `Promise<void>`   |
| `checkDirectory(path: string)`                      | `Promise<void>`   |
| `checkSymlink(path: string, literalTarget: string)` | `Promise<void>`   |

Successful command output requires valid UTF-8 and preserves text, including trailing newlines.
Invalid UTF-8 causes an execution error, not a repaired result.
`Environment` exposes no Docker client.

## Errors, deadlines and cleanup

| Condition                                                             | Rejection or cause                                                                       |
| --------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Invalid command strings, empty argv or executable, or empty file path | `TypeError`. NUL is invalid in command strings, file paths, and link targets.            |
| Command execution failure                                             | `ExecutionError` with `partialResult` and the original `cause`, but no exit code.        |
| Docker failure                                                        | `DockerError`, with optional `statusCode`. Daemon diagnostics do not enter child stderr. |
| Operation deadline                                                    | `DeadlineError`.                                                                         |
| Callback rejection                                                    | The original value, including a non-Error value, unless cleanup also fails.              |
| Callback returns after cancellation                                   | The cancellation reason.                                                                 |
| Multiple operation or cleanup failures                                | `AggregateError` containing the failures.                                                |
| Cleanup failure alone                                                 | The cleanup error.                                                                       |

An `ExecutionError` cause can be a `DockerError`, `DeadlineError`, cancellation reason, or native I/O or decoding error.
Partial output uses replacement characters for invalid or incomplete UTF-8 sequences.
An exit code requires exec inspection with `Running: false`, not output EOF alone.

| Budget or boundary  | TypeScript behavior                                                                                                                       |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| Operation           | Fixed 60 seconds from helper invocation, including image inspection and container startup. Vitest scheduling time is outside this budget. |
| Cleanup             | Fresh 30 seconds per owned container, up to 60 seconds total. Lease cleanup is attempted even if case cleanup fails.                      |
| Vitest timeout      | Allow for operation and cleanup, for example `125_000` milliseconds as above.                                                             |
| Cancellation        | Destroys the HTTP request and attached response stream. `environment.signal` lets caller code cooperate.                                  |
| Callback completion | Closes `environment.signal` and its owned connections.                                                                                    |

See [shared lifecycle and cleanup limits](../README.md#lifecycle-deadlines-and-cancellation) for image retention, container removal, and unresolved creation outcomes.
The library removes only containers with matching ownership labels.

## Docker support

| Setting or capability  | Support                                                                                                                       |
| ---------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| Default `DOCKER_HOST`  | `unix:///var/run/docker.sock`                                                                                                 |
| Other endpoints        | `unix:///path/to/docker.sock`, `http://host:port`, `tcp://host:port`                                                          |
| Docker CLI contexts    | Not read. Set `DOCKER_HOST` for a nondefault socket.                                                                          |
| Transport              | Linux-container Engine over Unix sockets or unencrypted HTTP.                                                                 |
| Rejected configuration | Nonempty `DOCKER_TLS_VERIFY` or `DOCKER_CERT_PATH`, HTTPS, URL credentials, URL paths other than `/`, queries, and fragments. |
| Unsupported            | TLS, Windows named pipes, Windows containers, and HTTP 101 hijacking.                                                         |
| Exec stream            | HTTP 200 raw stream with `{ Detach: false, Tty: false }`.                                                                     |
| File transfer          | Docker archive endpoint, without command fallback.                                                                            |
| Data size              | The adapter buffers output and archives in memory. Use small CLI test fixtures.                                               |

## Development

See [TypeScript maintenance](../AGENTS.md#typescript) for repository maintenance.
