{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE ScopedTypeVariables #-}
module Test.CLI.E2E
  ( Command(..), Result(..), Output(..), E2EError(..), Environment
  , Case(..), CaseResult(..), runCases, containerID
  , run, checkCommand, writeFile, readFile, checkFile
  , checkMissing, checkRegularFile, checkDirectory, checkSymlink
  ) where

import Prelude hiding (readFile, writeFile)
import Control.Concurrent.Async (mapConcurrently)
import Control.Concurrent.QSem
import Control.Exception hiding (AssertionFailed)
import Control.Monad (unless, void, when)
import qualified Codec.Archive.Tar as Tar
import qualified Codec.Archive.Tar.Entry as Tar
import Data.Aeson hiding (Result)
import qualified Data.Aeson.KeyMap as KM
import qualified Data.ByteString as BS
import qualified Data.ByteString.Lazy as LBS
import qualified Data.Set as Set
import qualified Data.Text as T
import qualified Data.Text.Encoding as TE
import Numeric (showHex)
import System.FilePath.Posix (takeDirectory, takeFileName)
import System.IO (IOMode(ReadMode), withBinaryFile)
import Network.HTTP.Client (responseBody, responseStatus, brConsume, withResponse)
import Network.HTTP.Types (statusCode)
import Test.CLI.E2E.Internal

-- Cases are independent. Outcomes preserve arbitrary caller exceptions.
data Case = Case { name :: String, action :: Environment -> IO () }
data CaseResult = CaseResult { caseName :: String, outcome :: Either SomeException () }

containerID :: Environment -> String
containerID (Environment _ ident _) = ident

runCases :: String -> [Case] -> IO [CaseResult]
runCases image cases = do
  validateString image
  when (null image) (throwIO (InvalidInput "image must not be empty"))
  let names = map name cases
  when (any null names || Set.size (Set.fromList names) /= length names)
    (throwIO (InvalidInput "case names must be nonempty and unique"))
  withEngine $ \engine -> do
    setupDeadline <- newDeadline 60
    imageID <- bounded setupDeadline $ jsonRequest engine "GET" ("/images/" ++ encodeSegment image ++ "/json") Null identifier
    ownedContainer engine imageID setupDeadline False $ \_ -> do
      slots <- newQSem 2
      mapConcurrently (runCase engine imageID slots) cases

runCase :: Engine -> String -> QSem -> Case -> IO CaseResult
runCase engine image slots testCase = bracket_ (waitQSem slots) (signalQSem slots) $ do
  result <- try $ do
    deadline <- newDeadline 60
    ownedContainer engine image deadline True (action testCase . (\ident -> Environment engine ident deadline))
  case result of
    Left err | Just (_ :: SomeAsyncException) <- fromException err -> throwIO err
    _ -> pure (CaseResult (name testCase) result)

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
      (Right _, Left err) -> throwIO (CleanupFailed err Nothing)
      (Left err, Left cleanupError) -> throwIO (CleanupFailed cleanupError (Just err))

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

checkCommand :: Environment -> Command -> Result -> IO ()
checkCommand environment command expected = do
  actual <- run environment command
  unless (actual == expected) (throwIO (AssertionFailed ("command " ++ show command ++ ": actual=" ++ show actual ++ " expected=" ++ show expected)))

writeFile :: Environment -> FilePath -> BS.ByteString -> IO ()
writeFile (Environment engine ident deadline) path bytes = do
  validateString path
  let base = takeFileName path
  when (null base || base == "." || base == "..") (throwIO (InvalidInput "file path must name a file"))
  tarPath <- either (throwIO . InvalidInput) pure (Tar.toTarPath False base)
  let entry = (Tar.fileEntry tarPath (LBS.fromStrict bytes)) { Tar.entryPermissions = 0o644 }
  bounded deadline $ void $ bytesRequest engine "PUT"
    ("/containers/" ++ encodeSegment ident ++ "/archive?path=" ++ encodeSegment (takeDirectory path)) (Tar.write [entry])

readFile :: Environment -> FilePath -> IO BS.ByteString
readFile (Environment engine ident deadline) path = do
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

checkFile :: Environment -> FilePath -> BS.ByteString -> IO ()
checkFile environment path expected = do
  actual <- readFile environment path
  unless (actual == expected) (throwIO (AssertionFailed ("file " ++ show path ++ ": actual=" ++ show actual ++ " expected=" ++ show expected)))

probe :: Environment -> String -> FilePath -> IO Bool
probe environment flag path = do
  result <- run environment (Command ["test", flag, path] Nothing [])
  case exitCode result of
    0 -> pure True
    1 -> pure False
    code -> throwIO (Infrastructure ("path probe exit code " ++ show code ++ ": " ++ show result))

checkMissing :: Environment -> FilePath -> IO ()
checkMissing environment path = do
  exists <- probe environment "-e" path
  linked <- probe environment "-L" path
  when (exists || linked) (throwIO (AssertionFailed ("expected missing path: " ++ path)))

checkPhysical :: Environment -> String -> FilePath -> IO ()
checkPhysical environment flag path = do
  matches <- probe environment flag path
  linked <- probe environment "-L" path
  unless (matches && not linked) (throwIO (AssertionFailed ("expected physical " ++ flag ++ " path: " ++ path)))

checkRegularFile :: Environment -> FilePath -> IO ()
checkRegularFile environment = checkPhysical environment "-f"

checkDirectory :: Environment -> FilePath -> IO ()
checkDirectory environment = checkPhysical environment "-d"

checkSymlink :: Environment -> FilePath -> FilePath -> IO ()
checkSymlink environment path target = do
  linked <- probe environment "-L" path
  unless linked (throwIO (AssertionFailed ("expected symbolic link: " ++ path)))
  checkCommand environment (Command ["readlink", "--", path] Nothing [])
    (Result 0 (TE.encodeUtf8 (T.pack (target ++ "\n"))) BS.empty)
