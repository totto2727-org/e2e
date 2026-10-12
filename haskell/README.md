# Haskell CLI E2E

This guide serves Haskell users who know `IO` and Hspec, and contributors who run the package checks.
The [root contract](../README.md) defines the shared behavior.
`Test.CLI.E2E` is the only exposed library module.

## Use the library

Build your Linux application image before the test starts.
The image entrypoint must keep the container running, for example with `sleep infinity`.
The library inspects the local image and does not build, pull, or remove images.

```haskell
{-# LANGUAGE OverloadedStrings #-}
import Test.Hspec
import Test.CLI.E2E

main :: IO ()
main = hspec $ do
  it "greeting" $
    withEnvironment "e2e-example:local" $ \environment ->
      checkCommand environment
        (Command ["echo", "hello from e2e"] Nothing [])
        (Result 0 "hello from e2e\n" "")
```

Each standard Hspec `it` invokes `withEnvironment` only when the framework selects and executes it.
Hspec owns example names, filtering, reporting, fail-fast behavior, and scheduling.
Use `--match greeting` to execute only matching examples and `--dry-run` to list them without contacting Docker.
For suites whose examples can run concurrently, use Hspec's `parallel` and `--jobs N` controls rather than a library-owned gate.
The acceptance suite stays sequential because its per-example label-set audits assume exclusive ownership during each snapshot window.
There is no `Case`, outcome list, shared runner, or `beforeAll` batch of hidden actions.
Each helper invocation resolves its own local immutable image ID, retains it with a stopped lease, creates one fresh running container, and removes both before returning the callback value or rethrowing its exception.
Case actions can use Hspec assertions directly.

## Values and operations

| Value or operation           | Haskell API                                                                 |
| ---------------------------- | --------------------------------------------------------------------------- |
| Scoped lifecycle             | `withEnvironment :: String -> (Environment -> IO a) -> IO a`                |
| Command                      | `Command { args :: [String], workingDir :: Maybe String, env :: [String] }` |
| Result                       | `Result { exitCode :: Int, stdout :: ByteString, stderr :: ByteString }`    |
| Execute                      | `run :: Environment -> Command -> IO Result`                                |
| Exact command assertion      | `checkCommand :: Environment -> Command -> Result -> IO ()`                 |
| File transfer                | `writeFile`, `readFile`, `checkFile`                                        |
| Physical path assertions     | `checkMissing`, `checkRegularFile`, `checkDirectory`, `checkSymlink`        |
| Read-only container identity | `containerID :: Environment -> String`                                      |

`Environment` is opaque and valid only during its case action.
Do not retain it or start unjoined operations that use it after the action returns.
There is no public Docker client accessor.
Use `Prelude hiding (readFile, writeFile)` when you import the file operations without qualification.

`Command` uses argv entries, not shell text.
It preserves empty arguments and environment values such as `KEY=`.
An empty argv, an empty executable, or NUL in any command string is invalid.
`Nothing` uses the image's working directory.
`env` contains Docker environment entries such as `KEY=value`.
Use an explicit command such as `Command ["sh", "-c", script] Nothing []` when shell syntax is needed.

Both result streams are strict `ByteString` values.
No text decode, newline conversion, or stream merging occurs.
Ordering is preserved within each stream, not between streams.
For non-ASCII expectations, encode text with `Data.Text.Encoding.encodeUtf8`.
Do not use a `ByteString` string literal as a UTF-8 encoder.
A nonzero application code, including `7`, is a normal `Result`.
Every result field is compared exactly by `checkCommand`, including empty streams.

File operations transfer exact bytes through Docker archives.
`writeFile environment path bytes` writes mode `0644`; the parent directory must exist.
`readFile environment path` requires an archive with exactly one regular file.
It rejects directories and symbolic-link archive entries.
`checkFile environment path bytes` compares every byte.
Use absolute container paths for file transfer so that archive paths do not depend on daemon interpretation.

Path assertions use `test` and `readlink` inside the container.
The image must provide `test -e`, `test -L`, `test -f`, `test -d`, and `readlink --`.
`checkMissing` rejects dangling links.
Regular-file and directory assertions reject symbolic links to those types.
`checkSymlink environment path literalTarget` compares the unchanged target, including relative or dangling targets.
Probe codes other than `0` and `1` are infrastructure errors.

## Errors, deadlines, and cleanup

The library throws the typed `E2EError` exception for invalid input, protocol errors, failed assertions, deadline expiry, and cleanup failures.
`ExecutionFailed cause output` preserves the original exception and captured stdout and stderr.
`DeadlineExceeded output` preserves bytes captured before timeout, including an incomplete frame payload when available.
Neither error carries an exit code.
A successful exec inspection is required before `Result` can be returned.
`CleanupFailed cleanupCause Nothing` reports failed cleanup when the callback otherwise succeeded.
If the callback or startup failed, that original exception is rethrown unchanged after attempting both container and lease cleanup, even if cleanup also failed.
This preserves Hspec assertion metadata and asynchronous exception identity rather than wrapping or collecting them.
In that dual-failure case the original exception takes precedence.
The secondary cleanup error is reported to host stderr with a best-effort, one-second-bounded diagnostic that cannot replace the original exception.

