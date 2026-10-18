# Caseyard

Libraries for CLI end-to-end tests in disposable Linux containers.
Define tests with your language's standard framework.
Use Caseyard for container, command, and file operations.

## Choose an implementation

| Language   | Usage and API                                                                                   | Test framework |
| ---------- | ----------------------------------------------------------------------------------------------- | -------------- |
| Go         | [Usage](./go/README.md#usage) · [API](./go/README.md#api)                                       | `testing`      |
| Haskell    | [Usage](./haskell/README.md#use-the-library) · [API](./haskell/README.md#values-and-operations) | Hspec          |
| TypeScript | [Usage](./typescript/README.md#usage) · [API](./typescript/README.md#values-and-operations)     | Vitest         |
| Rust       | [Usage and API](./rust/README.md#public-api)                                                    | Tokio tests    |

## Prerequisites and setup

- Access to a Linux-container Docker Engine.
- A local application image, built before the tests.
- The runtime and dependencies in your language guide above.

| Image requirement                                                        | When needed          |
| ------------------------------------------------------------------------ | -------------------- |
| An entrypoint that keeps the container running, such as `sleep infinity` | All tests            |
| `test` with `-e`, `-L`, `-f`, and `-d`                                   | Path kinds           |
| `readlink` with `--` support                                             | Literal link targets |
| An existing parent directory                                             | File transfers       |

Use the setup instructions in your language guide.
The Go, Haskell, and Rust libraries do not require JavaScript tools.

### Static driver for snapshots and terminals

Snapshots and terminals also require a caller-supplied static Linux driver.
Follow [driver setup](./docs/driver.md) to build it for the container architecture and install it inside each environment scope.
Command and file APIs do not require the driver.

## Purpose and boundaries

Keep application workflows and assertions in your tests.

| Caseyard provides                                  | Caller or test framework provides                           |
| -------------------------------------------------- | ----------------------------------------------------------- |
| A fresh container from a local image               | Image builds, test discovery, scheduling, and reporting     |
| Argv execution and observable output or file state | An explicit shell when shell syntax is needed               |
| Scoped resources and cleanup attempts              | Application readiness checks and application-specific state |

Caseyard does not mount the host workspace or remove the caller's image.
Backend image-pull behavior is defined in each language guide.

## Shared values

| Value                                 | Contract                                                                                                  |
| ------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| Test scope                            | One ordinary test body owns the environment lifetime.                                                     |
| Command                               | Nonempty argv and executable, with optional working directory and environment entries. No implicit shell. |
| Arguments, directory, and environment | Preserve argument boundaries, empty argument values, and environment values. Reject NUL before execution. |
| Result                                | Exit code and separate stdout/stderr. Order is preserved within each stream, not between streams.         |
| Environment                           | Command and file operations for one case container.                                                       |

File APIs preserve exact bytes.
Language guides define command-output decoding and partial-output limits.
A nonzero application exit is a result.
Infrastructure failure, cancellation, or deadline expiry is an error without a valid exit code.
Whole-scope cancellation can propagate a framework exception instead of returning an outcome.

## Shared operations

The names below identify behavior.
Language-specific spellings are in the language guides.

| Operation          | Contract                                                                                                        |
| ------------------ | --------------------------------------------------------------------------------------------------------------- |
| With environment   | Resolve a local image, start a fresh container, and attempt cleanup at scope exit.                              |
| Run command        | Return the exit code and separate output streams.                                                               |
| Write file         | Copy exact bytes with mode `0644`. The parent directory must exist.                                             |
| Read file          | Return exact file bytes.                                                                                        |
| Path kind          | Return missing, physical file, physical directory, symbolic link, or other. Inspect links before their targets. |
| Read symbolic link | Return the literal target without resolution or normalization. A dangling target is valid.                      |

Use the framework's standard equality matcher for command results, file bytes, path kinds, and snapshot values.
Caseyard returns observations and infrastructure errors, not assertion failures.
Path-probe exit codes other than `0` or `1` are errors, not evidence of absence.

### File-state snapshots

For tests that compare selected paths before and after an application operation.
Pass captured values to native equality matchers, or use [Vitest's saved snapshots](https://vitest.dev/guide/snapshot.html) for a version-controlled baseline.
Caseyard does not implement snapshot storage, serializers, or update commands.

| Aspect         | Contract                                                                                                                                             |
| -------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Selected paths | Explicit paths distinguish missing, physical file, physical directory, and symbolic link.                                                            |
| Compared state | Exact file bytes and literal link-target bytes, including dangling links.                                                                            |
| Representation | Values in deterministic path order, suitable for native equality and diff reporting. JSON whitespace and equivalent resolved links can still differ. |
| Exclusions     | No recursion or metadata comparison. Add each descendant path that matters.                                                                          |
| Read failures  | Permission errors, read failures, and unsupported path kinds fail capture, not record absence.                                                       |
| Mutation       | Capture does not change expected values or application content. Framework snapshot updates affect only the test baseline.                            |
| Consistency    | Capture is not atomic across paths or concurrent writes. Synchronize the application before comparison.                                              |

### Terminal sessions

For tests that wait for output before sending input or checking intermediate file state.
Use sequential session operations and bounded waits, not fixed sleeps for readiness.

| Aspect            | Contract                                                                                                                                                                  |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Startup           | Command argv, working directory, and environment apply to the child. Default geometry: 40 rows × 120 columns. Valid range: `1..65535`.                                    |
| Channels          | Stdin and stderr use a PTY. Stdout is a separate byte stream. Terminal output can include echo and line-discipline effects. No cross-channel ordering guarantee.          |
| Pipe input        | Optional finite stdin bytes followed by EOF. Reject interactive send in this mode. Report unread input after exit.                                                        |
| Expect            | Match nonempty literal bytes across reads. Consume through each match with a separate cursor per channel. Retain trailing bytes.                                          |
| Input             | Send raw bytes. Ctrl-C and Ctrl-D are not aliases for an API signal or transport closure.                                                                                 |
| Resize            | Update geometry and send `SIGWINCH` to the application process group.                                                                                                     |
| Signals           | Send `SIGINT`, `SIGTERM`, `SIGHUP`, `SIGQUIT`, `SIGKILL`, `SIGUSR1`, or `SIGUSR2` to that process group.                                                                  |
| Observations      | Cumulative raw bytes, running/exited/signaled status, and normalized termios. File assertions can run while the child is running.                                         |
| Restoration       | Ignore kernel bookkeeping and equivalent standard baud-rate encodings, but retain other mode/rate differences. A running observation is not proof of restoration at exit. |
| Screen and cursor | Assert application-specific restoration against raw ANSI bytes in the consumer test.                                                                                      |
| Timeouts          | Finite, positive, at most 60 seconds. The default 50-second watchdog does not extend the environment budget.                                                              |
| Scope exit        | Attempt to stop the process group, reap the child, and remove session resources. Language guides define cancellation and partial-output limits.                           |

Container teardown is the final process boundary after cancellation or transport failure.
If the image's PID 1 does not reap adopted processes, a stopped session server can remain as a zombie until container teardown.

## Lifecycle, deadlines, and cancellation

| Boundary              | Contract                                                                                                                 |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| Operation budget      | One minute, starting before local image inspection. Shared by all case operations. Does not bound arbitrary caller code. |
| Missing image         | Fail before container startup.                                                                                           |
| Container             | Start a fresh instance from the resolved image with its entrypoint.                                                      |
| Scheduling            | The test framework owns scheduling. Caseyard adds no suite-wide concurrency limit.                                       |
| Completion or failure | Attempt container cleanup and preserve the caller's image. Language guides define cleanup timing and error handling.     |
| Cancellation recovery | Backend-specific. Closing an exec connection does not prove that the remote process stopped.                             |

Daemon outages and unresolved creation outcomes can prevent cleanup.
Recovery cannot find a container created only after both the failed request and the cleanup lookup complete.

## Docker connections

The baseline is a local Linux-container Engine over a Unix socket.

| Language   | Connection support                                                                                                               |
| ---------- | -------------------------------------------------------------------------------------------------------------------------------- |
| Go         | Testcontainers configuration and TLS, with negotiated or explicit API versions. Initial TLS dialing follows the upstream dialer. |
| Haskell    | Unix sockets and HTTP/TCP with unversioned Engine endpoints. Reject TLS and explicit API-version overrides.                      |
| TypeScript | Testcontainers Node.js discovery and configuration. See the language guide for endpoint and cancellation limits.                 |
| Rust       | Testcontainers Rust configuration and API negotiation. See the language guide for endpoint and cleanup limits.                   |

Support in one implementation does not imply support in another.

## Development

See [AGENTS.md](./AGENTS.md) for repository setup, toolchains, and validation.

## License

[MIT](./LICENSE).
