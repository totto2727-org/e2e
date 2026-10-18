{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE ScopedTypeVariables #-}
module Test.CLI.E2E
  ( Command(..), Result(..), Output(..), E2EError(..), Environment
  , withEnvironment, containerID, Driver, installDriver
  , run, writeFile, readFile, PathKind(..), pathKind, readSymlink
  , SnapshotEntry(..), FileSystemSnapshot(..)
  , module Test.CLI.E2E.Terminal
  , captureSnapshot
  ) where

import Prelude hiding (readFile, writeFile)
import Control.Exception
import Control.Monad (unless, void, when)
import qualified Codec.Archive.Tar as Tar
import qualified Codec.Archive.Tar.Entry as Tar
import Data.Aeson hiding (Result)
import Data.IORef
import Data.List (isPrefixOf)
import Data.Aeson.Types (parseEither)
import qualified Data.Map.Strict as Map
import qualified Data.Set as Set
import qualified Data.Aeson.KeyMap as KM
import qualified Data.ByteString as BS
import qualified Data.ByteString.Lazy as LBS
import qualified Data.Text as T
import qualified Data.Text.Encoding as TE
import Numeric (showHex)
import System.FilePath.Posix (takeDirectory, takeFileName)
import System.IO (IOMode(ReadMode), withBinaryFile)
import System.Timeout (timeout)
import qualified System.IO as IO
import Network.HTTP.Client (responseBody, responseStatus, brConsume, withResponse)
import Network.HTTP.Types (statusCode)
import Test.CLI.E2E.Internal
import Test.CLI.E2E.Terminal

containerID :: Environment -> String
containerID (Environment _ ident _ _) = ident

-- Hspec (or another test framework) owns names, filtering and scheduling.
-- Each invocation leases the resolved image and owns one fresh case container.
withEnvironment :: String -> (Environment -> IO a) -> IO a
withEnvironment image use = do
  validateString image
  when (null image) (throwIO (InvalidInput "image must not be empty"))
  deadline <- newDeadline 60
  withEngine $ \engine -> do
    imageID <- bounded deadline $ jsonRequest engine "GET" ("/images/" ++ encodeSegment image ++ "/json") Null identifier
    ownedContainer engine imageID deadline False $ \_ ->
      ownedContainer engine imageID deadline True $ \ident -> do
        installed <- newIORef False
        use (Environment engine ident deadline installed)

-- Ownership is registered before the create request can reach the daemon.
-- Inspect the random name and verify its label before removing by immutable ID.
ownedContainer :: Engine -> String -> Deadline -> Bool -> (String -> IO a) -> IO a
ownedContainer engine image deadline start use = do
  token <- withBinaryFile "/dev/urandom" ReadMode $ \entropyHandle -> BS.hGet entropyHandle 24
  unless (BS.length token == 24) (throwIO (Infrastructure "could not obtain ownership entropy"))
  let suffix = concatMap (\b -> let h = showHex b "" in if length h == 1 then '0':h else h) (BS.unpack token)
      ownedName = "haskell-e2e-" ++ suffix
      cleanup = cleanupOwned engine ownedName suffix
  mask $ \restore -> do
    result <- try $ restore $ do
      ident <- bounded deadline $ jsonRequest engine "POST" ("/containers/create?name=" ++ ownedName)
        (object ["Image" .= image, "Labels" .= object ["org.e2e.haskell.owner" .= suffix]]) identifier
      when start (void (bounded deadline (bytesRequest engine "POST" ("/containers/" ++ encodeSegment ident ++ "/start") LBS.empty)))
      use ident
    removed <- try cleanup
    case (result, removed) of
      (Right value, Right ()) -> pure value
      (Left err, Right ()) -> throwIO (err :: SomeException)
      (Right _, Left err) | Just (_ :: SomeAsyncException) <- fromException err -> throwIO err
      (Right _, Left err) -> throwIO (CleanupFailed err Nothing)
      (Left err, Left cleanupError) -> do
        -- A secondary cleanup failure must be visible without replacing the
        -- original Hspec assertion or asynchronous cancellation. Diagnostics
        -- are best-effort and bounded so even a blocked stderr cannot hold us.
        void (try (timeout 1000000 (IO.hPutStrLn IO.stderr
          ("Haskell E2E cleanup failed: " ++ displayException (cleanupError :: SomeException))))
          :: IO (Either SomeException (Maybe ())))
        throwIO (err :: SomeException)

cleanupOwned :: Engine -> String -> String -> IO ()
cleanupOwned engine@(Engine manager _) ownedName token = do
  deadline <- newDeadline 30
  bounded deadline $ do
    req <- request engine "GET" ("/containers/" ++ ownedName ++ "/json") LBS.empty
    inspected <- withResponse req manager $ \response -> do
      let status = statusCode (responseStatus response)
      if status == 404 then pure Nothing else do
        unless (status == 200) (throwIO (Infrastructure ("cleanup inspect HTTP " ++ show status)))
        bytes <- BS.concat <$> brConsume (responseBody response)
        value <- either (throwIO . Infrastructure) pure (eitherDecodeStrict' bytes)
        case value of
          Object o -> case (KM.lookup "Id" o, KM.lookup "Config" o) of
            (Just (String ident), Just (Object config)) -> case KM.lookup "Labels" config of
              Just (Object labels) | KM.lookup "org.e2e.haskell.owner" labels == Just (String (T.pack token)) -> pure (Just (T.unpack ident))
              _ -> throwIO (Infrastructure "cleanup refused: ownership label does not match")
            _ -> throwIO (Infrastructure "invalid cleanup inspection")
          _ -> throwIO (Infrastructure "invalid cleanup inspection")
    case inspected of
      Nothing -> pure ()
      Just ident -> void (bytesRequest engine "DELETE" ("/containers/" ++ encodeSegment ident ++ "?force=true&v=true") LBS.empty)

writeFile :: Environment -> FilePath -> BS.ByteString -> IO ()
writeFile = writeFileMode 0o644

writeFileMode :: Tar.Permissions -> Environment -> FilePath -> BS.ByteString -> IO ()
writeFileMode permissions (Environment engine ident deadline _) path bytes = do
  validateString path
  let base = takeFileName path
  when (null base || base == "." || base == "..") (throwIO (InvalidInput "file path must name a file"))
  tarPath <- either (throwIO . InvalidInput) pure (Tar.toTarPath False base)
  let entry = (Tar.fileEntry tarPath (LBS.fromStrict bytes)) { Tar.entryPermissions = permissions }
  bounded deadline $ void $ bytesRequest engine "PUT"
    ("/containers/" ++ encodeSegment ident ++ "/archive?path=" ++ encodeSegment (takeDirectory path)) (Tar.write [entry])

readFile :: Environment -> FilePath -> IO BS.ByteString
readFile (Environment engine ident deadline _) path = do
  validateString path
  bounded deadline $ do
    archive <- bytesRequest engine "GET" ("/containers/" ++ encodeSegment ident ++ "/archive?path=" ++ encodeSegment path) LBS.empty
    let collect Tar.Done = pure []
        collect (Tar.Fail err) = throwIO (Infrastructure ("file archive: " ++ show err))
        collect (Tar.Next entry rest) = case Tar.entryContent entry of
          Tar.NormalFile bytes _ -> (LBS.toStrict bytes :) <$> collect rest
          _ -> throwIO (Infrastructure "file archive contains a non-regular entry")
    files <- collect (Tar.read (LBS.fromStrict archive))
    case files of
      [bytes] -> pure bytes
      _ -> throwIO (Infrastructure "file archive must contain exactly one regular file")

probe :: Environment -> String -> FilePath -> IO Bool
probe environment flag path = do
  result <- run environment (Command ["test", flag, path] Nothing [])
  case exitCode result of
    0 -> pure True
    1 -> pure False
    code -> throwIO (Infrastructure ("path probe exit code " ++ show code ++ ": " ++ show result))

data PathKind = PathMissing | PathRegularFile | PathDirectory | PathSymlink | PathOther
  deriving (Eq, Show)

validatePhysicalPath :: FilePath -> IO ()
validatePhysicalPath path = do
  validateString path
  when (null path) (throwIO (InvalidInput "path must not be empty"))

pathKind :: Environment -> FilePath -> IO PathKind
pathKind environment path = do
  validatePhysicalPath path
  classify [("-L", PathSymlink), ("-f", PathRegularFile), ("-d", PathDirectory), ("-e", PathOther)]
  where
    classify [] = pure PathMissing
    classify ((flag, kind):rest) = do
      matches <- probe environment flag path
      if matches then pure kind else classify rest

readSymlink :: Environment -> FilePath -> IO BS.ByteString
readSymlink environment path = do
  validatePhysicalPath path
  result <- run environment (Command ["readlink", "--", path] Nothing [])
  case BS.unsnoc (stdout result) of
    Just (target, 10) | exitCode result == 0 && BS.null (stderr result) -> pure target
    _ -> throwIO (Infrastructure ("symbolic link read failed: " ++ show result))

-- A directory records its kind only. Links retain their literal, unresolved target.
data SnapshotEntry = Missing | RegularFile BS.ByteString | Directory | Symlink BS.ByteString
  deriving (Eq, Show)
newtype FileSystemSnapshot = FileSystemSnapshot { snapshotEntries :: Map.Map FilePath SnapshotEntry }
  deriving (Eq, Show)
-- Installation is explicit. The caller selects a trusted static Linux binary
-- for the container architecture and the environment owns its private directory.
installDriver :: Environment -> BS.ByteString -> IO Driver
installDriver environment@(Environment _ _ _ installed) bytes = do
  when (BS.null bytes) (throwIO (InvalidInput "driver binary must not be empty"))
  alreadyInstalled <- atomicModifyIORef' installed (\previous -> (True, previous))
  when alreadyInstalled (throwIO (InvalidInput "driver installation has already been attempted in this environment"))
  created <- run environment (Command ["mktemp", "-d", "/tmp/caseyard-driver-XXXXXXXXXXXX"] Nothing [])
  unless (exitCode created == 0 && BS.null (stderr created))
    (throwIO (Infrastructure ("driver directory creation failed: " ++ show created)))
  text <- either (throwIO . Infrastructure . show) (pure . T.unpack) (TE.decodeUtf8' (stdout created))
  directory <- case lines text of
    [path] | "/tmp/caseyard-driver-" `isPrefixOf` path,
      not (null (drop 21 path)), all (`elem` (['a'..'z'] ++ ['A'..'Z'] ++ ['0'..'9'])) (drop 21 path) -> pure path
    _ -> throwIO (Infrastructure "invalid private driver directory")
  let executable = directory ++ "/driver"
  -- Docker archives are root-owned even in non-root images. Supply executable
  -- permissions in the archive instead of asking the image user to chmod it.
  writeFileMode 0o755 environment executable bytes
  pure (Driver environment executable)

captureSnapshot :: Driver -> [FilePath] -> IO FileSystemSnapshot
captureSnapshot (Driver environment executable) paths = do
  mapM_ validatePath paths
  let selected = Set.fromList paths
      requestBody = object ["paths" .= Set.toAscList selected]
  result <- run environment (Command [executable, "snapshot", T.unpack (TE.decodeUtf8 (LBS.toStrict (encode requestBody)))] Nothing [])
  unless (exitCode result == 0 && BS.null (stderr result))
    (throwIO (Infrastructure ("filesystem snapshot failed: " ++ show result)))
  value <- either (throwIO . Infrastructure . ("snapshot JSON: " ++)) pure (eitherDecodeStrict' (stdout result))
  entries <- either (throwIO . Infrastructure . ("snapshot structure: " ++)) pure (parseEither decodeReply value)
  let captured = Map.fromList entries
  unless (Map.size captured == length entries && Map.keysSet captured == selected)
    (throwIO (Infrastructure "snapshot returned duplicate, missing, or unselected paths"))
  pure (FileSystemSnapshot captured)
  where
    validatePath path = do
      validateString path
      when (null path) (throwIO (InvalidInput "snapshot path must not be empty"))
      when (path /= "/" && last path == '/')
        (throwIO (InvalidInput "snapshot path must not have a trailing separator"))
    decodeReply = withObject "snapshot reply" $ \o -> do
      ok <- o .: "ok"
      unless ok (fail "snapshot helper failed")
      o .: "value" >>= withArray "snapshot entries" (mapM decodeEntry . foldr (:) [])
    decodeEntry = withObject "snapshot entry" $ \o -> do
      path <- o .: "path"
      kind <- o .: "kind"
      bytes <- o .: "data" >>= decodeBytes
      target <- o .: "target" >>= decodeBytes
      entry <- case kind :: String of
        "missing" | BS.null bytes && BS.null target -> pure Missing
        "file" | BS.null target -> pure (RegularFile bytes)
        "directory" | BS.null bytes && BS.null target -> pure Directory
        "symlink" | BS.null bytes -> pure (Symlink target)
        _ -> fail "unsupported or inconsistent physical path kind"
      pure (path, entry)
    decodeBytes value = do
      bytes <- parseJSON value
      unless (all (\n -> n >= (0 :: Int) && n <= 255) bytes) (fail "invalid snapshot byte array")
      pure (BS.pack (map fromIntegral bytes))
