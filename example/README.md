# Go CLI E2E example

This optional Testcontainers example demonstrates the reusable `github.com/totto2727-org/e2e/cli` package against a small Docker image.

## Usage

Run the example from the repository root:

```bash
just e2e
```

## Key features

- Exercises exact command output and exit-code checks.
- Exercises multi-command file creation and exact file-content checks.
- Exercises expected non-zero CLI results without treating them as test failures.

## Prerequisites

- **Go**: Go 1.25 or newer.
- **Docker**: A running Docker daemon reachable by Testcontainers.
- **Just**: Required for the root `just e2e` command.

## Setup

1. Clone the repository and enter its directory.

```bash
git clone https://github.com/totto2727-org/e2e.git
cd e2e
```

2. Run the example.

```bash
just e2e
```

## API

This module exposes no user-facing API; it is a runnable fixture for the reusable [`cli` package](../README.md#api).

## Development

For repository structure and development commands, see [the root AGENTS.md](../AGENTS.md).

## License

No license has been declared for this repository.

_This README was generated from the [share-artifact skill](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/SKILL.md) and [README template](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/readme/template.md)._
