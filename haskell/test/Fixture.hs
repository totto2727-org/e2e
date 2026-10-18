{-# LANGUAGE OverloadedStrings #-}
module Fixture where

import Control.Concurrent.Async
import Control.Concurrent.MVar
import Control.Exception
import Control.Monad (forever, void)
import qualified Data.ByteString as BS
import qualified Data.ByteString.Char8 as B8
import Data.List (find)
import qualified Network.Socket as NS
import qualified Network.Socket.ByteString as NSB
import Text.Read (readMaybe)

data Incoming = Incoming { verb :: BS.ByteString, target :: BS.ByteString, payload :: BS.ByteString, headerLines :: [BS.ByteString] }
  deriving (Show)

withServer :: (Incoming -> NS.Socket -> IO ()) -> (String -> IO a) -> IO a
withServer handler use = NS.withSocketsDo $ bracket open NS.close $ \listener -> do
  NS.SockAddrInet port _ <- NS.getSocketName listener
  workers <- newMVar []
  bracket (async (forever $ mask $ \restore -> do
      (socket, _) <- restore (NS.accept listener)
      worker <- async (restore (receive socket >>= \incoming -> handler incoming socket) `finally` NS.close socket)
      modifyMVar_ workers (pure . (worker:)))) cancel $ \_ ->
    use ("http://127.0.0.1:" ++ show port) `finally` (readMVar workers >>= mapM_ cancel)
  where
    open = bracketOnError (NS.socket NS.AF_INET NS.Stream NS.defaultProtocol) NS.close $ \socket -> do
      NS.setSocketOption socket NS.ReuseAddr 1
      NS.bind socket (NS.SockAddrInet 0 (NS.tupleToHostAddress (127,0,0,1)))
      NS.listen socket 32
      pure socket

receive :: NS.Socket -> IO Incoming
receive socket = headers BS.empty
  where
    headers bytes = case B8.breakSubstring "\r\n\r\n" bytes of
      (_, rest) | BS.null rest -> do
        more <- NSB.recv socket 4096
        if BS.null more then throwIO (userError "request ended in headers") else headers (bytes <> more)
      (headBytes, rest) -> do
        let rows = B8.lines headBytes
            first = case rows of [] -> []; row:_ -> B8.words row
            lengthRow = find (B8.isPrefixOf "Content-Length:") rows
            count = maybe 0 (maybe 0 id . readMaybe . B8.unpack . B8.dropWhile (== ' ') . BS.drop 15) lengthRow
        body <- exact count (BS.drop 4 rest)
        case first of
          method:path:_ -> pure (Incoming method path body (map (B8.filter (/= '\r')) rows))
          _ -> throwIO (userError "invalid request line")
    exact count bytes
      | BS.length bytes >= count = pure (BS.take count bytes)
      | otherwise = do
          more <- NSB.recv socket 4096
          if BS.null more then throwIO (userError "request ended in body") else exact count (bytes <> more)

respond :: NS.Socket -> Int -> BS.ByteString -> IO ()
respond socket code body = NSB.sendAll socket
  ("HTTP/1.1 " <> B8.pack (show code) <> " status\r\nContent-Length: " <> B8.pack (show (BS.length body)) <> "\r\nConnection: close\r\n\r\n" <> body)

frame :: Int -> BS.ByteString -> BS.ByteString
frame stream body = BS.pack [fromIntegral stream,0,0,0,
  fromIntegral (size `div` 16777216), fromIntegral (size `div` 65536),
  fromIntegral (size `div` 256), fromIntegral size] <> body
  where size = BS.length body

stallResponse :: NS.Socket -> BS.ByteString -> MVar () -> IO ()
stallResponse socket bytes closed = do
  NSB.sendAll socket ("HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n" <> bytes)
  void (NSB.recv socket 1) `finally` putMVar closed ()
