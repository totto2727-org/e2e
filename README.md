# Caseyard

Small libraries for CLI end-to-end tests in disposable Linux containers.
Use your language's standard test framework to define cases and call a scoped environment helper inside each test.
The libraries supplement, rather than replace, each language and framework's best practices for test structure and naming.
Each case receives a fresh container, command execution, and file assertions.

## Choose an implementation

The shared API is implemented in all four languages.
Names and value types follow each language's conventions.

| Language   | Usage and API                                                                                     | Test framework | Command and file operations | Scoped cleanup |
| ---------- | ------------------------------------------------------------------------------------------------- | -------------- | --------------------------- | -------------- |
| Go         | [Usage](./go/README.md#usage) and [API](./go/README.md#api)                                       | `testing`      | Implemented                 | Implemented    |
| Haskell    | [Usage](./haskell/README.md#use-the-library) and [API](./haskell/README.md#values-and-operations) | Hspec          | Implemented                 | Implemented    |
| TypeScript | [Usage](./typescript/README.md#usage) and [API](./typescript/README.md#values-and-operations)     | Vitest         | Implemented                 | Implemented    |
| Rust       | [Usage and API](./rust/README.md#public-api)                                                      | Tokio tests    | Implemented                 | Implemented    |

## Prerequisites and setup

- Access to a Linux-container Docker Engine.
- A local application image that the caller builds before running tests.
- The language runtime and dependencies listed in the selected implementation's README.

The image must remain running long enough to receive commands; `sleep infinity` is one suitable entrypoint for a CLI fixture.
Path assertions require `test` with `-e`, `-L`, `-f`, and `-d`, and `readlink` with `--` support in the image.
File transfers require an existing parent directory in the container.

Use the selected implementation's setup instructions to add the library to your project.
The implementations can be consumed separately; JavaScript tools are not required to use the Go, Haskell, or Rust libraries.
No package publication or release is implied by the implementation table.

### Static driver for snapshots and terminals

Snapshot and terminal APIs require a caller-supplied Caseyard driver executable.
Build it once for the Linux architecture of the application image, then read the binary into bytes and install it through the language's public driver API inside each environment scope.
The SDK transfers the executable after container startup and gives it a private path and executable mode.
Neither Python nor Go is required inside the application image.
The language packages do not embed binaries or download executable code.

With Go installed, build from a Caseyard checkout:

```sh
cd go
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='-s -w' -o target/driver-linux-arm64 ./cmd/caseyard-driver
```

Use `GOARCH=amd64` and the corresponding output name for an x86-64 container.
Select the container architecture, which can differ from the host or Engine architecture when emulation is used.
Match the driver source revision to the SDK revision.
Only execute a driver built from trusted source.
A wrong architecture or incompatible driver is an error, not an automatic download or interpreter fallback.
The image must provide `mktemp` and an executable, writable `/tmp` directory.
The SDK transfers the helper with executable permissions into a private directory, including when the image uses a non-root user.
Terminal sessions also require Linux PTYs through `/dev/ptmx` and `/dev/pts`.
See the language guides for installation calls and complete examples.

## Purpose and boundaries

Exercise the real executable through argv commands and observable output or filesystem state.
Keep multi-command application workflows inside the test.
The libraries do not build or remove the caller's application image, mount the host workspace, or manage application-specific state.
Build the application image before testing.
Image pull policies and backend infrastructure images, such as Testcontainers' resource reaper, are documented in each language README.
Commands are argv entries, not shell strings.
Shell syntax requires an explicit shell command.
There is no generic application-readiness check.
Optional terminal sessions provide real PTY interaction through the caller-supplied static driver.

## Shared values

- **Test scope**: a lifetime owned by one ordinary test body. The framework controls naming, discovery, selection, assertions, scheduling, and reporting.
- **Command**: nonempty argv with a nonempty executable, an optional working directory, and optional environment entries. Argument boundaries, empty argument values, and environment values are preserved. NUL in argv, working directory, or environment entries is rejected before execution.
- **Result**: exit code, stdout, and stderr. Output streams remain separate. Ordering between streams is not defined.
- **Environment**: command and file operations scoped to one case container.

Languages may expose command output as text or bytes; their READMEs define non-UTF-8 handling.
File APIs always transfer exact bytes.
A nonzero application exit code is a command result, not an infrastructure error.
Daemon failures, attachment failures, decode errors, cancellation, and deadline expiry are errors.
An exit code is valid only when execution succeeds without an infrastructure error.
Captured output on failure depends on what the backend exposes. Each language README defines its partial-output and decoding limits.
Cancelling or abandoning the whole test scope may instead propagate framework cancellation without delivering a command outcome.
Each language README explains that boundary and its error representation.

## Shared operations

These names identify behaviors, not mandatory spellings in every language.

| Operation           | Contract                                                                                                                            |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| With environment    | Resolve a local prebuilt image and provide a fresh container inside an ordinary test. Attempt owned-resource cleanup at scope exit. |
| Run command         | Execute a `Command` and return a separate-stream `Result`.                                                                          |
| Check command       | Compare the exit code, stdout, and stderr exactly with an expected `Result`. An empty expected stream asserts absence.              |
| Write file          | Copy exact bytes to a container path with mode `0644`. The parent directory must exist.                                             |
| Read file           | Return exact bytes from a container file.                                                                                           |
| Check file          | Read a file and compare exact bytes.                                                                                                |
| Check missing       | Reject any existing path, including a dangling symbolic link.                                                                       |
| Check regular file  | Require a physical regular file, not a symbolic link to a file.                                                                     |
| Check directory     | Require a physical directory, not a symbolic link to a directory.                                                                   |
| Check symbolic link | Compare the literal target without resolving or normalizing it. A dangling target is valid.                                         |

Path-probe exit codes other than `0` or `1` are infrastructure errors, not successful absence checks.

### File-state snapshots

Capture an explicit set of container paths before an operation, then compare those paths with the saved state.
Snapshots distinguish missing paths, physical regular files, physical directories, and symbolic links, including dangling links.
File contents and literal link targets are compared as exact bytes.
JSON whitespace and different literal targets that resolve to the same file still count as changes.
Differences identify each path and its existence, kind, content, or target change in deterministic order.

A directory entry records its kind, not its descendants.
Snapshots do not include permissions, ownership, timestamps, or inode numbers.
Add each descendant path explicitly when its state matters.
Acquisition is not atomic across paths or concurrent application writes.
Synchronize the application operation before comparison.
Read failures and unsupported path kinds are errors, never successful missing-path or unchanged-state observations.
Capture and comparison do not update expected values or application content.

### Terminal sessions

Start a scoped terminal session with explicit argv, working directory, environment, and terminal dimensions.
Stdin and stderr use a real PTY, while stdout remains a separate byte stream.
An alternative stdin mode supplies finite pipe bytes and EOF while stderr still uses the terminal.
The result exposes unread pipe input after child exit.

Wait for literal output bytes before sending input.
Matches span read boundaries and consume output through the matched bytes, so repeated prompts cannot reuse an earlier match.
Each channel has a separate match position, and ordering between channels is not defined.
Send raw bytes, resize the terminal with `SIGWINCH`, or send an external process-group signal.
Ctrl-C and Ctrl-D input are bytes, not aliases for an external signal or transport closure.

Observations preserve raw stdout and terminal bytes, process status, and normalized termios state.
Termios comparison excludes kernel bookkeeping and treats inherited and explicit equal standard input/output baud rates as equivalent.
Different baud-rate encodings and terminal mode changes remain part of the comparison.
Normal exit and signal termination are distinct results.
Screen and cursor restoration assertions remain in the consumer test, using the raw control sequences.
File checks and snapshots can run between terminal operations while the child is still running.
Use sequential session operations and bounded waits rather than fixed sleeps as readiness checks.

Each terminal has an additional watchdog of at most one minute, with a default of fifty seconds.
It does not extend the environment's operation budget.
Leaving a terminal scope attempts to kill its process group, reap the child, and remove its descriptors and control socket.
The container remains the final cleanup boundary on environment cancellation or transport failure.
If the image's PID 1 does not reap adopted processes, an exited session server can remain as a zombie until container teardown.
Language guides describe their scope-cancellation and partial-output limits.

## Lifecycle, deadlines, and cancellation

The helper starts a one-minute operation budget before inspecting the local image.
An absent image causes an error before container startup.
Each invocation starts a fresh case container from the resolved image with the image's entrypoint.
All case operations share that budget.
The library does not queue tests or impose a suite-wide concurrency limit.

On completion or failure, the helper attempts to remove its case container and preserves the caller-owned image.
Resource reapers, cancellation recovery, and cleanup timing follow the backend and language-specific scope implementation.
Cleanup errors are reported according to the language's error model.
The operation budget bounds library operations, not arbitrary caller code.
See each language README for cancellation behavior and limits on startup or cleanup requests.

Closing an exec connection does not prove that the remote process stopped.
Container cleanup is the final process boundary.
Cleanup cannot guarantee recovery if the daemon commits a creation only after both the failed request and cleanup lookup have completed.
Daemon outages and unresolved creation outcomes can therefore prevent cleanup.

## Docker connections

The baseline is a local Linux-container Engine over a Unix socket.
Connection support differs by implementation.

| Language   | Connection support                                                                                                                                 |
| ---------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go         | Testcontainers configuration, including configured TLS, with negotiated or explicit API versions. Initial TLS dialing follows the upstream dialer. |
| Haskell    | Unix sockets and HTTP/TCP. Unversioned Engine endpoints; TLS and explicit API-version overrides are rejected.                                      |
| TypeScript | Testcontainers Node.js connection discovery and configuration. See its language guide for supported endpoints and cancellation limits.             |
| Rust       | Testcontainers Rust connection configuration and API negotiation. See its language guide for supported endpoints and cleanup limits.               |

Use the language README for endpoint configuration and transport limits.
Support in one implementation does not imply support in another.

## Development

For repository setup, toolchains, architecture constraints, and validation commands, read [AGENTS.md](./AGENTS.md).

## License

[MIT](./LICENSE).
