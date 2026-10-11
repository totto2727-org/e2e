set shell := ["bash", "-euo", "pipefail", "-c"]

default:
    @just --list

fix:
    just go/fix
    just typescript/fix
    just rust/fix

check:
    just go/check
    just haskell/check
    just typescript/check
    just rust/check

build:
    just go/build
    just haskell/build
    just typescript/build
    just rust/build

test:
    just go/test
    just haskell/test
    just typescript/test
    just rust/test

e2e:
    just go/e2e
    just haskell/e2e
    just typescript/e2e
    just rust/e2e

ci:
    just go/ci
    just haskell/ci
    just typescript/ci
    just rust/ci
