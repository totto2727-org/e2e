# Rust Docker acceptance scenarios

Source: [e2e.rs](./e2e.rs)

## `filesystem_snapshots`

### Scope

Verify explicit physical path snapshots and deterministic differences without baseline updates.

### Commands under test

The installed static driver captures state.
`sh`, `mkdir`, `ln`, `rmdir`, `touch`, and `mkfifo` prepare and mutate fixtures.

### Arguments and options

Capture `/workspace/binary`, `/workspace/directory`, `/workspace/missing`, `/workspace/link`, and `/workspace/dangling`.
The regular file contains `[0, 255, 10]`, and links have literal relative targets.

### Preconditions and fixtures

Build `e2e-example:local` and both [static driver artifacts](../README.md#static-driver-installation) before this ignored Docker test.
The test selects the binary for the container's `uname -m` and installs it through the public API.

### Execution flow

1. Create a regular file, directory, valid link, and dangling link, leaving one selected path absent.
2. Capture a baseline and check every state, exact bytes, and literal targets.
3. Add an unselected directory child and change file permissions, then confirm the baseline still matches.
4. Mutate content, target, kind, and existence.
5. Compare fresh state against the original baseline and assert each sorted difference.
6. Reject a FIFO and a path whose parent is a regular file.

### Expected results

Differences appear in lexical path order with `Content`, `Target`, `Kind`, and `Existence` reasons.
The baseline's bytes remain unchanged, and `check_snapshot` returns an assertion for the mutations.
Unsupported kinds and inspection errors are driver failures, not missing paths.

### Notes

The snapshot API does not recurse or compare metadata.

## `filesystem_snapshot_literal_paths_deletion_and_json_bytes`

### Scope

Verify file deletion, selected paths with spaces, empty regular files, exact JSON bytes, and literal symlink retargeting.

### Commands under test

The static driver captures state, while `ln`, `rm`, and `cat` mutate or inspect fixtures.

### Arguments and options

Select `/workspace/file with spaces`, `/workspace/delete me`, `/workspace/empty`, and `/workspace/link`.

### Preconditions and fixtures

Install the matching driver, write valid pretty-printed JSON, and create a link to the spaced filename.

### Execution flow

1. Verify the empty file baseline, rewrite JSON with the same semantic value but different whitespace and final newline, and delete one regular file.
2. Change the link target from `file with spaces` to `./file with spaces` and prove it still resolves to the same current file bytes.
3. Compare snapshots and verify the original baseline remains unchanged.

### Expected results

Sorted differences report `Existence`, `Content`, and `Target`, while the empty regular file remains unchanged.

### Notes

Neither JSON semantics nor equivalent resolved link targets erase literal-byte differences.

## `terminal_raw_bytes_expect_resize_and_restoration`

### Scope

Verify raw terminal interaction, separate stdout, incremental byte expectations, and termios restoration.

### Commands under test

`sh`, `stty`, `printf`, and `dd` run inside a scoped terminal.

### Arguments and options

Use default terminal options, set the working directory to `/workspace`, and set `CASEYARD_VALUE=literal value`.
Send raw bytes `[3, 4]` and resize to 24 rows by 80 columns.

### Preconditions and fixtures

Install the matching driver and write `/workspace/unchanged` with bytes `baseline`.

### Execution flow

1. Save termios, switch to raw mode without echo, and print an ANSI terminal prompt plus stdout context.
2. Expect both channels separately, resize, and check the unchanged file from the session callback.
3. Send literal control bytes and match each stdout byte in sequence.
4. Restore termios, exit `7`, and inspect the final observation and escaped session handle.

### Expected results

Stdout is exactly `/workspace:literal value:` followed by `[3, 4]`.
Terminal bytes are exactly `ESC[2JreadyESC[0m`, and both normalized termios values match.
Exit `7` is an observation, and the escaped session handle returns cancellation after scope exit.

### Notes

The second byte expectation verifies that the first match retains trailing buffered output.

## `terminal_key_bytes_context_sigint_and_cancel_snapshot`

### Scope

Verify exact input bytes for Up, Down, Space, Enter, Esc, Ctrl-D, and Ctrl-C, literal process context, and an external SIGINT without filesystem changes.

### Commands under test

The first `sh` fixture uses `stty` to enter raw mode and `dd` to copy exactly eleven input bytes to stdout.
The second prints readiness and replaces itself with `sleep infinity` before the caller sends SIGINT.

### Arguments and options

The input is exactly `ESC [ A ESC [ B SPACE CR ESC 0x04 0x03`.
Use `/workspace`, `HOME=/workspace/synthetic-home`, literal argv containing spaces, quotes, and an empty string, and `TERM=caseyard-unsupported-terminal` for the raw session.
The SIGINT session uses `TERM=xterm-256color`.

### Preconditions and fixtures

Install the matching driver, write `/workspace/unchanged` with `[0, 255, 10]`, and capture it with absent `/workspace/not-created` as an explicit snapshot baseline.

### Execution flow

1. Expect readiness in raw mode, send the eleven bytes, and wait for normal exit.
2. Compare exact stdout context and byte payload, separate terminal output, and restored termios.
3. Assert the baseline remains unchanged.
4. Start the second session, expect readiness, send `TerminalSignal::Interrupt`, and wait for signal termination.
5. Compare cwd, HOME, and TERM output and assert the same snapshot baseline after cancellation.

### Expected results

All eleven bytes are preserved exactly, including Ctrl-D and Ctrl-C, without closing or signaling the raw-mode child.
The external signal is independently observed as signal `2`, and selected filesystem state remains unchanged after both sessions.

### Notes

An unsupported TERM value is passed through literally.
This generic transport fixture does not claim that an application implements unsupported-terminal fallback or keyboard navigation semantics.

## `terminal_split_prompt_consumption_and_missing_match_diagnostic`

### Scope

Verify a token split across two input-gated writes, repeated prompt consumption, and bounded missing-match diagnostics.

### Commands under test

A raw-mode shell emits `TO`, waits for one byte, emits `KENprompt`, waits for another byte, and emits a second `prompt`.

### Arguments and options

Use exact `TOKEN` and `prompt` expectations with five-second success budgets and 100-millisecond negative-observation budgets.

### Preconditions and fixtures

Install the matching driver and establish stdout readiness before the first negative observation.

### Execution flow

1. Require the incomplete `TOKEN` expectation to fail with retained `TO` output before sending the first input byte.
2. Match the complete token and first prompt, then require a second prompt expectation to fail rather than reuse consumed output.
3. Assert the error identifies terminal bytes and includes complete actual observations, then send the second byte and match the newly emitted prompt.

### Expected results

The final terminal bytes are exactly `TOKENpromptprompt`, stdout is `split-ready`, and the process exits `0`.

### Notes

The input gates force the split token across separate driver reads without assuming ordering between channels.

## `terminal_tiny_recovery_long_output_and_screen_restoration`

### Scope

Verify an observable running no-op in a tiny terminal, geometry recovery, substantial stdout without truncation, and normal screen/cursor restoration.

### Commands under test

`sh`, `stty`, and `dd` implement byte-gated readiness, while `head -c 131072 /dev/zero` produces the long output.

### Arguments and options

Start at one row and eight columns, send Enter, resize to 40 rows and 120 columns, and send Down-arrow bytes.

### Preconditions and fixtures

Capture unchanged and absent file paths before the session.

### Execution flow

1. Expect tiny-terminal readiness, send Enter, and require a running observation with empty stdout and unchanged filesystem snapshot.
2. Resize, send Down-arrow input, and expect the observed new geometry.
3. Expect the trailing stdout marker, wait for exit, and compare every output byte and restoration sequence.

### Expected results

Stdout is exactly 131072 zero bytes followed by `trailing-end`.
Termios matches its initial value, raw terminal output restores cursor visibility and the original screen, and the baseline remains unchanged.

### Notes

This generic fixture verifies SDK transport and observation capabilities, not application-specific list rendering.

## `terminal_cancel_and_interrupt_restore_termios_screen_and_cursor`

### Scope

Verify restoration after an Esc cancellation and after an external process-group SIGINT.

### Commands under test

A raw-mode shell saves termios, enters the alternate screen, hides the cursor, and restores all three observable states before leaving.

### Arguments and options

One session reads an Esc byte, and a second installs an INT trap that restores state and re-raises SIGINT.

### Preconditions and fixtures

Install the matching driver and snapshot one unchanged file plus one absent path.

### Execution flow

1. Expect readiness before sending Esc or SIGINT through their distinct public operations.
2. Wait for exit and compare exact raw restoration bytes and normalized termios.
3. Check the same filesystem snapshot after each session.

### Expected results

Esc cancellation returns exit `0`, external SIGINT returns signal `2`, stdout stays empty, and both sessions restore cursor visibility, screen, termios, and selected filesystem state.

### Notes

Restoration is implemented by this consumer fixture and observed by the SDK, not automatically injected by the library.

## `terminal_pipe_input_unread_bytes_and_signal_are_observable`

### Scope

Verify finite pipe stdin, unread input, and an explicit process-group signal independently of literal terminal input.

### Commands under test

One shell prints readiness and exits without reading stdin.
A `cat` session consumes finite input through EOF, and a final shell prints readiness and replaces itself with `sleep infinity`.

### Arguments and options

The first session receives pipe input `[0, 255, 4]`.
The second uses terminal stdin and receives `TerminalSignal::Terminate`.

### Preconditions and fixtures

Install the matching driver in a fresh root-image environment.

### Execution flow

1. Expect the first child's readiness, reject terminal `send`, and wait for exit.
2. Check that all supplied pipe bytes remain unread.
3. Supply `[0, 255, 10]` to `cat`, require exact stdout, empty terminal output, and no unread bytes after EOF.
4. Start the final child, expect readiness, send SIGTERM, and wait.

### Expected results

The first observation has exit `7`, empty stdout, and exact unread bytes `[0, 255, 4]`.
The final session reports `Signaled { signal: 15 }`.

### Notes

A signal operation is not a Ctrl-C byte alias.

## `terminal_callback_failure_closes_process_and_socket`

### Scope

Verify that a callback error does not leave the terminal process or control socket alive.

### Commands under test

A shell records its PID, prints readiness, and replaces itself with `sleep infinity`.
`kill -0` probes the exact recorded PID, and `find` checks this fresh container's private terminal directories.

### Arguments and options

Use default terminal options and `/workspace/terminal-pid` for the process identity.

### Preconditions and fixtures

Install the matching driver in a fresh environment.

### Execution flow

1. Expect readiness and return an intentional typed assertion error from the session callback.
2. Require the original callback-error variant.
3. Probe the child PID and scan `/tmp` for terminal socket directories.

### Expected results

The original error is preserved, the child PID no longer exists, and no owned terminal directory remains.

### Notes

The ordinary environment remains usable after the terminal scope fails.

## `terminal_future_drop_closes_process_and_escaped_handle`

### Scope

Verify Tokio Drop fallback without dropping the surrounding environment.

### Commands under test

The child fixture, PID probe, and directory scan match the preceding cleanup case.

### Arguments and options

Use a readiness expectation and a oneshot channel to return an escaped session handle while the callback remains pending.

### Preconditions and fixtures

Install the matching driver and keep the Tokio runtime alive while dropping the terminal future.

### Execution flow

1. Poll the terminal scope until readiness and receipt of the escaped handle.
2. Drop the pending scope and require immediate cancellation from the escaped handle.
3. Poll the exact child PID for at most five seconds, then inspect terminal directories.

### Expected results

The asynchronous fallback closes the child process and socket directory within the observation budget.

### Notes

Readiness is established by bytes, not a fixed sleep.

## `terminal_panic_is_resumed_after_process_cleanup`

### Scope

Verify that a callback panic is resumed only after explicit terminal cleanup.

### Commands under test

Use the same PID-recording `sleep infinity` fixture, PID probe, and terminal-directory scan.

### Arguments and options

The callback panics with a concrete `123_u32` payload after readiness.

### Preconditions and fixtures

Install the matching driver in a fresh environment.

### Execution flow

1. Catch unwinding around the terminal scope.
2. Verify the original panic payload.
3. Check that the child PID and terminal directory are absent.

### Expected results

The payload remains `123_u32`, and cleanup completes before the panic reaches its caller.

### Notes

This test checks a session panic without destroying the surrounding environment.

## `nonroot_driver_installation_snapshot_and_terminal`

### Scope

Verify that a nonroot image user can execute a driver uploaded by Docker.

### Commands under test

`id -u` observes the image user, the driver captures one absent path, and `printf nonroot` runs in a terminal.

### Arguments and options

Use `e2e-example-nonroot:local`, `/tmp/caseyard-nonroot-missing`, and default terminal options.

### Preconditions and fixtures

Build the explicit nonroot image and matching static driver artifacts before this ignored test.

### Execution flow

1. Install the helper through the same public API used by root-image tests.
2. Require a nonzero user ID and capture the absent path.
3. Create a readable file and accessible directory, snapshot them, then remove read/search permissions with mode `000`.
4. Require explicit driver errors from `snapshot` for the unreadable file and inaccessible child, and from `check_snapshot` against the readable baseline.
5. Run the terminal child and wait for exit.

### Expected results

The initial snapshot reports `Missing`, permission failures never become missing or unchanged results, stdout is exactly `nonroot`, and the application exits `0`.

### Notes

Driver uploads use executable archive mode without a chmod operation on a potentially root-owned file.

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
