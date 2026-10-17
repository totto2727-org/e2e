# Rust Docker acceptance scenarios

Source: [e2e.rs](./e2e.rs)

## `greeting`

### Scope

Verify exact greeting and UTF-8 output through two commands in one scoped environment.

### Commands under test

| Command path | Purpose                           |
| ------------ | --------------------------------- |
| `echo`       | Print literal greeting arguments. |

### Arguments and options

| Argument or option | Purpose                          |
| ------------------ | -------------------------------- |
| `hello from e2e`   | One literal greeting argv entry. |
| `UTF-8`            | Second literal text argv entry.  |

### Preconditions and fixtures

The caller-built `e2e-example:local` has `echo` and a long-running default entrypoint.
The ignored native Tokio test invokes `with_environment` directly and owns no shared suite.

### Execution flow

1. Await `with_environment(IMAGE, callback)` to inspect the local image and start a fresh Testcontainers-managed case container from its immutable ID.
2. Run `Command::new(["echo", "hello from e2e"])` and compare every result field.
3. Run `Command::new(["echo", "UTF-8"])` and compare every result field with a native assertion.
4. Await independent cleanup before returning from the native test.

### Expected results

Both exit statuses are `0`, stdout is respectively exactly `hello from e2e\n` and `UTF-8\n`, and stderr is exactly empty.
No filesystem state is asserted.

### Notes

The native runner can select this case by its exact name.
Docker execution is ignored in native CI.

## `file_workflow`

### Scope

Verify multi-command file persistence, exact text bytes, and binary file transfer in one container.

### Commands under test

| Command path | Purpose                                               |
| ------------ | ----------------------------------------------------- |
| `sh`         | Write and append text using an explicit shell script. |
| `cat`        | Read the persisted file through command execution.    |
| `stat`       | Check the uploaded file's mode.                       |

### Arguments and options

| Argument or option                     | Purpose                                         |
| -------------------------------------- | ----------------------------------------------- |
| `-c`                                   | Interpret the following single script argument. |
| `echo first > /workspace/result.txt`   | Create the first line.                          |
| `echo second >> /workspace/result.txt` | Append the second line.                         |
| `/workspace/result.txt`                | Literal `cat` file argument.                    |

### Preconditions and fixtures

The caller-built `e2e-example:local` provides `sh`, `echo`, `cat`, `stat`, and an existing `/workspace` directory.
The ignored test calls `with_environment` for its own fresh container.

### Execution flow

1. Run `["sh", "-c", "echo first > /workspace/result.txt"]`.
2. Run `["sh", "-c", "echo second >> /workspace/result.txt"]` in the same environment.
3. Run `["cat", "/workspace/result.txt"]` and compare exact stdout.
4. Check the file bytes with `check_file("/workspace/result.txt", b"first\nsecond\n")`.
5. Write bytes `[0, 255, 10]` to `/workspace/binary` and check those exact bytes.
6. Run `["stat", "-c", "%a", "/workspace/binary"]` and compare stdout with exactly `644\n`.
7. Await scoped cleanup.

### Expected results

All commands exit `0` with empty stderr.
The shell commands have empty stdout, while `cat` outputs exactly `first\nsecond\n`.
The text file contains exactly `first\nsecond\n` and the binary file exactly `[0, 255, 10]` with mode `0644` within this scope.

### Notes

The shell is explicit caller argv, not an implicit library quoting layer.
No image mutation is performed by the helper.

## `expected_failure`

### Scope

Verify a nonzero application exit remains a normal command result available for exact assertion.

### Commands under test

| Command path | Purpose                                   |
| ------------ | ----------------------------------------- |
| `sh`         | Print text and return a specified status. |

### Arguments and options

| Argument or option              | Purpose                                             |
| ------------------------------- | --------------------------------------------------- |
| `-c`                            | Evaluate one script argv entry.                     |
| `echo expected-failure; exit 7` | Produce the expected stdout and application status. |

### Preconditions and fixtures

