{-# LANGUAGE OverloadedStrings #-}

module MultipleScenariosSpec (spec) where

import Test.CLI.E2E (Command (..), Result (..), run, withEnvironment)
import Test.Hspec (Spec, it, shouldReturn)

spec :: Spec
spec = do
  it "prints greeting" $
    withEnvironment "example-cli:local" $ \environment ->
      run environment (Command ["echo", "hello from e2e"] Nothing [])
        `shouldReturn` Result 0 "hello from e2e\n" ""
  it "returns expected failure" $
    withEnvironment "example-cli:local" $ \environment ->
      run environment (Command ["false"] Nothing [])
        `shouldReturn` Result 1 "" ""
