# Caseyard for TypeScript

`@totto2727/caseyard` provides scoped container environments for TypeScript users writing CLI tests.
It uses [Testcontainers for Node.js](https://node.testcontainers.org/) and implements the [shared contract](../README.md) without a test-framework dependency.

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
    "@totto2727/caseyard": "^0.1.0"
  }
}
```

The package exports ESM JavaScript and TypeScript declarations.
Consumers do not build this repository.

## Usage

```ts
import { withEnvironment } from '@totto2727/caseyard'
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

Environment entries must use `NAME=value` with a nonempty name.
Empty values and values that contain `=` are preserved, and the last entry for a duplicate name wins.

Testcontainers supplies separately decoded stdout and stderr, including trailing newlines.
Its string decoding does not guarantee strict UTF-8 validation or preservation across split multibyte sequences.
`Environment` exposes no Docker client.

## Filesystem snapshots

Use `await environment.snapshot(paths)` to capture explicit paths and `await environment.checkSnapshot(expected)` to check a baseline without changing it.
Snapshots and terminals require a caller-supplied static Linux driver binary for the container architecture.
Call `await installDriver(environment, executableBytes)` once inside each environment before these operations.
Import `installDriver` from `@totto2727/caseyard`.
Build or acquire the [shared driver](../go/cmd/caseyard-driver) as an artifact alongside your application image.
The library stages the bytes in a private temporary directory and makes the driver executable.
The container owns the staged file lifetime.
The image needs `mktemp`, but it does not need Go or Python.
The private driver uses mode `0755` inside its private directory, including in non-root images.
Ordinary `writeFile` calls still use mode `0644`.
Existing command and file operations do not need the driver.

```ts
const before = await environment.snapshot(['/workspace/output', '/workspace/config'])
await environment.run({ args: ['my-application', 'update'] })
const after = await environment.snapshot(['/workspace/output', '/workspace/config'])
const differences = compareSnapshots(before, after)
```

Import `compareSnapshots` from `@totto2727/caseyard` for pure comparisons.
Each `FilesystemSnapshot.entries` item contains a `path` and a `state`.
State is `{ kind: 'missing' }`, `{ kind: 'file', bytes: Buffer }`, `{ kind: 'directory' }`, or `{ kind: 'symlink', target: Buffer }`.
Regular files preserve exact bytes, and links preserve literal targets, including dangling links.
Returned file and link-target byte buffers are defensive copies.
Paths are sorted, and repeated capture paths are captured once.
Comparisons return sorted `{ path, reason }` differences with `existence`, `kind`, `content`, or `target` reasons.
Paths present in only one snapshot produce an `existence` difference.

Snapshots are read-only and do not update baselines, traverse directories, or compare metadata.
Only a missing path is absence. Permission errors, read errors, invalid traversal, and unsupported kinds such as FIFOs reject capture.
Capture is not atomic across paths or concurrent filesystem changes.
Symbolic-link targets preserve arbitrary bytes without text decoding.

## Terminal sessions

After [driver installation](#filesystem-snapshots), use `withTerminal(environment, command, options, callback)` from `@totto2727/caseyard`.
The command inherits its `workingDir` and `env` settings.
The callback receives a `TerminalSession` and can also use ordinary environment operations.

```ts
await withTerminal(
  environment,
  { args: ['my-application', 'interactive'] },
  { rows: 24, columns: 80 },
  async (terminal) => {
    await terminal.expect('terminal', Buffer.from('Ready> '))
    await terminal.send(Buffer.from('quit\n'))
    const result = await terminal.wait()
    expect(result.status).toEqual({ kind: 'exited', code: 0 })
    expect(result.termiosRestored).toBe(true)
  },
)
```

| `TerminalSession` member           | Behavior                                                                                                                                                                                 |
| ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `observe()`                        | Return cumulative raw buffers and current process and terminal settings without waiting for exit.                                                                                        |
| `expect(channel, bytes, timeout?)` | Match nonempty raw bytes on `stdout` or `terminal`, including matches across reads. Each channel has an independent consumed cursor. Trailing bytes remain available for the next match. |
| `send(bytes)`                      | Write literal bytes to terminal stdin. Ctrl-C and Ctrl-D bytes are not API signal or close operations.                                                                                   |
| `resize(rows, columns)`            | Set terminal geometry and send SIGWINCH to the foreground process group.                                                                                                                 |
| `signal(name)`                     | Send `SIGINT`, `SIGTERM`, `SIGHUP`, `SIGQUIT`, `SIGKILL`, `SIGUSR1`, or `SIGUSR2` to the application process group.                                                                      |
| `wait(timeout?)`                   | Wait for process exit and return drained output and final observations.                                                                                                                  |

All session methods return `Promise<TerminalObservation>`.
`stdout`, `terminal`, and `unreadInput` are `Buffer` values with no text decoding or ANSI removal.
Stdout uses a separate pipe. Stderr uses the terminal. There is no cross-channel ordering guarantee.
Status is `{ kind: 'running' }`, `{ kind: 'exited', code }`, or `{ kind: 'signaled', signal }`.
`initialTermios` and `currentTermios` contain normalized terminal settings.
`termiosRestored` compares those settings, excluding the kernel bookkeeping flag PENDIN.
A true flag while the process runs does not prove that the application restores settings on exit.
Screen and cursor restoration checks remain caller-owned assertions on raw terminal bytes.

Options default to `rows: 40`, `columns: 120`, and `timeout: 50` seconds.
Geometry must use integers from 1 through 65535.
Timeouts must be finite, positive, and at most 60 seconds. They do not extend the environment deadline.
Set `input: Uint8Array` to supply finite pipe stdin followed by EOF while stderr remains on the terminal.
In pipe mode, `send` rejects and `unreadInput` reports bytes not consumed by the application after exit.

Await every session operation before the callback returns.
The scope closes the terminal on success or failure and invalidates the handle.
Terminal cleanup has a fresh 30-second waiting budget independent of the environment operation budget.
Callback and cleanup failures are retained together in an `AggregateError`.
An expected-byte timeout or premature exit raises `TerminalError`, with captured raw output in `observation` when the driver provides it.
On environment cancellation, container teardown is the final process boundary.
The terminal watchdog also stops surviving application processes if the client disappears.

## Errors, deadlines and cleanup

| Condition                                                                                                         | Rejection or cause                                                                         |
| ----------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| Invalid command strings, empty argv or executable, invalid image, empty file path, or malformed environment entry | `TypeError`. NUL is invalid in command strings, image names, file paths, and link targets. |
| Command execution failure                                                                                         | `ExecutionError` with empty `partialResult` streams and a `cause`, but no exit code.       |
| Docker failure                                                                                                    | `DockerError`, with optional `statusCode`. Daemon diagnostics do not enter child stderr.   |
| Operation deadline                                                                                                | `DeadlineError`.                                                                           |
| Callback rejection                                                                                                | The original value, including a non-Error value, unless cleanup also fails.                |
| Callback returns after cancellation                                                                               | The cancellation reason.                                                                   |
| Multiple operation or cleanup failures                                                                            | `AggregateError` containing the failures.                                                  |
| Cleanup failure alone                                                                                             | The cleanup error.                                                                         |

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
