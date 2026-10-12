{-# LANGUAGE OverloadedStrings #-}

module Main (main) where

import Control.Exception (throwIO)
import Control.Monad (forM_)
import Test.CLI.E2E (Case (..), CaseResult (..), Command (..), Environment, Result (..), checkCommand, runCases)
import Test.Hspec (hspec, it)

main :: IO ()
main = do
  results <- runCases "example-cli:local"
    [ Case "prints greeting" printsGreetingScenario
    , Case "returns expected failure" returnsExpectedFailureScenario
    ]
  hspec $ forM_ results $ \result ->
    it (caseName result) (either throwIO pure (outcome result))

printsGreetingScenario :: Environment -> IO ()
printsGreetingScenario environment =
  checkCommand environment
    (Command ["echo", "hello from e2e"] Nothing [])
    (Result 0 "hello from e2e\n" "")

returnsExpectedFailureScenario :: Environment -> IO ()
returnsExpectedFailureScenario environment =
  checkCommand environment
    (Command ["false"] Nothing [])
    (Result 1 "" "")
