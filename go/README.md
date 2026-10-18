# Caseyard for Go

Write CLI E2E tests with Go's `testing` package and a container for each test.
For common image requirements and lifecycle behavior, see the [shared contract](../README.md).

## Setup

| Requirement       | Value                                                         |
| ----------------- | ------------------------------------------------------------- |
| Go                | 1.25 or newer                                                 |
| Docker connection | A daemon accessible through Testcontainers configuration      |
| Application image | A local image with the CLI under test, built before the tests |

Add the module to your test project:

```sh
go get github.com/totto2727-org/e2e/go@33a47d7e3e23aa3e41dc647403e56341a96732b5
```

> [!IMPORTANT]
> Testcontainers can pull its resource-reaper image.
> The library does not build, pull, or remove your application image.

## Usage

```go
package cli_test

import (
	"testing"

	"github.com/totto2727-org/e2e/go/cli"
)

func TestVersion(t *testing.T) {
	t.Parallel()
	environment := cli.NewEnvironment(t, "my-cli-e2e:local")
	result, err := environment.Run(cli.Command{Args: []string{"my-cli", "--version"}})
	if err != nil {
		t.Fatal(err)
	}
	want := cli.Result{ExitCode: 0, Stdout: "my-cli version 1.0.0\n", Stderr: ""}
	if result != want {
		t.Fatalf("result=%#v want=%#v", result, want)
	}
}
```

For sequential tests, omit `t.Parallel()`.
For more cases, see the [Go example](../.agents/skills/cli-e2e/references/example/multiple_scenarios_test.go).

## API

### Per-test environment

| API or condition                                | Behavior                                                                                             |
| ----------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `cli.NewEnvironment(t, image) *cli.Environment` | Creates a fresh environment for the calling test.                                                    |
| Setup failure                                   | Fails the calling test. Registers cleanup for acquired or partially created resources.               |
| Test completion                                 | Uses `t.Cleanup` to cancel operations, close clients, and remove the test container and image lease. |
| Cleanup failure                                 | Reports an error through `t.Error`.                                                                  |
| Test names, selection, and concurrency          | Remain under Go's test runner. The library does not call `t.Run` or `t.Parallel`.                    |

> [!IMPORTANT]
> For parallel tests, call `NewEnvironment` after `t.Parallel()`.
> The one-minute operation budget starts at `NewEnvironment`, before image resolution and setup.

### Commands and results

| Field                            | Type       | Meaning                                                                      |
| -------------------------------- | ---------- | ---------------------------------------------------------------------------- |
| `Command.Args`                   | `[]string` | Argument vector with a nonempty executable at index zero. No implicit shell. |
| `Command.WorkingDir`             | `string`   | Optional directory for the command.                                          |
| `Command.Env`                    | `[]string` | Optional environment entries.                                                |
| `Result.ExitCode`                | `int`      | Application exit code. Valid only when `Run` returns no error.               |
| `Result.Stdout`, `Result.Stderr` | `string`   | Separate output streams. Strings preserve bytes without UTF-8 validation.    |

| Method                                         | Result                                                                   |
| ---------------------------------------------- | ------------------------------------------------------------------------ |
| `Run(command cli.Command) (cli.Result, error)` | Executes the command. A nonzero application exit code is not a Go error. |

NUL bytes in arguments, the directory, or environment entries cause an error before execution.
Compare complete results with `==` and report mismatches with `t.Fatalf`.

```go
result, err := environment.Run(cli.Command{
	Args:       []string{"my-cli", "status"},
	WorkingDir: "/workspace/project",
	Env:        []string{"HOME=/workspace/home"},
})
if err != nil {
	t.Fatalf("status: %v; partial stdout=%q stderr=%q", err, result.Stdout, result.Stderr)
}
if result.ExitCode != 0 {
	t.Fatalf("status exit=%d stderr=%q", result.ExitCode, result.Stderr)
}
```

### Files and paths

