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
- Enter the toolchain environment with `nix develop` before running Just or Go commands.
- Use Just as the task runner; this repository has no JavaScript workspace.
- Keep Docker-backed tests behind the explicit `e2e` recipe.
- Check every returned error and wrap errors with `%w` when adding context.

### Standard tasks

- `nix develop` — Enter the environment that supplies Go, golangci-lint, and Just.
- `just fix` — Format and autofix the root and example Go modules with golangci-lint.
- `just check` — Check formatting and lint findings for both Go modules.
- `just build` — Build both Go modules.
- `just test` — Run reusable package tests with the race detector.
- `just e2e` — Run the Docker E2E example with verbose output.
- `just ci` — Run checks, builds, reusable tests, and Docker E2E in sequence.

## Architecture

### Reusable package

- `cli.Run` resolves a prebuilt image from the local daemon without pulling, then creates and cleans a fresh container for each `cli.Case`.
- A temporary stopped container retains the resolved image ID until every case completes; cleanup removes that lease, not the image.
- `cli.Environment` owns the command and file assertion primitives used inside a case.
- At most two cases run concurrently; each case has a bounded context.

### Example module

- `example/` is a separate Go module that replaces the root module with the local checkout.
- `example/Dockerfile` supplies the image used by the opt-in Docker scenarios.
- Multi-command and domain-specific workflows stay in the example or consumer test, not in the reusable package.

## Development tools

- **Go 1.25**: Builds and tests both modules.
- **Docker CLI**: Builds the example image before the Go test starts.
- **Testcontainers for Go**: Manages isolated Docker containers for each case.
- **golangci-lint**: Formats and lints Go code.
- **Just**: Runs repository development tasks.
- **Nix**: Provides the Go, golangci-lint, and Just development shell.

## Package-specific rules

- Keep the reusable root module independent from the example module's local `replace` directive.
- Keep Docker-backed scenarios opt-in through `just e2e`; unit and race tests must not require a Docker daemon.
- Pass command arguments as argv entries and compare caller-visible exit codes, stdout, or copied file content explicitly.
- Preserve bounded build and command contexts plus the two-case concurrency limit unless a measured requirement justifies changing them.
- Run `just ci` inside `nix develop` before handoff when Docker is available; otherwise report the skipped Docker surface and run `just check`, `just build`, and `just test`.

_This AGENTS.md was generated from the [share-artifact skill](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/SKILL.md) and [AGENTS template](https://raw.githubusercontent.com/totto2727-org/agent/refs/heads/main/plugins/totto2727-coding/skills/share-artifact/agents/template.md)._
