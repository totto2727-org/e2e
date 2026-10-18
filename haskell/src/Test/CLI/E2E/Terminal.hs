{-# LANGUAGE DeriveDataTypeable #-}
{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE ScopedTypeVariables #-}
module Test.CLI.E2E.Terminal
  ( Terminal, TerminalOptions(..), defaultTerminalOptions
  , TerminalChannel(..), TerminalSignal(..), TerminalStatus(..)
  , TermiosValue(..), TerminalObservation(..), TerminalError(..)
  , withTerminal, observeTerminal, expectTerminal, sendTerminal
  , resizeTerminal, signalTerminal, waitTerminal
  ) where

import Control.Exception hiding (AssertionFailed)
import Control.Monad (unless, void, when)
import Data.Aeson hiding (Result)
import qualified Data.Aeson.KeyMap as KM
import Data.Aeson.Types (Pair, Parser, parseEither)
import qualified Data.ByteString as BS
import qualified Data.ByteString.Lazy as LBS
import Data.IORef
import qualified Data.Text as T
import qualified Data.Text.Encoding as TE
import Data.Typeable (Typeable)
import qualified System.IO as IO
import System.Timeout (timeout)
import Test.CLI.E2E.Internal

-- The callback owns this handle. A closed handle fails before any transport I/O.
data Terminal = Terminal Driver String (IORef Bool)
data TerminalOptions = TerminalOptions
  { terminalRows :: Int
  , terminalColumns :: Int
  , terminalInput :: Maybe BS.ByteString
  , terminalTimeout :: Double
  } deriving (Eq, Show)
defaultTerminalOptions :: TerminalOptions
defaultTerminalOptions = TerminalOptions 40 120 Nothing 50

data TerminalChannel = StandardOutput | TerminalOutput deriving (Eq, Show)
data TerminalSignal = Interrupt | Terminate | Hangup | Quit | Kill | User1 | User2 deriving (Eq, Show)
data TerminalStatus = TerminalRunning | TerminalExited Int | TerminalSignaled Int deriving (Eq, Show)
data TermiosValue = TermiosInteger Integer | TermiosArray [TermiosValue] deriving (Eq, Show)
data TerminalObservation = TerminalObservation
  { terminalStdout :: BS.ByteString
  , terminalOutput :: BS.ByteString
  , terminalUnreadInput :: BS.ByteString
  , terminalStatus :: TerminalStatus
  , terminalTermiosRestored :: Bool
  , terminalInitialTermios :: [TermiosValue]
  , terminalCurrentTermios :: [TermiosValue]
  } deriving (Eq, Show)
data TerminalError = TerminalFailed String (Maybe TerminalObservation)
  | TerminalPartialFailure String BS.ByteString BS.ByteString
  deriving (Show, Typeable)
instance Exception TerminalError

validateDuration :: Double -> IO ()
validateDuration seconds = when (isNaN seconds || isInfinite seconds || seconds <= 0 || seconds > 60)
  (throwIO (InvalidInput "terminal timeout must be finite and in (0, 60] seconds"))
validateGeometry :: Int -> Int -> IO ()
validateGeometry rows columns = unless (all (\n -> n >= 1 && n <= 65535) [rows, columns])
  (throwIO (InvalidInput "terminal rows and columns must be in 1..65535"))

withTerminal :: Driver -> Command -> TerminalOptions -> (Terminal -> IO a) -> IO a
withTerminal capability command options use = do
  validateCommand command
  validateGeometry (terminalRows options) (terminalColumns options)
  validateDuration (terminalTimeout options)
  mask $ \restore -> do
    path <- restore $ driver capability "start"
      (object ["argv" .= args command, "rows" .= terminalRows options, "columns" .= terminalColumns options,
        "input" .= fmap BS.unpack (terminalInput options), "timeout" .= terminalTimeout options])
      command (withObject "terminal start" $ \o -> do
        path <- o .: "path"
        if null path || '\0' `elem` path then fail "invalid terminal path" else pure path)
    closed <- newIORef False
    let terminal = Terminal capability path closed
    result <- try (restore (use terminal))
    removed <- try (closeTerminal terminal)
    case (result, removed) of
      (Right value, Right ()) -> pure value
      (Left err, Right ()) -> throwIO (err :: SomeException)
      (Right _, Left err) | Just (_ :: SomeAsyncException) <- fromException err -> throwIO err
      (Right _, Left err) -> throwIO (CleanupFailed err Nothing)
      (Left err, Left cleanupError) -> do
        void (try (timeout 1000000 (IO.hPutStrLn IO.stderr
          ("Haskell terminal cleanup failed: " ++ displayException (cleanupError :: SomeException))))
          :: IO (Either SomeException (Maybe ())))
        throwIO (err :: SomeException)

-- Driver requests use the same command effects and environment deadline as run.
driver :: Driver -> String -> Value -> Command -> (Value -> Parser a) -> IO a
driver (Driver environment executable) mode body command decodeValue = do
  result <- run environment (Command [executable, mode, T.unpack (TE.decodeUtf8 (LBS.toStrict (encode body)))]
    (workingDir command) (env command))
  value <- either (throwIO . Infrastructure . ("terminal JSON: " ++)) pure (eitherDecodeStrict' (stdout result))
  let decodeReply = withObject "terminal reply" $ \o -> do
        ok <- o .: "ok"
        if ok then Right <$> (o .: "value" >>= decodeValue) else do
          message <- o .: "error"
          observed <- o .:? "observation"
          failure <- case observed of
            Nothing -> pure (TerminalFailed message Nothing)
            Just observedValue -> withObject "terminal failure observation" (\fields ->
              if KM.size fields == 2 && KM.member "stdout" fields && KM.member "terminal" fields
                then TerminalPartialFailure message <$> (fields .: "stdout" >>= decodeBytes) <*> (fields .: "terminal" >>= decodeBytes)
                else TerminalFailed message . Just <$> decodeObservation observedValue) observedValue
          pure (Left failure)
  reply <- either (throwIO . Infrastructure . ("terminal structure: " ++)) pure (parseEither decodeReply value)
  case reply of
    Left failure -> throwIO failure
    Right answer -> do
      unless (exitCode result == 0 && BS.null (stderr result))
        (throwIO (Infrastructure ("terminal helper failed: " ++ show result)))
      pure answer

requestTerminal :: Terminal -> [Pair] -> IO TerminalObservation
requestTerminal (Terminal capability path closed) fields = do
  isClosed <- readIORef closed
  when isClosed (throwIO (InvalidInput "terminal session is closed"))
  driver capability "request" (object ("path" .= path : fields))
    (Command [] Nothing []) decodeObservation

observeTerminal :: Terminal -> IO TerminalObservation
observeTerminal terminal = requestTerminal terminal ["operation" .= ("observe" :: String)]
expectTerminal :: Terminal -> TerminalChannel -> BS.ByteString -> Double -> IO TerminalObservation
expectTerminal terminal channel bytes seconds = do
  when (BS.null bytes) (throwIO (InvalidInput "expected terminal bytes must not be empty"))
  validateDuration seconds
  requestTerminal terminal ["operation" .= ("expect" :: String),
    "channel" .= (case channel of StandardOutput -> "stdout" :: String; TerminalOutput -> "terminal"),
    "data" .= BS.unpack bytes, "timeout" .= seconds]
sendTerminal :: Terminal -> BS.ByteString -> IO TerminalObservation
sendTerminal terminal bytes = requestTerminal terminal ["operation" .= ("send" :: String), "data" .= BS.unpack bytes]
resizeTerminal :: Terminal -> Int -> Int -> IO TerminalObservation
resizeTerminal terminal rows columns = do
  validateGeometry rows columns
  requestTerminal terminal ["operation" .= ("resize" :: String), "rows" .= rows, "columns" .= columns]
signalTerminal :: Terminal -> TerminalSignal -> IO TerminalObservation
signalTerminal terminal signal = requestTerminal terminal ["operation" .= ("signal" :: String), "name" .= name]
  where
    name = case signal of
      Interrupt -> "SIGINT" :: String
      Terminate -> "SIGTERM"
      Hangup -> "SIGHUP"
      Quit -> "SIGQUIT"
      Kill -> "SIGKILL"
      User1 -> "SIGUSR1"
      User2 -> "SIGUSR2"
waitTerminal :: Terminal -> Double -> IO TerminalObservation
waitTerminal terminal seconds = do
  validateDuration seconds
  requestTerminal terminal ["operation" .= ("wait" :: String), "timeout" .= seconds]

closeTerminal :: Terminal -> IO ()
closeTerminal (Terminal (Driver (Environment engine ident _ installed) executable) path closed) = do
  writeIORef closed True
  deadline <- newDeadline 30
  driver (Driver (Environment engine ident deadline installed) executable) "request"
    (object ["path" .= path, "operation" .= ("close" :: String)]) (Command [] Nothing [])
    (\value -> if value == Null then pure () else fail "close must return null")

decodeBytes :: Value -> Parser BS.ByteString
decodeBytes value = do
  bytes <- parseJSON value :: Parser [Int]
  unless (all (\n -> n >= 0 && n <= 255) bytes) (fail "invalid terminal byte array")
  pure (BS.pack (map fromIntegral bytes))
decodeTermios :: Value -> Parser TermiosValue
decodeTermios (Array values) = TermiosArray <$> mapM decodeTermios (foldr (:) [] values)
decodeTermios value = TermiosInteger <$> parseJSON value

decodeObservation :: Value -> Parser TerminalObservation
decodeObservation = withObject "terminal observation" $ \o -> do
  out <- o .: "stdout" >>= decodeBytes
  term <- o .: "terminal" >>= decodeBytes
  unread <- o .: "unreadInput" >>= decodeBytes
  status <- o .: "status" >>= withObject "terminal status" (\s -> do
    kind <- s .: "kind"
    case kind :: String of
      "running" -> do
        when (KM.member "code" s || KM.member "signal" s) (fail "running status has an exit result")
        pure TerminalRunning
      "exited" -> do
        when (KM.member "signal" s) (fail "exited status has a signal")
        code <- s .: "code"
        if code < 0 then fail "negative terminal exit code" else pure (TerminalExited code)
      "signaled" -> do
        when (KM.member "code" s) (fail "signaled status has an exit code")
        signal <- s .: "signal"
        if signal <= 0 then fail "invalid terminal signal" else pure (TerminalSignaled signal)
      _ -> fail "invalid terminal status")
  restored <- o .: "termiosRestored"
  initial <- o .: "initialTermios" >>= withArray "initial termios" (mapM decodeTermios . foldr (:) [])
  current <- o .: "currentTermios" >>= withArray "current termios" (mapM decodeTermios . foldr (:) [])
  pure (TerminalObservation out term unread status restored initial current)
