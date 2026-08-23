# Go CLI E2E example

This optional Testcontainers example demonstrates the reusable `github.com/totto2727-org/e2e/cli` package against a small Docker image.

## Usage

Run the example once without cloning the repository:

```bash
nix run github:totto2727-org/e2e
```

A successful run builds the `e2e-example:local` image, reports each scenario as `PASS`, and finishes with an overall `PASS`:

```text
--- PASS: TestCLI
    --- PASS: TestCLI/stdout
    --- PASS: TestCLI/file_output
    --- PASS: TestCLI/expected_failure
PASS
```

The scenarios check exit code `0` with `hello from e2e\n`, multi-command file output of `first\nsecond\n`, and an expected exit code `7` with `expected-failure\n`.

For a checked-out copy containing `flake.nix`, use the same launcher locally:

```bash
nix run .
```

After the one-time installation in Setup, run the launcher from any directory:

```bash
e2e-example
```

To use the reusable package in your own tests, see the [root README usage](../README.md#usage).

## Key features

- Exercises exact command output and exit-code checks.
- Exercises multi-command file creation and exact file-content checks.
- Exercises expected non-zero CLI results without treating them as test failures.

## Prerequisites

- The example inherits the repository's [Go and Docker requirements](../README.md#prerequisites).
- **Nix**: Required for the provided immediate and installed launchers.

## Setup

### Run without permanent installation

```bash
nix run github:totto2727-org/e2e
```

### Install persistently

```bash
nix profile add github:totto2727-org/e2e
```

### Use from a consumer flake

Expose the upstream package and app from the consumer's `flake.nix`:

```nix
{
  inputs.e2e.url = "github:totto2727-org/e2e";

  outputs = { e2e, ... }: {
    packages.aarch64-darwin.e2e-example = e2e.packages.aarch64-darwin.default;
    apps.aarch64-darwin.e2e-example = e2e.apps.aarch64-darwin.default;
  };
}
```

Run the exposed app with `nix run .#e2e-example`.

## API

This module exposes no user-facing API; it is a runnable fixture for the reusable [`cli` package](../README.md#api).

_This README was generated from the [share-artifact skill](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/SKILL.md) and [README template](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/readme/template.md)._
