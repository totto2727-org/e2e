# Haskell Docker acceptance scenarios

Source: [CLISpec.hs](./CLISpec.hs)

## Filesystem snapshots

The `filesystem snapshots` example creates a binary regular file, a physical directory, a file link, and a dangling link.
It also selects an absent path.
The test loads a caller-built static driver for the container architecture and installs it once per environment.
The image needs neither Python nor Go.
The first capture asserts every entry, exact binary bytes `[0,255,13,10]`, an empty file, and JSON at a path that contains spaces.
A second capture remains equal after a permission change and creation of an unselected directory child.
The application workflow then changes bytes, replaces a directory with a link, changes a literal link target without changing its resolved destination, creates the absent directory, deletes a regular file, and changes JSON whitespace without changing its meaning.
Compare the captured maps with Hspec `shouldBe` to verify exact content, kind, target, and existence changes.
Repeated capture must leave the application bytes unchanged.

The `snapshot rejects unsupported physical kinds` example creates a FIFO with `mkfifo` and requires an infrastructure error.
The `snapshot does not treat a non-directory parent as absence` example reads a child path below a regular file and requires an infrastructure error.
Native transport regressions also cover permission diagnostics, malformed responses, driver read failures, invalid paths, unchanged snapshots, and selected versus unselected missing paths.

All snapshot cases use the same owned-container and caller-image audits as the other examples.

Each scenario is registered as an ordinary Hspec `it` and invokes `withEnvironment` through the per-example `auditedEnvironment` helper.
No scenario runs before Hspec selects and executes its example.
Hspec `--match` selects actual execution, and `--dry-run` lists examples without contacting Docker.
Each invocation inspects the caller-built image, holds a private stopped lease of its immutable ID, and creates one fresh running container.
The private test-only Engine audit snapshots the caller image ID and all `org.e2e.haskell.owner` container IDs before each invocation, requires the running container ID to be absent from the prior snapshot, and checks equal snapshots and image IDs after cleanup.
These audits assume sequential examples and no other Haskell Docker invocation during their snapshot windows.
The library itself has no concurrency gate. Consumers without snapshot audits can use Hspec `parallel` and `--jobs N`.

## Scoped terminals

These examples use the installed static driver through the public `withTerminal` callback.
Each example loads the amd64 or arm64 binary selected by the container's `uname -m` response.
The driver binaries and the root and nonroot fixture images must be built before these examples run.
The root `pree2e` task supplies these prerequisites.

| Example                                                                     | Commands and fixtures                                                                                                                                                                               | Required observations                                                                                                                                                                                                                                                  |
| --------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `terminal raw bytes and restoration`                                        | `sh`, `stty`, and `dd` save terminal settings, enter raw mode, write ANSI prompt bytes to stderr, and read Up/Down/Right/Left, Space, Enter, Esc, Ctrl-D, and Ctrl-C bytes into `/workspace/input`. | Every input byte is unchanged. Readiness precedes exit. Raw-mode termios differ, ANSI output remains exact, stdout includes separate NUL and byte 255, exit is zero, and original termios are restored.                                                                |
| `terminal preserves argv cwd HOME and TERM=...`                             | `sh` prints an argument with spaces, an empty argument, its directory, HOME, and TERM. Parameters use `xterm-256color` and `caseyard-unsupported`.                                                  | Exact argument boundaries, working directory, HOME, and both TERM values reach the application. Unsupported-TERM policy belongs to the application.                                                                                                                    |
| `terminal external SIGINT reaches the foreground process group`             | `sh` saves termios, disables echo, hides the cursor, and installs an INT handler that restores termios and prints cursor-show bytes.                                                                | External `Interrupt` reaches the handler, exit 7 remains an application result, ANSI bytes are exact, and original termios are restored after exit.                                                                                                                    |
| `terminal prompt matching spans observations and consumes repeated matches` | `sh` emits `pro`, waits for input, then emits `mpt> tail`. A later input produces the complete prompt again.                                                                                        | An incomplete prompt yields a bounded error without consuming its prefix. A complete match spans observed fragments, trailing bytes remain available, consumed prompts cannot replay, failure retains channel/output/running state, and the next prompt still matches. |
| `terminal tiny geometry recovers before draining long output`               | A 1-row, 8-column `sh` fixture waits for input. After resize it prints its geometry, waits again, then emits 128 KiB plus an end marker on stdout.                                                  | Mid-run state remains running with empty stdout and an unchanged file snapshot. Resize reaches 24 by 80. Complete long output is retained, termios are restored, and the final snapshot remains unchanged.                                                             |
| `terminal callback cancellation preserves filesystem snapshot`              | A waiting process records its PID. The callback throws `ThreadKilled` after byte readiness.                                                                                                         | The original asynchronous exception survives cleanup, its process group is gone, and the subsequent snapshot equals its baseline.                                                                                                                                      |
| `terminal pipe input remains unread on no-op failure`                       | `sh` writes `ready` to stderr and exits 7 without reading pipe input `[0,255,10]`.                                                                                                                  | Exact terminal bytes, empty stdout, exit 7, all input unread, restored termios, and rejection of terminal sends in pipe mode.                                                                                                                                          |
| `terminal pipe input delivers exact bytes and EOF`                          | `cat` reads pipe input `[0,255,13,10]` until EOF.                                                                                                                                                   | Exact separate stdout, empty terminal output, no unread input, and exit zero.                                                                                                                                                                                          |
| `terminal resize and signal`                                                | `sh` installs a WINCH handler that runs `stty size`, prints readiness, and waits.                                                                                                                   | Resize produces `12 34` through the terminal. `Terminate` produces signal 15 without stdout.                                                                                                                                                                           |
| `terminal scope closes a surviving process group`                           | `sh` records its PID, prints readiness, and waits in `sleep infinity`.                                                                                                                              | Returning from the callback kills the surviving process group before a normal environment command checks its PID.                                                                                                                                                      |

