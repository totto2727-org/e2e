# Caseyard for Haskell

For library users who know `IO` and Hspec.
See [shared prerequisites](../README.md#prerequisites-and-setup), [values](../README.md#shared-values), and [operations](../README.md#shared-operations).

## Setup

> [!IMPORTANT]
> The Hackage setup below requires publication of `caseyard` at a matching version.
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
      checkCommand environment
        (Command ["echo", "hello from e2e"] Nothing [])
        (Result 0 "hello from e2e\n" "")
```

`test/Spec.hs`, using [`hspec-discover`](https://hspec.github.io/hspec-discover.html):

```haskell
{-# OPTIONS_GHC -F -pgmF hspec-discover #-}
```

[Workflow example](../.agents/skills/cli-e2e/references/example/haskell/MultipleScenariosSpec.hs) · [Expected behavior](../.agents/skills/cli-e2e/references/example/haskell/MultipleScenariosSpec.md)

## Values and operations

`Test.CLI.E2E` is the only exposed module.

| Value or operation           | Haskell API                                                                              |
| ---------------------------- | ---------------------------------------------------------------------------------------- |
| Scoped lifecycle             | `withEnvironment :: String -> (Environment -> IO a) -> IO a`                             |
| Command                      | `Command { args :: [String], workingDir :: Maybe String, env :: [String] }`              |
| Result                       | `Result { exitCode :: Int, stdout :: ByteString, stderr :: ByteString }`                 |
| Captured output              | `Output { capturedStdout :: ByteString, capturedStderr :: ByteString }`                  |
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

## Driver installation

The snapshot and terminal APIs require a caller-supplied static Linux `caseyard-driver` binary for the container architecture.
See [driver setup](../README.md) for build and prerequisite details.
The image does not need Python or Go.
The original command and file APIs do not require this driver.

Load the trusted binary on the host, then install it once inside each environment:

```haskell
import qualified Data.ByteString as BS

binary <- BS.readFile "/path/to/driver-linux-arm64"
withEnvironment "e2e-example:local" $ \environment -> do
  driver <- installDriver environment binary
  snapshot <- captureSnapshot driver ["/workspace/result.txt"]
  checkSnapshot driver snapshot
```

`installDriver :: Environment -> ByteString -> IO Driver` returns an opaque capability bound to that environment.
Installation uses `mktemp` and the existing archive transfer to place a mode-0755 executable inside a mode-0700 private directory.
The executable can be root-owned, so installation does not require the image user to change its mode.
The container owns the directory, including any partially installed binary.
Container cleanup removes it.
Do not retain the capability after the environment callback returns.
Installation is reserved atomically, so a second or concurrent installation attempt in the same environment fails with `InvalidInput`.
A failed installation also consumes the reservation. Create a fresh environment to retry.
No binary is embedded in the Haskell package.

## Filesystem snapshots

`captureSnapshot driver paths` reads only the listed paths.
It records `Missing`, `RegularFile bytes`, `Directory`, or `Symlink literalTarget` in `FileSystemSnapshot`.
Regular files retain exact bytes, including NUL and non-UTF-8 bytes.
Symbolic links retain their literal target bytes, including dangling targets, embedded newlines, and non-UTF-8 bytes.
Directories record only their kind, not their children.
No operation records permissions, timestamps, ownership, or other metadata.
The capture is sequential, not an atomic filesystem transaction.

```haskell
before <- captureSnapshot driver ["/workspace/result.txt", "/workspace/cache"]
-- Run the application with run or checkCommand.
after <- captureSnapshot driver ["/workspace/result.txt", "/workspace/cache"]
let differences = diffSnapshots before after
checkSnapshot driver before
```

`diffSnapshots` is pure and returns one `SnapshotDifference` per changed path, sorted by path.
Each difference contains `differencePath`, `differenceChange`, `differenceBefore`, and `differenceAfter`.
The change is `ExistenceChanged`, `KindChanged`, `ContentChanged`, or `TargetChanged`.
`Nothing` means that a snapshot did not select the path, which differs from `Just Missing`.
Duplicate input paths select one observation.
Empty paths, NUL, and trailing separators other than `/` are invalid.
A trailing separator could cause the operating system to follow a final symbolic link.

`checkSnapshot` captures the expected snapshot's selected paths again and throws `AssertionFailed` with the sorted differences on mismatch.
It never updates the expected value or writes to the container.
Permission errors, read failures, malformed inspection results, and unsupported kinds such as FIFOs or sockets fail the operation.
Only a physical inspection result of `ENOENT` records `Missing`.
See [Docker snapshot scenarios](e2e/CLISpec.md#filesystem-snapshots).

## Scoped terminals

`withTerminal :: Driver -> Command -> TerminalOptions -> (Terminal -> IO a) -> IO a` owns one terminal session for its callback.
The application has a controlling terminal and a foreground process group.
Its stdout remains a separate pipe.
Its stderr and, by default, stdin use the terminal.
The original `Command` working directory and environment reach the application.

```haskell
withTerminal driver (Command ["application"] (Just "/workspace") [])
  defaultTerminalOptions $ \terminal -> do
    _ <- expectTerminal terminal TerminalOutput "prompt> " 5
    _ <- sendTerminal terminal "answer\n"
    finished <- waitTerminal terminal 5
    print (terminalStatus finished, terminalTermiosRestored finished)
```

| Value or operation                              | Contract                                                                                                                                                             |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `defaultTerminalOptions`                        | 40 rows, 120 columns, terminal stdin, 50-second watchdog.                                                                                                            |
| `TerminalOptions`                               | `terminalRows`, `terminalColumns`, `terminalInput :: Maybe ByteString`, and `terminalTimeout :: Double`.                                                             |
| `observeTerminal terminal`                      | Read cumulative output and process state without waiting for exit.                                                                                                   |
| `expectTerminal terminal channel bytes seconds` | Match nonempty raw bytes on `StandardOutput` or `TerminalOutput`, including bytes split across reads. Advance that channel's match cursor, retaining trailing bytes. |
| `sendTerminal terminal bytes`                   | Write exact terminal input, including literal Ctrl-C and Ctrl-D bytes. Not a signal or transport-close operation.                                                    |
| `resizeTerminal terminal rows columns`          | Update geometry and send SIGWINCH to the foreground process group.                                                                                                   |
| `signalTerminal terminal signal`                | Send `Interrupt`, `Terminate`, `Hangup`, `Quit`, `Kill`, `User1`, or `User2` to the process group.                                                                   |
| `waitTerminal terminal seconds`                 | Wait for child exit and drain both output channels.                                                                                                                  |

Each operation returns `TerminalObservation` with `terminalStdout`, `terminalOutput`, `terminalUnreadInput`, `terminalStatus`, `terminalTermiosRestored`, `terminalInitialTermios`, and `terminalCurrentTermios`.
`terminalStatus` is `TerminalRunning`, `TerminalExited code`, or `TerminalSignaled signalNumber`.
Raw output is a strict `ByteString` with ANSI sequences unchanged.
Terminal output can include stderr, terminal echo, and line-discipline transformations.
There is no ordering guarantee between stdout and terminal output.
Termios values use `TermiosInteger` and nested `TermiosArray` values.
PENDIN bookkeeping is excluded from the termios comparison.
A restored flag while running is not proof of application restoration after exit.
Screen and cursor restoration remain caller assertions against raw bytes.

Set `terminalInput = Just bytes` for finite pipe input followed by EOF.
The application still uses the terminal for stderr.
After exit, `terminalUnreadInput` contains input bytes that the application did not consume.
`sendTerminal` fails when pipe input is selected.
With terminal stdin, Ctrl-D is a literal input byte, whose effect depends on the application's terminal mode.

Rows and columns must be in `1..65535`.
All timeout values must be finite, positive, and at most 60 seconds.
The session watchdog does not extend the environment's deadline.
A failed expectation throws `TerminalFailed message observation`, including the captured observation when available.
A watchdog response with only raw output throws `TerminalPartialFailure message stdoutBytes terminalBytes` without inventing an exit status or termios state.
Malformed protocol data throws `Infrastructure`.
Application exit codes and signals are observations, not helper failures.

The callback can use ordinary environment file operations between terminal operations.
This permits mid-run no-op and tiny-terminal assertions without waiting for process exit.
A retained terminal handle fails after its callback returns.
Scope exit closes the session and kills surviving descendants, including on assertion failure or asynchronous cancellation.
Cleanup gets a fresh thirty-second budget and preserves the original callback exception if cleanup also fails.
If startup loses its response, the driver watchdog and environment teardown remain the final cleanup boundaries.
See [terminal Docker scenarios](e2e/CLISpec.md#scoped-terminals).

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