The caller-built image supplies `sh` and `echo`.
This ignored native test obtains a fresh environment with `with_environment`.

### Execution flow

1. Run `["sh", "-c", "echo expected-failure; exit 7"]`.
2. Compare all fields with `expected(7, "expected-failure\n", "")`.
3. Await scoped cleanup and propagate errors.

### Expected results

The inspected application exit is exactly `7`, stdout exactly `expected-failure\n`, and stderr exactly empty.
A nonzero application result is not treated as a Docker or transport failure.

### Notes

There are no filesystem assertions.

## `separate_streams`

### Scope

Verify inspected application status, separate streams, working-directory override, and environment override.

### Commands under test

| Command path | Purpose                                                   |
| ------------ | --------------------------------------------------------- |
| `sh`         | Print environment and directory values plus a diagnostic. |

### Arguments and options

| Argument or option                                                   | Purpose                                       |
| -------------------------------------------------------------------- | --------------------------------------------- |
| `-c`                                                                 | Evaluate the following script argv entry.     |
| `printf '%s:%s\n' "$HOME" "$PWD"; printf 'diagnostic\n' >&2; exit 7` | Emit both streams and the application status. |
| `HOME=/workspace/synthetic-home`                                     | Override one process environment entry.       |
| `/workspace`                                                         | Set `Command.working_dir`.                    |

### Preconditions and fixtures

The caller-built image has `sh`, `printf`, and `/workspace`.
The ignored native test invokes `with_environment` directly.

### Execution flow

1. Build literal argv `["sh", "-c", "printf '%s:%s\\n' \"$HOME\" \"$PWD\"; printf 'diagnostic\\n' >&2; exit 7"]`, set the working directory and environment entry.
2. Await `check_command` and compare every command-result field.
3. Await scoped cleanup.

### Expected results

Exit is exactly `7`, stdout exactly `/workspace/synthetic-home:/workspace\n`, and stderr exactly `diagnostic\n`.
No filesystem mutation is asserted.

### Notes

The helper never infers application status from a host `docker exec` process.

## `physical_paths`

### Scope

Verify physical file and directory checks, missing paths, literal symlink targets, and dangling-link rejection as missing.

### Commands under test

| Command path | Purpose                                              |
| ------------ | ---------------------------------------------------- |
| `mkdir`      | Create a physical directory.                         |
| `ln`         | Create literal relative-target symlinks.             |
| `test`       | Used by the environment's physical-path assertions.  |
| `readlink`   | Used by the environment's literal symlink assertion. |

### Arguments and options

| Argument or option                                                                            | Purpose                                                              |
| --------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| `-s`                                                                                          | Create symbolic links.                                               |
| `result.txt`, `missing.txt`, `directory`                                                      | Literal relative symlink targets.                                    |
| `/workspace/directory`, `/workspace/link`, `/workspace/dangling`, `/workspace/directory-link` | Fixture paths.                                                       |
| `-e`, `-L`, `-f`, `-d`, `--`                                                                  | Physical-path and literal-target helper implementation requirements. |

### Preconditions and fixtures

The caller-built image has an existing `/workspace` and the commands listed above.
The ignored test creates all fixtures in its own `with_environment` callback.

### Execution flow

1. Write `/workspace/result.txt` with exact bytes `fixture\n`.
2. Run `["mkdir", "/workspace/directory"]`.
3. Run `["ln", "-s", "result.txt", "/workspace/link"]`, `["ln", "-s", "missing.txt", "/workspace/dangling"]`, and `["ln", "-s", "directory", "/workspace/directory-link"]` in order.
4. Check physical file, physical directory, missing `/workspace/missing.txt`, and literal `result.txt` and `missing.txt` symlink targets.
5. Assert errors when treating the dangling link as missing, the file link as a physical regular file, and the directory link as a physical directory.
6. Assert a literal-target mismatch for `/workspace/link` versus `/workspace/result.txt`.
7. Await scoped cleanup.

### Expected results

