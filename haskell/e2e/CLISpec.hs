{-# LANGUAGE OverloadedStrings #-}
module CLISpec (spec) where

import Prelude hiding (readFile, writeFile)
import Control.Exception (finally)
import Control.Monad (forM_)
import Data.Aeson hiding (Result)
import qualified Data.ByteString as BS
import qualified Data.Set as Set
import GHC.Clock (getMonotonicTimeNSec)
import Test.Hspec
import Test.CLI.E2E
import Test.CLI.E2E.Internal (withEngine, newDeadline, bounded, jsonRequest, identifier)

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

  it "deadline" $ auditedEnvironment $ \e -> do
    started <- getMonotonicTimeNSec
    run e (command ["sleep", "infinity"]) `shouldThrow` deadlineError
    finished <- getMonotonicTimeNSec
    let seconds = fromIntegral (finished - started) / 1000000000 :: Double
    seconds `shouldSatisfy` (\s -> s >= 50 && s <= 70)

-- Per-example audits run inside Hspec, so --match and --dry-run select actual work.
-- Keep this audit suite sequential because label snapshots include other invocations.
auditedEnvironment :: (Environment -> IO ()) -> IO ()
auditedEnvironment use = do
  imageBefore <- imageID
  containersBefore <- ownedIDs
  withEnvironment "e2e-example:local" (\environment -> do
    containerID environment `shouldSatisfy` (`Set.notMember` containersBefore)
    use environment) `finally` do
      ownedIDs `shouldReturn` containersBefore
      imageID `shouldReturn` imageBefore

imageID :: IO String
imageID = withEngine $ \engine -> do
  deadline <- newDeadline 30
  bounded deadline $ jsonRequest engine "GET" "/images/e2e-example%3Alocal/json" Null identifier

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

assertion :: E2EError -> Bool
assertion (AssertionFailed _) = True
assertion _ = False
deadlineError :: E2EError -> Bool
deadlineError (DeadlineExceeded _) = True
deadlineError _ = False
