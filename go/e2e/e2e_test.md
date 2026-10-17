# CLI helper scenarios

Source: [e2e_test.go](./e2e_test.go)

This document is for contributors who maintain the Docker-backed acceptance tests.
The source lives at `go/e2e/e2e_test.go` in the same Go module as the implementation and is selected with the standard `e2e` build tag, not a separate example module.
From `go/`, run `go test -race -tags=e2e -parallel=2 ./e2e` after building `e2e-example:local` from `fixtures/cli/Dockerfile` at the repository root.
Ordinary untagged `go test` excludes these Docker-backed tests.
The six single-container tests call `cli.NewEnvironment(t, "e2e-example:local")` inside their bodies after `t.Parallel()`, receiving a fresh container and their own immutable image leases.
The seventh test uses two sequential `t.Run` child scopes, each with its own environment and cleanup.
The standard `go test -parallel=2` setting controls concurrency, not the library.

## `TestStdoutScenario`

### Scope

Check exact exit status and separate output streams through `CheckCommand`.

### Commands under test

| Command path | Purpose           |
| ------------ | ----------------- |
| `echo`       | Print a greeting. |

### Arguments and options

| Argument or option | Purpose                            |
| ------------------ | ---------------------------------- |
| `hello from e2e`   | Pass the greeting as one argument. |

### Preconditions and fixtures

- The image contains `echo`.
- No file fixture is necessary.

### Execution flow

1. Run argv `["echo", "hello from e2e"]` through `CheckCommand(command, want)`, with an expected `cli.Result`.

### Expected results

- The exit code is `0`.
- Stdout is exactly `hello from e2e\n`.
- Stderr is exactly empty.

### Notes

- Empty stderr is an intentional exact assertion, not an ignored stream.

## `TestFileOutputScenario`

### Scope

Check a file workflow with multiple commands in one container.

### Commands under test

| Command path | Purpose                               |
| ------------ | ------------------------------------- |
| `sh`         | Write and append file content.        |
| `cat`        | Read the completed file.              |
| `stat`       | Read the binary file permission bits. |

### Arguments and options

| Argument or option      | Purpose                                                        |
| ----------------------- | -------------------------------------------------------------- |
| `-c`                    | Execute the fixture script or select the `stat` output format. |
| `%a`                    | Print the file permission bits in octal.                       |
| `/workspace/result.txt` | Select the text fixture file.                                  |
| `/workspace/binary.bin` | Select the binary fixture file.                                |

### Preconditions and fixtures

- The image contains `sh`, `cat`, and GNU `stat`.
- `/workspace` exists in the image.
- The scenario starts without the result file.

### Execution flow

1. Run argv `["sh", "-c", "echo first > /workspace/result.txt"]`.
2. Run argv `["sh", "-c", "echo second >> /workspace/result.txt"]`.
3. Run argv `["cat", "/workspace/result.txt"]`.
4. Call `CheckFile("/workspace/result.txt", []byte("first\nsecond\n"))` to copy and compare the file bytes.
5. Write bytes `00 ff 0a` to `/workspace/binary.bin` through `WriteFile`, then compare them through `CheckFile`.
6. Run argv `["stat", "-c", "%a", "/workspace/binary.bin"]` through `CheckCommand`.

### Expected results

- Every command exits with code `0`.
- The two write commands produce empty stdout.
- Every command produces exactly empty stderr.
- The read command's stdout and the copied file contain exactly `first\nsecond\n`.
- The binary file preserves NUL, the non-UTF-8 byte `ff`, and a newline exactly.
- The permission command returns exactly `644\n` on stdout, empty stderr, and exit code `0`.

### Notes

- All four commands use `CheckCommand(command, want)` with exact `cli.Result` expectations in the same isolated container.

## `TestExpectedFailureScenario`

### Scope

Check that an expected nonzero command result does not fail the scenario.

### Commands under test

| Command path | Purpose                                          |
| ------------ | ------------------------------------------------ |
| `sh`         | Print a message and return a selected exit code. |

### Arguments and options

| Argument or option              | Purpose                                                                    |
| ------------------------------- | -------------------------------------------------------------------------- |
| `-c`                            | Execute the fixture script.                                                |
| `echo expected-failure; exit 7` | Pass the script as one argument, selecting exit code `7` inside the shell. |

