# e2e

Go 1.25 package for reusable Testcontainers-based CLI end-to-end test helpers.

## Commands

- `vp run fix` formats and fixes the reusable package with golangci-lint.
- `vp run check` checks formatting and lint findings for the reusable package.
- `vp run test` runs reusable package tests with the race detector and shuffled order.
- `vp run build` builds the reusable package.
- `vp run --filter @totto2727/e2e-example check` checks the example module.
- `go -C example test -race -shuffle=on -count=1 -v -parallel=2 ./...` runs the Docker E2E example explicitly.

## Conventions

- Keep reusable assertions in `cli/` and scenario-specific workflows in `example/`.
- Do not add a standard Vite+ `test` task to the example because it starts Docker.
- Check every returned error and wrap errors with `%w` when adding context.
