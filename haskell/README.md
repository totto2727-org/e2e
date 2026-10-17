# Haskell CLI E2E

This guide serves Haskell users who know `IO` and Hspec.
The [root contract](../README.md) defines the shared behavior.
`Test.CLI.E2E` is the only exposed library module.

## Setup

Use an ordinary consumer `cabal.project`:

```cabal
packages: .
```

After the package is published to Hackage, add `cli-e2e >=0.1.0.0 && <0.2` and `hspec >=2.10 && <2.12` to your test suite's `build-depends`.
Cabal resolves the library from Hackage, not a neighboring checkout.
These instructions describe the published-package setup and do not assert that this version is already available.
The library requires `base >=4.18 && <5` and a running Linux-container Docker Engine.

## Use the library

Build your Linux application image before the test starts.
The image entrypoint must keep the container running, for example with `sleep infinity`.
The library inspects the local image and does not build, pull, or remove images.

```haskell
{-# LANGUAGE OverloadedStrings #-}
module GreetingSpec (spec) where

import Test.Hspec
import Test.CLI.E2E

spec :: Spec
spec = do
  it "greeting" $
    withEnvironment "e2e-example:local" $ \environment ->
      checkCommand environment
        (Command ["echo", "hello from e2e"] Nothing [])
        (Result 0 "hello from e2e\n" "")
```

Save the example as `GreetingSpec.hs` in the test source directory.
Use this one-line `Spec.hs` as the suite entry point:

```haskell
{-# OPTIONS_GHC -F -pgmF hspec-discover #-}
```

In the Cabal test suite, set `main-is: Spec.hs`, list `GreetingSpec` in `other-modules`, and add `build-tool-depends: hspec-discover:hspec-discover >=2.10 && <2.12`.
[`hspec-discover`](https://hspec.github.io/hspec-discover.html) generates the entry point and collects the `spec` exported by each `*Spec.hs` module.
Test modules do not define `main`, and the library does not register tests.

Each standard Hspec `it` invokes `withEnvironment` only when the framework selects and executes it.
Hspec owns example names, filtering, reporting, fail-fast behavior, and scheduling.
Use `--match greeting` to execute only matching examples and `--dry-run` to list them without contacting Docker.
For suites whose examples can run concurrently, use Hspec's `parallel` and `--jobs N` controls rather than a library-owned gate.
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

## Development

See [Haskell maintenance](../AGENTS.md#haskell) for repository maintenance.
