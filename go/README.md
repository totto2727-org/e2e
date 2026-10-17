# Go CLI E2E

Reusable Go helpers for non-interactive CLI end-to-end tests in isolated Testcontainers containers.
This reference is for Go developers who use the package to write consumer tests.
See the [shared language-independent contract](../README.md) for the common behavior.

## Prerequisites

- Go 1.25 or newer.
- A running Docker daemon reachable by Testcontainers.
- A caller-built local image containing the CLI under test.

## Setup

Use the checkout as a local dependency in your consumer's `go.mod`:

```go.mod
require github.com/totto2727-org/e2e/go v0.0.0

replace github.com/totto2727-org/e2e/go => /path/to/e2e/go
```

Replace `/path/to/e2e/go` with the checkout's Go module directory.
The local `replace` resolves the dependency without a published release.

## Usage

Build the image before starting the Go test.
Import `github.com/totto2727-org/e2e/go/cli`.
The library does not build, pull, or remove the caller's application image.
Testcontainers may pull its resource-reaper infrastructure image according to its own configuration.

```go
package cli_test

import (
	"testing"

	"github.com/totto2727-org/e2e/go/cli"
)

func TestVersion(t *testing.T) {
	t.Parallel() // Optional: scheduling belongs to the consumer.
	environment := cli.NewEnvironment(t, "my-cli-e2e:local")
	if err := environment.CheckCommand(
		cli.Command{Args: []string{"my-cli", "--version"}},
		cli.Result{ExitCode: 0, Stdout: "my-cli version 1.0.0\n", Stderr: ""},
	); err != nil {
		t.Fatal(err)
	}
}
```

See the [complete consumer examples](../.agents/skills/document-e2e-scenarios/references/example/README.md) for runnable greeting and expected-failure tests.

## API

### Per-test environment

`cli.NewEnvironment(t, image) *cli.Environment` prepares a fresh container for the calling standard Go test and returns it only after setup succeeds.
Call it inside each ordinary `Test` function or `t.Run` callback, after `t.Parallel()` when parallel execution is desired.
Test names, selection, registration, and parallel scheduling belong to Go's test runner and the consumer, not the library.
Every invocation resolves the caller-built image locally without pulling and creates its own temporary stopped lease container retaining that immutable image ID.
`t.Cleanup` owns cancellation, execution-client closure, base-client closure, scenario-container removal, and finally lease release, in that order.
Setup failures fail the calling test only after cleanup has been registered for acquired or partially created resources.
Cleanup never removes the caller-owned image.

For complete examples using ordinary Go `Test` functions, see the [Go scenario sample](../.agents/skills/document-e2e-scenarios/references/example/multiple_scenarios_test.go), which creates the environment inside each test function.

### Commands and results

```go
type Command struct {
	Args       []string
	WorkingDir string
	Env        []string
}

type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}
```

`environment.Run(command)` returns `(cli.Result, error)` using one native split-output execution path.
`Args` must contain a nonempty executable at index zero.
NUL bytes in argv entries, `WorkingDir`, or `Env` entries are rejected before runtime execution.
Arguments are passed as argv entries, not joined or interpreted by a shell.
Use an explicit shell executable only when the command itself requires shell syntax.
`WorkingDir` and `Env` optionally set the command directory and environment entries.

Stdout and stderr remain separate, with ordering preserved within each stream, not between streams.
They are never implicitly concatenated.
Go strings retain stream bytes without UTF-8 validation.
A nonzero command exit code is a result, not a Go error.
On transport or cancellation errors, the returned result retains any captured partial stdout and stderr, but `ExitCode` is not usable when `err != nil`.
Always check the error before interpreting the exit code.

Go delegates stream demultiplexing to Moby's `StdCopy`.
That upstream decoder accepts EOF inside an unfinished frame, so malformed-stream detection is not exhaustive.
Cancellation still returns a context error; the partial result contains bytes already delivered by the decoder.

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

`environment.CheckCommand(command, want)` runs a `cli.Command` and compares all three fields of the expected `cli.Result` exactly.
An empty expected `Stderr` intentionally asserts that no diagnostic output was written.
Execution errors are returned before result comparison.

```go
if err := environment.CheckCommand(
	cli.Command{Args: []string{"my-cli", "status"}},
	cli.Result{ExitCode: 0, Stdout: "ready\n", Stderr: ""},
); err != nil {
	t.Fatal(err)
}
```

### Files

| Method                                         | Behavior                                                                                |
| ---------------------------------------------- | --------------------------------------------------------------------------------------- |
| `WriteFile(path string, content []byte) error` | Copy exact bytes to a container file with mode `0644`. The parent directory must exist. |
| `ReadFile(path string) ([]byte, error)`        | Copy file bytes from the container for custom assertions.                               |
| `CheckFile(path string, want []byte) error`    | Copy and compare exact file bytes.                                                      |

```go
if err := environment.WriteFile("/workspace/result.txt", []byte("done\n")); err != nil {
	t.Fatal(err)
}
if err := environment.CheckFile("/workspace/result.txt", []byte("done\n")); err != nil {
	t.Fatal(err)
}
```

### Physical path assertions

The image must provide `test` with `-e`, `-L`, `-f`, and `-d`, plus `readlink` with `--` support.
All methods return an error if the asserted state does not match or an infrastructure operation fails.
Unexpected probe exit codes are errors, not successful absence checks.

| Method                              | Expected state                                                                            |
| ----------------------------------- | ----------------------------------------------------------------------------------------- |
| `CheckMissing(path)`                | No path exists, including no dangling symbolic link.                                      |
| `CheckRegularFile(path)`            | A physical regular file, not a symbolic link to one.                                      |
| `CheckDirectory(path)`              | A physical directory, not a symbolic link to one.                                         |
| `CheckSymlink(path, literalTarget)` | A symbolic link with exactly the specified literal target, even if that target is absent. |

`CheckSymlink` does not resolve or normalize the target.
The literal target `result.txt` differs from `/workspace/result.txt`.

## Execution limits

- The library never calls `t.Run` or `t.Parallel` and imposes no fixed concurrency scheduler.
- Consumers choose sequential tests or call `t.Parallel()` and configure `go test -parallel`, for example `-parallel=2`.
- Each invocation receives a one-minute context before image resolution and container setup. Call the helper after `t.Parallel()` so time waiting on Go's runner does not consume this budget.
- Cancellation closes the command's owned output connection and preserves `context.Canceled` or `context.DeadlineExceeded` for `errors.Is` checks.
- Closing the connection does not itself stop the process. Return from the test after a deadline error so container cleanup can stop remaining processes.
- Scenario cleanup uses a fresh 30-second context independent of the case deadline. The image lease has its own 30-second cleanup context.
- Contexts bound helper operations, not arbitrary Go test code.
- Initial daemon dialing uses the configured Testcontainers dialer; remote TLS connection establishment follows that dialer's behavior.

## Development

See [Go maintenance](../AGENTS.md#go) for repository maintenance.

## License

No license has been declared for this repository.
