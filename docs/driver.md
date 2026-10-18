# Install the snapshot and terminal driver

For test authors who need [snapshots or terminal sessions](../README.md#file-state-snapshots).
Command and file APIs do not need this driver.

## Requirements

| Location          | Requirement                                                                  |
| ----------------- | ---------------------------------------------------------------------------- |
| Build host        | Go and a trusted Caseyard checkout at the SDK revision                       |
| Application image | Linux amd64 or arm64, `mktemp`, and a writable `/tmp` that permits execution |
| Terminal sessions | `/dev/ptmx` and `/dev/pts` in the container                                  |

The application image needs neither Go nor Python.
The SDK does not embed or download an executable.

## Build

From the Caseyard checkout, build both Linux architectures:

```sh
cd go
for arch in amd64 arm64
do
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags='-s -w' -o "target/driver-linux-$arch" ./cmd/caseyard-driver
done
```

> [!IMPORTANT]
> Select the **container** architecture, not the test host or Engine architecture.
> Emulation can make these differ.
> Use only trusted driver bytes from the matching SDK revision.
> An incompatible driver fails without an automatic fallback.

## Install in each test scope

Read the matching executable on the host, then pass its bytes to the language's install operation after environment creation.

| Language   | Installation API                                                                      |
| ---------- | ------------------------------------------------------------------------------------- |
| Go         | [`environment.InstallDriver(bytes)`](../go/README.md#state-and-terminal-driver-setup) |
| Haskell    | [`installDriver environment bytes`](../haskell/README.md#driver-installation)         |
| TypeScript | [`installDriver(environment, bytes)`](../typescript/README.md#filesystem-snapshots)   |
| Rust       | [`env.install_driver(&bytes).await`](../rust/README.md#static-driver-installation)    |

Installation places an executable in a private directory, including in non-root images.
The container owns its lifetime.
Do not keep driver or terminal handles beyond their scope.
