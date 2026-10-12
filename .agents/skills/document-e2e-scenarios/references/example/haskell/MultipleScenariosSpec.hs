{-# LANGUAGE OverloadedStrings #-}

module Main (main) where

import Test.CLI.E2E (Command (..), Environment, Result (..), checkCommand, withEnvironment)
import Test.Hspec (hspec, it)

main :: IO ()
main = hspec $ do
  it "prints greeting" $
    withEnvironment "example-cli:local" printsGreetingScenario
  it "returns expected failure" $
    withEnvironment "example-cli:local" returnsExpectedFailureScenario

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
