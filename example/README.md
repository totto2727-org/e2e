# Go CLI E2E example

This optional Testcontainers example demonstrates the reusable `github.com/totto2727-org/e2e/cli` package against a small Docker image.

## Usage

Run the checked-in example from the repository root:

```bash
just e2e
```

A successful run builds the `e2e-example:local` image, reports each scenario as `PASS`, and finishes with an overall `PASS`:

- `TestCLI/stdout` checks exit code `0` and the exact `hello from e2e\n` output.
- `TestCLI/file_output` runs multiple commands, then checks the copied file contains exactly `first\nsecond\n`.
- `TestCLI/expected_failure` checks that exit code `7` and `expected-failure\n` are handled as the expected result.

To use the reusable package in your own tests, see the [root README usage](../README.md#usage).

## Key features

- Exercises exact command output and exit-code checks.
- Exercises multi-command file creation and exact file-content checks.
- Exercises expected non-zero CLI results without treating them as test failures.

## Prerequisites

- **Go**: Go 1.25 or newer.
- **Docker**: The Docker CLI available on `PATH` for the pre-test build and a running daemon reachable by Testcontainers.
- **Just**: Required for the root `just e2e` command.

## Setup

1. Clone the checked-in example and enter the repository.

```bash
git clone https://github.com/totto2727-org/e2e.git
cd e2e
```

## API

This module exposes no user-facing API; it is a runnable fixture for the reusable [`cli` package](../README.md#api).

_This README was generated from the [share-artifact skill](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/SKILL.md) and [README template](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/readme/template.md)._
