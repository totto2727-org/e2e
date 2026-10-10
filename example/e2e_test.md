# CLI helper scenarios

Source: [e2e_test.go](./e2e_test.go)

This document is for contributors who maintain the Docker-backed acceptance tests.
Each scenario receives a fresh container from the caller-built `e2e-example:local` image.

## `stdoutScenario`

### Scope

Check the existing exact-output API.

### Commands under test

| Command path | Purpose |
| --- | --- |
| `echo` | Print a greeting. |

### Arguments and options

| Argument or option | Purpose |
| --- | --- |
| `hello from e2e` | Pass the greeting as one argument. |

### Preconditions and fixtures

- The image contains `echo`.
- No file fixture is necessary.

### Execution flow

1. Run argv `["echo", "hello from e2e"]` through `CheckStdout`.

### Expected results

- The exit code is `0`.
- Output is exactly `hello from e2e\n`.

### Notes

- This case preserves coverage of the existing API.

## `fileOutputScenario`

### Scope

Check a file workflow with multiple commands in one container.

### Commands under test

| Command path | Purpose |
| --- | --- |
| `sh` | Write and append file content. |
| `cat` | Read the completed file. |

### Arguments and options

| Argument or option | Purpose |
| --- | --- |
| `-c` | Execute the fixture script. |
| `/workspace/result.txt` | Select the fixture file. |

### Preconditions and fixtures

- `/workspace` exists in the image.
- The scenario starts without the result file.

### Execution flow

1. Run argv `["sh", "-c", "echo first > /workspace/result.txt"]`.
2. Run argv `["sh", "-c", "echo second >> /workspace/result.txt"]`.
3. Run argv `["cat", "/workspace/result.txt"]`.
4. Copy the file with `CheckFile` and compare its bytes.

### Expected results

- Every command exits with code `0`.
- The two write commands produce no output.
- The read command and copied file contain exactly `first\nsecond\n`.

### Notes

- All three commands use the same isolated container.

## `expectedFailureScenario`

### Scope

Check that an expected nonzero command result does not fail the scenario.

### Commands under test

| Command path | Purpose |
| --- | --- |
| `sh` | Print a message and return a selected exit code. |

### Arguments and options

| Argument or option | Purpose |
| --- | --- |
| `-c` | Execute the fixture script. |
| `7` | Select the expected exit code. |

### Preconditions and fixtures

- The image contains `sh`.
- No file fixture is necessary.

### Execution flow

1. Run argv `["sh", "-c", "echo expected-failure; exit 7"]` through `CheckStdout`.

### Expected results

- The exit code is `7`.
- Output is exactly `expected-failure\n`.

### Notes

- The expected command failure is a passing test result.

## `separatedOutputScenario`

### Scope

Check separate output streams, working directory, environment, and existing output compatibility.

### Commands under test

| Command path | Purpose |
| --- | --- |
| `sh` | Print the environment and a diagnostic. |

### Arguments and options

| Argument or option | Purpose |
| --- | --- |
| `-c` | Execute the output fixture script. |
| `HOME=/workspace/synthetic-home` | Set a synthetic home value for the command. |
| `/workspace` | Set the command working directory. |

### Preconditions and fixtures

- `/workspace` exists.
- The synthetic home value does not require a directory.

### Execution flow

1. Run argv `["sh", "-c", "printf '%s:%s\\n' \"$HOME\" \"$PWD\"; printf 'diagnostic\\n' >&2; exit 7"]` through `CheckCommand`, with working directory `/workspace` and environment entry `HOME=/workspace/synthetic-home`.
2. Run the same `Command` through the existing `Run` API.

### Expected results

- Both commands return exit code `7`.
- `CheckCommand` receives stdout `/workspace/synthetic-home:/workspace\n` and stderr `diagnostic\n` separately.
- The existing `Run` result contains `/workspace/synthetic-home:/workspace\ndiagnostic\n` in `Stdout`.

### Notes

- The existing API retains stdout-followed-by-stderr concatenation.
- The test does not require chronological ordering between the streams.

## `pathAssertionsScenario`

### Scope

Check physical path types, absent paths, and literal symbolic-link targets.

### Commands under test

| Command path | Purpose |
| --- | --- |
| `mkdir` | Create a directory fixture. |
| `ln` | Create symbolic-link fixtures. |
| `test` | Check path existence and type through helper assertions. |
| `readlink` | Read a literal symbolic-link target through the helper. |

### Arguments and options

| Argument or option | Purpose |
| --- | --- |
| `-s` | Create a symbolic link. |
| `-e`, `-L`, `-f`, `-d` | Probe existence, symbolic links, regular files, and directories. |
| `--` | End options before the path passed to `readlink`. |

### Preconditions and fixtures

- `WriteFile` creates `/workspace/result.txt` with bytes `fixture\n`.
- `/workspace/missing.txt` does not exist.

### Execution flow

1. Run argv `["mkdir", "/workspace/directory"]`.
2. Run argv `["ln", "-s", "result.txt", "/workspace/link"]`.
3. Run argv `["ln", "-s", "missing.txt", "/workspace/dangling"]`.
4. Check the regular file, directory, missing target, and literal targets `result.txt` and `missing.txt`.
5. Confirm that `CheckMissing` rejects `/workspace/dangling`.
6. Confirm that `CheckRegularFile` rejects `/workspace/link`.
7. Confirm that `CheckSymlink` rejects `/workspace/result.txt` as the expected literal target of `/workspace/link`.

### Expected results

- All fixture commands return exit code `0` with empty stdout and stderr.
- The positive path assertions succeed.
- A dangling link is not treated as an absent path.
- A symbolic link to a regular file is not treated as a physical regular file.
- An absolute resolved target is not accepted in place of the relative literal target.

### Notes

- The image must provide `test` and `readlink` with the listed options.
- No host path is mounted or changed.

## `deadlineScenario`

### Scope

Check that a command with an open output connection returns when the case deadline expires.

### Commands under test

| Command path | Purpose |
| --- | --- |
| `sleep` | Keep the command running until the harness cancels it. |

### Arguments and options

| Argument or option | Purpose |
| --- | --- |
| `infinity` | Keep the command running without a normal exit. |

### Preconditions and fixtures

- The image contains `sleep` with support for `infinity`.
- The runner assigns the existing one-minute case budget after the parallel slot is acquired.

### Execution flow

1. Record the command start time.
2. Run argv `["sleep", "infinity"]` through `Exec`.
3. Check the returned error with `errors.Is(err, context.DeadlineExceeded)`.
4. Check that the elapsed command time is at most 70 seconds.
5. Return from the callback so the runner can remove the container with a fresh cleanup deadline.

### Expected results

- The call returns a deadline error, not a normal exit result.
- It returns within the case budget plus the test's scheduling allowance.
- The E2E task checks that the caller-owned image remains and no scenario or lease container remains.

### Notes

- The 70-second assertion allows scheduling overhead around the one-minute case budget.
- Closing an exec connection does not itself signal the process to exit.
- Container cleanup stops the remaining process after the callback returns.
- This case adds approximately one minute to the Docker-backed test run.