### Preconditions and fixtures

- The image contains `sh`.
- No file fixture is necessary.

### Execution flow

1. Run argv `["sh", "-c", "echo expected-failure; exit 7"]` through `CheckCommand(command, want)`, expecting exit code `7`.

### Expected results

- The exit code is `7`.
- Stdout is exactly `expected-failure\n`.
- Stderr is exactly empty.

### Notes

- The expected command failure is a passing test result.

## `TestSeparatedOutputScenario`

### Scope

Check native separate output streams, working directory, and environment through both `CheckCommand` and `Run`.

### Commands under test

| Command path | Purpose                                 |
| ------------ | --------------------------------------- |
| `sh`         | Print the environment and a diagnostic. |

### Arguments and options

| Argument or option               | Purpose                                     |
| -------------------------------- | ------------------------------------------- |
| `-c`                             | Execute the output fixture script.          |
| `HOME=/workspace/synthetic-home` | Set a synthetic home value for the command. |
| `/workspace`                     | Set the command working directory.          |

### Preconditions and fixtures

- `/workspace` exists.
- The synthetic home value does not require a directory.

### Execution flow

1. Run argv `["sh", "-c", "printf '%s:%s\\n' \"$HOME\" \"$PWD\"; printf 'diagnostic\\n' >&2; exit 7"]` through `CheckCommand`, with working directory `/workspace` and environment entry `HOME=/workspace/synthetic-home`.
2. Run the same `Command` through `Run` and check its Go error before comparing the returned `Result` with the same expected value.

### Expected results

- Both executions succeed without a Go error and return exit code `7`.
- Both `CheckCommand` and `Run` preserve exact stdout `/workspace/synthetic-home:/workspace\n` and stderr `diagnostic\n` separately.
- The `Run` result equals the complete expected `cli.Result`.

### Notes

- Neither API concatenates stdout and stderr.
- The test does not require chronological ordering between the streams.

## `TestPathAssertionsScenario`

### Scope

Check physical path types, absent paths, and literal symbolic-link targets.

### Commands under test

| Command path | Purpose                                                  |
| ------------ | -------------------------------------------------------- |
| `mkdir`      | Create a directory fixture.                              |
| `ln`         | Create symbolic-link fixtures.                           |
| `test`       | Check path existence and type through helper assertions. |
| `readlink`   | Read a literal symbolic-link target through the helper.  |

### Arguments and options

| Argument or option     | Purpose                                                          |
| ---------------------- | ---------------------------------------------------------------- |
| `-s`                   | Create a symbolic link.                                          |
| `-e`, `-L`, `-f`, `-d` | Probe existence, symbolic links, regular files, and directories. |
| `--`                   | End options before the path passed to `readlink`.                |

### Preconditions and fixtures

- `WriteFile("/workspace/result.txt", []byte("fixture\n"))` creates the file fixture.
- `/workspace/missing.txt` does not exist.

### Execution flow

1. Run argv `["mkdir", "/workspace/directory"]`.
2. Run argv `["ln", "-s", "result.txt", "/workspace/link"]`.
3. Run argv `["ln", "-s", "missing.txt", "/workspace/dangling"]`.
4. Run argv `["ln", "-s", "directory", "/workspace/directory-link"]`.
5. Check the regular file, directory, missing target, and literal targets `result.txt` and `missing.txt`.
6. Confirm that `CheckMissing` rejects `/workspace/dangling`.
7. Confirm that `CheckRegularFile` rejects `/workspace/link` and `CheckDirectory` rejects `/workspace/directory-link`.
8. Confirm that `CheckSymlink` rejects `/workspace/result.txt` as the expected literal target of `/workspace/link`.

### Expected results

- All fixture commands return exit code `0` with empty stdout and stderr.
- The positive path assertions succeed.
- A dangling link is not treated as an absent path.
- A symbolic link to a regular file is not treated as a physical regular file.
- A symbolic link to a directory is not treated as a physical directory.
- An absolute resolved target is not accepted in place of the relative literal target.

### Notes

- The image must provide `test` and `readlink` with the listed options.
- No host path is mounted or changed.

## `TestDeadlineScenario`

### Scope

Check that a command with an open output connection returns when the case deadline expires.

