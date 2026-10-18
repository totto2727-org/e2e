{-# LANGUAGE DeriveDataTypeable #-}
{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE ScopedTypeVariables #-}
-- closeManager is intentional: cancellation must not wait for GC to close owned resources.
{-# OPTIONS_GHC -Wno-deprecations #-}
module Test.CLI.E2E.Internal where

import Control.Concurrent (threadDelay)
import Control.Exception hiding (AssertionFailed)
import Control.Monad (unless, when)
import Data.Aeson hiding (Result)
import Data.Aeson.Types (Parser, parseEither)
import qualified Data.ByteString as BS
import qualified Data.ByteString.Char8 as B8
import qualified Data.ByteString.Lazy as LBS
import Data.IORef
import Data.List (isPrefixOf, isInfixOf)
import qualified Data.Text as T
import qualified Data.Text.Encoding as TE
import Data.Typeable (Typeable)
import Data.Word (Word64)
import GHC.Clock (getMonotonicTimeNSec)
import Network.HTTP.Client hiding (host, path)
import Network.HTTP.Types
import qualified Network.Socket as NS
import qualified Network.Socket.ByteString as NSB
import System.Environment (lookupEnv)
import System.Timeout (timeout)

-- Output has no exit code until successful exec inspection.
data Output = Output { capturedStdout :: BS.ByteString, capturedStderr :: BS.ByteString }
  deriving (Eq, Show)
data E2EError
  = InvalidInput String
  | Infrastructure String
  | DeadlineExceeded Output
  | ExecutionFailed SomeException Output
  | AssertionFailed String
  | CleanupFailed SomeException (Maybe SomeException)
  deriving (Show, Typeable)
instance Exception E2EError

data Command = Command { args :: [String], workingDir :: Maybe String, env :: [String] }
  deriving (Eq, Show)
data Result = Result { exitCode :: Int, stdout :: BS.ByteString, stderr :: BS.ByteString }
  deriving (Eq, Show)
data Engine = Engine Manager String
newtype Deadline = Deadline Word64

data Environment = Environment Engine String Deadline (IORef Bool)
-- Bound to one environment. The private staged executable is removed with its container.
data Driver = Driver Environment FilePath

validateCommand :: Command -> IO ()
validateCommand c = do
  case args c of
    [] -> throwIO (InvalidInput "argv must contain an executable")
    "" : _ -> throwIO (InvalidInput "executable must not be empty")
    _ -> pure ()
  mapM_ validateString (args c ++ maybe [] (:[]) (workingDir c) ++ env c)

validateString :: String -> IO ()
validateString s = when ('\0' `elem` s) (throwIO (InvalidInput "NUL is not permitted"))

newDeadline :: Int -> IO Deadline
newDeadline seconds = do
  now <- getMonotonicTimeNSec
  pure (Deadline (now + fromIntegral seconds * 1000000000))

bounded :: Deadline -> IO a -> IO a
bounded (Deadline deadline) action = do
  now <- getMonotonicTimeNSec
  when (now >= deadline) (throwIO (DeadlineExceeded (Output BS.empty BS.empty)))
  result <- timeout (fromIntegral ((deadline - now) `div` 1000)) action
  maybe (throwIO (DeadlineExceeded (Output BS.empty BS.empty))) pure result

withEngine :: (Engine -> IO a) -> IO a
withEngine action = do
  host <- lookupEnv "DOCKER_HOST"
  tls <- lookupEnv "DOCKER_TLS_VERIFY"
  certificates <- lookupEnv "DOCKER_CERT_PATH"
  version <- lookupEnv "DOCKER_API_VERSION"
  when (any (maybe False (not . null)) [tls, certificates]) (throwIO (InvalidInput "Docker TLS is unsupported"))
  when (maybe False (not . null) version) (throwIO (InvalidInput "DOCKER_API_VERSION is unsupported: this adapter uses unversioned Engine endpoints"))
  withEngineHost (maybe "unix:///var/run/docker.sock" id host) action

withEngineHost :: String -> (Engine -> IO a) -> IO a
withEngineHost endpoint action = do
  (settings, base) <- if "unix://" `isPrefixOf` endpoint
    then do
      let socketPath = drop 7 endpoint
      validateString socketPath
      unless ("/" `isPrefixOf` socketPath) (throwIO (InvalidInput "Unix socket path must be absolute"))
      let connectUnix _ _ _ = bracketOnError (NS.socket NS.AF_UNIX NS.Stream NS.defaultProtocol) NS.close $ \sock -> do
            NS.connect sock (NS.SockAddrUnix socketPath)
            makeConnection (NSB.recv sock 32768) (NSB.sendAll sock) (NS.close sock)
      pure (defaultManagerSettings { managerRawConnection = pure connectUnix }, "http://localhost")
    else if "tcp://" `isPrefixOf` endpoint || "http://" `isPrefixOf` endpoint
      then pure (defaultManagerSettings, if "tcp://" `isPrefixOf` endpoint then "http://" ++ drop 6 endpoint else endpoint)
      else throwIO (InvalidInput "Supported DOCKER_HOST schemes: unix, tcp, http. TLS is unsupported")
  -- No proxy or pooled connections may retain an exec socket after cancellation.
  bracket (newManager (managerSetProxy noProxy settings)) closeManager $ \manager -> action (Engine manager base)

encodeSegment :: String -> String
encodeSegment = B8.unpack . urlEncode True . TE.encodeUtf8 . T.pack

request :: Engine -> Method -> String -> LBS.ByteString -> IO Request
request (Engine _ base) verb path body = do
  initial <- parseRequest (base ++ path)
  pure initial { method = verb, requestBody = RequestBodyLBS body,
    requestHeaders = [(hContentType, if verb == "PUT" && "/archive?" `isInfixOf` path then "application/x-tar" else "application/json"), (hConnection, "close"), ("Accept-Encoding", "identity")],
    responseTimeout = responseTimeoutNone, checkResponse = \_ _ -> pure (), redirectCount = 0 }

withResponseBody :: Engine -> Method -> String -> LBS.ByteString -> (Response BodyReader -> IO a) -> IO a
withResponseBody engine@(Engine manager _) verb path body action = do
  req <- request engine verb path body
  withResponse req manager $ \response -> do
    let code = statusCode (responseStatus response)
    unless (code >= 200 && code < 300) $ do
      diagnostic <- brConsume (responseBody response)
      throwIO (Infrastructure (verbString ++ " " ++ path ++ ": HTTP " ++ show code ++ " " ++ show (BS.concat diagnostic)))
    action response
  where verbString = B8.unpack verb

bytesRequest :: Engine -> Method -> String -> LBS.ByteString -> IO BS.ByteString
bytesRequest engine verb path body = withResponseBody engine verb path body (fmap BS.concat . brConsume . responseBody)

jsonRequest :: Engine -> Method -> String -> Value -> (Value -> Parser a) -> IO a
jsonRequest engine verb path body parser = do
  bytes <- bytesRequest engine verb path (encode body)
  value <- either (throwIO . Infrastructure . ("JSON decode: " ++)) pure (eitherDecodeStrict' bytes)
  either (throwIO . Infrastructure . ("JSON structure: " ++)) pure (parseEither parser value)

identifier :: Value -> Parser String
identifier = withObject "ID" $ \o -> do
  ident <- o .: "Id"
  if null ident then fail "empty Docker identifier" else pure ident

-- Handles arbitrary chunk boundaries and records even a truncated frame payload.
demux :: IO BS.ByteString -> IORef Output -> IO ()
demux next outputs = loop BS.empty
  where
    fill count buffer
      | BS.length buffer >= count = pure buffer
      | otherwise = do
          chunk <- next
          if BS.null chunk then pure buffer else fill count (buffer <> chunk)
    loop pending = do
      headerAndRest <- fill 8 pending
      if BS.null headerAndRest then pure () else do
        when (BS.length headerAndRest < 8) (throwIO (Infrastructure "truncated exec frame header"))
        let (header, rest) = BS.splitAt 8 headerAndRest
            stream = BS.index header 0
            size = BS.foldl' (\n b -> n * 256 + fromIntegral b) (0 :: Word64) (BS.drop 4 header)
        unless ((stream == 1 || stream == 2) && BS.take 3 (BS.drop 1 header) == BS.replicate 3 0)
          (throwIO (Infrastructure "invalid exec stream frame"))
        consume stream size rest
    append stream chunk = modifyIORef' outputs $ \o -> if stream == 1
      then o { capturedStdout = capturedStdout o <> chunk }
      else o { capturedStderr = capturedStderr o <> chunk }
    consume stream remaining pending
      | remaining == 0 = loop pending
      | BS.null pending = do
          chunk <- next
          when (BS.null chunk) (throwIO (Infrastructure "truncated exec frame payload"))
          consume stream remaining chunk
      | otherwise = do
          let count = fromIntegral (min remaining (fromIntegral (BS.length pending)))
              (payload, rest) = BS.splitAt count pending
          append stream payload
          consume stream (remaining - fromIntegral count) rest

run :: Environment -> Command -> IO Result
run (Environment engine container deadline _) command = do
  validateCommand command
  outputs <- newIORef (Output BS.empty BS.empty)
  let execute = bounded deadline $ do
        exec <- jsonRequest engine "POST" ("/containers/" ++ encodeSegment container ++ "/exec")
          (object ["Cmd" .= args command, "WorkingDir" .= maybe "" id (workingDir command), "Env" .= env command,
            "AttachStdout" .= True, "AttachStderr" .= True, "Tty" .= False]) identifier
        withResponseBody engine "POST" ("/exec/" ++ encodeSegment exec ++ "/start")
          (encode (object ["Detach" .= False, "Tty" .= False])) $ \response ->
            demux (brRead (responseBody response)) outputs
        let inspect = do
              (running, code) <- jsonRequest engine "GET" ("/exec/" ++ encodeSegment exec ++ "/json") Null
                (withObject "exec inspection" $ \o -> (,) <$> o .: "Running" <*> o .: "ExitCode")
              if running then threadDelay 100000 >> inspect else do
                when (code < 0) (throwIO (Infrastructure "invalid exit code"))
                Output out err <- readIORef outputs
                pure (Result code out err)
        inspect
  execute `catch` \(failure :: SomeException) -> do
    partial <- readIORef outputs
    case fromException failure of
      Just (DeadlineExceeded _) -> throwIO (DeadlineExceeded partial)
      _ -> case fromException failure :: Maybe SomeAsyncException of
        Just _ -> throwIO failure
        Nothing -> throwIO (ExecutionFailed failure partial)
