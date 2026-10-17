# Go CLI E2E

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
