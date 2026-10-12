# Rust CLI E2E library

Standalone, unpublished `cli-e2e` crate using Tokio and the native [Bollard Docker Engine client](https://docs.rs/bollard/).
It follows the [shared contract](../README.md), never invokes `docker exec`, builds or pulls images, or removes caller-owned images.

## Public API

```rust,no_run
use cli_e2e::{run_cases, Case, Command, CommandResult, Error};

#[tokio::test]
async fn greeting() -> Result<(), Error> {
    run_cases("e2e-example:local", vec![Case::new("greeting", |env| async move {
        let result = env.run(Command::new(["echo", "hello"])).await?;
        assert_eq!(result.exit_code, 0);
        assert_eq!(result.stdout, b"hello\n");
        assert_eq!(result.stderr, b"");
        env.check_command(Command::new(["true"]), CommandResult::default()).await
    })]).await
}
```

- `run_cases(image: &str, cases: Vec<Case>) -> Result<(), Error>` is async and accepts unique, nonempty case names.
- `Case::new(name: impl Into<String>, action)` accepts a `Send + 'static` closure returning a `Send + 'static` future with `Output = Result<(), Error>` and passes an owned `Environment`.
- `Command { args: Vec<String>, working_dir: Option<String>, env: Vec<String> }` preserves argv boundaries and `KEY=value` entries.
- `Command::new(args)` accepts an iterator of strings and defaults the other fields.
- `CommandResult { exit_code: i64, stdout: Vec<u8>, stderr: Vec<u8> }` intentionally does not conflict with Rust's `Result`.
- `Environment::run(Command) -> Result<CommandResult, Error>` and `check_command(Command, CommandResult) -> Result<(), Error>` are async.
- Async file APIs are `write_file(&str, &[u8]) -> Result<(), Error>`, `read_file(&str) -> Result<Vec<u8>, Error>`, and `check_file(&str, &[u8]) -> Result<(), Error>`.
- Async physical-path assertions are `check_missing(&str)`, `check_regular_file(&str)`, `check_directory(&str)`, and `check_symlink(path: &str, literal_target: &str)`, each returning `Result<(), Error>`.
- `container_id() -> &str` exposes only the immutable identity for isolation checks.

Empty executables and NUL in image, case name, argv, working directory, environment entries, paths, and literal link targets are rejected before the corresponding Docker operation.
Empty non-executable arguments and environment values are preserved.
A nonzero application exit, including `7`, is `Ok(CommandResult)`.
Exact comparisons include both empty streams, with no trimming or UTF-8 conversion.
`Error::Execution { failure: ExecutionFailure, output: PartialOutput }` retains bytes received before transport errors, deadline expiry or cancellation and has no valid exit status.
`ExecutionFailure` distinguishes `Deadline`, `Cancelled`, native `Docker` errors and `Protocol` errors.
`Error::Multiple` retains earlier case failures alongside `Error::Cleanup { resource, cause }`, wrapped with the case name when applicable.

File transfer uses Docker tar archives with mode `0644`, exact bytes, and an already-existing parent directory.
Reading requires a single regular-file archive entry.
Physical assertions require `test` supporting `-e`, `-L`, `-f`, `-d`, and `readlink --` in the image.
Links are not accepted as physical files or directories, dangling links are not missing, and link targets are compared literally.

## Lifecycle and cancellation

The library inspects a local image once, resolves its immutable ID, creates a stopped lease, and creates fresh case containers without changing the image entrypoint or command.
Two slots bound concurrency, with each 60-second absolute operation deadline starting after acquiring its slot and including create/start.
After a callback completes, its environment token is cancelled before cleanup, including escaped clones.
Every resource has a UUID name and a per-run ownership label registered before creation is attempted.
Cleanup resolves that name, verifies ownership, removes only the inspected immutable ID, and retries transient failures within a fresh 30-second budget independent of the case deadline.
Slots remain held until cleanup completes, and the stopped lease is cleaned after all cases.

Bollard's owned exec stream and input writer are dropped on timeout or cancellation, so output readers do not outlive their operation.
Closing a connection does not prove the remote command stopped; container removal is the process boundary.
Callbacks may use Tokio assertions or framework callbacks and panics are caught for cleanup.
The operation budget does not preempt arbitrary caller code that does not await library operations.
Dropping `run_cases` signals cancellation to its supervised task; keep the Tokio runtime alive until background cleanup finishes, at most the outstanding bounded setup plus cleanup budgets.
An aborted caller cannot receive the eventual cleanup error, and terminating the runtime or process cannot guarantee cleanup.

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

On 2026-10-11, the root-owned Rust Nix shell passed formatting, Clippy with warnings denied, build, 16 native tests and the rustdoc example.
Explicit real Docker acceptance passed all six common scenarios plus two identity cases, including the default 60-second deadline, distinct IDs, no additional owned containers remaining, and preservation of the caller-owned image ID.
This is local Linux-container acceptance, not a claim of TLS, remote production daemon, or Windows support.

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
Native tests require no Docker and include binary chunks, exact comparisons, argv validation, archive bytes/mode, cancellation, retained cleanup failures, and a real localhost HTTP upgrade stream proving deadline output capture and socket closure.
`tests/e2e.rs` is ignored during normal tests and runs only through the explicit `e2e` task against caller-built `e2e-example:local`.
It covers greeting, multi-command file persistence and byte copy, expected exit `7`, separated streams plus working directory/environment, physical and literal/dangling paths, and the default 60-second command deadline.
Additional identity cases assert distinct container IDs, with before/after checks for residual scenario/lease containers and caller-image preservation.
Native transport tests are not real Docker acceptance.

API references consulted through the repository doc-search workflow: [Bollard source](https://github.com/fussybeaver/bollard), [Docker Engine API](https://docs.docker.com/reference/api/engine/).
