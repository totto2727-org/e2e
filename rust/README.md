# Rust CLI E2E library

Standalone, unpublished `cli-e2e` crate using Tokio and the native [Bollard Docker Engine client](https://docs.rs/bollard/).
It follows the [shared contract](../README.md), never invokes `docker exec`, builds or pulls images, or removes caller-owned images.

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

## Validation status

The scoped API migration on 2026-10-12 is validated by native formatting, linting, build, tests, rustdoc compilation, consumer-example compilation, and native test discovery.
Real Docker acceptance of the scoped API passed on 2026-10-12 through the public root `vp run e2e` task: seven independent Tokio tests, including six common behaviors and the identity/cleanup audit, plus two exact consumer sample tests.
The standard Cargo name filter also executed only `greeting`, with six other acceptance tests filtered out. Cleanup and image preservation passed without requiring unrelated tests to execute.
These observations cover the local Linux-container daemon, not TLS, remote production daemons, or Windows.

## Tasks and acceptance

Run from repository root with the root-owned Rust Nix shell:

```sh
pnpm exec vp run @totto2727/e2e-rust#check
pnpm exec vp run @totto2727/e2e-rust#build
pnpm exec vp run @totto2727/e2e-rust#test
# Root must first build e2e-example:local from go/example/Dockerfile.
pnpm exec vp run @totto2727/e2e-rust#e2e
```

`ci` runs rustfmt, Clippy with warnings denied, build, native tests and the compiled rustdoc example.
The local Justfile follows `template/rust-simple`, extending Cargo invocations with `--locked` and the explicit ignored acceptance target.
All build, Clippy, and test commands enable `--all-features`; Clippy also covers `--all-targets`.
The template's `unsafe_code = "forbid"`, denied `all`/`pedantic`, `unwrap_used`, and `expect_used` lint policies are retained.
`rust-toolchain.toml` uses the template's stable minimal toolchain with Clippy and rustfmt.
The crate retains its existing Rust 1.88 minimum rather than lowering it to the CLI template's 1.85.
Build outputs are in `rust/target/`.
For standalone Rust work without the JavaScript task runner, use `nix develop .#rust -c just --justfile rust/Justfile ci` and the module-local `fix` task.
Native tests require no Docker and include binary chunks, exact comparisons, argv validation, archive bytes/mode, cancellation, retained cleanup failures, and real localhost HTTP fixtures for exec streams and scoped lifecycle.
Lifecycle regressions verify generic callback values/errors, escaped environment cancellation, both synchronous and asynchronous original panic payloads after case/lease removal, and dropped-future cleanup while the runtime stays alive.
`tests/e2e.rs` is ignored during normal tests and runs only through the explicit `e2e` task against caller-built `e2e-example:local`.
It covers greeting, multi-command file persistence and byte copy, expected exit `7`, separated streams plus working directory/environment, physical and literal/dangling paths, and the default 60-second command deadline.
The independent identity/isolation test asserts distinct container IDs, scope-owner-specific residual scenario/lease checks that remain valid under filtering or concurrent tests, and caller-image preservation.
Select one native case without invoking unrelated cases: `cargo test --locked --all-features --test e2e greeting -- --ignored --exact`.
List the seven independent acceptance tests without Docker: `cargo test --locked --all-features --test e2e -- --list`.
Native transport tests are not real Docker acceptance.

API references consulted through the repository doc-search workflow: [Bollard source](https://github.com/fussybeaver/bollard), [Docker Engine API](https://docs.docker.com/reference/api/engine/).
