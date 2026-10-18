{-# LANGUAGE OverloadedStrings #-}
module CLISpec (spec) where

import Prelude hiding (readFile, writeFile)
import Control.Exception (finally, throwIO)
import Control.Monad (forM_)
import Data.Aeson hiding (Result)
import qualified Data.ByteString as BS
import qualified Data.Map.Strict as Map
import qualified Data.Set as Set
import GHC.Clock (getMonotonicTimeNSec)
import Test.Hspec hiding (before, after)
import Test.CLI.E2E
import Test.CLI.E2E.Internal (withEngine, newDeadline, bounded, jsonRequest, identifier, encodeSegment)

spec :: Spec
spec = describe "caller-built Docker image" $ do
  it "greeting" $ auditedEnvironment $ \e ->
    checkCommand e (command ["echo", "hello from e2e"]) (Result 0 "hello from e2e\n" "")

  it "file workflow" $ auditedEnvironment $ \e -> do
    checkCommand e (command ["sh", "-c", "echo first > /workspace/result.txt"]) (Result 0 "" "")
    checkCommand e (command ["sh", "-c", "echo second >> /workspace/result.txt"]) (Result 0 "" "")
    checkCommand e (command ["cat", "/workspace/result.txt"]) (Result 0 "first\nsecond\n" "")
    checkFile e "/workspace/result.txt" "first\nsecond\n"
    writeFile e "/workspace/bytes" (BS.pack [0,255,10])
    readFile e "/workspace/bytes" `shouldReturn` BS.pack [0,255,10]
    checkCommand e (command ["stat", "-c", "%a", "/workspace/bytes"]) (Result 0 "644\n" "")

  it "expected failure" $ auditedEnvironment $ \e ->
    checkCommand e (command ["sh", "-c", "echo expected-failure; exit 7"]) (Result 7 "expected-failure\n" "")

  it "separate streams" $ auditedEnvironment $ \e -> do
    let c = Command ["sh", "-c", "printf '%s:%s\\n' \"$HOME\" \"$PWD\"; printf 'diagnostic\\n' >&2; exit 7"]
          (Just "/workspace") ["HOME=/workspace/synthetic-home"]
        expected = Result 7 "/workspace/synthetic-home:/workspace\n" "diagnostic\n"
    checkCommand e c expected
    run e c `shouldReturn` expected

  it "physical paths" $ auditedEnvironment $ \e -> do
    writeFile e "/workspace/result.txt" "fixture\n"
    forM_ [["mkdir", "/workspace/directory"], ["ln", "-s", "result.txt", "/workspace/link"],
           ["ln", "-s", "missing.txt", "/workspace/dangling"], ["ln", "-s", "directory", "/workspace/dirlink"]] $ \argv ->
      checkCommand e (command argv) (Result 0 "" "")
    checkRegularFile e "/workspace/result.txt"
    checkDirectory e "/workspace/directory"
    checkMissing e "/workspace/missing.txt"
    checkSymlink e "/workspace/link" "result.txt"
    checkSymlink e "/workspace/dangling" "missing.txt"
    checkMissing e "/workspace/dangling" `shouldThrow` assertion
    checkRegularFile e "/workspace/link" `shouldThrow` assertion
    checkDirectory e "/workspace/dirlink" `shouldThrow` assertion
    checkSymlink e "/workspace/link" "/workspace/result.txt" `shouldThrow` assertion

  it "filesystem snapshots" $ auditedEnvironment $ \e -> do
    capability <- installedDriver e
    writeFile e "/workspace/bytes" (BS.pack [0,255,13,10])
    checkCommand e (command ["sh", "-c", "mkdir /workspace/directory; ln -s bytes /workspace/link; ln -s absent /workspace/dangling"])
      (Result 0 "" "")
    let paths = ["/workspace/missing", "/workspace/link", "/workspace/dangling", "/workspace/directory", "/workspace/bytes"]
    before <- captureSnapshot capability paths
    snapshotEntries before `shouldBe` Map.fromList
      [("/workspace/bytes", RegularFile (BS.pack [0,255,13,10])), ("/workspace/directory", Directory),
       ("/workspace/link", Symlink "bytes"), ("/workspace/dangling", Symlink "absent"), ("/workspace/missing", Missing)]
    checkSnapshot capability before
    checkCommand e (command ["sh", "-c", "touch /workspace/directory/ignored; chmod 600 /workspace/bytes"])
      (Result 0 "" "")
    captureSnapshot capability paths `shouldReturn` before
    writeFile e "/workspace/bytes" (BS.pack [0,254,13,10])
    checkCommand e (command ["sh", "-c", "rmdir /workspace/missing 2>/dev/null; mkdir /workspace/missing; rm /workspace/link; ln -s ./bytes /workspace/link; rm -r /workspace/directory; ln -s missing /workspace/directory"])
      (Result 0 "" "")
    after <- captureSnapshot capability paths
    map (\difference -> (differencePath difference, differenceChange difference)) (diffSnapshots before after)
      `shouldBe` [("/workspace/bytes", ContentChanged), ("/workspace/directory", KindChanged),
                  ("/workspace/link", TargetChanged), ("/workspace/missing", ExistenceChanged)]
    checkSnapshot capability before `shouldThrow` assertion
    readFile e "/workspace/bytes" `shouldReturn` BS.pack [0,254,13,10]

  it "snapshot rejects unsupported physical kinds" $ auditedEnvironment $ \e -> do
    capability <- installedDriver e
    checkCommand e (command ["mkfifo", "/workspace/fifo"]) (Result 0 "" "")
    captureSnapshot capability ["/workspace/fifo"] `shouldThrow` infrastructureError

  it "snapshot does not treat a non-directory parent as absence" $ auditedEnvironment $ \e -> do
    capability <- installedDriver e
    writeFile e "/workspace/file" "bytes"
    captureSnapshot capability ["/workspace/file/child"] `shouldThrow` infrastructureError

  it "terminal raw bytes and restoration" $ auditedEnvironment $ \e -> do
    capability <- installedDriver e
    let script = "saved=$(stty -g); stty raw -echo; printf '\\033[?25lprompt\\033[0m' >&2; dd bs=1 count=2 of=/workspace/input 2>/dev/null; stty \"$saved\"; printf '\\033[?25h' >&2; printf '%s:%s' \"$PWD\" \"$TOKEN\"; printf '\\000\\377'"
    withTerminal capability (Command ["sh", "-c", script] (Just "/workspace") ["TOKEN=custom"]) defaultTerminalOptions $ \terminal -> do
      ready <- expectTerminal terminal TerminalOutput "\ESC[?25lprompt\ESC[0m" 5
      terminalStatus ready `shouldBe` TerminalRunning
      terminalTermiosRestored ready `shouldBe` False
      _ <- sendTerminal terminal (BS.pack [3,4])
      finished <- waitTerminal terminal 5
      terminalStdout finished `shouldBe` ("/workspace:custom" <> BS.pack [0,255])
      terminalOutput finished `shouldBe` "\ESC[?25lprompt\ESC[0m\ESC[?25h"
      terminalUnreadInput finished `shouldBe` BS.empty
      terminalStatus finished `shouldBe` TerminalExited 0
      terminalTermiosRestored finished `shouldBe` True
      terminalCurrentTermios finished `shouldBe` terminalInitialTermios finished
      checkFile e "/workspace/input" (BS.pack [3,4])

  it "terminal pipe input remains unread on no-op failure" $ auditedEnvironment $ \e -> do
    capability <- installedDriver e
    let input = BS.pack [0,255,10]
    withTerminal capability (command ["sh", "-c", "printf ready >&2; exit 7"])
      defaultTerminalOptions { terminalInput = Just input } $ \terminal -> do
        finished <- waitTerminal terminal 5
        terminalStdout finished `shouldBe` BS.empty
        terminalOutput finished `shouldBe` "ready"
        terminalUnreadInput finished `shouldBe` input
        terminalStatus finished `shouldBe` TerminalExited 7
        terminalTermiosRestored finished `shouldBe` True
        sendTerminal terminal "input" `shouldThrow` terminalFailureError

  it "terminal pipe input delivers exact bytes and EOF" $ auditedEnvironment $ \e -> do
    capability <- installedDriver e
    let input = BS.pack [0,255,13,10]
    withTerminal capability (command ["cat"]) defaultTerminalOptions { terminalInput = Just input } $ \terminal -> do
      finished <- waitTerminal terminal 5
      terminalStdout finished `shouldBe` input
      terminalOutput finished `shouldBe` BS.empty
      terminalUnreadInput finished `shouldBe` BS.empty
      terminalStatus finished `shouldBe` TerminalExited 0

  it "terminal resize and signal" $ auditedEnvironment $ \e -> do
    capability <- installedDriver e
    let script = "trap 'stty size >&2' WINCH; printf ready >&2; while :; do sleep 0.1; done"
    withTerminal capability (command ["sh", "-c", script]) defaultTerminalOptions $ \terminal -> do
      _ <- expectTerminal terminal TerminalOutput "ready" 5
      _ <- resizeTerminal terminal 12 34
      _ <- expectTerminal terminal TerminalOutput "12 34\r\n" 5
      _ <- signalTerminal terminal Terminate
      finished <- waitTerminal terminal 5
      terminalStatus finished `shouldBe` TerminalSignaled 15
      terminalStdout finished `shouldBe` BS.empty

  it "terminal scope closes a surviving process group" $ auditedEnvironment $ \e -> do
    capability <- installedDriver e
    withTerminal capability (command ["sh", "-c", "echo $$ > /workspace/terminal.pid; printf ready >&2; sleep infinity"])
      defaultTerminalOptions $ \terminal -> do
        _ <- expectTerminal terminal TerminalOutput "ready" 5
        pure ()
    checkCommand e (command ["sh", "-c", "! kill -0 $(cat /workspace/terminal.pid) 2>/dev/null"])
      (Result 0 "" "")

  it "nonroot image installs one driver for snapshots and terminals" $ auditedImage "e2e-example-nonroot:local" $ \e -> do
    capability <- installedDriver e
    checkCommand e (command ["sh", "-c", "printf owned > /tmp/state"]) (Result 0 "" "")
    captured <- captureSnapshot capability ["/tmp/state", "/tmp/absent"]
    snapshotEntries captured `shouldBe` Map.fromList [("/tmp/state", RegularFile "owned"), ("/tmp/absent", Missing)]
    withTerminal capability (command ["sh", "-c", "printf ready >&2; printf success"]) defaultTerminalOptions $ \terminal -> do
      _ <- expectTerminal terminal TerminalOutput "ready" 5
      finished <- waitTerminal terminal 5
      terminalStdout finished `shouldBe` "success"
      terminalOutput finished `shouldBe` "ready"
      terminalStatus finished `shouldBe` TerminalExited 0
      terminalTermiosRestored finished `shouldBe` True

  it "nonroot snapshot inspection permission failure is not absence" $ auditedImage "e2e-example-nonroot:local" $ \e -> do
    capability <- installedDriver e
    checkCommand e (command ["sh", "-c", "mkdir /tmp/private; touch /tmp/private/file; chmod 000 /tmp/private"])
      (Result 0 "" "")
    captureSnapshot capability ["/tmp/private/file"] `shouldThrow` infrastructureError

  it "nonroot snapshot file read permission failure is not absence" $ auditedImage "e2e-example-nonroot:local" $ \e -> do
    capability <- installedDriver e
    checkCommand e (command ["sh", "-c", "printf secret > /tmp/unreadable; chmod 000 /tmp/unreadable"])
      (Result 0 "" "")
    captureSnapshot capability ["/tmp/unreadable"] `shouldThrow` infrastructureError

  it "deadline" $ auditedEnvironment $ \e -> do
    started <- getMonotonicTimeNSec
    run e (command ["sleep", "infinity"]) `shouldThrow` deadlineError
    finished <- getMonotonicTimeNSec
    let seconds = fromIntegral (finished - started) / 1000000000 :: Double
    seconds `shouldSatisfy` (\s -> s >= 50 && s <= 70)

-- Per-example audits run inside Hspec, so --match and --dry-run select actual work.
-- Keep this audit suite sequential because label snapshots include other invocations.
auditedEnvironment :: (Environment -> IO ()) -> IO ()
auditedEnvironment = auditedImage "e2e-example:local"

auditedImage :: String -> (Environment -> IO ()) -> IO ()
auditedImage image use = do
  imageBefore <- imageID image
  containersBefore <- ownedIDs
  withEnvironment image (\environment -> do
    containerID environment `shouldSatisfy` (`Set.notMember` containersBefore)
    use environment) `finally` do
      ownedIDs `shouldReturn` containersBefore
      imageID image `shouldReturn` imageBefore

imageID :: String -> IO String
imageID image = withEngine $ \engine -> do
  deadline <- newDeadline 30
  bounded deadline $ jsonRequest engine "GET" ("/images/" ++ encodeSegment image ++ "/json") Null identifier

ownedIDs :: IO (Set.Set String)
ownedIDs = withEngine $ \engine -> do
  deadline <- newDeadline 30
  bounded deadline $ do
    ids <- jsonRequest engine "GET"
      "/containers/json?all=true&filters=%7B%22label%22%3A%5B%22org.e2e.haskell.owner%22%5D%7D" Null
      (withArray "containers" $ \values -> mapM (withObject "container" (\o -> o .: "Id")) (foldr (:) [] values))
    pure (Set.fromList ids)

command :: [String] -> Command
command argv = Command argv Nothing []

installedDriver :: Environment -> IO Driver
installedDriver environment = do
  architecture <- run environment (command ["uname", "-m"])
  suffix <- case architecture of
    Result 0 "aarch64\n" "" -> pure "arm64"
    Result 0 "x86_64\n" "" -> pure "amd64"
    other -> throwIO (Infrastructure ("unsupported acceptance architecture: " ++ show other))
  bytes <- BS.readFile ("../go/target/driver-linux-" ++ suffix)
  installDriver environment bytes

terminalFailureError :: TerminalError -> Bool
terminalFailureError _ = True

assertion :: E2EError -> Bool
assertion (AssertionFailed _) = True
assertion _ = False
infrastructureError :: E2EError -> Bool
infrastructureError (Infrastructure _) = True
infrastructureError _ = False

deadlineError :: E2EError -> Bool
deadlineError (DeadlineExceeded _) = True
deadlineError _ = False