### Commands under test

| Command path | Purpose                                                |
| ------------ | ------------------------------------------------------ |
| `sleep`      | Keep the command running until the harness cancels it. |

### Arguments and options

| Argument or option | Purpose                                         |
| ------------------ | ----------------------------------------------- |
| `infinity`         | Keep the command running without a normal exit. |

### Preconditions and fixtures

- The image contains `sleep` with support for `infinity`.
- `cli.NewEnvironment` starts the one-minute invocation budget before image resolution and container setup, after the standard test runner resumes `t.Parallel()`.

### Execution flow

1. Record the command start time.
2. Run argv `["sleep", "infinity"]` through `Run(cli.Command{Args: args})`.
3. Check the returned error with `errors.Is(err, context.DeadlineExceeded)` without inspecting the unusable exit code.
4. Check that the elapsed command time is at most 70 seconds.
5. Return from the test so `t.Cleanup` cancels execution, closes the clients, and removes the container and lease with fresh cleanup deadlines.

### Expected results

- The call returns a deadline error, not a normal exit result.
- It returns within the case budget plus the test's scheduling allowance.
- The E2E task checks that the caller-owned image remains and no scenario or lease container remains.

### Notes

- The 70-second assertion allows scheduling overhead around the one-minute case budget.
- Closing an exec connection does not itself signal the process to exit.
- Container cleanup stops the remaining process after the test returns, using a fresh 30-second context.
- This case adds approximately one minute to the Docker-backed test run.

## `TestIsolationScenario`

### Scope

Check that two sequential native child test scopes observe distinct container hostnames and do not share a file marker.

### Commands under test

| Command path | Purpose                                      |
| ------------ | -------------------------------------------- |
| `cat`        | Read the container's observed hostname.      |
| `test`       | Check marker absence through `CheckMissing`. |

### Arguments and options

| Argument or option                | Purpose                                                                   |
| --------------------------------- | ------------------------------------------------------------------------- |
| `/etc/hostname`                   | Read the hostname supplied to each container.                             |
| `/workspace/isolation-marker.txt` | Select the marker written only in the first child scope.                  |
| `-e`, `-L`                        | Probe marker existence and symbolic-link presence through `CheckMissing`. |

### Preconditions and fixtures

- The caller has built `e2e-example:local` from the shared `fixtures/cli/Dockerfile`.
- The image provides `cat`, `test`, and `/workspace`, and Docker supplies `/etc/hostname`.
- The parent test calls `t.Parallel()`, but neither child calls `t.Parallel()`.
- Each child calls `cli.NewEnvironment(t, "e2e-example:local")` inside its body so cleanup belongs to that child scope.

### Execution flow

1. Enter `t.Run("first", ...)` and create its environment.
2. Run argv `["cat", "/etc/hostname"]`, check its Go error and result, and retain the exact stdout in the parent test's local variable.
3. Call `CheckMissing("/workspace/isolation-marker.txt")`.
4. Call `WriteFile("/workspace/isolation-marker.txt", []byte("first case\n"))`, then `CheckFile` with the same path and exact bytes.
5. Return from the first child so its registered cleanup finishes before `t.Run` returns. If that child fails, return from the parent without starting the second child.
6. Enter `t.Run("second", ...)`, create a new environment, and run argv `["cat", "/etc/hostname"]`.
7. Check the Go error and command result, then compare the observed stdout with the first child's stdout.
8. Call `CheckMissing("/workspace/isolation-marker.txt")` in the second environment, then return so its cleanup runs.

### Expected results

- Both hostname commands return no Go error, exit code `0`, and exactly empty stderr.
- Each hostname stdout is nonempty after `strings.TrimSpace`. No fixed hostname value or newline shape is asserted.
- The two exact hostname stdout values differ.
- The marker is absent before the first write, contains exactly `first case\n` after that write, and is absent in the second container, including no dangling symbolic link at that path.
- Every file operation and path assertion succeeds without an error.

### Notes

- All case-specific actions and assertions are directly inside standard `testing` test bodies.
- `cli.NewEnvironment` owns child cleanup. The test does not add a runner, public API, or manual container removal.
- Hostname and marker observations establish isolation through the existing public API. This scenario does not independently inspect Docker container IDs, daemon-side removal, or image retention.
