# Rust CLI E2E library

For library users who know Tokio tests.
See [shared prerequisites](../README.md#prerequisites-and-setup), [values](../README.md#shared-values), and [operations](../README.md#shared-operations).

## Setup

Requirements: Rust 1.88 or newer. No OpenSSL or pkg-config dependency.

> [!IMPORTANT]
> The crates.io setup below requires publication of a matching package.
> Registry availability is not verified.

Consumer `Cargo.toml`, after publication:

```toml
[dev-dependencies]
cli-e2e = "0.1"
tokio = { version = "1", features = ["rt-multi-thread", "macros"] }
```

## Public API

```rust,no_run
use cli_e2e::{with_environment, Command, CommandResult, Error, ScopeError};

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

[Workflow example](../.agents/skills/document-e2e-scenarios/references/example/rust/multiple_scenarios.rs) · [Expected behavior](../.agents/skills/document-e2e-scenarios/references/example/rust/multiple_scenarios.md)

```rust,ignore
pub async fn with_environment<F, Fut, T, E>(
    image: &str,
    callback: F,
) -> Result<T, ScopeError<E>>
where
    F: FnOnce(Environment) -> Fut + Send + 'static,
    Fut: Future<Output = Result<T, E>> + Send + 'static,
    T: Send + 'static,
    E: Send + 'static;
```

Move owned fixtures into the callback.
No future boxing or library-specific callback error conversion is required.

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

| Language-specific behavior | Constraint                                                                                                                  |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| Additional invalid input   | Empty image, empty path, or NUL in image, paths, or literal link targets. Reject before the corresponding Docker operation. |
| Output                     | Separate byte vectors. No trimming or UTF-8 conversion.                                                                     |
| `read_file`                | Exactly one regular-file tar archive entry.                                                                                 |

### Errors

| Error or condition                     | Behavior                                                                                                                                   |
| -------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `Error::Execution { failure, output }` | Bytes received before transport failure, deadline, or cancellation. No valid exit status.                                                  |
| `ExecutionFailure`                     | `Deadline`, `Cancelled`, native `Docker` error, or `Protocol` error.                                                                       |
| `ScopeError<E>`                        | `Infrastructure(Error)`, `Callback(E)`, or `Cleanup { prior: Option<Box<ScopeError<E>>>, errors: Vec<Error> }`.                            |
| Cleanup failure                        | Retain the prior typed callback or infrastructure error and every resource cleanup failure.                                                |
| Callback assertion panic               | Resume unwinding with the original payload after cleanup. Report accompanying cleanup failures to stderr instead of replacing the payload. |

## Lifecycle and cancellation

See the shared [lifecycle, deadlines, and cancellation](../README.md#lifecycle-deadlines-and-cancellation) contract.

| Rust-specific behavior       | Constraint                                                                                                                                            |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| Operation deadline           | Starts on the helper's first poll, before connection negotiation.                                                                                     |
| Callback completion or panic | Cancel the environment token, including escaped clones, before cleanup.                                                                               |
| Uncertain create response    | Keep inspecting within the cleanup budget. The first 404 is not proof of absence. Retain a typed failure if ownership or absence cannot be confirmed. |
| Exec timeout or cancellation | Drop Bollard's owned stream and input writer. Output readers do not outlive the operation.                                                            |

> [!IMPORTANT]
> Dropping `with_environment` signals cancellation to its supervised task.
> Keep the Tokio runtime alive until background cleanup finishes, including up to two sequential 30-second cleanup budgets.
> An aborted caller cannot receive the eventual cleanup error or panic.
> Runtime or process termination cannot guarantee cleanup.

> [!IMPORTANT]
> Cancellation is cooperative. Blocking synchronous callback code can delay cancellation and cleanup indefinitely.
> Detached callback tasks remain caller-owned, but escaped environment operations observe cancellation.

## Docker endpoint support

| Setting or capability                    | Support                                                                                                                                   |
| ---------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| Default `DOCKER_HOST`                    | `unix:///var/run/docker.sock`                                                                                                             |
| Accepted endpoints                       | `unix:///path`, `http://host:port`, `tcp://host:port`                                                                                     |
| TLS/HTTPS and other schemes              | Rejected.                                                                                                                                 |
| `DOCKER_TLS_VERIFY`                      | Reject nonempty values other than `0`.                                                                                                    |
| `DOCKER_CERT_PATH`, `DOCKER_API_VERSION` | Reject when set, including empty values.                                                                                                  |
| API version                              | [Bollard](https://docs.rs/bollard/) negotiates the minimum of the daemon's API and its compile-time default (`1.53` with Bollard 0.21.1). |
| Other unsupported features               | Docker contexts, SSH endpoints, Windows containers, interactive stdin, PTY, and service/readiness orchestration.                          |

## Development

See [Rust maintenance](../AGENTS.md#rust).
