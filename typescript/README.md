# Caseyard for TypeScript

`@totto2727/caseyard` provides scoped container environments for TypeScript users writing CLI tests.
It uses [Testcontainers for Node.js](https://node.testcontainers.org/) and implements the [shared contract](../README.md) without a test-framework dependency.

## Setup

| Requirement                    | Consumer setup                                                                                      |
| ------------------------------ | --------------------------------------------------------------------------------------------------- |
| Runtime                        | Node.js 24 or newer.                                                                                |
| Container image and Engine     | [Shared prerequisites](../README.md#prerequisites-and-setup) and [Docker support](#docker-support). |
| Test framework for the example | Vitest in your test dependencies.                                                                   |

Add the dependency to your `package.json` and install it with your package manager:

```json
{
  "devDependencies": {
    "@totto2727/caseyard": "^0.1.0"
  }
}
```

The package exports ESM JavaScript and TypeScript declarations.
Consumers do not build this repository.

## Usage

```ts
import { withEnvironment } from '@totto2727/caseyard'
import { test } from 'vitest'

test.concurrent('greeting', async (context) => {
  await withEnvironment(
    'my-application:local',
    async (environment) => {
      context
        .expect(
          await environment.run({ args: ['my-application', 'hello'], workingDir: '/workspace', env: ['MODE=test'] }),
        )
        .toEqual({ exitCode: 0, stdout: 'hello\n', stderr: '' })
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
> Forward `context.signal` to stop waiting for library operations when Vitest cancels the test.
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

| `Environment` member                         | Type                |
| -------------------------------------------- | ------------------- |
| `readonly signal`                            | `AbortSignal`       |
| `run(command: Command)`                      | `Promise<Result>`   |
| `writeFile(path: string, bytes: Uint8Array)` | `Promise<void>`     |
| `readFile(path: string)`                     | `Promise<Buffer>`   |
| `pathKind(path: string)`                     | `Promise<PathKind>` |
| `readSymlink(path: string)`                  | `Promise<string>`   |

`PathKind` is `'missing' | 'file' | 'directory' | 'symlink' | 'other'` and never follows a link to classify its target.
`readSymlink` returns the literal decoded target without the command's final newline.
It preserves newlines in the target, but exact arbitrary target bytes require a snapshot.
Use your framework's matchers on returned observations, for example `context.expect(await environment.readFile(path)).toEqual(bytes)`.

Environment entries must use `NAME=value` with a nonempty name.
Empty values and values that contain `=` are preserved, and the last entry for a duplicate name wins.

Testcontainers supplies separately decoded stdout and stderr, including trailing newlines.
Its string decoding does not guarantee strict UTF-8 validation or preservation across split multibyte sequences.
`Environment` exposes no Docker client.

## Filesystem snapshots

See the [shared snapshot contract](../README.md#file-state-snapshots), [driver setup](../README.md#static-driver-for-snapshots-and-terminals), and the [snapshot example](./test/docker.test.ts#L173).
Functions and types below are exported from `@totto2727/caseyard`.

| API or value                                                                     | TypeScript contract                                                                                                                                                                                                 |
| -------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `installDriver(environment: Environment, executable: Uint8Array): Promise<void>` | Installs once per environment. Rejects empty bytes and concurrent or replacement installations. Failed installations can be retried. Required for nonempty snapshots and terminals, not command or file operations. |
| `environment.snapshot(paths: readonly string[]): Promise<FilesystemSnapshot>`    | Sorts paths and captures duplicates once. Empty input returns an empty snapshot.                                                                                                                                    |
| `FilesystemSnapshot`                                                             | Plain `{ entries: readonly SnapshotEntry[] }` observation. File and link-target buffers are defensive copies on every read.                                                                                         |
| `SnapshotEntry`                                                                  | `{ path: string, state: FileState }`.                                                                                                                                                                               |
| `FileState`                                                                      | `{ kind: 'missing' }`, `{ kind: 'file', bytes: Buffer }`, `{ kind: 'directory' }`, or `{ kind: 'symlink', target: Buffer }`.                                                                                        |

Compare observations with `context.expect(await environment.snapshot(paths)).toEqual(before)`.
Use `context.expect(await environment.snapshot(paths)).toMatchSnapshot()` for a reviewed, saved Vitest baseline, including exact bytes and literal link targets.
Vitest owns baseline creation, comparison, and explicit updates with `vitest -u`.
The library never stores or updates baselines.

## Terminal sessions

See the [shared terminal contract](../README.md#terminal-sessions) and the [terminal example](./test/docker.test.ts#L284).
`withTerminal` and the types below are exported from `@totto2727/caseyard`.
Session methods belong to `TerminalSession`.

| API or value                                                                                                                                           | TypeScript contract                                                                                                                                                                                                                                                               |
| ------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `withTerminal<T>(environment: Environment, command: Command, options: TerminalOptions, action: (terminal: TerminalSession) => Promise<T>): Promise<T>` | Returns the callback value after cleanup. Await all session operations before the callback returns.                                                                                                                                                                               |
| `TerminalOptions`                                                                                                                                      | `rows?: number`, `columns?: number`, `timeout?: number` (seconds), `input?: Uint8Array`. Omitted fields select shared defaults.                                                                                                                                                   |
| `observe()`                                                                                                                                            | `Promise<TerminalObservation>`, as for all session methods below.                                                                                                                                                                                                                 |
| `expect(channel: TerminalChannel, bytes: Uint8Array, timeout?: number)`                                                                                | Matches output. Timeout in seconds.                                                                                                                                                                                                                                               |
| `send(bytes: Uint8Array)`                                                                                                                              | Sends terminal input.                                                                                                                                                                                                                                                             |
| `resize(rows: number, columns: number)`                                                                                                                | Sets terminal geometry.                                                                                                                                                                                                                                                           |
| `signal(name: TerminalSignal)`                                                                                                                         | Sends a supported signal.                                                                                                                                                                                                                                                         |
| `wait(timeout?: number)`                                                                                                                               | Waits for exit and drained output. Timeout in seconds.                                                                                                                                                                                                                            |
| `TerminalChannel`                                                                                                                                      | `'stdout'` or `'terminal'`.                                                                                                                                                                                                                                                       |
| `TerminalSignal`                                                                                                                                       | `'SIGINT'`, `'SIGTERM'`, `'SIGHUP'`, `'SIGQUIT'`, `'SIGKILL'`, `'SIGUSR1'`, or `'SIGUSR2'`.                                                                                                                                                                                       |
| `TerminalObservation`                                                                                                                                  | `stdout`, `terminal`, `unreadInput`: `Buffer`. `status`: `TerminalStatus`. `initialTermios`, `currentTermios`: terminal settings. `termiosRestored`: `boolean`.                                                                                                                   |
| `TerminalStatus`                                                                                                                                       | `{ kind: 'running' }`, `{ kind: 'exited', code: number }`, or `{ kind: 'signaled', signal: number }`.                                                                                                                                                                             |
| `TerminalError.observation`                                                                                                                            | Captured output on expected-byte timeout or premature exit, when supplied by the driver.                                                                                                                                                                                          |
| Scope cleanup                                                                                                                                          | Invalidates the handle on success or failure. Uses a fresh 30-second waiting budget independent of the environment operation budget. Callback and cleanup failures produce an `AggregateError`. After environment cancellation, container teardown is the final process boundary. |

## Errors, deadlines and cleanup

| Condition                                                                                                         | Rejection or cause                                                                       |
| ----------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Invalid command strings, empty argv or executable, invalid image, empty file path, or malformed environment entry | `TypeError`. NUL is invalid in command strings, image names, and file paths.             |
| Command execution failure                                                                                         | `ExecutionError` with empty `partialResult` streams and a `cause`, but no exit code.     |
| Docker failure                                                                                                    | `DockerError`, with optional `statusCode`. Daemon diagnostics do not enter child stderr. |
| Operation deadline                                                                                                | `DeadlineError`.                                                                         |
| Callback rejection                                                                                                | The original value, including a non-Error value, unless cleanup also fails.              |
| Callback returns after cancellation                                                                               | The cancellation reason.                                                                 |
| Multiple operation or cleanup failures                                                                            | `AggregateError` containing the failures.                                                |
| Cleanup failure alone                                                                                             | The cleanup error.                                                                       |

An `ExecutionError` cause can be a `DockerError`, `DeadlineError`, or cancellation reason.
A `DockerError` retains the underlying Testcontainers error as its `cause`.
Testcontainers does not expose partial output when exec fails.
The library rejects missing or negative exit codes, but Testcontainers does not verify `Running: false` after output closes.

| Budget or boundary  | TypeScript behavior                                                                                                                               |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| Operation           | Fixed 60-second budget from helper invocation, including startup. Vitest scheduling is outside this budget.                                       |
| Cleanup             | Fresh 30-second waiting budget for `stop({ timeout: 0, remove: true, removeVolumes: true })`. Underlying requests can continue after this budget. |
| Vitest timeout      | Allow for operation and cleanup, for example `125_000` milliseconds as above.                                                                     |
| Cancellation        | Rejects pending operation waits and signals caller code. Testcontainers does not expose cancellation of startup, exec, or copy requests.          |
| Callback completion | Closes `environment.signal`, then attempts to stop and remove the case container.                                                                 |

See [shared lifecycle and cleanup limits](../README.md#lifecycle-deadlines-and-cancellation) for the common lifecycle.
Containers that finish startup after cancellation receive a cleanup attempt when startup resolves.
Cleanup failure at that point is logged through Testcontainers because the caller's promise has already settled.
Partial startup failures rely on Testcontainers' resource reaper, not immediate scope cleanup.
Do not disable the resource reaper when you need this recovery.
Application images are not removed, but there is no image lease against external pruning.

## Docker support

| Setting or capability | Support                                                                                                                                                                                                                                                           |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Runtime configuration | Testcontainers handles runtime discovery, `DOCKER_HOST`, and TLS settings. See its [configuration](https://node.testcontainers.org/configuration/) and [supported runtimes](https://node.testcontainers.org/supported-container-runtimes/). Use Linux containers. |
| Application image     | Must exist locally. `PullPolicy.neverPull()` prevents application-image downloads.                                                                                                                                                                                |
| Resource reaper       | Testcontainers manages Ryuk and its helper image, which can require a download and access to the Docker socket.                                                                                                                                                   |
| File transfer         | `copyContentToContainer` writes mode `0644`. `copyArchiveFromContainer` supplies the archive for byte-preserving reads.                                                                                                                                           |
| Data size             | Output and file contents are buffered in memory. Use small CLI test fixtures.                                                                                                                                                                                     |

## Development

See [TypeScript maintenance](../AGENTS.md#typescript) for repository maintenance.