Fixture commands exit `0` with both streams empty.
The physical and literal-target assertions succeed only for their stated paths and targets.
Each deliberately invalid physical or literal-target assertion returns `Error::Assertion`.
The test writes the fixture bytes but does not independently read them back in this scenario.

### Notes

Relative and absolute link-target strings are not normalized into equivalence.

## `deadline`

### Scope

Verify the default one-minute operation deadline terminates an attached command operation.

### Commands under test

| Command path | Purpose                                                |
| ------------ | ------------------------------------------------------ |
| `sleep`      | Keep the process running until the operation deadline. |

### Arguments and options

| Argument or option | Purpose                                   |
| ------------------ | ----------------------------------------- |
| `infinity`         | Request an indefinitely sleeping process. |

### Preconditions and fixtures

The caller-built image provides `sleep infinity` and remains running.
The ignored native test starts a host timer immediately before calling `with_environment`.

### Execution flow

1. Await `with_environment` with its default single 60-second operation budget including setup.
2. Await `run(Command::new(["sleep", "infinity"]))`.
3. Assert `Error::Execution { failure: ExecutionFailure::Deadline, .. }` in the callback.
4. Return the case container ID, await cleanup, and check elapsed time.
5. Inspect that ID and require Docker's container-not-found response.

### Expected results

The command returns a typed deadline execution error, not an application exit result.
Total observed time including normal cleanup is less than 70 seconds, and the case container is absent after cleanup.
No output bytes or exit status are asserted for this intentionally incomplete command.

### Notes

The timing assertion assumes a responsive local daemon for setup and cleanup.
The helper does not preempt arbitrary callback futures that avoid environment operations.

## `scopes_are_fresh_remove_owned_containers_and_preserve_image`

### Scope

Verify two independent scoped environments have distinct container identities, no cross-scope fixture state, no owned residual containers, and an unchanged caller image.

### Commands under test

| Command path | Purpose                                                      |
| ------------ | ------------------------------------------------------------ |
| `test`       | Used by `check_missing` to establish fresh filesystem state. |

### Arguments and options

| Argument or option              | Purpose                                               |
| ------------------------------- | ----------------------------------------------------- |
| `/workspace/scoped-fixture`     | Path that must be missing at the start of each scope. |
| `org.testcontainers.managed-by` | Label that identifies Testcontainers ownership.       |

### Preconditions and fixtures

The caller-built image has an existing `/workspace`, the required `test` predicates, and no `/workspace/scoped-fixture`.
The native test uses a separate Docker handle only for acceptance observations, not library execution.

### Execution flow

1. Inspect the caller's image ID before the scopes.
2. Invoke `with_environment` twice sequentially. In each callback, check the fixture path is missing, write `owned\n`, inspect the Testcontainers managed-by label, and return the container ID.
3. After each helper returns, inspect its exact container ID and require a Docker 404 response.
4. Assert the two immutable container identities differ.
5. Inspect the caller image again and compare its ID with the original.

### Expected results

Both scopes start without the fixture, and each Testcontainers-managed container is absent after its completed scope.
Container IDs differ, and the caller image ID is unchanged.
The fixture is written but its content is not independently read back in this test.

### Notes

Exact-ID audits remain valid when another native test runs concurrently or only this test is selected.
This is one lifecycle acceptance case, not an aggregate runner for other scenarios.

## `working_directory_preserves_literal_argv`

### Scope

Verify that a working-directory override preserves empty arguments, spaces, quotes, and shell syntax as literal argv.

### Commands under test

| Command path | Purpose                                                      |
| ------------ | ------------------------------------------------------------ |
| `mkdir`      | Create a directory whose name contains shell syntax.         |
| `printf`     | Print each literal argument between angle brackets.          |
| `test`       | Check that an injection-like argument did not create a file. |

### Arguments and options

