set shell := ["bash", "-euo", "pipefail", "-c"]

[default]
all:
    go build ./...

# Build caller-owned Linux executables without a C runtime dependency.
driver:
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p target
    for arch in amd64 arm64; do
        CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags='-s -w' -o "target/driver-linux-$arch" ./cmd/caseyard-driver
    done
