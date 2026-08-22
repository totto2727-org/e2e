# e2e

Reusable Go helpers for running CLI end-to-end test cases in isolated Testcontainers containers.

## Usage

Build the CLI image before starting the Go test. This library assumes a prebuilt local image and intentionally does not build or remove images; `cli.Run` only resolves and uses the supplied image.

```go
package cli_test

import (
	"testing"

	"github.com/totto2727-org/e2e/cli"
)

func TestCLI(t *testing.T) {
	cli.Run(t, "my-cli-e2e:local", []cli.Case{
		{Name: "version", Run: func(t *testing.T, environment *cli.Environment) {
			if err := environment.CheckStdout(cli.StdoutExpectation{
				Command:  []string{"my-cli", "--version"},
				ExitCode: 0,
				Stdout:   "my-cli version 1.0.0\n",
			}); err != nil {
				t.Fatal(err)
			}
		}},
	})
}
```

For runnable Docker commands and the exact passing scenarios they produce, see the [Go CLI E2E example](./example/README.md#usage).

## Key features

- Reuses one caller-supplied image that was built before the test starts.
- Resolves the image from the local Docker daemon and fails without pulling when the tag is missing.
- Holds the image ID with a temporary stopped container and removes that lease after every case finishes.
- Starts a fresh container for each case and removes Testcontainers resources after the test without removing the image.
- Runs at most two cases concurrently while keeping each case isolated.
- Verifies command exit codes, multiplexed output, and copied file contents.

## Prerequisites

- **Go**: Go 1.25 or newer.
- **Docker**: A running daemon reachable by Testcontainers and a prebuilt image for the CLI under test.

## Setup

1. Add the helper package to the Go module that owns your tests.

```bash
go get github.com/totto2727-org/e2e
```

## API

### `cli.Case`

Names one scenario and receives its isolated `*cli.Environment`.

```go
caseDefinition := cli.Case{Name: "version", Run: versionScenario}
```

### `cli.Run`

Resolves the prebuilt image locally, then runs each case in a fresh container without pulling or removing the image.

```go
cli.Run(t, "my-cli-e2e:local", []cli.Case{caseDefinition})
```

### `cli.Environment`

Provides command and file primitives for one isolated case. The value is supplied to a `cli.Case` callback.

```go
func versionScenario(t *testing.T, environment *cli.Environment) {
	if err := environment.CheckStdout(cli.StdoutExpectation{
		Command:  []string{"my-cli", "--version"},
		ExitCode: 0,
		Stdout:   "my-cli version 1.0.0\n",
	}); err != nil {
		t.Fatal(err)
	}
}
```

### `(*cli.Environment).Exec`

Executes an argv command and returns its exit code plus multiplexed stdout/stderr for custom assertions.

```go
result, err := environment.Exec([]string{"my-cli", "status"})
if err != nil {
	t.Fatal(err)
}
if result.ExitCode != 0 {
	t.Fatalf("status failed: %s", result.Stdout)
}
```

### `cli.Command` and `(*cli.Environment).Run`

Executes an argv command with a per-command working directory and environment, without a shell wrapper.

```go
result, err := environment.Run(cli.Command{
	Args:       []string{"my-cli", "sync"},
	WorkingDir: "/workspace/project",
	Env:        []string{"HOME=/workspace/home"},
})
if err != nil {
	t.Fatal(err)
}
```

### `cli.Result`

Contains the command `ExitCode` and captured `Stdout` returned by `Exec`.

```go
if result.ExitCode == 0 {
	fmt.Print(result.Stdout)
}
```

### `cli.StdoutExpectation`

Describes the argv command, expected exit code, and exact multiplexed output for `CheckStdout`.

```go
expectation := cli.StdoutExpectation{
	Command:  []string{"echo", "hello"},
	ExitCode: 0,
	Stdout:   "hello\n",
}
```

### `(*cli.Environment).CheckStdout`

Runs a command and returns an error unless its exit code and exact output match the expectation.

```go
if err := environment.CheckStdout(expectation); err != nil {
	t.Fatal(err)
}
```

### `cli.FileExpectation`

Describes a container file path and exact byte content for `CheckFile`.

```go
expectation := cli.FileExpectation{
	Path:    "/workspace/result.txt",
	Content: []byte("done\n"),
}
```

### `cli.File` and `(*cli.Environment).WriteFile`

Copies exact bytes into a container file with mode `0644`.

```go
if err := environment.WriteFile(cli.File{
	Path:    "/workspace/config.json",
	Content: []byte("{}\n"),
}); err != nil {
	t.Fatal(err)
}
```

### `(*cli.Environment).ReadFile`

Copies exact bytes from a container file for custom assertions.

```go
content, err := environment.ReadFile("/workspace/result.txt")
if err != nil {
	t.Fatal(err)
}
if string(content) != "done\n" {
	t.Fatalf("content=%q", content)
}
```

### `(*cli.Environment).CheckFile`

Copies a file from the container and returns an error unless its exact bytes match the expectation.

```go
if err := environment.CheckFile(expectation); err != nil {
	t.Fatal(err)
}
```

## Development

For repository structure and development commands, see [AGENTS.md](./AGENTS.md).

## License

No license has been declared for this repository.

_This README was generated from the [share-artifact skill](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/SKILL.md) and [README template](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/readme/template.md)._