| Argument or option                                             | Purpose                                                   |
| -------------------------------------------------------------- | --------------------------------------------------------- |
| `--`                                                           | End `mkdir` options.                                      |
| `/workspace/a 'quoted' $(literal)`                             | Literal directory used by `Command.working_dir`.          |
| `<%s>\n`                                                       | Print each argument without reinterpretation.             |
| `""`, `two words`, `$(touch /workspace/injected)`, `'literal'` | Exercise empty, spaced, shell-like, and quoted arguments. |

### Preconditions and fixtures

The caller-built image provides `sh`, `mkdir`, `printf`, `test`, and `/workspace`.
The test owns one fresh container.

### Execution flow

1. Run `["mkdir", "--", "/workspace/a 'quoted' $(literal)"]` and require a zero exit with empty streams.
2. Set the working directory to that literal path.
3. Run `["printf", "<%s>\n", "", "two words", "$(touch /workspace/injected)", "'literal'"]`.
4. Compare every result field, then call `check_missing("/workspace/injected")`.

### Expected results

The command exits `0` with empty stderr and stdout exactly `<>\n<two words>\n<$(touch /workspace/injected)>\n<'literal'>\n`.
The injection-like argument does not create `/workspace/injected`.

### Notes

The library's fixed directory-change script receives caller values as positional arguments.
This case does not separately print or assert the resulting directory.

## `callback_error_is_preserved_after_container_removal`

### Scope

Verify that a concrete caller error survives successful container cleanup.

### Commands under test

No application command is executed.
The test observes the native scope result and Docker container inspection.

### Arguments and options

The callback returns its container ID and the error marker `7_u8` as one tuple error.

### Preconditions and fixtures

The caller-built image is available to Testcontainers.
The ignored test creates one fresh environment.

### Execution flow

1. Enter `with_environment` and return `Err((container_id, 7_u8))` from its callback.
2. Destructure the result as `ScopeError::Callback` and compare the marker with `7`.
3. Inspect the returned container ID using Testcontainers' configured client.

### Expected results

The typed callback tuple remains intact.
Container inspection returns Docker 404 after the helper returns.

### Notes

This case covers successful removal after a caller error, not a daemon cleanup failure.

## `callback_panic_is_resumed_after_container_removal`

### Scope

Verify that callback unwinding preserves its original typed panic payload after cleanup.

### Commands under test

No application command is executed.
The test observes the panic payload and Docker container inspection.

### Arguments and options

The callback panics with the value `123_u32`.
A Tokio oneshot channel carries the exact container ID to the test.

### Preconditions and fixtures

The caller-built image is available to Testcontainers.
The native test catches the scope panic with `FutureExt::catch_unwind`.

### Execution flow

1. Enter the callback and send its container ID through the channel.
2. Call `std::panic::panic_any(123_u32)`.
3. Await the caught scope result and downcast its panic payload to `u32`.
4. Inspect the recorded container ID using Testcontainers' configured client.

### Expected results

The original panic payload is exactly `123_u32`.
Container inspection returns Docker 404 before the test completes.

### Notes

The test catches the panic only to assert its typed payload and cleanup result.

## `dropping_scope_uses_testcontainers_cleanup`

### Scope

Verify native Testcontainers Drop cleanup when a caller discards an active scope future.

### Commands under test

No application command is executed.
The test observes Docker container inspection after dropping the scope.

### Arguments and options

The callback sends its container ID through a Tokio oneshot channel, then awaits a pending future.

### Preconditions and fixtures

The caller-built image is available and `TESTCONTAINERS_COMMAND` is unset.
The Tokio runtime remains alive while the scope future is dropped.

### Execution flow

1. Pin `with_environment` and poll it alongside the container-ID channel.
2. Receive the exact container ID after callback entry.
3. Drop the owned pinned scope future, not just a borrowed pin.
4. Inspect the recorded ID using Testcontainers' configured client.

### Expected results

Container inspection returns Docker 404 after the native scope is dropped.
The callback does not complete normally.

### Notes

This verifies dropping an idle callback on a live runtime.
It does not simulate abrupt process termination or an unresponsive daemon.
