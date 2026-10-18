# Caseyard for Rust

For library users who know Tokio tests.
See [shared prerequisites](../README.md#prerequisites-and-setup), [values](../README.md#shared-values), and [operations](../README.md#shared-operations).
Containers and command transport use [Testcontainers 0.28](https://docs.rs/testcontainers/0.28.0/testcontainers/).

## Setup

Requirements: Rust 1.88 or newer and a native C toolchain for Testcontainers' default `ring` TLS provider.
No OpenSSL or pkg-config dependency is required.
The Linux image needs `sh` when `Command.working_dir` is set.

> [!IMPORTANT]
> The crates.io setup below requires publication of a matching package.
> Registry availability is not verified.

Consumer `Cargo.toml`, after publication:

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
        assert_eq!(result.exit_code, 0);
        assert_eq!(result.stdout, b"hello\n");
        assert_eq!(result.stderr, b"");
        env.check_command(Command::new(["true"]), CommandResult::default()).await
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

The following methods take `&self` on `Environment`.
Except for `container_id`, all methods are async.

| Method                                            | Return type                                                       |
| ------------------------------------------------- | ----------------------------------------------------------------- |
| `run(Command)`                                    | `Result<CommandResult, Error>`                                    |
| `check_command(Command, CommandResult)`           | `Result<(), Error>`                                               |
| `write_file(path: &str, bytes: &[u8])`            | `Result<(), Error>`                                               |
| `read_file(path: &str)`                           | `Result<Vec<u8>, Error>`                                          |
| `check_file(path: &str, expected: &[u8])`         | `Result<(), Error>`                                               |
| `check_missing(path: &str)`                       | `Result<(), Error>`                                               |
| `check_regular_file(path: &str)`                  | `Result<(), Error>`                                               |
| `check_directory(path: &str)`                     | `Result<(), Error>`                                               |
| `check_symlink(path: &str, literal_target: &str)` | `Result<(), Error>`                                               |
| `container_id()`                                  | `&str`, immutable identity only. No mutable Docker client access. |

| Language-specific behavior | Constraint                                                                                                                                                                                                      |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Additional invalid input   | Empty image, empty path, NUL input, or an environment entry without a nonempty key and `=` separator.                                                                                                           |
| Output                     | Separate byte vectors. No trimming or UTF-8 conversion. Application nonzero exits remain results.                                                                                                               |
| Working directory          | A fixed `sh` script changes directory, then executes argv. Caller paths and arguments are positional parameters, not script interpolation. A failed directory change returns the shell's status and diagnostic. |
| `read_file`                | Native `ContainerAsync::copy_file_from` reads the first regular file, skips additional archive entries, and rejects directories. Symbolic-link behavior follows Docker.                                         |
| `write_file`               | Testcontainers has no public running-container upload method. The helper uses its configured, re-exported Bollard client and a single-file tar archive with mode `0644`.                                        |

### Static driver installation

Snapshots and terminal sessions require a trusted caller-built static Linux driver.
See [driver setup](../README.md#static-driver-for-snapshots-and-terminals) for the supported build procedure and architecture requirements.
Call `env.install_driver(&bytes).await?` once inside each environment scope, before either feature.
The library uploads it with executable mode `0755` inside a private mode-`0700` directory.
The executable mode also supports nonroot image users when Docker assigns uploaded files to root.
Installation is shared by cloned environment handles and rejects empty bytes or a second installation.
The helper is removed with the container.
Existing command and file APIs do not require installation.
The crate neither downloads nor embeds a binary, invokes Go, nor requires Python in the image.
The image must support execution from `/tmp` and provide `mktemp` for installation.

### Filesystem snapshots

Install the driver before filesystem snapshots.
`env.snapshot(paths).await?` captures only the explicit paths and returns a `FilesystemSnapshot` with a sorted `BTreeMap<String, PathState>` named `entries`.
Duplicate paths are captured once.
`PathState` is `Missing`, `RegularFile { content: Vec<u8> }`, `Directory`, or `Symlink { target: Vec<u8> }`.
File bytes and literal link targets are exact, including dangling links.
Permission errors, read errors, unsupported kinds such as FIFOs, and malformed transport responses fail capture instead of recording absence.

```rust,ignore
let baseline = env.snapshot(["/workspace/config", "/workspace/output"]).await?;
// Run application commands here.
env.check_snapshot(&baseline).await?;
let after = env.snapshot(baseline.entries.keys().cloned()).await?;
let differences = baseline.differences(&after);
```

`check_snapshot` captures the baseline paths again and returns an assertion error for any change.
`differences` is a pure comparison over the union of selected paths.
It returns sorted `PathDifference` values with `path`, `changes`, `before`, and `after` fields.
`SnapshotChange` identifies `Existence`, `Kind`, `Content`, or `Target` changes.
Snapshots do not recurse, compare metadata, write files, or update baselines automatically.
Capture is sequential, not an atomic filesystem transaction, so callers must stop concurrent filesystem changes when they need a stable baseline.

### Scoped terminal sessions

`env.with_terminal(command, options, callback).await` returns `Result<T, ScopeError<E>>` and preserves the callback's concrete error type.
The command's working directory and environment entries apply to the child.
The child has a controlling Linux terminal with stdin and stderr attached, while stdout is a separate pipe.
`TerminalOptions::default()` selects 40 rows, 120 columns, terminal stdin, and a 50-second watchdog.
Set `input: Some(bytes)` to supply finite pipe stdin plus EOF instead, with stderr still on the terminal.
Dimensions must be nonzero `u16` values, and timeouts must be greater than zero and at most 60 seconds.
Neither timeout extends the environment's existing budget.

```rust,ignore
use caseyard::{TerminalChannel, TerminalOptions, TerminalStatus};
use std::time::Duration;

let driver = std::fs::read("caseyard-driver-linux-arm64")?;
env.install_driver(&driver).await?;
let observed = env.with_terminal(
    Command::new(["sh", "-c", "printf 'name? ' >&2; read name; printf '%s' \"$name\""]),
    TerminalOptions::default(),
    |terminal| async move {
        terminal.expect(TerminalChannel::Terminal, b"name? ", Duration::from_secs(5)).await?;
        terminal.send(b"Caseyard\n").await?;
        terminal.wait(Duration::from_secs(5)).await
    },
).await?;
assert_eq!(observed.status, TerminalStatus::Exited { code: 0 });
assert_eq!(observed.stdout, b"Caseyard");
```

| `TerminalSession` method          | Behavior                                                                                                                          |
| --------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| `observe()`                       | Return cumulative raw bytes and current process observations without waiting for exit.                                            |
| `expect(channel, bytes, timeout)` | Match nonempty exact bytes across reads, consume through the match, and retain trailing bytes for the next match on that channel. |
| `send(bytes)`                     | Send literal bytes to the terminal. Reject pipe stdin. Ctrl-C and Ctrl-D remain literal input.                                    |
| `resize(rows, columns)`           | Update terminal dimensions and send SIGWINCH to the application process group.                                                    |
| `signal(TerminalSignal)`          | Send an explicit named process-group signal, independently of terminal input.                                                     |
| `wait(timeout)`                   | Wait for exit, drain output, and return an observation even for nonzero application exit.                                         |

Every method is async and returns `Result<TerminalObservation, Error>`.
`TerminalObservation` contains cumulative `stdout`, `terminal`, and `unread_input` byte vectors, typed `status`, `termios_restored`, `initial_termios`, and `current_termios`.
`TerminalStatus` is `Running`, `Exited { code }`, or `Signaled { signal }`.
`TerminalSettings` is a tuple of input, output, control, and local flags, input and output speeds, and control-character bytes.
The helper excludes PENDIN bookkeeping from comparison.
Termios restoration during execution is only an observation, not proof of restoration at exit.
ANSI, screen, and cursor restoration assertions remain caller-owned raw-byte checks.
There is no cross-channel ordering guarantee.
Pipe input that the application did not consume is reported in `unread_input` after exit.

The terminal is closed after success, callback failure, or panic, and escaped session handles then return cancellation errors.
Each terminal close has a fresh total 30-second budget that includes session-lock acquisition and the helper request.
Session cancellation also stops in-flight or queued requests before close.
Dropping a terminal-scope future marks its handles closed and schedules best-effort close through Tokio's blocking pool because Testcontainers' exec futures are not `Send`.
Keep the Tokio runtime alive for this fallback.
The helper watchdog and environment container teardown remain the final process boundaries if the close request cannot complete, including a lost start response.
Ordinary environment assertions can run between terminal requests in the callback.

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