For required image utilities and file-transfer conditions, see [shared prerequisites](../README.md#prerequisites-and-setup).

| Method                                         | Expected result                                                                                       |
| ---------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `WriteFile(path string, content []byte) error` | Copies exact bytes with mode `0644`. The parent directory must exist.                                 |
| `ReadFile(path string) ([]byte, error)`        | Returns exact file bytes.                                                                             |
| `PathKind(path string) (cli.PathKind, error)`  | Returns the physical kind without following links. No driver required.                                |
| `ReadSymlink(path string) (string, error)`     | Returns the literal link target, including trailing newlines and non-UTF-8 bytes. No driver required. |

Use `bytes.Equal` for file bytes and `==` for path kinds and link targets.
Path observations reject empty paths and NUL bytes before execution.
Path-probe exit codes other than `0` and `1` are errors, not evidence of absence.

## State and terminal driver setup

For snapshot and terminal prerequisites, see [driver setup](../README.md#static-driver-for-snapshots-and-terminals).

| Method                               | Contract                                                                                                                                                                                     |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `InstallDriver(binary []byte) error` | Installs once per environment. Rejects empty bytes and replacement after success. Failed installations can be retried. Required for snapshots and terminals, not command or file operations. |

## Filesystem snapshots

See the [shared snapshot contract](../README.md#file-state-snapshots), [Docker scenario source](./e2e/snapshot_test.go), and [scenario details](./e2e/snapshot_test.md).

| API or value                                               | Go contract                                                                                       |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `Snapshot(paths []string) (cli.FilesystemSnapshot, error)` | Requires a nonempty list of nonempty, NUL-free paths. Rejects duplicates.                         |
| `snapshot.Entries() []cli.SnapshotEntry`                   | Returns a fresh slice sorted by path. Use `slices.Equal` to compare captures or expected entries. |
| `cli.SnapshotEntry`                                        | Comparable `Path`, `Kind`, `Content`, and `Target` fields. Strings preserve exact bytes.          |
| `cli.FilesystemSnapshot`                                   | Immutable in-memory state. The zero value has no entries. No persisted snapshot storage.          |
| `cli.PathKind`                                             | `missing`, `file`, `directory`, `symlink`, or `other`. Snapshots reject `other` kinds.            |

## Terminal sessions

See the [shared terminal contract](../README.md#terminal-sessions), [Docker scenario source](./e2e/terminal_test.go), and [scenario details](./e2e/terminal_test.md).
Methods below belong to `*cli.Terminal` unless specified otherwise.

| API or value                                                                                                      | Go contract                                                                                                                                                            |
| ----------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `environment.WithTerminal(command cli.Command, options cli.TerminalOptions, use func(*cli.Terminal) error) error` | Closes the session after callback success, error, or panic.                                                                                                            |
| `cli.TerminalOptions`                                                                                             | `Rows`, `Columns`: `int`. `Timeout`: `time.Duration`. Zero values select shared defaults. `Input []byte`: `nil` for terminal stdin, non-nil for finite pipe input.     |
| `Observe()`                                                                                                       | Returns `(cli.TerminalObservation, error)`, as do `Expect`, `SendInput`, `Resize`, `Signal`, and `Wait`.                                                               |
| `Expect(channel cli.TerminalChannel, data []byte, timeout time.Duration)`                                         | Channels: `cli.TerminalStdout`, `cli.TerminalOutput`.                                                                                                                  |
| `SendInput(data []byte)`                                                                                          | Sends terminal input.                                                                                                                                                  |
| `Resize(rows, columns int)`                                                                                       | Sets terminal geometry.                                                                                                                                                |
| `Signal(name string)`                                                                                             | Sends a supported signal.                                                                                                                                              |
| `Wait(timeout time.Duration)`                                                                                     | Waits for exit and drained output.                                                                                                                                     |
| `Close() error`                                                                                                   | Public, idempotent close with a fresh 30-second cleanup budget. Other operations fail after closure.                                                                   |
| `cli.TerminalObservation`                                                                                         | `Stdout`, `Terminal`, `UnreadInput`: `[]byte`. `Status`: `cli.TerminalStatus`. `InitialTermios`, `CurrentTermios`: `*cli.TerminalSettings`. `TermiosRestored`: `bool`. |
| `cli.TerminalStatus`                                                                                              | `Kind`: `running`, `exited`, or `signaled`. Read `ExitCode` or `Signal` only for the corresponding kind.                                                               |
| `*cli.TerminalError`                                                                                              | Expect failures retain captured observations in `Observation cli.TerminalObservation`.                                                                                 |

## Execution limits

| Condition                       | Behavior or required action                                                                                              |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| Output order                    | Preserves order within each stream, not between stdout and stderr.                                                       |
| Transport or cancellation error | Retains captured output. Do not use `ExitCode` when `err != nil`.                                                        |
| Cancellation                    | Closes the command connection. Use `errors.Is` with `context.Canceled` or `context.DeadlineExceeded`.                    |
| Deadline error                  | Return from the test so container cleanup can stop remaining processes. Closing the connection alone does not stop them. |
| Cleanup                         | Uses a fresh 30-second context for the test container and a separate 30-second context for the image lease.              |
| Caller code                     | The operation context does not bound arbitrary code in the test.                                                         |
| Remote TLS connection           | Initial connection establishment follows the configured Testcontainers dialer.                                           |
| Malformed output                | The Moby decoder can accept EOF inside an incomplete frame. Detection of malformed streams is not exhaustive.            |

## Development

See [Go maintenance](../AGENTS.md#go).

## License

See the [MIT license](../README.md#license).