The library has no concurrency limit or scheduling queue.
Each invocation starts one absolute 60-second operation budget before image inspection, lease creation, and fresh container creation.
Commands and file operations share the remaining budget, rather than receiving new per-operation timeouts.
Framework-queued examples have not invoked the helper and therefore spend none of that budget.
Each owned resource cleanup gets one fresh 30-second total budget for inspection and removal, independent of the operation deadline.
The helper does not finish until cleanup of both resources has been attempted.

Library operations use `System.Timeout` and close owned HTTP responses when interrupted.
Asynchronous cancellation propagates after cleanup rather than becoming a successful callback result.
Raw owner cancellation preserves the original asynchronous exception, such as `ThreadKilled` or `AsyncCancelled`.
That exception does not carry captured output and the cancelled owner receives no `Result`.
This differs from deadline and synchronous execution errors, which carry `Output`.
Arbitrary caller code is not forcibly stopped by the case budget.
For example, a case action that waits forever without calling the library will wait forever unless its owner cancels it.
Do not use `uninterruptibleMask` around library calls.
Closing an exec connection does not prove that the remote process stopped; container removal is the final process boundary.

The helper generates a 192-bit ownership token from `/dev/urandom` before sending container creation requests.
Cleanup inspects the unique name, verifies `org.e2e.haskell.owner`, and removes the matching immutable container ID.
It never deletes the caller's image.
A daemon can finish a create request after a disconnected client has completed its cleanup inspection.
The shared contract documents this late-daemon race; the adapter cannot prove that no future server-side commit will occur.

## Docker connection support

The baseline is a local Linux-container Docker Engine.
`DOCKER_HOST` accepts `unix:///absolute/socket`, `tcp://host:port`, or `http://host:port`.
The default is `unix:///var/run/docker.sock`.
Docker contexts and the Docker CLI are not used.
TLS, `https://`, and other endpoint schemes are unsupported.
Nonempty `DOCKER_TLS_VERIFY` or `DOCKER_CERT_PATH` values are rejected rather than downgraded to plaintext.
Nonempty `DOCKER_API_VERSION` is also rejected: this adapter uses the Engine's unversioned endpoints.
Only use unencrypted TCP with a trusted local endpoint.
Remote daemon and Windows-container support is not claimed.

Exec start uses an HTTP `200` close-delimited raw stream with `Tty=false`, without requesting a protocol upgrade.
The adapter validates fragmented eight-byte stream headers and separates stdout from stderr, then polls exec inspection under the same deadline.
See the [official Docker Engine API](https://docs.docker.com/reference/api/engine/) and [http-client API](https://hackage.haskell.org/package/http-client/docs/Network-HTTP-Client.html).

## Contributor checks

Run these commands from the repository root:

```sh
pnpm exec vp run @totto2727/e2e-haskell#check
pnpm exec vp run @totto2727/e2e-haskell#build
pnpm exec vp run @totto2727/e2e-haskell#test
```

`check` typechecks the library, native tests, and opt-in Docker scenarios with warnings treated as errors.
It also checks the Cabal manifest.
The checkout has no declared license or release maintainer, so only those two publication checks are excluded.
This is not a claim that Hackage would accept the package.
`test` runs Hspec against controlled HTTP/socket fixtures without Docker.
There is no Haskell workspace template to align with; this package retains idiomatic Cabal manifests and Hspec suites rather than inventing template settings.
The local tasks use explicit Cabal suite targets, with build outputs under `haskell/tmp/cabal/`.
For standalone Haskell work without the JavaScript task runner, `nix develop .#haskell -c just --justfile haskell/Justfile ci` runs the same native checks.
The root Nix shell supplies pinned GHC, Cabal, and installed compatible packages.
The package's published dependency bounds do not require `allow-newer`.

Consumers can add this directory as a local Cabal package.
For example, run `cabal build --offline --builddir=tmp/cabal lib:cli-e2e` from `haskell/` within the Nix shell.
Normal `cabal test native` does not require Docker.
The `docker-e2e` suite is disabled unless `-fdocker-e2e` is supplied explicitly.
The task commands select `test:native` or `-fdocker-e2e test:docker-e2e` before execution.
Within a selected suite Hspec `--match` filters actual example execution and does not merely filter already-collected outcomes.

After the caller builds `e2e-example:local`, run the separate Docker task:

```sh
pnpm exec vp run @totto2727/e2e-haskell#e2e
```

This task does not build or pull an image.
It runs the [six common acceptance scenarios](e2e/Spec.md) as direct Hspec examples, checking each example's fresh container identity, removed case and lease containers, and unchanged image ID.
The native suite is transport evidence, not Docker acceptance.
