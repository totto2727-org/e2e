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
go get github.com/totto2727-org/e2e/go@v0.0.0-20261017065452-5b5def6e7ca1
```

The pseudo-version selects a source revision, not a tagged release.

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
	if err := environment.CheckCommand(
		cli.Command{Args: []string{"my-cli", "--version"}},
		cli.Result{ExitCode: 0, Stdout: "my-cli version 1.0.0\n", Stderr: ""},
	); err != nil {
		t.Fatal(err)
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

| Method                                                     | Result                                                                                              |
| ---------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| `Run(command cli.Command) (cli.Result, error)`             | Executes the command. A nonzero application exit code is not a Go error.                            |
| `CheckCommand(command cli.Command, want cli.Result) error` | Runs the command and compares all three fields exactly. Returns execution errors before comparison. |

NUL bytes in arguments, the directory, or environment entries cause an error before execution.
An empty expected stream asserts that the command wrote no output to that stream.

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

| Method                                           | Expected result                                                              |
| ------------------------------------------------ | ---------------------------------------------------------------------------- |
| `WriteFile(path string, content []byte) error`   | Copies exact bytes with mode `0644`. The parent directory must exist.        |
| `ReadFile(path string) ([]byte, error)`          | Returns exact file bytes.                                                    |
| `CheckFile(path string, want []byte) error`      | Compares exact file bytes.                                                   |
| `CheckMissing(path string) error`                | Requires absence, including absence of a dangling symbolic link.             |
| `CheckRegularFile(path string) error`            | Requires a physical regular file, not a symbolic link.                       |
| `CheckDirectory(path string) error`              | Requires a physical directory, not a symbolic link.                          |
| `CheckSymlink(path, literalTarget string) error` | Compares the literal target without normalization. The target can be absent. |

All checks return errors for mismatches or infrastructure failures.
Path-probe exit codes other than `0` and `1` are errors, not evidence of absence.

## State and terminal driver setup

Filesystem snapshots and terminal sessions require a caller-supplied static Linux driver.
The application image needs neither Python nor Go.
Build the driver for the image architecture with `CGO_ENABLED=0`, as described in [shared driver setup](../README.md#static-driver-for-snapshots-and-terminals).
Existing command and file APIs do not require this setup.

Read the built executable into `[]byte` and install it once after creating the environment:

```go
binary, err := os.ReadFile("caseyard-driver-linux-arm64")
if err != nil {
	t.Fatal(err)
}
if err := environment.InstallDriver(binary); err != nil {
	t.Fatal(err)
}
```

The example also requires an import for `os`.
Select the executable for the container architecture, not the test host architecture.
`InstallDriver` rejects empty bytes and replacement of an installed driver.
It uses an unpredictable private directory and transfers the executable with mode `0755`.
The container lifecycle owns the installed executable.
Snapshot and terminal operations fail explicitly if no driver is installed.

## Filesystem snapshots

The snapshot stores private in-memory state for a nonempty list of explicit, unique, NUL-free paths.
It distinguishes missing paths, physical regular files, physical directories, and literal symbolic links, including dangling links.
File contents and link targets are compared as exact bytes.

```go
before, err := environment.Snapshot([]string{"/workspace/config", "/workspace/output"})
if err != nil {
	t.Fatal(err)
}
// Run the application workflow here.
if err := environment.CheckSnapshot(before); err != nil {
	t.Fatal(err)
}
```

| Method                                                                             | Result                                                                                  |
| ---------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| `Snapshot(paths []string) (cli.FilesystemSnapshot, error)`                         | Captures the selected paths without changing them. The zero snapshot value is invalid.  |
| `CompareSnapshot(before cli.FilesystemSnapshot) ([]cli.SnapshotDifference, error)` | Captures the same paths again. Returns one difference per changed path, sorted by path. |
| `CheckSnapshot(before cli.FilesystemSnapshot) error`                               | Requires unchanged state. Reports all path differences without updating the snapshot.   |

Each difference contains `Path`, `Change`, `BeforeKind`, and `AfterKind`.
`Change` identifies `existence`, `kind`, `content`, or `target`.
Read failures, permission failures, and unsupported kinds such as FIFOs are errors, not missing or unchanged paths.
Directory children, permissions, timestamps, and other metadata are not captured.
Capture is not atomic across paths or concurrent application writes.
There is no recursive capture, golden file, or automatic update.

## Terminal sessions

`WithTerminal` requires the [installed driver](#state-and-terminal-driver-setup).
It starts an application with a controlling terminal and closes the session when the callback returns, fails, or panics.
Stdout remains a separate pipe. Stderr uses the terminal.
`Command.WorkingDir` and `Command.Env` apply to the application.

```go
err := environment.WithTerminal(
	cli.Command{Args: []string{"my-cli", "interactive"}},
	cli.TerminalOptions{Rows: 24, Columns: 80},
	func(terminal *cli.Terminal) error {
		if _, err := terminal.Expect(cli.TerminalOutput, []byte("Name: "), 5*time.Second); err != nil {
			return err
		}
		if _, err := terminal.SendInput([]byte("Ada\n")); err != nil {
			return err
		}
		observed, err := terminal.Wait(5*time.Second)
		if err != nil {
			return err
		}
		if observed.Status.Kind != "exited" || observed.Status.ExitCode != 0 {
			return fmt.Errorf("unexpected terminal status: %+v", observed.Status)
		}
		return nil
	},
)
if err != nil {
	t.Fatal(err)
}
```

The example also requires imports for `fmt` and `time`.

| API                              | Behavior                                                                                                                                                       |
| -------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TerminalOptions`                | Defaults to 40 rows, 120 columns, and a 50-second watchdog. Explicit dimensions must be in `1..65535`. Timeout must be positive and at most 60 seconds.        |
| `TerminalOptions.Input`          | `nil` selects terminal stdin. A non-nil byte slice selects finite pipe input plus EOF. An empty non-nil slice supplies immediate pipe EOF.                     |
| `Observe()`                      | Returns cumulative raw bytes and the current process and termios observations.                                                                                 |
| `Expect(channel, data, timeout)` | Matches nonempty raw bytes on `TerminalStdout` or `TerminalOutput`. Each channel has an independent consumed cursor. Trailing buffered bytes remain available. |
| `SendInput(data)`                | Sends literal terminal bytes. Ctrl-C and Ctrl-D are not aliases for signals or connection closure. Pipe stdin rejects this operation.                          |
| `Resize(rows, columns)`          | Updates geometry and sends `SIGWINCH` to the application process group.                                                                                        |
| `Signal(name)`                   | Sends `SIGINT`, `SIGTERM`, `SIGHUP`, `SIGQUIT`, `SIGKILL`, `SIGUSR1`, or `SIGUSR2` to the application process group.                                           |
| `Wait(timeout)`                  | Waits for application exit and drains output. Application failure is an observation, not an infrastructure error.                                              |
| `Close()`                        | Idempotently kills the owned process group and removes session resources with a fresh 30-second cleanup budget. Other operations fail after closure.           |

`TerminalObservation` preserves `Stdout`, `Terminal`, and final `UnreadInput` bytes without ANSI processing or UTF-8 conversion.
`Status.Kind` is `running`, `exited`, or `signaled`; inspect `ExitCode` or `Signal` only for the corresponding kind.
`InitialTermios`, `CurrentTermios`, and `TermiosRestored` describe terminal attributes, not screen or cursor restoration.
A restored flag while the application runs does not prove restoration on exit.
Use raw terminal bytes for application-specific screen assertions and normal environment methods inside the callback for file checks.
Expect failures return captured observations with a `*cli.TerminalError` diagnostic.
There is no cross-channel output ordering guarantee.
The watchdog does not extend the environment deadline.
Container teardown remains the final process boundary after environment cancellation or a lost start response.

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

See the [repository license status](../README.md#license).
