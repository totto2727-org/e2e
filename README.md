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

## Purpose and boundaries

Exercise the real executable through argv commands and observable output or filesystem state.
Keep multi-command application workflows inside the test.
The libraries do not build or remove the caller's application image, mount the host workspace, or manage application-specific state.
Build the application image before testing.
Image pull policies and backend infrastructure images, such as Testcontainers' resource reaper, are documented in each language README.
Commands are argv entries, not shell strings.
Shell syntax requires an explicit shell command.
There is no generic application-readiness check or interactive terminal support.

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

No license has been declared for this repository.
