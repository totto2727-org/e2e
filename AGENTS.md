# e2e

## Repository structure

```text
cli/      Reusable Testcontainers-based CLI end-to-end test helpers.
example/  Independent Go module demonstrating the helpers with Docker.
```

The root Go module owns the reusable package. The example module keeps Docker execution explicit so normal package tests do not require Docker.

## Development commands

### Execution rules

- Run commands from the repository root unless a task explicitly targets `example/`.
- Use Just as the task runner; this repository has no JavaScript workspace.
- Keep Docker-backed tests behind the explicit `e2e` recipe.
- Check every returned error and wrap errors with `%w` when adding context.

### Standard tasks

- `just fix` — Format and autofix the root and example Go modules with golangci-lint.
- `just check` — Check formatting and lint findings for both Go modules.
- `just build` — Build both Go modules.
- `just test` — Run reusable package tests with the race detector.
- `just e2e` — Run the Docker E2E example with verbose output.
- `just ci` — Run checks, builds, reusable tests, and Docker E2E in sequence.

## Architecture

### Reusable package

- `cli.Run` builds one caller-supplied image, then creates and cleans a fresh container for each `cli.Case`.
- `cli.Environment` owns the command and file assertion primitives used inside a case.
- At most two cases run concurrently; image building and each case have bounded contexts.

### Example module

- `example/` is a separate Go module that replaces the root module with the local checkout.
- `example/Dockerfile` supplies the image used by the opt-in Docker scenarios.
- Multi-command and domain-specific workflows stay in the example or consumer test, not in the reusable package.

## Development tools

- **Go 1.25**: Builds and tests both modules.
- **Testcontainers for Go**: Builds images and manages isolated Docker containers.
- **golangci-lint**: Formats and lints Go code.
- **Just**: Runs repository development tasks.
- **Nix**: Provides the Go, golangci-lint, and Just development shell.

_This AGENTS.md was generated from the [share-artifact skill](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/SKILL.md) and [AGENTS template](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/agents/template.md)._
