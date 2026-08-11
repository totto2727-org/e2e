# e2e

Go 1.25 package for reusable Testcontainers-based CLI end-to-end test helpers.

## Commands

- `just fix` formats and fixes both Go modules with golangci-lint.
- `just check` checks formatting and lint findings for both Go modules.
- `just test` runs reusable package tests with the race detector.
- `just build` builds both Go modules.
- `just e2e` runs the Docker E2E example explicitly.
- `just ci` runs check, build, reusable tests, and Docker E2E.

## Conventions

- Keep reusable assertions in `cli/` and scenario-specific workflows in `example/`.
- Use Just as the task runner; this repository has no JavaScript workspace.
- Keep Docker E2E behind the explicit `e2e` recipe.
- Check every returned error and wrap errors with `%w` when adding context.
