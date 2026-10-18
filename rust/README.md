# Caseyard for Rust

For library users who know Tokio tests.
See [shared prerequisites](../README.md#prerequisites-and-setup), [values](../README.md#shared-values), and [operations](../README.md#shared-operations).
Containers and command transport use [Testcontainers 0.28](https://docs.rs/testcontainers/0.28.0/testcontainers/).

## Setup

Requirements: Rust 1.88 or newer and a native C toolchain for Testcontainers' default `ring` TLS provider.
No OpenSSL or pkg-config dependency is required.
The Linux image needs `sh` when `Command.working_dir` is set.

Consumer `Cargo.toml`:

```toml
[dev-dependencies]
caseyard = "0.1"
tokio = { version = "1", features = ["rt-multi-thread", "macros"] }
```

## Public API

```rust,no_run
use caseyard::{with_environment, Command, CommandResult, Error, ScopeError};

#[tokio::test]
#[ignore = "requires caller-built e2e-example:local and Docker"]
async fn greeting() -> Result<(), ScopeError<Error>> {
    with_environment("e2e-example:local", |env| async move {
        let result = env.run(Command::new(["echo", "hello"])).await?;
        assert_eq!(result, CommandResult {
            exit_code: 0,
            stdout: b"hello\n".to_vec(),
            stderr: Vec::new(),
        });
        Ok(())
    }).await
}
```

[Workflow example](../.agents/skills/cli-e2e/references/example/rust/multiple_scenarios.rs) · [Expected behavior](../.agents/skills/cli-e2e/references/example/rust/multiple_scenarios.md)

```rust,ignore
pub async fn with_environment<F, Fut, T, E>(
    image: &str,
    callback: F,
) -> Result<T, ScopeError<E>>
where
    F: FnOnce(Environment) -> Fut,
    Fut: Future<Output = Result<T, E>>;
```

Callbacks can borrow fixtures and use non-`Send` values.
No future boxing or library-specific callback error conversion is required.
Testcontainers' file-copy future is not `Send`, so a scope that reads files cannot be passed to `tokio::spawn`.
Await it directly in the native Tokio test.

| Public value               | Fields or constructor                                                  |
| -------------------------- | ---------------------------------------------------------------------- |
| `Command`                  | `args: Vec<String>`, `working_dir: Option<String>`, `env: Vec<String>` |
| `Command::new(args)`       | An iterator of strings. Other fields use their defaults.               |
| `Command::validate(&self)` | `Result<(), Error>`                                                    |
| `CommandResult`            | `exit_code: i64`, `stdout: Vec<u8>`, `stderr: Vec<u8>`                 |
| `PartialOutput`            | `stdout: Vec<u8>`, `stderr: Vec<u8>`. No exit code.                    |

Use native `assert_eq!` for command results, byte vectors, path kinds, and snapshots.
The observation types implement `Debug` and `PartialEq`; no snapshot dependency is required.

The following methods take `&self` on `Environment`.
Except for `container_id`, all methods are async.

| Method                                 | Return type                                                       |
| -------------------------------------- | ----------------------------------------------------------------- |
| `run(Command)`                         | `Result<CommandResult, Error>`                                    |
| `write_file(path: &str, bytes: &[u8])` | `Result<(), Error>`                                               |
| `read_file(path: &str)`                | `Result<Vec<u8>, Error>`                                          |
| `path_kind(path: &str)`                | `Result<PathKind, Error>`                                         |
| `read_symlink(path: &str)`             | `Result<Vec<u8>, Error>`. Literal target bytes.                   |
| `container_id()`                       | `&str`, immutable identity only. No mutable Docker client access. |

`PathKind` is `Missing`, `RegularFile`, `Directory`, `Symlink`, or `Other`.
`path_kind` and `read_symlink` use the image's `test` and `readlink` commands, without a driver.

| Language-specific behavior | Constraint                                                                                                                                                                                                      |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Additional invalid input   | Empty image, empty path, NUL input, or an environment entry without a nonempty key and `=` separator.                                                                                                           |
| Output                     | Separate byte vectors. No trimming or UTF-8 conversion. Application nonzero exits remain results.                                                                                                               |
| Working directory          | A fixed `sh` script changes directory, then executes argv. Caller paths and arguments are positional parameters, not script interpolation. A failed directory change returns the shell's status and diagnostic. |
| `read_file`                | Native `ContainerAsync::copy_file_from` reads the first regular file, skips additional archive entries, and rejects directories. Symbolic-link behavior follows Docker.                                         |
| `write_file`               | Testcontainers has no public running-container upload method. The helper uses its configured, re-exported Bollard client and a single-file tar archive with mode `0644`.                                        |

### Static driver installation

See [driver setup](../README.md#static-driver-for-snapshots-and-terminals) for trusted binary and image requirements.
The existing command and file APIs do not require a driver.

| API or constraint                        | Rust behavior                                                                    |
| ---------------------------------------- | -------------------------------------------------------------------------------- |
| `env.install_driver(bytes: &[u8]).await` | `Result<(), Error>`. Install once per environment before snapshots or terminals. |
| Cloned handles                           | Share the installation. Empty bytes and a second installation are rejected.      |

### Filesystem snapshots

See the [shared snapshot contract](../README.md#file-state-snapshots) and [Docker scenarios](tests/e2e.md#filesystem_snapshots) ([source](tests/e2e.rs)).

| API or value                | Type or behavior                                                                                   |
| --------------------------- | -------------------------------------------------------------------------------------------------- |
| `env.snapshot(paths).await` | `paths: impl IntoIterator<Item = impl Into<String>>`. Returns `Result<FilesystemSnapshot, Error>`. |
| `FilesystemSnapshot`        | `entries: BTreeMap<String, PathState>`                                                             |
| `PathState`                 | `Missing`, `RegularFile { content: Vec<u8> }`, `Directory`, or `Symlink { target: Vec<u8> }`       |
| Unselected path             | `None`, distinct from `Some(PathState::Missing)`.                                                  |

```rust,ignore
let after = env.snapshot(baseline.entries.keys().cloned()).await?;
assert_eq!(after, baseline);
```

### Scoped terminal sessions

See the [shared terminal contract](../README.md#terminal-sessions) and [Docker scenarios](tests/e2e.md#terminal_raw_bytes_expect_resize_and_restoration) ([source](tests/e2e.rs)).

| API or value                                          | Type or behavior                                                                                                                                                     |
| ----------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `env.with_terminal(command, options, callback).await` | `Result<T, ScopeError<E>>`. Preserves the callback's concrete error type.                                                                                            |
| `TerminalOptions`                                     | `rows: u16`, `columns: u16`, `input: Option<Vec<u8>>` (`None`: terminal, `Some`: pipe), `timeout: Duration`. Use `TerminalOptions::default()` for shared defaults.   |
| `TerminalSession` methods                             | All are async and return `Result<TerminalObservation, Error>`.                                                                                                       |
| `observe()`                                           | Read the current observation.                                                                                                                                        |
| `expect(channel, bytes, timeout)`                     | `TerminalChannel`, `&[u8]`, `Duration` arguments.                                                                                                                    |
| `send(bytes)`                                         | `&[u8]` input.                                                                                                                                                       |
| `resize(rows, columns)`                               | `u16` dimensions.                                                                                                                                                    |
| `signal(signal)`                                      | `TerminalSignal` argument.                                                                                                                                           |
| `wait(timeout)`                                       | `Duration` argument.                                                                                                                                                 |
| `TerminalChannel`                                     | `Stdout` or `Terminal`                                                                                                                                               |
| `TerminalSignal`                                      | `Interrupt`, `Terminate`, `Hangup`, `Quit`, `Kill`, `User1`, or `User2`                                                                                              |
| `TerminalObservation`                                 | `stdout, terminal, unread_input: Vec<u8>`, `status: TerminalStatus`, `termios_restored: bool`, `initial_termios, current_termios: TerminalSettings`                  |
| `TerminalStatus`                                      | `Running`, `Exited { code: i64 }`, or `Signaled { signal: u32 }`. Application exits are observations, not helper failures.                                           |
| `TerminalSettings`                                    | Tuple of six `u64` values (input, output, control, local flags, input speed, output speed) and control-character `Vec<u8>`.                                          |
| Scope exit                                            | Success, callback failure, or panic closes the session. Escaped handles return cancellation errors. Cancellation stops in-flight or queued requests before close.    |
| Terminal-close budget                                 | A fresh total 30 seconds, including session-lock acquisition and the helper request. Distinct from [container cleanup timing](#lifecycle-and-cancellation).          |
| Scope future dropped                                  | Marks handles closed and schedules best-effort close through Tokio's blocking pool because Testcontainers exec futures are not `Send`. Keep the Tokio runtime alive. |
| Incomplete close or lost start response               | The helper watchdog and container teardown remain the final process boundaries.                                                                                      |

### Errors

| Error or condition                       | Behavior                                                                                                                                                          |
| ---------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Error::Execution { failure, output }`   | Retains bytes already drained by the helper. No valid exit status. Stdout is drained before stderr, so unread stderr is not guaranteed on failure.                |
| `ExecutionFailure`                       | `Deadline`, `Cancelled`, boxed native `Testcontainers` error, `Io`, or `Protocol`.                                                                                |
| Other infrastructure errors              | `Error::Testcontainers`, `Docker` for image inspection or upload, and `Archive` for local tar creation. Native causes remain available through `source()`.        |
| `Error::Driver { message, observation }` | Retains helper diagnostics and an optional boxed `TerminalFailureObservation`, either complete typed observations or raw partial channels at a watchdog boundary. |
| `ScopeError<E>`                          | `Infrastructure(Error)`, `Callback(E)`, or `Cleanup { prior: Option<Box<ScopeError<E>>>, errors: Vec<Error> }`.                                                   |
| Explicit cleanup failure                 | Retains the prior typed callback error. Testcontainers' Drop fallback logs its own failures instead of returning them.                                            |
| Callback panic                           | Resumes the original payload after explicit cleanup. An accompanying cleanup failure is reported to stderr.                                                       |

## Lifecycle and cancellation

See the shared [lifecycle, deadlines, and cancellation](../README.md#lifecycle-deadlines-and-cancellation) contract, with these Rust-specific limits.

| Rust-specific behavior           | Constraint                                                                                                                                                                                                    |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Operation deadline               | One 60-second budget starts before client setup and local image inspection. It does not preempt arbitrary callback code.                                                                                      |
| Image selection                  | Inspect the caller's local image, then start its immutable ID through `GenericImage` and `AsyncRunner`. No image lease is created or image removal requested.                                                 |
| Pull behavior                    | Testcontainers has no `Never` pull policy. A create-time 404 can trigger its pull fallback even after successful local inspection.                                                                            |
| Callback completion or panic     | Cancel escaped environment handles, then call `ContainerAsync::rm`. Handles cannot extend container ownership.                                                                                                |
| Cleanup timing                   | Wait at most 30 seconds for active operations to release scope access. Removal and Drop use Testcontainers' own request timeouts, not a strict 30-second total budget.                                        |
| Scope future dropped             | Cancel environment operations and use Testcontainers' native Drop cleanup. No custom supervisor or cleanup retry protocol runs. Keep the runtime alive while dropping the scope.                              |
| Exec timeout or cancellation     | Drop native output readers. Testcontainers owns its background stream task, which can remain until the command emits more output or the container is removed. Immediate connection closure is not guaranteed. |
| Startup failure or lost response | Recovery follows Testcontainers. No stronger guarantee is made for a lost creation response or a failure before a container guard exists.                                                                     |
| Process signals                  | Testcontainers' `watchdog` feature is enabled for its SIGINT, SIGTERM, and SIGQUIT recovery. This is not a guarantee for abrupt runtime termination or SIGKILL.                                               |

> [!IMPORTANT]
> Leave `TESTCONTAINERS_COMMAND` unset for automatic Drop cleanup.
> Testcontainers' `keep` setting disables that fallback.
> Blocking callback code and detached tasks remain caller-owned and can delay cleanup.

## Docker endpoint support

Endpoint discovery, TLS, and connection configuration use [Testcontainers' Docker host resolution](https://docs.rs/testcontainers/0.28.0/testcontainers/#docker-host-resolution).
The same public `docker_client_instance` factory supplies image inspection and upload.
This package does not maintain a separate endpoint resolver or API-version negotiation layer.

Testcontainers' default features support Unix sockets, HTTP/TCP, and HTTPS/TLS.
Its optional properties-file configuration is not enabled by this package.
Docker contexts, SSH endpoints, and Windows containers are not part of this library's public contract.
Interactive terminal stdin is available only through the optional installed-driver terminal API, not ordinary `run`.

## Development

See [Rust maintenance](../AGENTS.md#rust).
