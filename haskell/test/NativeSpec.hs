{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE ScopedTypeVariables #-}
module NativeSpec (spec) where

import Prelude hiding (readFile, writeFile)
import Control.Concurrent.Async
import Control.Concurrent.MVar
import Control.Exception
import Control.Monad (forM_, void)
import Data.Aeson.Types (Pair)
import Data.Aeson hiding (Result)
import qualified Codec.Archive.Tar as Tar
import qualified Codec.Archive.Tar.Entry as Tar
import qualified Data.Text.Encoding as TE
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
import Test.Hspec hiding (before, after)
import Test.CLI.E2E
import Test.CLI.E2E.Internal (Environment(..), Driver(..), Deadline(..), demux, validateCommand, withEngineHost)
import Fixture

spec :: Spec
spec = do
  describe "command input" $ do
    forM_ [Command [] Nothing [], Command [""] Nothing [], Command ["echo", "x\0y"] Nothing [],
           Command ["echo"] (Just "/x\0") [], Command ["echo"] Nothing ["X=a\0b"]] $ \command ->
      it ("rejects " ++ show command) (validateCommand command `shouldThrow` invalidInput)
    it "preserves empty arguments and environment values" $
      validateCommand (Command ["echo", ""] (Just "/workspace") ["X="]) `shouldReturn` ()
    forM_ ["", "image\0name"] $ \image ->
      it ("rejects invalid image " ++ show image ++ " before contacting Docker") $
        withEnvironment image (const (pure ())) `shouldThrow` invalidInput
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
      withServer handler $ \host -> withTestEnvironment host 1000000000 $ \environment -> do
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
    it "does not treat an HTTP attach failure as a process result" $
      withServer (\incoming socket -> if target incoming == "/containers/c/exec"
        then respond socket 201 "{\"Id\":\"e\"}" else respond socket 500 "daemon failure") $ \host ->
          withTestEnvironment host 1000000000 $ \environment ->
            run environment (Command ["program"] Nothing []) `shouldThrow` executionFailure
    it "retains complete and incomplete frame output on decode error" $
      withExec (frame 2 "err" <> BS.take 10 (frame 1 "abcdef")) 0 $ \environment ->
        run environment (Command ["program"] Nothing []) `shouldThrow` partialFailure (Output "ab" "err")
    it "deadline closes a stalled output connection and keeps partial bytes" $ do
      closed <- newEmptyMVar
      withServer (\incoming socket -> if target incoming == "/containers/c/exec"
        then respond socket 201 "{\"Id\":\"e\"}" else stallResponse socket (frame 1 "partial") closed) $ \host ->
          withTestEnvironment host 100000000 $ \environment -> do
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
          withTestEnvironment host 1000000000 $ \environment -> do
            withAsync (run environment (Command ["program"] Nothing [])) $ \worker -> do
              takeMVar attached
              cancel worker
              timeout 1000000 (takeMVar closed) `shouldReturn` Just ()
    it "deadline also bounds waiting for response headers" $ do
      reached <- newEmptyMVar
      closed <- newEmptyMVar
      withServer (\_ socket -> putMVar reached () >> void (NSB.recv socket 1) >> putMVar closed ()) $ \host ->
        withTestEnvironment host 100000000 $ \environment -> do
          run environment (Command ["program"] Nothing []) `shouldThrow` deadlineWith (Output "" "")
          takeMVar reached
          timeout 1000000 (takeMVar closed) `shouldReturn` Just ()
  describe "file archive transport" $ do
    it "writes exact bytes with mode 0644 into the existing parent" $ do
      received <- newEmptyMVar
      withServer (\incoming socket -> putMVar received incoming >> respond socket 200 "") $ \host ->
        withTestEnvironment host 1000000000 $ \environment -> do
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
    it "reads exact bytes" $ do
      path <- either (throwIO . userError) pure (Tar.toTarPath False "data.bin")
      let archive = LBS.toStrict (Tar.write [Tar.fileEntry path (LBS.pack [0,255,10])])
      withServer (\_ socket -> respond socket 200 archive) $ \host ->
        withTestEnvironment host 1000000000 $ \environment -> do
          readFile environment "/workspace/data.bin" `shouldReturn` BS.pack [0,255,10]
    it "rejects malformed archives" $
      withServer (\_ socket -> respond socket 200 "not a tar") $ \host ->
        withTestEnvironment host 1000000000 $ \environment ->
          readFile environment "/workspace/file" `shouldThrow` infrastructure
    it "rejects a directory instead of treating it as a file" $ do
      path <- either (throwIO . userError) pure (Tar.toTarPath True "directory")
      let archive = LBS.toStrict (Tar.write [Tar.directoryEntry path])
      withServer (\_ socket -> respond socket 200 archive) $ \host ->
        withTestEnvironment host 1000000000 $ \environment ->
          readFile environment "/workspace/directory" `shouldThrow` infrastructure
    it "rejects NUL file paths before transport" $
      withTestEnvironment "http://127.0.0.1:1" 1000000000 $ \environment ->
        writeFile environment "/workspace/x\0y" "bytes" `shouldThrow` invalidInput
  describe "physical path observations" $ do
    forM_ ["", "/bad\0path"] $ \path ->
      it ("rejects invalid observation paths before transport: " ++ show path) $
        withTestEnvironment "http://127.0.0.1:1" 1000000000 $ \e -> do
          pathKind e path `shouldThrow` invalidInput
          readSymlink e path `shouldThrow` invalidInput
    forM_ [("-L", PathSymlink), ("-f", PathRegularFile), ("-d", PathDirectory), ("-e", PathOther)] $ \(flag, kind) ->
      it ("observes " ++ show kind ++ " without following links") $
        withCommands (\argv -> if argv == ["test", flag, "/path"] then Result 0 "" "" else Result 1 "" "") $ \e ->
          pathKind e "/path" `shouldReturn` kind
    it "observes a missing path" $
      withCommands (const (Result 1 "" "")) $ \e -> pathKind e "/missing" `shouldReturn` PathMissing
    it "classifies a dangling link before following its target" $
      withCommands (\argv -> if argv == ["test", "-L", "/dangling"] then Result 0 "" "" else Result 1 "" "") $ \e ->
        pathKind e "/dangling" `shouldReturn` PathSymlink
    it "preserves literal target bytes and removes only the readlink delimiter" $
      withCommands (const (Result 0 (BS.pack [255] <> "relative\n\n") "")) $ \e ->
        readSymlink e "/link" `shouldReturn` (BS.pack [255] <> "relative\n")
    it "rejects probe code 2 as infrastructure failure" $
      withCommands (const (Result 2 "" "bad probe")) $ \e -> pathKind e "/path" `shouldThrow` infrastructure
    forM_ [Result 1 "" "not a link", Result 0 "unterminated" "", Result 0 "target\n" "diagnostic"] $ \failure ->
      it ("rejects failed or malformed link reads: " ++ show failure) $
        withCommands (const failure) $ \e -> readSymlink e "/path" `shouldThrow` infrastructure
  describe "driver installation" $ do
    it "transfers exact executable bytes without chmod on a root-owned archive entry" $ do
      uploaded <- newEmptyMVar
      let directory = "/tmp/caseyard-driver-Ab123"
          handler incoming socket
            | verb incoming == "PUT" = putMVar uploaded incoming >> respond socket 200 ""
            | target incoming == "/containers/c/exec" = respond socket 201 "{\"Id\":\"e\"}"
            | target incoming == "/exec/e/start" = respond socket 200 (frame 1 (directory <> "\n"))
            | otherwise = respond socket 200 "{\"Running\":false,\"ExitCode\":0}"
      withServer handler $ \host -> withTestEnvironment host 1000000000 $ \e -> do
        Driver _ executable <- installDriver e (BS.pack [0,255,10])
        executable `shouldBe` "/tmp/caseyard-driver-Ab123/driver"
        incoming <- takeMVar uploaded
        target incoming `shouldBe` "/containers/c/archive?path=%2Ftmp%2Fcaseyard-driver-Ab123"
        case Tar.read (LBS.fromStrict (payload incoming)) of
          Tar.Next entry Tar.Done -> do
            Tar.entryPermissions entry `shouldBe` 0o755
            Tar.entryContent entry `shouldBe` Tar.NormalFile (LBS.pack [0,255,10]) 3
          archive -> expectationFailure (show archive)
        installDriver e "another binary" `shouldThrow` invalidInput
    it "rejects empty binary bytes before transport" $
      withTestEnvironment "http://127.0.0.1:1" 1000000000 $ \e -> installDriver e "" `shouldThrow` invalidInput
    forM_ ["/arbitrary/path\n", "/tmp/caseyard-driver-../bad\n", "/tmp/caseyard-driver-\n", "one\ntwo\n"] $ \directory ->
      it ("rejects invalid directory response: " ++ show directory) $
        withExec (frame 1 directory) 0 $ \e -> installDriver e "binary" `shouldThrow` infrastructure
  describe "filesystem snapshots" $ do
    it "captures only explicit paths with exact file bytes and literal dangling links" $ do
      let bytes = BS.pack [0,255,13,10]
          entries = [("/bytes", RegularFile bytes), ("/dir", Directory),
            ("/link", Symlink (BS.pack [255,10])), ("/missing", Missing)]
      withCommands (const (snapshotReply entries)) $ \e -> do
        captured <- captureSnapshot (Driver e "/driver") ["/missing", "/link", "/dir", "/bytes", "/bytes"]
        captured `shouldBe` FileSystemSnapshot (Map.fromList entries)
        captureSnapshot (Driver e "/driver") (Map.keys (snapshotEntries captured)) `shouldReturn` captured
    forM_ [Result 1 "" "permission denied", Result 1 "" "not a directory", Result 1 "" "unsupported physical path kind",
           Result 0 "invalid JSON" "", Result 0 "{\"kind\":\"socket\"}" "", Result 0 "{\"kind\":\"symlink\"}" ""] $ \failure ->
      it ("fails instead of recording absence for " ++ show failure) $
        withCommands (const failure) $ \e -> captureSnapshot (Driver e "/driver") ["/path"] `shouldThrow` infrastructure
    it "propagates file read failures rather than recording missing" $
      withCommands (const (Result 1 "{\"ok\":false,\"error\":\"file read failed\",\"observation\":null}" "")) $ \e ->
        captureSnapshot (Driver e "/driver") ["/file"] `shouldThrow` infrastructure
    forM_ [snapshotReply [], snapshotReply [("/path", Missing), ("/path", Missing)],
           snapshotReply [("/unselected", Missing)],
           Result 0 "{\"ok\":true,\"value\":[{\"path\":\"/path\",\"kind\":\"file\",\"data\":[256],\"target\":[]}]}" ""] $ \malformed ->
      it ("rejects incomplete, duplicate, unexpected or invalid-byte snapshots " ++ show malformed) $
        withCommands (const malformed) $ \e -> captureSnapshot (Driver e "/driver") ["/path"] `shouldThrow` infrastructure
    forM_ ["", "/bad\0path", "/link/"] $ \path ->
      it ("rejects invalid snapshot path before transport: " ++ show path) $
        withTestEnvironment "http://127.0.0.1:1" 1000000000 $ \e ->
          captureSnapshot (Driver e "/driver") [path] `shouldThrow` invalidInput
  describe "scoped terminals" $ do
    it "preserves raw observations and sends typed session operations through the shared driver" $ do
      requests <- newMVar []
      let reply request = do
            modifyMVar_ requests (pure . (++ [request]))
            pure (terminalReply request)
          options = TerminalOptions 24 80 (Just (BS.pack [0,255,10])) 12
          observed = TerminalObservation (BS.pack [0,255]) "\ESC[0m" (BS.pack [10,255])
            (TerminalExited 7) True [TermiosInteger 1, TermiosArray [TermiosInteger 2]] [TermiosInteger 1, TermiosArray [TermiosInteger 2]]
      withTerminalReplies reply $ \e -> do
        withTerminal e (Command ["app", "", "two words"] (Just "/workspace") ["EMPTY="]) options $ \terminal -> do
          observeTerminal terminal `shouldReturn` observed
          expectTerminal terminal TerminalOutput "prompt" 2 `shouldReturn` observed
          sendTerminal terminal (BS.pack [3,4]) `shouldReturn` observed
          resizeTerminal terminal 1 2 `shouldReturn` observed
          signalTerminal terminal Interrupt `shouldReturn` observed
          waitTerminal terminal 3 `shouldReturn` observed
        events <- readMVar requests
        events `shouldBe`
          [ ("start", object ["argv" .= (["app", "", "two words"] :: [String]), "rows" .= (24 :: Int),
              "columns" .= (80 :: Int), "input" .= ([0,255,10] :: [Int]), "timeout" .= (12 :: Double)])
          , ("request", terminalRequest "observe" [])
          , ("request", terminalRequest "expect" ["channel" .= ("terminal" :: String), "data" .= ([112,114,111,109,112,116] :: [Int]), "timeout" .= (2 :: Double)])
          , ("request", terminalRequest "send" ["data" .= ([3,4] :: [Int])])
          , ("request", terminalRequest "resize" ["rows" .= (1 :: Int), "columns" .= (2 :: Int)])
          , ("request", terminalRequest "signal" ["name" .= ("SIGINT" :: String)])
          , ("request", terminalRequest "wait" ["timeout" .= (3 :: Double)])
          , ("request", terminalRequest "close" [])
          ]
    it "rejects a retained session handle once its callback has returned" $
      withTerminalReplies (pure . terminalReply) $ \e -> do
        terminal <- withTerminal e (Command ["app"] Nothing []) defaultTerminalOptions pure
        observeTerminal terminal `shouldThrow` invalidInput
    it "preserves the caller failure and closes its session" $ do
      requests <- newMVar []
      original <- newIORef ()
      withTerminalReplies (\request -> modifyMVar_ requests (pure . (++ [request])) >> pure (terminalReply request)) $ \e -> do
        withTerminal e (Command ["app"] Nothing []) defaultTerminalOptions (\_ -> throwIO (CallerFailure original))
          `shouldThrow` (\(CallerFailure actual) -> actual == original)
      events <- readMVar requests
      map (\(_, value) -> case value of Object fields -> KM.lookup "operation" fields; _ -> Nothing) events
        `shouldBe` [Nothing, Just (String "close")]
    it "preserves asynchronous cancellation and closes its session" $ do
      closed <- newEmptyMVar
      withTerminalReplies (\request -> do
        whenClose request (putMVar closed ())
        pure (terminalReply request)) $ \e -> do
          withTerminal e (Command ["app"] Nothing []) defaultTerminalOptions (\_ -> throwIO ThreadKilled)
            `shouldThrow` (== ThreadKilled)
          takeMVar closed
    it "reports cleanup failure when the callback succeeds" $
      withTerminalReplies (\request -> pure (if isClose request then terminalFailure else terminalReply request)) $ \e ->
        withTerminal e (Command ["app"] Nothing []) defaultTerminalOptions (const (pure ())) `shouldThrow` cleanupFailure
    it "preserves the caller failure when close also fails" $ do
      original <- newIORef ()
      withTerminalReplies (\request -> pure (if isClose request then terminalFailure else terminalReply request)) $ \e ->
        withTerminal e (Command ["app"] Nothing []) defaultTerminalOptions (\_ -> throwIO (CallerFailure original))
          `shouldThrow` (\(CallerFailure actual) -> actual == original)
    it "retains complete observations on expected-byte failure" $
      withTerminalReplies (\request -> pure (case request of
        ("start", _) -> terminalReply request
        _ | isClose request -> terminalReply request
        _ -> Result 1 (LBS.toStrict (encode (object ["ok" .= False, "error" .= ("waiting for stdout bytes" :: String),
               "observation" .= terminalObservationValue]))) "")) $ \e ->
        withTerminal e (Command ["app"] Nothing []) defaultTerminalOptions (\terminal ->
          expectTerminal terminal StandardOutput "missing" 1 `shouldThrow`
            (\failure -> case failure of
              TerminalFailed message observation -> message == "waiting for stdout bytes" &&
                fmap terminalStdout observation == Just (BS.pack [0,255])
              _ -> False))
    it "preserves partial watchdog output without inventing process or termios state" $
      withTerminalReplies (\request -> pure (case request of
        ("start", _) -> terminalReply request
        _ | isClose request -> terminalReply request
        _ -> Result 1 "{\"ok\":false,\"error\":\"watchdog expired\",\"observation\":{\"stdout\":[0,255],\"terminal\":[27,91]}}" "")) $ \e ->
          withTerminal e (Command ["app"] Nothing []) defaultTerminalOptions (\terminal ->
            observeTerminal terminal `shouldThrow` (\failure -> case failure of
              TerminalPartialFailure message out term -> message == "watchdog expired" && out == BS.pack [0,255] && term == BS.pack [27,91]
              _ -> False))
    forM_ [object ["stdout" .= ([256] :: [Int])], object ["stdout" .= ([-1] :: [Int])],
           object ["stdout" .= ([1.5] :: [Double])], object ["status" .= object ["kind" .= ("unknown" :: String)]],
           object ["status" .= object ["kind" .= ("running" :: String), "code" .= (7 :: Int)]],
           object ["status" .= object ["kind" .= ("exited" :: String), "code" .= (0 :: Int), "signal" .= (2 :: Int)]],
           object ["status" .= object ["kind" .= ("signaled" :: String), "signal" .= (2 :: Int), "code" .= (0 :: Int)]]] $ \invalid ->
      it ("rejects malformed terminal observations " ++ show invalid) $
        withTerminalReplies (\request -> pure (case request of
          ("start", _) -> terminalReply request
          _ | isClose request -> terminalReply request
          _ -> Result 0 (LBS.toStrict (encode (object ["ok" .= True, "value" .= overrideObservation invalid]))) "")) $ \e ->
            withTerminal e (Command ["app"] Nothing []) defaultTerminalOptions (\terminal ->
              observeTerminal terminal `shouldThrow` infrastructure)
    forM_ [defaultTerminalOptions { terminalRows = 0 }, defaultTerminalOptions { terminalColumns = 65536 },
           defaultTerminalOptions { terminalTimeout = 0 }, defaultTerminalOptions { terminalTimeout = 0/0 },
           defaultTerminalOptions { terminalTimeout = 1/0 }] $ \options ->
      it ("rejects invalid terminal options before transport " ++ show options) $
        withTestEnvironment "http://127.0.0.1:1" 1000000000 $ \e ->
          withTerminal (Driver e "/driver") (Command ["app"] Nothing []) options (const (pure ())) `shouldThrow` invalidInput
    it "rejects empty expectations and invalid operation sizes and timeouts" $
      withTerminalReplies (pure . terminalReply) $ \e ->
        withTerminal e (Command ["app"] Nothing []) defaultTerminalOptions $ \terminal -> do
          expectTerminal terminal StandardOutput "" 1 `shouldThrow` invalidInput
          expectTerminal terminal TerminalOutput "x" 0 `shouldThrow` invalidInput
          resizeTerminal terminal 0 20 `shouldThrow` invalidInput
          waitTerminal terminal 61 `shouldThrow` invalidInput
  describe "scoped environment lifecycle" $ do
    it "returns the callback value after removing its fresh container and lease" $
      withDaemon False False $ \host resources requests -> withDockerHost host $ do
        identities <- mapM (\_ -> withEnvironment "local:image" (pure . containerID)) [1..5 :: Int]
        Map.size (Map.fromList [(ident, ()) | ident <- identities]) `shouldBe` 5
        readMVar resources `shouldReturn` Map.empty
        events <- readMVar requests
        let createdImages = [image | Incoming "POST" path bytes _ <- events, "/containers/create?" `BS.isPrefixOf` path,
              Just (Object config) <- [decodeStrict' bytes], Just (String image) <- [KM.lookup "Image" config]]
        createdImages `shouldBe` replicate 10 "sha256:local"
        length [() | Incoming "POST" path _ _ <- events, "/start" `BS.isSuffixOf` path] `shouldBe` 5
        length [() | Incoming "GET" path _ _ <- events, "/images/" `BS.isPrefixOf` path] `shouldBe` 5
    it "removes an owned lease even when its create response fails" $
      withDaemonResponse True False False $ \host resources _ -> withDockerHost host $ do
        withEnvironment "local:image" (const (pure ())) `shouldThrow` infrastructure
        readMVar resources `shouldReturn` Map.empty
    it "cancellation removes both resources and preserves AsyncCancelled" $
      withDaemon False False $ \host resources _ -> withDockerHost host $ do
        entered <- newEmptyMVar
        blocked <- newEmptyMVar
        withAsync (withEnvironment "local:image" (\_ -> putMVar entered () >> takeMVar blocked)) $ \worker -> do
          takeMVar entered
          cancel worker
          result <- waitCatch worker
          case result of
            Left err -> fromException err `shouldBe` Just AsyncCancelled
            Right () -> expectationFailure "cancellation was swallowed"
          readMVar resources `shouldReturn` Map.empty
    it "throws startup failure after removing both owned resources" $
      withDaemon True False $ \host resources _ -> withDockerHost host $ do
        withEnvironment "local:image" (const (pure ())) `shouldThrow` infrastructure
        readMVar resources `shouldReturn` Map.empty
    it "throws cleanup failure instead of returning the callback value" $
      withDaemon False True $ \host _ _ -> withDockerHost host $
        withEnvironment "local:image" (const (pure ())) `shouldThrow` cleanupFailure
    forM_ [False, True] $ \removalFails -> do
      it ("preserves caller exception identity with cleanup failure=" ++ show removalFails) $
        withDaemon False removalFails $ \host _ _ -> withDockerHost host $ do
          original <- newIORef ()
          withEnvironment "local:image" (\_ -> throwIO (CallerFailure original))
            `shouldThrow` (\(CallerFailure observed) -> observed == original)
      it ("preserves Hspec failure identity with cleanup failure=" ++ show removalFails) $
        withDaemon False removalFails $ \host _ _ -> withDockerHost host $ do
          original <- try (Result 7 "actual" "stderr" `shouldBe` Result 0 "expected" "") :: IO (Either SomeException ())
          case original of
            Left err -> do
              result <- try (withEnvironment "local:image" (\_ -> throwIO err)) :: IO (Either SomeException ())
              case result of
                Left actual -> do
                  (fromException actual :: Maybe SomeAsyncException) `shouldSatisfy` maybe True (const False)
                  show actual `shouldBe` show err
                  (fromException actual :: Maybe E2EError) `shouldSatisfy` maybe True (const False)
                Right () -> expectationFailure "Hspec exception was swallowed"
            Right () -> expectationFailure "expected Hspec failure"
      it ("preserves ThreadKilled with cleanup failure=" ++ show removalFails) $
        withDaemon False removalFails $ \host _ _ -> withDockerHost host $
          withEnvironment "local:image" (\_ -> throwIO ThreadKilled)
            `shouldThrow` (== ThreadKilled)
    it "leaves concurrency to the caller without a fixed two-slot gate" $
      withDaemon False False $ \host resources _ -> withDockerHost host $ do
        entered <- newEmptyMVar
        release <- newEmptyMVar
        active <- newMVar (0 :: Int)
        let use _ = do
              count <- modifyMVar active (\n -> pure (n + 1, n + 1))
              if count == 3 then putMVar entered () else pure ()
              readMVar release
        withAsync (mapConcurrently (\_ -> withEnvironment "local:image" use) [1..3 :: Int]) $ \worker -> do
          timeout 2000000 (takeMVar entered) `shouldReturn` Just ()
          putMVar release ()
          void (wait worker)
        readMVar resources `shouldReturn` Map.empty
  describe "unsupported backend" $ do
    it "rejects TLS explicitly" $ withEngineHost "https://localhost:2376" (const (pure ())) `shouldThrow` invalidInput

invalidInput, infrastructure, executionFailure, cleanupFailure :: E2EError -> Bool
invalidInput (InvalidInput _) = True
invalidInput _ = False
infrastructure (Infrastructure _) = True
infrastructure _ = False
executionFailure (ExecutionFailed _ _) = True
executionFailure _ = False
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

withTestEnvironment :: String -> Word -> (Environment -> IO a) -> IO a
withTestEnvironment host duration action = withEngineHost host $ \engine -> do
  now <- getMonotonicTimeNSec
  installed <- newIORef False
  action (Environment engine "c" (Deadline (now + fromIntegral duration)) installed)
withExec :: BS.ByteString -> Int -> (Environment -> IO a) -> IO a
withExec bytes code action = withServer handler $ \host -> withTestEnvironment host 1000000000 action
  where
    handler incoming socket
      | target incoming == "/containers/c/exec" = respond socket 201 "{\"Id\":\"e\"}"
      | target incoming == "/exec/e/start" = respond socket 200 bytes
      | otherwise = respond socket 200 (LBS.toStrict (encode (object ["Running" .= False, "ExitCode" .= code])))

withCommands :: ([String] -> Result) -> (Environment -> IO a) -> IO a
withCommands answer = withCommandArchives answer Map.empty

withCommandArchives :: ([String] -> Result) -> Map.Map BS.ByteString BS.ByteString -> (Environment -> IO a) -> IO a
withCommandArchives answer = withCommandEffects (pure . answer)

withCommandEffects :: ([String] -> IO Result) -> Map.Map BS.ByteString BS.ByteString -> (Environment -> IO a) -> IO a
withCommandEffects answer archives use = do
  current <- newMVar (Result 0 "" "")
  let handler incoming socket
        | Just archive <- Map.lookup (target incoming) archives = respond socket 200 archive
        | target incoming == "/containers/c/exec" =
            case decodeStrict' (payload incoming) of
              Just (Object config) -> case KM.lookup "Cmd" config of
                Just (Array values) -> do
                  let argv = [T.unpack value | String value <- foldr (:) [] values]
                  modifyMVar_ current (const (answer argv))
                  respond socket 201 "{\"Id\":\"e\"}"
                _ -> respond socket 400 "invalid command"
              _ -> respond socket 400 "invalid json"
        | target incoming == "/exec/e/start" = do
            result <- readMVar current
            respond socket 200 (frame 1 (stdout result) <> frame 2 (stderr result))
        | otherwise = do
            result <- readMVar current
            respond socket 200 (LBS.toStrict (encode (object ["Running" .= False, "ExitCode" .= exitCode result])))
  withServer handler $ \host -> withTestEnvironment host 1000000000 use

data CallerFailure = CallerFailure (IORef ())
instance Show CallerFailure where show _ = "CallerFailure"
instance Exception CallerFailure

snapshotReply :: [(FilePath, SnapshotEntry)] -> Result
snapshotReply entries = Result 0 (LBS.toStrict (encode (object ["ok" .= True, "value" .= map entryValue entries]))) ""
  where
    entryValue (path, entry) = object ["path" .= path, "kind" .= kind, "data" .= BS.unpack bytes, "target" .= BS.unpack targetBytes]
      where
        (kind, bytes, targetBytes) = case entry of
          Missing -> ("missing" :: String, BS.empty, BS.empty)
          RegularFile contents -> ("file", contents, BS.empty)
          Directory -> ("directory", BS.empty, BS.empty)
          Symlink targetBytes' -> ("symlink", BS.empty, targetBytes')

terminalObservationValue :: Value
terminalObservationValue = object
  [ "stdout" .= ([0,255] :: [Int]), "terminal" .= ([27,91,48,109] :: [Int])
  , "unreadInput" .= ([10,255] :: [Int]), "status" .= object ["kind" .= ("exited" :: String), "code" .= (7 :: Int)]
  , "termiosRestored" .= True, "initialTermios" .= termios, "currentTermios" .= termios
  ]
  where termios = [Number 1, toJSON ([2] :: [Int])]

overrideObservation :: Value -> Value
overrideObservation (Object fields) = case terminalObservationValue of
  Object defaults -> Object (KM.union fields defaults)
  _ -> Object fields
overrideObservation other = other

terminalRequest :: String -> [Pair] -> Value
terminalRequest operation fields = object (["path" .= ("/tmp/session/socket" :: String), "operation" .= operation] ++ fields)
terminalReply :: (String, Value) -> Result
terminalReply request@(mode, _) = Result 0 (LBS.toStrict (encode (object ["ok" .= True, "value" .= value]))) ""
  where value
          | mode == "start" = object ["path" .= ("/tmp/session/socket" :: String)]
          | isClose request = Null
          | otherwise = terminalObservationValue
terminalFailure :: Result
terminalFailure = Result 1 "{\"ok\":false,\"error\":\"close failure\",\"observation\":null}" ""
isClose :: (String, Value) -> Bool
isClose (_, Object fields) = KM.lookup "operation" fields == Just (String "close")
isClose _ = False
whenClose :: (String, Value) -> IO () -> IO ()
whenClose request action = if isClose request then action else pure ()

withTerminalReplies :: ((String, Value) -> IO Result) -> (Driver -> IO a) -> IO a
withTerminalReplies answer use = withCommandEffects respondTo Map.empty (\environment -> use (Driver environment "/driver"))
  where
    respondTo argv = case argv of
      ["/driver", mode, body] -> case eitherDecodeStrict' (TE.encodeUtf8 (T.pack body)) of
        Right value -> answer (mode, value)
        Left err -> throwIO (userError err)
      _ -> throwIO (userError ("unexpected driver argv: " ++ show argv))

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
