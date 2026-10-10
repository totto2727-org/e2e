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
- `TestCLI/separated_output` checks stdout and stderr separately, command directory and environment, and existing combined-output compatibility.
- `TestCLI/path_assertions` checks physical files and directories, absent paths, dangling links, and literal symbolic-link targets.
- `TestCLI/deadline` checks that `sleep infinity` returns a deadline error instead of blocking the harness.

The deadline case adds approximately one minute to the run.
The task permits eight parallel Go tests while the helper limits active cases to two.
After the tests, the task checks that the caller-owned image remains and no scenario or lease container remains.
For the complete fixture and assertion contract, see [the scenario document](./e2e_test.md).

To use the reusable package in your own tests, see the [root README usage](../README.md#usage).

## Key features

- Exercises exact command output and exit-code checks.
- Exercises multi-command file creation and exact file-content checks.
- Exercises expected non-zero CLI results without treating them as test failures.
- Exercises cancellation of an attached command and container cleanup after its deadline.
- Exercises separate output streams and path assertions through the public helper API.

## Prerequisites

- The example inherits the repository's [Go and Docker requirements](../README.md#prerequisites).
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
