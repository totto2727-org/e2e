# Haskell CLI E2E

For library users who know `IO` and Hspec.
See [shared prerequisites](../README.md#prerequisites-and-setup), [values](../README.md#shared-values), and [operations](../README.md#shared-operations).

## Setup

> [!IMPORTANT]
> The Hackage setup below requires publication of `cli-e2e` at a matching version.
> Registry availability is not verified.

Consumer `cabal.project`:

```cabal
packages: .
```

Consumer test suite:

```cabal
test-suite greeting
  type: exitcode-stdio-1.0
  hs-source-dirs: test
  main-is: Spec.hs
  other-modules: GreetingSpec
  default-language: Haskell2010
  build-depends:
    base >=4.18 && <5,
    cli-e2e >=0.1.0.0 && <0.2,
    hspec >=2.10 && <2.12
  build-tool-depends:
    hspec-discover:hspec-discover >=2.10 && <2.12
```

## Use the library

`test/GreetingSpec.hs`:

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

`test/Spec.hs`, using [`hspec-discover`](https://hspec.github.io/hspec-discover.html):

```haskell
{-# OPTIONS_GHC -F -pgmF hspec-discover #-}
```

[Workflow example](../.agents/skills/document-e2e-scenarios/references/example/haskell/MultipleScenariosSpec.hs) · [Expected behavior](../.agents/skills/document-e2e-scenarios/references/example/haskell/MultipleScenariosSpec.md)

## Values and operations

`Test.CLI.E2E` is the only exposed module.

| Value or operation           | Haskell API                                                                              |
| ---------------------------- | ---------------------------------------------------------------------------------------- |
| Scoped lifecycle             | `withEnvironment :: String -> (Environment -> IO a) -> IO a`                             |
| Command                      | `Command { args :: [String], workingDir :: Maybe String, env :: [String] }`              |
| Result                       | `Result { exitCode :: Int, stdout :: ByteString, stderr :: ByteString }`                 |
| Execute                      | `run :: Environment -> Command -> IO Result`                                             |
| Exact command assertion      | `checkCommand :: Environment -> Command -> Result -> IO ()`                              |
| Write bytes                  | `writeFile :: Environment -> FilePath -> ByteString -> IO ()`                            |
| Read bytes                   | `readFile :: Environment -> FilePath -> IO ByteString`                                   |
| Compare bytes                | `checkFile :: Environment -> FilePath -> ByteString -> IO ()`                            |
| Physical path assertions     | `checkMissing`, `checkRegularFile`, `checkDirectory :: Environment -> FilePath -> IO ()` |
| Literal link assertion       | `checkSymlink :: Environment -> FilePath -> FilePath -> IO ()`                           |
| Read-only container identity | `containerID :: Environment -> String`                                                   |

For unqualified file operations:

```haskell
import Prelude hiding (readFile, writeFile)
```

| Language-specific behavior | Constraint                                                                                                              |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `Environment`              | Opaque, with no public Docker client accessor. Do not retain it or leave operations running after the callback returns. |
| `workingDir = Nothing`     | Use the image's working directory.                                                                                      |
| `env`                      | Docker entries such as `KEY=value`, including `KEY=`.                                                                   |
| Output                     | Strict `ByteString` values. No decoding or newline conversion.                                                          |
| Non-ASCII expectations     | Use `Data.Text.Encoding.encodeUtf8`. A `ByteString` string literal is not a UTF-8 encoder.                              |
| Additional invalid input   | Empty image, or NUL in image, paths, or literal link targets.                                                           |
| File transfer              | Use absolute container paths to avoid daemon-dependent path interpretation.                                             |
| `readFile`                 | Exactly one regular-file archive entry. Reject directories and symbolic-link entries.                                   |

## Errors, deadlines, and cleanup

See the shared [lifecycle, deadlines, and cancellation](../README.md#lifecycle-deadlines-and-cancellation) contract.

| Condition                                                               | Haskell behavior                                                                                                             |
| ----------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Invalid input, protocol or assertion failure, deadline, cleanup failure | Typed `E2EError` exception.                                                                                                  |
| `ExecutionFailed cause output`                                          | Original exception and captured streams. No exit code.                                                                       |
| `DeadlineExceeded output`                                               | Captured bytes, including an incomplete frame payload when available. No exit code.                                          |
| Callback succeeds, cleanup fails                                        | `CleanupFailed cleanupCause Nothing`.                                                                                        |
| Startup or callback fails                                               | Rethrow the original exception after cleanup, even if cleanup fails. Preserve Hspec assertion metadata.                      |
| Original failure plus cleanup failure                                   | Report secondary cleanup failure to host stderr, best-effort, with a one-second bound. Never replace the original exception. |
| Asynchronous cancellation                                               | Preserve the original exception, such as `ThreadKilled` or `AsyncCancelled`, after cleanup. No captured output or `Result`.  |
| Interrupted library operation                                           | `System.Timeout` interrupts the operation. Owned HTTP responses close.                                                       |

> [!IMPORTANT]
> A callback that waits forever outside library operations needs owner cancellation.
> Do not use `uninterruptibleMask` around library calls.

## Docker connection support

| Setting or capability                   | Support                                                                                                          |
| --------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| Default `DOCKER_HOST`                   | `unix:///var/run/docker.sock`                                                                                    |
| Accepted endpoints                      | `unix:///absolute/socket`, `tcp://host:port`, `http://host:port`                                                 |
| TLS and other schemes                   | Unsupported, including `https://`.                                                                               |
| `DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH` | Reject nonempty values. No plaintext downgrade.                                                                  |
| `DOCKER_API_VERSION`                    | Reject nonempty values. Use unversioned Engine endpoints.                                                        |
| Docker contexts and CLI                 | Not used.                                                                                                        |
| Other daemons                           | Remote daemon and Windows-container support is not claimed.                                                      |
| Exec transport                          | HTTP `200`, close-delimited raw stream, `Tty=false`, no upgrade request. Validate fragmented eight-byte headers. |

> [!CAUTION]
> Use unencrypted TCP only with a trusted local endpoint.

References: [Docker Engine API](https://docs.docker.com/reference/api/engine/), [http-client API](https://hackage.haskell.org/package/http-client/docs/Network-HTTP-Client.html).

## Development

See [Haskell maintenance](../AGENTS.md#haskell).
