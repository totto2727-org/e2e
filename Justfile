set shell := ["bash", "-euo", "pipefail", "-c"]

default:
    @just --list

fix:
    just go/fix

check:
    just go/check

build:
    just go/build

test:
    just go/test

e2e:
    just go/e2e

ci: check build test
