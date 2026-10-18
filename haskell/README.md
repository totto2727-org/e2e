# Caseyard for Haskell

For library users who know `IO` and Hspec.
See [shared prerequisites](../README.md#prerequisites-and-setup), [values](../README.md#shared-values), and [operations](../README.md#shared-operations).

## Setup

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
    caseyard >=0.1.0.0 && <0.2,
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
      run environment (Command ["echo", "hello from e2e"] Nothing [])
        `shouldReturn` Result 0 "hello from e2e\n" ""
```

`test/Spec.hs`, using [`hspec-discover`](https://hspec.github.io/hspec-discover.html):

```haskell
{-# OPTIONS_GHC -F -pgmF hspec-discover #-}
```

[Workflow example](../.agents/skills/cli-e2e/references/example/haskell/MultipleScenariosSpec.hs) · [Expected behavior](../.agents/skills/cli-e2e/references/example/haskell/MultipleScenariosSpec.md)

## Values and operations

`Test.CLI.E2E` is the only exposed module.

| Value or operation           | Haskell API                                                                      |
| ---------------------------- | -------------------------------------------------------------------------------- |
| Scoped lifecycle             | `withEnvironment :: String -> (Environment -> IO a) -> IO a`                     |
| Command                      | `Command { args :: [String], workingDir :: Maybe String, env :: [String] }`      |
| Result                       | `Result { exitCode :: Int, stdout :: ByteString, stderr :: ByteString }`         |
| Captured output              | `Output { capturedStdout :: ByteString, capturedStderr :: ByteString }`          |
| Execute                      | `run :: Environment -> Command -> IO Result`                                     |
| Write bytes                  | `writeFile :: Environment -> FilePath -> ByteString -> IO ()`                    |
| Read bytes                   | `readFile :: Environment -> FilePath -> IO ByteString`                           |
| Verdicts                     | Use Hspec `shouldReturn` for operations and `shouldBe` for returned values.      |
| Physical path kind           | `pathKind :: Environment -> FilePath -> IO PathKind`                             |
| Path kind                    | `PathMissing`, `PathRegularFile`, `PathDirectory`, `PathSymlink`, or `PathOther` |
| Literal link target          | `readSymlink :: Environment -> FilePath -> IO ByteString`                        |
| Read-only container identity | `containerID :: Environment -> String`                                           |

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
| Path observations          | `pathKind` and `readSymlink` reject empty paths and NUL before transport.                                               |
| `readSymlink`              | Preserve literal target bytes, including trailing newlines. A non-link or failed read throws `Infrastructure`.          |
| File transfer              | Use absolute container paths to avoid daemon-dependent path interpretation.                                             |
| `readFile`                 | Exactly one regular-file archive entry. Reject directories and symbolic-link entries.                                   |

## Driver installation

See [driver setup](../README.md#static-driver-for-snapshots-and-terminals) for trusted binary and image requirements.
The existing command and file APIs do not require a driver.

| API or constraint                                         | Haskell behavior                                                                                                                                    |
| --------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| `installDriver :: Environment -> ByteString -> IO Driver` | Install caller-supplied bytes once per environment. `Driver` is opaque and valid only inside that environment's callback.                           |
| Installation reservation                                  | Atomic. A second or concurrent attempt fails with `InvalidInput`. A failed installation consumes the reservation. Use a fresh environment to retry. |

## Filesystem snapshots

See the [shared snapshot contract](../README.md#file-state-snapshots) and [Docker scenarios](e2e/CLISpec.md#filesystem-snapshots) ([source](e2e/CLISpec.hs)).

| API or value         | Type or behavior                                                                                                     |
| -------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `captureSnapshot`    | `Driver -> [FilePath] -> IO FileSystemSnapshot`                                                                      |
| `FileSystemSnapshot` | `snapshotEntries :: Map FilePath SnapshotEntry`                                                                      |
| `SnapshotEntry`      | `Missing`, `RegularFile ByteString`, `Directory`, or `Symlink ByteString`                                            |
| Unselected path      | An absent map key, distinct from an observed `Missing`.                                                              |
| Compare snapshots    | Use `captureSnapshot driver paths` with Hspec `shouldReturn expected`, or compare `snapshotEntries` with `shouldBe`. |
| Invalid paths        | Empty paths, NUL, or trailing separators other than `/`.                                                             |

## Scoped terminals

See the [shared terminal contract](../README.md#terminal-sessions) and [Docker scenarios](e2e/CLISpec.md#scoped-terminals) ([source](e2e/CLISpec.hs)).

| API or value                                    | Type or behavior                                                                                                                                                                                                      |
| ----------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `withTerminal`                                  | `Driver -> Command -> TerminalOptions -> (Terminal -> IO a) -> IO a`                                                                                                                                                  |
| `TerminalOptions`                               | `terminalRows, terminalColumns :: Int`, `terminalInput :: Maybe ByteString` (`Nothing`: terminal, `Just`: pipe), `terminalTimeout :: Double`. Use `defaultTerminalOptions` for shared defaults.                       |
| `observeTerminal terminal`                      | `IO TerminalObservation`                                                                                                                                                                                              |
| `expectTerminal terminal channel bytes seconds` | `TerminalChannel`, `ByteString`, `Double` arguments. Returns `IO TerminalObservation`.                                                                                                                                |
| `sendTerminal terminal bytes`                   | `ByteString` input. Returns `IO TerminalObservation`.                                                                                                                                                                 |
| `resizeTerminal terminal rows columns`          | `Int` dimensions. Returns `IO TerminalObservation`.                                                                                                                                                                   |
| `signalTerminal terminal signal`                | `TerminalSignal` argument. Returns `IO TerminalObservation`.                                                                                                                                                          |
| `waitTerminal terminal seconds`                 | `Double` seconds. Returns `IO TerminalObservation`.                                                                                                                                                                   |
| `TerminalChannel`                               | `StandardOutput` or `TerminalOutput`                                                                                                                                                                                  |
| `TerminalSignal`                                | `Interrupt`, `Terminate`, `Hangup`, `Quit`, `Kill`, `User1`, or `User2`                                                                                                                                               |
| `TerminalObservation`                           | `terminalStdout, terminalOutput, terminalUnreadInput :: ByteString`, `terminalStatus :: TerminalStatus`, `terminalTermiosRestored :: Bool`, `terminalInitialTermios, terminalCurrentTermios :: [TermiosValue]`        |
| `TerminalStatus`                                | `TerminalRunning`, `TerminalExited Int`, or `TerminalSignaled Int`. Application exit and signals are observations, not helper failures.                                                                               |
| `TermiosValue`                                  | `TermiosInteger Integer` or nested `TermiosArray [TermiosValue]`                                                                                                                                                      |
| `TerminalError`                                 | `TerminalFailed String (Maybe TerminalObservation)` for failed expectations. `TerminalPartialFailure String ByteString ByteString` retains raw stdout and terminal output without fabricated status or termios state. |
| Malformed protocol                              | Throws `Infrastructure` from `E2EError`.                                                                                                                                                                              |
| Scope exit                                      | Close the session on success, failure, or asynchronous cancellation. Retained handles fail. Cleanup has a fresh 30-second budget and preserves the original callback exception if cleanup also fails.                 |
| Lost startup response                           | The driver watchdog and environment teardown remain the final cleanup boundaries.                                                                                                                                     |

## Errors, deadlines, and cleanup

See the shared [lifecycle, deadlines, and cancellation](../README.md#lifecycle-deadlines-and-cancellation) contract.

| Condition                                                  | Haskell behavior                                                                                                             |
| ---------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Invalid input, protocol failure, deadline, cleanup failure | Typed `E2EError` exception.                                                                                                  |
| Result, byte, path, or snapshot mismatch                   | The caller's test framework owns the assertion and failure rendering.                                                        |
| `ExecutionFailed cause output`                             | Original exception and captured streams. No exit code.                                                                       |
| `DeadlineExceeded output`                                  | Captured bytes, including an incomplete frame payload when available. No exit code.                                          |
| Callback succeeds, cleanup fails                           | `CleanupFailed cleanupCause Nothing`.                                                                                        |
| Startup or callback fails                                  | Rethrow the original exception after cleanup, even if cleanup fails. Preserve Hspec assertion metadata.                      |
| Original failure plus cleanup failure                      | Report secondary cleanup failure to host stderr, best-effort, with a one-second bound. Never replace the original exception. |
| Asynchronous cancellation                                  | Preserve the original exception, such as `ThreadKilled` or `AsyncCancelled`, after cleanup. No captured output or `Result`.  |
| Interrupted library operation                              | `System.Timeout` interrupts the operation. Owned HTTP responses close.                                                       |

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
