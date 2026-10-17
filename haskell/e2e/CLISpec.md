# Haskell Docker acceptance scenarios

Source: [CLISpec.hs](./CLISpec.hs)

Each scenario is registered as an ordinary Hspec `it` and invokes `withEnvironment` through the per-example `auditedEnvironment` helper.
No scenario runs before Hspec selects and executes its example.
Hspec `--match` selects actual execution, and `--dry-run` lists examples without contacting Docker.
Each invocation inspects the caller-built image, holds a private stopped lease of its immutable ID, and creates one fresh running container.
The private test-only Engine audit snapshots the caller image ID and all `org.e2e.haskell.owner` container IDs before each invocation, requires the running container ID to be absent from the prior snapshot, and checks equal snapshots and image IDs after cleanup.
These audits assume sequential examples and no other Haskell Docker invocation during their snapshot windows.
The library itself has no concurrency gate. Consumers without snapshot audits can use Hspec `parallel` and `--jobs N`.

## `greetingScenario`

### Scope

Verify the exact greeting through Hspec example `greeting`.

### Commands under test

| Command path | Purpose             |
| ------------ | ------------------- |
| `echo`       | Print the greeting. |

### Arguments and options

| Argument or option | Purpose                 |
| ------------------ | ----------------------- |
| `hello from e2e`   | One literal argv entry. |

### Preconditions and fixtures

The caller builds `e2e-example:local` from the [Go example Dockerfile](../../go/example/Dockerfile), providing `/workspace`, Linux tools, and a `sleep infinity` entrypoint.
The helper does not build, pull, or remove the image.

### Execution flow

1. Hspec executes `it "greeting"`, then the scoped helper prepares a fresh container.
2. Execute argv `["echo", "hello from e2e"]` using the image working directory and no environment overrides.
3. Compare `Result 0 "hello from e2e\n" ""` and complete cleanup and per-example audits.

### Expected results

Exit status is exactly `0`, stdout is exactly the ASCII bytes `hello from e2e\n`, and stderr is empty.
No filesystem mutation is asserted.

### Notes

All command result fields are compared byte-for-byte.
The per-example audits assert removal of the running container and stopped lease and preservation of the image ID.

## `fileWorkflowScenario`

### Scope

Verify shared container state across commands and exact binary file transfer through Hspec example `file workflow`.

### Commands under test

| Command path | Purpose                                                        |
| ------------ | -------------------------------------------------------------- |
| `sh`         | Write and append text using explicitly requested shell syntax. |
| `cat`        | Read the persisted text file.                                  |
| `stat`       | Inspect transferred file mode.                                 |

### Arguments and options

| Argument or option             | Purpose                                          |
| ------------------------------ | ------------------------------------------------ |
| `-c`                           | Pass an explicit shell script as one argv entry. |
| `/workspace/result.txt`        | Persisted text fixture path.                     |
| `-c`, `%a`, `/workspace/bytes` | Request numeric mode for the binary fixture.     |

### Preconditions and fixtures

The caller-built `e2e-example:local` image supplies an existing `/workspace` and `sh`, `cat`, and `stat`.
One `withEnvironment` invocation owns all commands and file operations for this example.

### Execution flow

1. Execute `["sh", "-c", "echo first > /workspace/result.txt"]`, expecting `Result 0 "" ""`.
2. Execute `["sh", "-c", "echo second >> /workspace/result.txt"]`, expecting `Result 0 "" ""`.
3. Execute `["cat", "/workspace/result.txt"]`, expecting `Result 0 "first\nsecond\n" ""`.
4. Compare the file bytes with `first\nsecond\n` using `checkFile`.
5. Write bytes `00 ff 0a` to `/workspace/bytes` through the archive API, then read and compare the exact bytes.
6. Execute `["stat", "-c", "%a", "/workspace/bytes"]`, expecting `Result 0 "644\n" ""`, then clean up and audit.

### Expected results

Every command exits `0` with empty stderr.
The text file contains exactly `first\nsecond\n`.
The binary file contains exactly bytes `00 ff 0a` and has mode `0644`.

### Notes

The fixture parent must exist before archive transfer.
All state is confined to this fresh container and removed with it. The image remains caller-owned.

## `expectedFailureScenario`

### Scope

Assert a normal nonzero application result in Hspec example `expected failure`.

### Commands under test

| Command path | Purpose                                               |
| ------------ | ----------------------------------------------------- |
| `sh`         | Print text and exit with the expected nonzero status. |

### Arguments and options

| Argument or option              | Purpose                                 |
| ------------------------------- | --------------------------------------- |
| `-c`                            | Execute the explicitly supplied script. |
| `echo expected-failure; exit 7` | One literal script argument.            |

### Preconditions and fixtures

The caller-built `e2e-example:local` provides `sh` and a long-running entrypoint.
Hspec executes the example before the scoped helper creates its private container and lease.

### Execution flow

1. Execute `["sh", "-c", "echo expected-failure; exit 7"]` with no working-directory or environment overrides.
2. Compare `Result 7 "expected-failure\n" ""`.
3. Clean up and run the per-example audits.

### Expected results

Exit status is exactly `7`, stdout is exactly `expected-failure\n`, and stderr is empty.
No file operations are asserted.

### Notes

Code `7` is not a transport or Docker exception.
The result requires exec inspection, rather than the status of a host Docker process.

