# Rust CLI E2E library

Standalone, unpublished `cli-e2e` crate using Tokio and the native [Bollard Docker Engine client](https://docs.rs/bollard/).
It follows the [shared contract](../README.md), never invokes `docker exec`, builds or pulls images, or removes caller-owned images.

## Setup

Use Rust 1.88 or newer and a running Linux-container Docker Engine.
Build your application image locally before running tests.
Its default entrypoint must keep the container running while commands execute.

After publication to crates.io, add the crate and Tokio to your consumer's `Cargo.toml`:

```toml
[dev-dependencies]
cli-e2e = "0.1"
tokio = { version = "1", features = ["rt-multi-thread", "macros"] }
```

Cargo resolves the crate from crates.io without a checkout path.
The current crate is unpublished, so this setup cannot be used until the matching package is released.

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

- `with_environment<F, Fut, T, E>(image: &str, callback: F) -> Result<T, ScopeError<E>>` is async and scopes one callback to one fresh environment.
- `F: FnOnce(Environment) -> Fut + Send + 'static`, `Fut: Future<Output = Result<T, E>> + Send + 'static`, and both `T` and `E` are `Send + 'static`.
- Move owned fixtures into the callback. There is no future boxing or library-specific error conversion required for callers, and concrete callback values and errors are preserved.
- Declare every case as its own native `#[tokio::test]`, with `#[ignore]` for explicit Docker selection. The native runner owns test names, filtering, reporting, ordering, and concurrency. There is no aggregate case API or fixed semaphore.
- `Command { args: Vec<String>, working_dir: Option<String>, env: Vec<String> }` preserves argv boundaries and `KEY=value` entries.
- `Command::new(args)` accepts an iterator of strings and defaults the other fields.
- `CommandResult { exit_code: i64, stdout: Vec<u8>, stderr: Vec<u8> }` intentionally does not conflict with Rust's `Result`.
- `Environment::run(Command) -> Result<CommandResult, Error>` and `check_command(Command, CommandResult) -> Result<(), Error>` are async.
- Async file APIs are `write_file(&str, &[u8]) -> Result<(), Error>`, `read_file(&str) -> Result<Vec<u8>, Error>`, and `check_file(&str, &[u8]) -> Result<(), Error>`.
- Async physical-path assertions are `check_missing(&str)`, `check_regular_file(&str)`, `check_directory(&str)`, and `check_symlink(path: &str, literal_target: &str)`, each returning `Result<(), Error>`.
- `container_id() -> &str` exposes only the immutable identity for isolation checks.

Empty executables and NUL in image, argv, working directory, environment entries, paths, and literal link targets are rejected before the corresponding Docker operation.
Empty non-executable arguments and environment values are preserved.
A nonzero application exit, including `7`, is `Ok(CommandResult)`.
Exact comparisons include both empty streams, with no trimming or UTF-8 conversion.
`Error::Execution { failure: ExecutionFailure, output: PartialOutput }` retains bytes received before transport errors, deadline expiry or cancellation and has no valid exit status.
`ExecutionFailure` distinguishes `Deadline`, `Cancelled`, native `Docker` errors and `Protocol` errors.
`ScopeError<E>` distinguishes `Infrastructure(Error)`, `Callback(E)`, and `Cleanup { prior: Option<Box<ScopeError<E>>>, errors: Vec<Error> }`.
Cleanup failures retain the original typed callback or infrastructure error and every resource cleanup failure.
Callback assertion panics keep their original payload and resume unwinding after cleanup, not an opaque runner error.
Cleanup failures accompanying a panic are reported to stderr rather than replacing its payload.

File transfer uses Docker tar archives with mode `0644`, exact bytes, and an already-existing parent directory.
Reading requires a single regular-file archive entry.
Physical assertions require `test` supporting `-e`, `-L`, `-f`, `-d`, and `readlink --` in the image.
Links are not accepted as physical files or directories, dangling links are not missing, and link targets are compared literally.

## Lifecycle and cancellation

Each helper invocation inspects its caller-built local image once, resolves its immutable ID, creates its own stopped lease, and creates a fresh case container without changing the image entrypoint or command.
A single 60-second absolute operation deadline starts when the helper is first polled, before connection negotiation, image inspection, lease creation, and container create/start.
The native runner controls concurrency, for example through `cargo test -- --test-threads=2`; no suite or lease is shared across native tests.
After a callback completes or panics, its environment token is cancelled before cleanup, including escaped clones.
Every resource has a UUID name and a per-scope ownership label registered before creation is attempted.
Cleanup resolves that name, verifies ownership, removes only the inspected immutable ID, and retries transient failures within a fresh 30-second total budget per resource independent of the operation deadline.
The case container is cleaned first, then the stopped lease. The helper does not return or resume the callback's panic until both cleanup attempts finish.

Bollard's owned exec stream and input writer are dropped on timeout or cancellation, so output readers do not outlive their operation.
Closing a connection does not prove the remote command stopped; container removal is the process boundary.
Callbacks may use native assertions, which unwind normally after supervised cleanup.
The operation budget does not preempt arbitrary caller code or futures that do not await library operations. Detached callback tasks are caller-owned, but escaped environment operations observe cancellation.
Dropping `with_environment` signals cancellation to its owned supervised task; keep the Tokio runtime alive until background cleanup finishes, including up to two sequential 30-second resource cleanup budgets.
Cancellation is cooperative. Blocking synchronous callback code can delay cancellation and cleanup indefinitely.
An aborted caller cannot receive the eventual cleanup error or panic, and terminating the runtime or process cannot guarantee cleanup.

## Docker endpoint support

The baseline is a Unix socket to a Linux-container Engine, with default `/var/run/docker.sock` and explicit `DOCKER_HOST=unix:///path` supported.
Plain `http://host:port` and `tcp://host:port` use native HTTP transport.
TLS/HTTPS and other endpoint schemes are explicitly rejected, as are `DOCKER_TLS_VERIFY` when enabled and `DOCKER_CERT_PATH`.
`DOCKER_API_VERSION` is explicitly rejected rather than silently overridden or downgraded.
Without it, Bollard 0.21.1 negotiates the minimum of the daemon's API and its compile-time default `1.53`, pinned by `Cargo.lock`.
After an uncertain create response, cleanup keeps inspecting the UUID name within its 30-second budget rather than accepting the first 404 as proof of absence.
If ownership or absence cannot be confirmed, the typed cleanup failure is retained.
Docker contexts, SSH endpoints, Windows containers, interactive stdin, PTY and service/readiness orchestration are not supported.
No OpenSSL or pkg-config dependencies are required.

## Development

See [Rust maintenance](../AGENTS.md#rust) for repository maintenance.
