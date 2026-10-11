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
import Control.Exception (throwIO)
import Control.Monad (forM_)
import Test.Hspec
import Test.CLI.E2E

main :: IO ()
main = do
  results <- runCases "e2e-example:local"
    [ Case "greeting" $ \environment ->
        checkCommand environment
          (Command ["echo", "hello from e2e"] Nothing [])
          (Result 0 "hello from e2e\n" "")
    ]
  hspec $ forM_ results $ \result ->
    it (caseName result) (either throwIO pure (outcome result))
```

Each action has its own named outcome.
`runCases` returns outcomes in input order after all case containers and the shared stopped image lease have been removed.
An image setup error or lease cleanup error throws from `runCases`.
A case error is stored in `outcome :: Either SomeException ()` so that one failed case does not hide other outcomes.
The Hspec examples above report each case independently.
Case actions can use Hspec assertions directly.
The actions run before Hspec reports their collected outcomes, so Hspec filters do not select actions in this example.
Select the `Case` list before `runCases` when you need to run only some actions.

## Values and operations

| Value or operation | Haskell API |
| --- | --- |
| Case | `Case { name :: String, action :: Environment -> IO () }` |
| Command | `Command { args :: [String], workingDir :: Maybe String, env :: [String] }` |
| Result | `Result { exitCode :: Int, stdout :: ByteString, stderr :: ByteString }` |
| Execute | `run :: Environment -> Command -> IO Result` |
| Exact command assertion | `checkCommand :: Environment -> Command -> Result -> IO ()` |
| File transfer | `writeFile`, `readFile`, `checkFile` |
| Physical path assertions | `checkMissing`, `checkRegularFile`, `checkDirectory`, `checkSymlink` |
| Read-only container identity | `containerID :: Environment -> String` |

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
`CleanupFailed cleanupCause originalCause` preserves a cleanup error and any earlier case error.
Caller exceptions remain available through `SomeException` in the named outcome.

At most two cases hold slots concurrently.
Each case starts an absolute 60-second budget after it acquires a slot.
The budget includes container creation, startup, commands, and file operations.
Queued cases do not spend that budget.
Each cleanup gets one fresh 30-second budget for inspection and removal, independent of the case deadline.
The slot remains held until cleanup finishes.
Image inspection and lease creation share a separate 60-second setup budget.

Library operations use `System.Timeout` and close owned HTTP responses when interrupted.
Asynchronous cancellation propagates after cleanup rather than becoming a successful case outcome.
Raw owner cancellation preserves the original asynchronous exception, such as `ThreadKilled` or `AsyncCancelled`.
That exception does not carry captured output and the cancelled owner receives no `Result`.
This differs from deadline and synchronous execution errors, which carry `Output`.
Arbitrary caller code is not forcibly stopped by the case budget.
For example, a case action that waits forever without calling the library will wait forever unless its owner cancels it.
Do not use `uninterruptibleMask` around library calls.
Closing an exec connection does not prove that the remote process stopped; container removal is the final process boundary.

The runner generates a 192-bit ownership token from `/dev/urandom` before sending container creation requests.
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
nix develop .#haskell -c just --justfile haskell/Justfile ci
```

`check` typechecks the library, native tests, and opt-in Docker scenarios with warnings treated as errors.
It also checks the Cabal manifest.
The checkout has no declared license or release maintainer, so only those two publication checks are excluded.
This is not a claim that Hackage would accept the package.
`test` runs Hspec against controlled HTTP/socket fixtures without Docker.
The root Nix shell supplies pinned GHC, Cabal, and installed compatible packages.
The package's published dependency bounds do not require `allow-newer`.

Consumers can add this directory as a local Cabal package.
For example, run `cabal build --offline --builddir=tmp/cabal lib:cli-e2e` from `haskell/` within the Nix shell.
Normal `cabal test native` does not require Docker.
The `docker-e2e` suite is disabled unless `-fdocker-e2e` is supplied explicitly.

After the caller builds `e2e-example:local`, run the separate Docker task:

```sh
nix develop .#haskell -c just --justfile haskell/Justfile e2e
```

This task does not build or pull an image.
It runs the [six common acceptance scenarios](e2e/Spec.md), then checks distinct container IDs, removed case and lease containers, and an unchanged image ID.
The native suite is transport evidence, not Docker acceptance.