## `separateStreamsScenario`

### Scope

Verify working-directory and environment overrides, separate streams, and consistent execution/assertion APIs in Hspec example `separate streams`.

### Commands under test

| Command path | Purpose                                 |
| ------------ | --------------------------------------- |
| `sh`         | Produce stdout and stderr and exit `7`. |

### Arguments and options

| Argument or option                                                   | Purpose                                                                     |
| -------------------------------------------------------------------- | --------------------------------------------------------------------------- |
| `-c`                                                                 | Execute an explicit script.                                                 |
| `printf '%s:%s\n' "$HOME" "$PWD"; printf 'diagnostic\n' >&2; exit 7` | One script argument using the overridden environment and working directory. |
| `/workspace`                                                         | Working-directory override, not an argv entry.                              |
| `HOME=/workspace/synthetic-home`                                     | Environment override, not an argv entry.                                    |

### Preconditions and fixtures

The caller-built `e2e-example:local` supplies `sh` and an existing `/workspace`.
A fresh environment is created inside Hspec execution.

### Execution flow

1. Set `Command` argv to `["sh", "-c", script]` using the literal script above, working directory `/workspace`, and the single environment entry above.
2. Call `checkCommand`, comparing `Result 7 "/workspace/synthetic-home:/workspace\n" "diagnostic\n"`.
3. Execute the same command again with `run` and compare the same complete result using Hspec `shouldReturn`.
4. Clean up and audit the invocation.

### Expected results

Both executions return status `7`, exact stdout `/workspace/synthetic-home:/workspace\n`, and exact stderr `diagnostic\n`.
No cross-stream ordering or filesystem mutation is asserted.

### Notes

Streams remain strict bytes and are not merged or decoded.
The callback's Hspec assertion failure propagates after the helper attempts cleanup.

## `physicalPathsScenario`

### Scope

Verify physical path types, literal link targets, and rejection of misleading links in Hspec example `physical paths`.

### Commands under test

| Command path | Purpose                                                                    |
| ------------ | -------------------------------------------------------------------------- |
| `mkdir`      | Create a physical directory.                                               |
| `ln`         | Create file, dangling, and directory links.                                |
| `test`       | Probe existence, link status, and physical types inside assertion helpers. |
| `readlink`   | Compare unchanged link targets inside `checkSymlink`.                      |

### Arguments and options

| Argument or option                       | Purpose                        |
| ---------------------------------------- | ------------------------------ |
| `-s`                                     | Create a symbolic link.        |
| `-e`, `-L`, `-f`, `-d`                   | Helper path probes.            |
| `--`                                     | End options to `readlink`.     |
| `result.txt`, `missing.txt`, `directory` | Relative literal link targets. |

### Preconditions and fixtures

The local `e2e-example:local` image supplies these Linux commands and `/workspace`.
Write `/workspace/result.txt` with exact bytes `fixture\n` before creating links.

### Execution flow

1. Execute `["mkdir", "/workspace/directory"]`.
2. Execute `["ln", "-s", "result.txt", "/workspace/link"]`.
3. Execute `["ln", "-s", "missing.txt", "/workspace/dangling"]`.
4. Execute `["ln", "-s", "directory", "/workspace/dirlink"]`. Every setup command must return `Result 0 "" ""`.
5. Assert a physical regular file and directory and missing `/workspace/missing.txt`.
6. Assert literal targets `result.txt` and `missing.txt` for the file and dangling links.
7. Require assertion failures for treating the dangling link as missing, the file link as a regular file, and the directory link as a directory.
8. Require an assertion failure when comparing `/workspace/link` with the resolved target `/workspace/result.txt`, then clean up and audit.

### Expected results

Physical file and directory probes succeed, missing paths are accepted, and relative or dangling link targets remain unchanged.
The four deliberately incorrect path assertions throw `AssertionFailed`.

### Notes

The fixture text bytes are written but not independently read back in this scenario.
All fixture filesystem state disappears with the case container. No host mounts are used.

## `deadlineScenario`

### Scope

Verify that stalled command execution uses the remaining invocation budget in Hspec example `deadline`.

### Commands under test

| Command path | Purpose                                              |
| ------------ | ---------------------------------------------------- |
| `sleep`      | Keep the exec attached until the operation deadline. |

### Arguments and options

| Argument or option | Purpose                      |
| ------------------ | ---------------------------- |
| `infinity`         | Request an indefinite sleep. |

### Preconditions and fixtures

The caller builds `e2e-example:local` with `sleep infinity` support.
The helper starts its single 60-second operation budget before image inspection and creation of the stopped lease and running container.

### Execution flow

1. Capture the monotonic clock inside the callback.
2. Execute `["sleep", "infinity"]` and require `DeadlineExceeded`, not a `Result`.
3. Capture the clock again and require elapsed command time between 50 and 70 seconds.
4. Perform fresh 30-second cleanup for each owned resource and audit removal and image preservation.

### Expected results

The command throws `DeadlineExceeded` and does not produce an application result or inspected exit status.
Measured command time is between 50 and 70 seconds.

### Notes

The timing assertion assumes image inspection and container setup consume less than roughly ten seconds of the shared budget.
Cleanup is outside the command measurement and has fresh independent budgets.
Native controlled HTTP/socket tests are transport evidence, not proof of real Docker acceptance.
Run the explicit Docker task only when Docker validation is authorized.
