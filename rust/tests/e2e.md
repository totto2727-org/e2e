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

1. Await `with_environment(IMAGE, callback)` to resolve a local immutable image and create a stopped lease plus a fresh case container.
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

### Arguments and options

| Argument or option                     | Purpose                                         |
| -------------------------------------- | ----------------------------------------------- |
| `-c`                                   | Interpret the following single script argument. |
| `echo first > /workspace/result.txt`   | Create the first line.                          |
| `echo second >> /workspace/result.txt` | Append the second line.                         |
| `/workspace/result.txt`                | Literal `cat` file argument.                    |

### Preconditions and fixtures

The caller-built `e2e-example:local` provides `sh`, `echo`, `cat`, and an existing `/workspace` directory.
The ignored test calls `with_environment` for its own fresh container.

### Execution flow

1. Run `["sh", "-c", "echo first > /workspace/result.txt"]`.
2. Run `["sh", "-c", "echo second >> /workspace/result.txt"]` in the same environment.
3. Run `["cat", "/workspace/result.txt"]` and compare exact stdout.
4. Check the file bytes with `check_file("/workspace/result.txt", b"first\nsecond\n")`.
5. Write bytes `[0, 255, 10]` to `/workspace/binary` and check those exact bytes.
6. Await scoped cleanup.

### Expected results

All commands exit `0` with empty stderr.
The shell commands have empty stdout, while `cat` outputs exactly `first\nsecond\n`.
The text file contains exactly `first\nsecond\n` and the binary file exactly `[0, 255, 10]` within this scope.

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
4. Await cleanup and check elapsed time.

### Expected results

The command returns a typed deadline execution error, not an application exit result.
Total observed time including normal cleanup is less than 70 seconds.
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

| Argument or option                 | Purpose                                                     |
| ---------------------------------- | ----------------------------------------------------------- |
| `/workspace/scoped-fixture`        | Path that must be missing at the start of each scope.       |
| Owner-specific Docker label filter | Audit only this scope, independently of other native tests. |

### Preconditions and fixtures

The caller-built image has an existing `/workspace`, the required `test` predicates, and no `/workspace/scoped-fixture`.
The native test uses a separate Docker handle only for acceptance observations, not library execution.

### Execution flow

1. Inspect the caller's image ID before the scopes.
2. Invoke `with_environment` twice sequentially. In each callback, check the fixture path is missing, write `owned\n`, inspect the case owner label, and return the container identity plus owner.
3. After each helper returns, list all containers using the exact scope-owner label filter and assert the result is empty.
4. Assert the two immutable container identities differ.
5. Inspect the caller image again and compare its ID with the original.

### Expected results

Both scopes start without the fixture, and both case and lease containers are absent after each completed scope.
Container IDs differ, and the caller image ID is unchanged.
The fixture is written but its content is not independently read back in this test.

### Notes

Owner-specific audits remain valid when another native test runs concurrently or only this test is selected.
This is one lifecycle acceptance case, not an aggregate runner for other scenarios.