Expectations use byte readiness, not sleeps.
The examples keep environment checks inside the terminal callback where the process must still be running.
The native suite separately checks scoped cleanup on caller failure and asynchronous exceptions, closed handles, malformed byte arrays, and error observations.
These generic fixtures verify SDK transport and lifecycle boundaries, not selection, list scrolling, or application rendering policy.
Actual long-list navigation and rendering belong to the consumer application's interactive scenarios.
ANSI screen and cursor restoration in these fixtures are application-owned output assertions, not restoration performed by the helper.
Literal Ctrl-C and Ctrl-D transport in raw mode is distinct from external signals and from canonical EOF behavior.

## Nonroot driver and permission scenarios

The `nonroot image installs one driver for snapshots and terminals` example uses `e2e-example-nonroot:local`.
It writes `/tmp/state` as the image user, captures its exact bytes plus a missing path, and runs a terminal command with separate `success` and `ready` output.
The same installed driver must support both operations without root privileges or a post-transfer `chmod`.

The `nonroot snapshot inspection permission failure is not absence` example creates a directory with mode `000` and selects a child beneath it.
The `nonroot snapshot file read permission failure is not absence` example selects a regular file with mode `000`.
Both operations must fail instead of recording `Missing`.
All nonroot examples use the same owned-container and caller-image preservation audits as the root examples.

## `greeting`

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

The caller builds `e2e-example:local` from the [shared CLI Dockerfile](../../fixtures/cli/Dockerfile), providing `/workspace`, Linux tools, and a `sleep infinity` entrypoint.
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

## `file workflow`

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
4. Compare the file bytes with `first\nsecond\n` using `readFile` and Hspec `shouldReturn`.
5. Write bytes `00 ff 0a` to `/workspace/bytes` through the archive API, then read and compare the exact bytes.
6. Execute `["stat", "-c", "%a", "/workspace/bytes"]`, expecting `Result 0 "644\n" ""`, then clean up and audit.

### Expected results

Every command exits `0` with empty stderr.
The text file contains exactly `first\nsecond\n`.
The binary file contains exactly bytes `00 ff 0a` and has mode `0644`.

### Notes

The fixture parent must exist before archive transfer.
All state is confined to this fresh container and removed with it. The image remains caller-owned.

## `expected failure`

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

## `separate streams`

### Scope

Verify working-directory and environment overrides and separate streams in Hspec example `separate streams`.

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
2. Call `run` and use Hspec `shouldReturn` to compare `Result 7 "/workspace/synthetic-home:/workspace\n" "diagnostic\n"`.
3. Clean up and audit the invocation.

### Expected results

Both executions return status `7`, exact stdout `/workspace/synthetic-home:/workspace\n`, and exact stderr `diagnostic\n`.
No cross-stream ordering or filesystem mutation is asserted.

### Notes

Streams remain strict bytes and are not merged or decoded.
The callback's Hspec assertion failure propagates after the helper attempts cleanup.

## `physical paths`

### Scope

Verify physical path types, literal link targets, and rejection of misleading links in Hspec example `physical paths`.

### Commands under test

| Command path | Purpose                                                                |
| ------------ | ---------------------------------------------------------------------- |
| `mkdir`      | Create a physical directory.                                           |
| `ln`         | Create file, dangling, and directory links.                            |
| `test`       | Observe existence, link status, and physical types through `pathKind`. |
| `readlink`   | Read unchanged literal link targets through `readSymlink`.             |

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
5. Compare `pathKind` results with `PathRegularFile`, `PathDirectory`, and `PathMissing` through Hspec `shouldReturn`.
6. Require `PathSymlink` for the file, dangling, and directory links.
7. Compare `readSymlink` results with the literal targets `result.txt`, `missing.txt`, and `directory`, then clean up and audit.

### Expected results

Physical regular files, directories, missing paths, and symbolic links have distinct observed kinds.
Relative and dangling link targets remain unchanged.

### Notes

The literal link targets are compared exactly.
The basic path observations do not require a driver.
All fixture filesystem state disappears with the case container. No host mounts are used.

## `deadline`

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
