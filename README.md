# e2e

Reusable Go helpers for running CLI end-to-end test cases in isolated Testcontainers containers.

## Usage

```go
func TestCLI(t *testing.T) {
	cli.Run(t, cli.ImageConfig{Context: ".", Dockerfile: "Dockerfile"}, []cli.Case{
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

## Key features

- Builds one caller-supplied Docker image and reuses it for every case.
- Starts a fresh container for each case and removes Testcontainers resources after the test.
- Runs at most two cases concurrently while keeping each case isolated.
- Verifies command exit codes, multiplexed output, and copied file contents.

## Prerequisites

- **Go**: Go 1.25 or newer.
- **Docker**: A running Docker daemon reachable by Testcontainers.

## Setup

1. Add the helper package to the Go module that owns your tests.

```bash
go get github.com/totto2727-org/e2e
```

2. Point `cli.ImageConfig` at the Docker build context and Dockerfile for the CLI under test, then add cases to a Go test file.

```bash
go test ./...
```

## API

### `cli.ImageConfig`

Describes the Docker build context and Dockerfile used for the shared test image.

```go
image := cli.ImageConfig{Context: ".", Dockerfile: "Dockerfile"}
```

### `cli.Case`

Names one scenario and receives its isolated `*cli.Environment`.

```go
caseDefinition := cli.Case{Name: "version", Run: versionScenario}
```

### `cli.Run`

Builds the configured image once and runs each case in a fresh container.

```go
cli.Run(t, image, []cli.Case{caseDefinition})
```

### `cli.Environment`

Provides command and file checks for one isolated case. The value is supplied to a `cli.Case` callback.

```go
func versionScenario(t *testing.T, environment *cli.Environment) {
	if err := environment.CheckStdout(cli.StdoutExpectation{
		Command:  []string{"my-cli", "--version"},
		ExitCode: 0,
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
