{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE ScopedTypeVariables #-}
module Main where

import Prelude hiding (readFile, writeFile)
import Control.Concurrent (threadDelay)
import Control.Concurrent.Async
import Control.Concurrent.MVar
import Control.Exception hiding (AssertionFailed)
import Control.Monad (forM_, void)
import Data.Aeson hiding (Result)
import qualified Codec.Archive.Tar as Tar
import qualified Codec.Archive.Tar.Entry as Tar
import qualified Data.Text as T
import qualified Data.ByteString as BS
import qualified Data.ByteString.Char8 as B8
import qualified Data.ByteString.Lazy as LBS
import Data.IORef
import qualified Data.Map.Strict as Map
import qualified Data.Aeson.KeyMap as KM
import GHC.Clock (getMonotonicTimeNSec)
import qualified Network.Socket.ByteString as NSB
import System.Environment (lookupEnv, setEnv, unsetEnv)
import System.Timeout (timeout)
import Test.Hspec
import Test.CLI.E2E hiding (action)
import Test.CLI.E2E.Internal (Environment(..), Deadline(..), demux, validateCommand, withEngineHost)
import Fixture

main :: IO ()
main = hspec $ do
  describe "command input" $ do
    forM_ [Command [] Nothing [], Command [""] Nothing [], Command ["echo", "x\0y"] Nothing [],
           Command ["echo"] (Just "/x\0") [], Command ["echo"] Nothing ["X=a\0b"]] $ \command ->
      it ("rejects " ++ show command) (validateCommand command `shouldThrow` invalidInput)
    it "preserves empty arguments and environment values" $
      validateCommand (Command ["echo", ""] (Just "/workspace") ["X="]) `shouldReturn` ()
    it "rejects duplicate case names before contacting Docker" $
      runCases "image" [Case "same" (const (pure ())), Case "same" (const (pure ()))] `shouldThrow` invalidInput
  describe "rawstream framing" $ do
    it "keeps streams and arbitrary bytes across every one-byte boundary" $ do
      let bytes = frame 1 (BS.pack [0,255,10]) <> frame 2 "error" <> frame 1 "tail"
      output <- decodeChunks (map BS.singleton (BS.unpack bytes))
      output `shouldBe` Output (BS.pack [0,255,10] <> "tail") "error"
    it "accepts zero-byte frames" $ decodeChunks [frame 1 "", frame 2 "x"] `shouldReturn` Output "" "x"
    it "rejects a truncated header" $ decodeChunks [BS.pack [1,0]] `shouldThrow` infrastructure
    it "retains bytes from a truncated payload" $ do
      ref <- newIORef (Output "" "")
      reader <- chunks [BS.take 11 (frame 1 "abcdef")]
      demux reader ref `shouldThrow` infrastructure
      readIORef ref `shouldReturn` Output "abc" ""
    it "rejects an unknown stream" $ decodeChunks [frame 3 "x"] `shouldThrow` infrastructure
    it "rejects nonzero reserved header fields" $
      decodeChunks [BS.pack [1,1,0,0,0,0,0,0]] `shouldThrow` infrastructure
  describe "controlled HTTP execution" $ do
    it "preserves argv, working directory and environment on the wire and disables compression" $ do
      requests <- newMVar []
      let handler incoming socket = do
            modifyMVar_ requests (pure . (++ [incoming]))
            if target incoming == "/containers/c/exec" then respond socket 201 "{\"Id\":\"e\"}"
            else if target incoming == "/exec/e/start" then respond socket 200 (frame 1 "ok")
            else respond socket 200 "{\"Running\":false,\"ExitCode\":0}"
      withServer handler $ \host -> withEnvironment host 1000000000 $ \environment -> do
        run environment (Command ["program", "", "two words"] (Just "/work dir") ["X=two words", "EMPTY="])
          `shouldReturn` Result 0 "ok" ""
        events <- readMVar requests
        case events of
          created:started:_ -> do
            decodeStrict' (payload created) `shouldBe` Just (object
              ["Cmd" .= (["program", "", "two words"] :: [String]), "WorkingDir" .= ("/work dir" :: String),
               "Env" .= (["X=two words", "EMPTY="] :: [String]), "AttachStdout" .= True,
               "AttachStderr" .= True, "Tty" .= False])
            headerLines started `shouldContain` ["Accept-Encoding: identity"]
          _ -> expectationFailure "missing exec requests"
    it "returns application code 7 with separate streams" $
      withExec (frame 1 "out" <> frame 2 "err") 7 $ \environment ->
        run environment (Command ["program", ""] (Just "/work") ["X="]) `shouldReturn` Result 7 "out" "err"
    forM_ [Result 0 "out" "err", Result 7 "" "err", Result 7 "out" ""] $ \wrong ->
      it ("compares all result fields: " ++ show wrong) $
        withExec (frame 1 "out" <> frame 2 "err") 7 $ \environment ->
          checkCommand environment (Command ["program"] Nothing []) wrong `shouldThrow` assertionFailure
    it "does not treat an HTTP attach failure as a process result" $
      withServer (\incoming socket -> if target incoming == "/containers/c/exec"
        then respond socket 201 "{\"Id\":\"e\"}" else respond socket 500 "daemon failure") $ \host ->
          withEnvironment host 1000000000 $ \environment ->
            run environment (Command ["program"] Nothing []) `shouldThrow` executionFailure
    it "retains complete and incomplete frame output on decode error" $
      withExec (frame 2 "err" <> BS.take 10 (frame 1 "abcdef")) 0 $ \environment ->
        run environment (Command ["program"] Nothing []) `shouldThrow` partialFailure (Output "ab" "err")
    it "deadline closes a stalled output connection and keeps partial bytes" $ do
      closed <- newEmptyMVar
      withServer (\incoming socket -> if target incoming == "/containers/c/exec"
        then respond socket 201 "{\"Id\":\"e\"}" else stallResponse socket (frame 1 "partial") closed) $ \host ->
          withEnvironment host 100000000 $ \environment -> do
            run environment (Command ["program"] Nothing []) `shouldThrow` deadlineWith (Output "partial" "")
            timeout 1000000 (takeMVar closed) `shouldReturn` Just ()
    it "cancellation closes a stalled response" $ do
      attached <- newEmptyMVar
      closed <- newEmptyMVar
      withServer (\incoming socket -> if target incoming == "/containers/c/exec"
        then respond socket 201 "{\"Id\":\"e\"}" else do
          NSB.sendAll socket "HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n"
          putMVar attached ()
          void (NSB.recv socket 1) `finally` putMVar closed ()) $ \host ->
          withEnvironment host 1000000000 $ \environment -> do
            withAsync (run environment (Command ["program"] Nothing [])) $ \worker -> do
              takeMVar attached
              cancel worker
              timeout 1000000 (takeMVar closed) `shouldReturn` Just ()
    it "deadline also bounds waiting for response headers" $ do
      reached <- newEmptyMVar
      closed <- newEmptyMVar
      withServer (\_ socket -> putMVar reached () >> void (NSB.recv socket 1) >> putMVar closed ()) $ \host ->
        withEnvironment host 100000000 $ \environment -> do
          run environment (Command ["program"] Nothing []) `shouldThrow` deadlineWith (Output "" "")
          takeMVar reached
          timeout 1000000 (takeMVar closed) `shouldReturn` Just ()
  describe "file archive transport" $ do
    it "writes exact bytes with mode 0644 into the existing parent" $ do
      received <- newEmptyMVar
      withServer (\incoming socket -> putMVar received incoming >> respond socket 200 "") $ \host ->
        withEnvironment host 1000000000 $ \environment -> do
          writeFile environment "/workspace/data.bin" (BS.pack [0,255,10])
          Incoming method path body headers <- takeMVar received
          headers `shouldContain` ["Accept-Encoding: identity"]
          headers `shouldContain` ["Content-Type: application/x-tar"]
          method `shouldBe` "PUT"
          path `shouldBe` "/containers/c/archive?path=%2Fworkspace"
          case Tar.read (LBS.fromStrict body) of
            Tar.Next entry Tar.Done -> do
              Tar.entryPermissions entry `shouldBe` 0o644
              Tar.entryContent entry `shouldBe` Tar.NormalFile (LBS.pack [0,255,10]) 3
            archive -> expectationFailure (show archive)
    it "reads exact bytes and rejects a file comparison mismatch" $ do
      path <- either (throwIO . userError) pure (Tar.toTarPath False "data.bin")
      let archive = LBS.toStrict (Tar.write [Tar.fileEntry path (LBS.pack [0,255,10])])
      withServer (\_ socket -> respond socket 200 archive) $ \host ->
        withEnvironment host 1000000000 $ \environment -> do
          readFile environment "/workspace/data.bin" `shouldReturn` BS.pack [0,255,10]
          checkFile environment "/workspace/data.bin" "wrong" `shouldThrow` assertionFailure
    it "rejects malformed archives" $
      withServer (\_ socket -> respond socket 200 "not a tar") $ \host ->
        withEnvironment host 1000000000 $ \environment ->
          readFile environment "/workspace/file" `shouldThrow` infrastructure
    it "rejects a directory instead of treating it as a file" $ do
      path <- either (throwIO . userError) pure (Tar.toTarPath True "directory")
      let archive = LBS.toStrict (Tar.write [Tar.directoryEntry path])
      withServer (\_ socket -> respond socket 200 archive) $ \host ->
        withEnvironment host 1000000000 $ \environment ->
          readFile environment "/workspace/directory" `shouldThrow` infrastructure
    it "rejects NUL file paths before transport" $
      withEnvironment "http://127.0.0.1:1" 1000000000 $ \environment ->
        writeFile environment "/workspace/x\0y" "bytes" `shouldThrow` invalidInput
  describe "physical path probes" $ do
    it "rejects a dangling link as missing" $
      withCommands (\argv -> if argv == ["test", "-L", "/dangling"] then Result 0 "" "" else Result 1 "" "") $ \e ->
        checkMissing e "/dangling" `shouldThrow` assertionFailure
    it "rejects a symbolic link to a regular file" $
      withCommands (const (Result 0 "" "")) $ \e -> checkRegularFile e "/link" `shouldThrow` assertionFailure
    it "rejects a symbolic link to a directory" $
      withCommands (const (Result 0 "" "")) $ \e -> checkDirectory e "/link" `shouldThrow` assertionFailure
    it "compares the literal target rather than its resolved path" $
      withCommands (\argv -> if take 1 argv == ["readlink"] then Result 0 "relative\n" "" else Result 0 "" "") $ \e -> do
        checkSymlink e "/link" "relative"
        checkSymlink e "/link" "/resolved/relative" `shouldThrow` assertionFailure
    it "rejects probe code 2 as infrastructure failure" $
      withCommands (const (Result 2 "" "bad probe")) $ \e -> checkMissing e "/path" `shouldThrow` infrastructure
  describe "owned case lifecycle" $ do
    it "creates unique identities, preserves image ID and removes cases and lease" $
      withDaemon False False $ \host resources requests -> withDockerHost host $ do
        identities <- newMVar []
        results <- runCases "local:image" [Case (show n) (\e -> modifyMVar_ identities (pure . (containerID e:))) | n <- [1..5 :: Int]]
        map caseName results `shouldBe` map show [1..5 :: Int]
        mapM_ assertSuccess results
        observed <- readMVar identities
        Map.size (Map.fromList [(ident, ()) | ident <- observed]) `shouldBe` 5
        readMVar resources `shouldReturn` Map.empty
        events <- readMVar requests
        let createdImages = [image | Incoming "POST" path bytes _ <- events, "/containers/create?" `BS.isPrefixOf` path,
              Just (Object config) <- [decodeStrict' bytes], Just (String image) <- [KM.lookup "Image" config]]
        createdImages `shouldBe` replicate 6 "sha256:local"
    it "removes an owned lease even when its create response fails" $
      withDaemonResponse True False False $ \host resources _ -> withDockerHost host $ do
        runCases "local:image" [Case "never starts" (const (pure ()))] `shouldThrow` infrastructure
        readMVar resources `shouldReturn` Map.empty
    it "cancellation removes both an active case and its lease" $
      withDaemon False False $ \host resources _ -> withDockerHost host $ do
        entered <- newEmptyMVar
        blocked <- newEmptyMVar
        withAsync (runCases "local:image" [Case "cancelled" (\_ -> putMVar entered () >> takeMVar blocked)]) $ \worker -> do
          takeMVar entered
          cancel worker
          readMVar resources `shouldReturn` Map.empty
    it "reports startup failure and still removes both owned resources" $
      withDaemon True False $ \host resources _ -> withDockerHost host $ do
        results <- runCases "local:image" [Case "startup" (const (pure ()))]
        case results of
          [result] -> outcome result `shouldSatisfy` either (const True) (const False)
          _ -> expectationFailure "missing case outcome"
        readMVar resources `shouldReturn` Map.empty
    it "reports cleanup failure rather than a successful outcome" $
      withDaemon False True $ \host _ _ -> withDockerHost host $
        runCases "local:image" [Case "cleanup" (const (pure ()))] `shouldThrow` cleanupFailure
    it "runs no more than two case actions concurrently" $
      withDaemon False False $ \host _ _ -> withDockerHost host $ do
        active <- newMVar (0 :: Int, 0 :: Int)
        let action' _ = bracket_
              (modifyMVar_ active (\(count, peak) -> pure (count + 1, max peak (count + 1))))
              (modifyMVar_ active (\(count, peak) -> pure (count - 1, peak))) (threadDelay 20000)
        results <- runCases "local:image" [Case (show n) action' | n <- [1..8 :: Int]]
        mapM_ assertSuccess results
        readMVar active `shouldReturn` (0,2)
  describe "unsupported backend" $ do
    it "rejects TLS explicitly" $ withEngineHost "https://localhost:2376" (const (pure ())) `shouldThrow` invalidInput

invalidInput, infrastructure, executionFailure, assertionFailure, cleanupFailure :: E2EError -> Bool
invalidInput (InvalidInput _) = True
invalidInput _ = False
infrastructure (Infrastructure _) = True
infrastructure _ = False
executionFailure (ExecutionFailed _ _) = True
executionFailure _ = False
assertionFailure (AssertionFailed _) = True
assertionFailure _ = False
cleanupFailure (CleanupFailed _ _) = True
cleanupFailure _ = False
partialFailure :: Output -> E2EError -> Bool
partialFailure want (ExecutionFailed _ actual) = actual == want
partialFailure _ _ = False
deadlineWith :: Output -> E2EError -> Bool
deadlineWith want (DeadlineExceeded actual) = actual == want
deadlineWith _ _ = False

chunks :: [BS.ByteString] -> IO (IO BS.ByteString)
chunks bytes = do
  ref <- newIORef bytes
  pure (atomicModifyIORef' ref (\remaining -> case remaining of [] -> ([], BS.empty); x:xs -> (xs,x)))
decodeChunks :: [BS.ByteString] -> IO Output
decodeChunks bytes = do
  ref <- newIORef (Output "" "")
  reader <- chunks bytes
  demux reader ref
  readIORef ref

withEnvironment :: String -> Word -> (Environment -> IO a) -> IO a
withEnvironment host duration action = withEngineHost host $ \engine -> do
  now <- getMonotonicTimeNSec
  action (Environment engine "c" (Deadline (now + fromIntegral duration)))
withExec :: BS.ByteString -> Int -> (Environment -> IO a) -> IO a
withExec bytes code action = withServer handler $ \host -> withEnvironment host 1000000000 action
  where
    handler incoming socket
      | target incoming == "/containers/c/exec" = respond socket 201 "{\"Id\":\"e\"}"
      | target incoming == "/exec/e/start" = respond socket 200 bytes
      | otherwise = respond socket 200 (LBS.toStrict (encode (object ["Running" .= False, "ExitCode" .= code])))

withCommands :: ([String] -> Result) -> (Environment -> IO a) -> IO a
withCommands answer use = do
  current <- newMVar (Result 0 "" "")
  let handler incoming socket
        | target incoming == "/containers/c/exec" =
            case decodeStrict' (payload incoming) of
              Just (Object config) -> case KM.lookup "Cmd" config of
                Just (Array values) -> do
                  let argv = [T.unpack value | String value <- foldr (:) [] values]
                  modifyMVar_ current (const (pure (answer argv)))
                  respond socket 201 "{\"Id\":\"e\"}"
                _ -> respond socket 400 "invalid command"
              _ -> respond socket 400 "invalid json"
        | target incoming == "/exec/e/start" = do
            result <- readMVar current
            respond socket 200 (frame 1 (stdout result) <> frame 2 (stderr result))
        | otherwise = do
            result <- readMVar current
            respond socket 200 (LBS.toStrict (encode (object ["Running" .= False, "ExitCode" .= exitCode result])))
  withServer handler $ \host -> withEnvironment host 1000000000 use

assertSuccess :: CaseResult -> IO ()
assertSuccess result = either throwIO pure (outcome result)

withDockerHost :: String -> IO a -> IO a
withDockerHost host use = bracket (mapM remember variables) (mapM_ restore) $ \_ -> do
  mapM_ apply variables
  use
  where
    variables = [("DOCKER_HOST", Just host), ("DOCKER_TLS_VERIFY", Nothing), ("DOCKER_CERT_PATH", Nothing), ("DOCKER_API_VERSION", Nothing)]
    remember (key, _) = (,) key <$> lookupEnv key
    restore (key, value) = maybe (unsetEnv key) (setEnv key) value
    apply = restore

withDaemon :: Bool -> Bool -> (String -> MVar (Map.Map BS.ByteString Value) -> MVar [Incoming] -> IO a) -> IO a
withDaemon = withDaemonResponse False

withDaemonResponse :: Bool -> Bool -> Bool -> (String -> MVar (Map.Map BS.ByteString Value) -> MVar [Incoming] -> IO a) -> IO a
withDaemonResponse createResponseFails startupFails removalFails use = do
  resources <- newMVar Map.empty
  events <- newMVar []
  let handler incoming socket = do
        modifyMVar_ events (pure . (++ [incoming]))
        let path = target incoming
            ident = BS.takeWhile (\b -> b /= 47 && b /= 63) (BS.drop 12 path)
        if "/images/" `BS.isPrefixOf` path then respond socket 200 "{\"Id\":\"sha256:local\"}"
        else if "/containers/create?name=" `BS.isPrefixOf` path then do
          let key = BS.drop 24 path
          case decodeStrict' (payload incoming) of
            Nothing -> respond socket 400 "invalid json"
            Just config -> do
              modifyMVar_ resources (pure . Map.insert key config)
              if createResponseFails then respond socket 500 "create response lost"
              else respond socket 201 (LBS.toStrict (encode (object ["Id" .= B8.unpack key])))
        else if "/start" `BS.isSuffixOf` path then respond socket (if startupFails then 500 else 204) ""
        else if verb incoming == "DELETE" then
          if removalFails then respond socket 500 "cleanup failed"
          else modifyMVar_ resources (pure . Map.delete ident) >> respond socket 204 ""
        else do
          owned <- Map.lookup ident <$> readMVar resources
          case owned of
            Nothing -> respond socket 404 "missing"
            Just config -> respond socket 200 (LBS.toStrict (encode (object ["Id" .= B8.unpack ident, "Config" .= config])))
  withServer handler $ \host -> use host resources events
