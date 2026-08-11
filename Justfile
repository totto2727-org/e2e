set shell := ["bash", "-euo", "pipefail", "-c"]

default: ci

fix:
    golangci-lint fmt
    golangci-lint run --fix ./...
    just example/fix

check:
    golangci-lint fmt --diff
    golangci-lint run ./...
    just example/check

build:
    go build ./...
    just example/build

test:
    go test -race ./...

e2e:
    just example/e2e

ci: check build test e2e
