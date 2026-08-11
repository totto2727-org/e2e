# e2e

Reusable Go helpers for running CLI end-to-end test cases in isolated Testcontainers containers.

## Layout

- `cli/` owns the reusable image, container, command, stdout, and file assertions.
- `example/` is an independent Go module that demonstrates the package against a small Docker image.

The reusable package is the repository root. The example remains explicit so normal package tests do not require Docker.

## Development

```sh
bun install
vp run fix
vp run check
vp run test
vp run build
vp run --filter @totto2727/e2e-example fix
vp run --filter @totto2727/e2e-example check
vp run --filter @totto2727/e2e-example build
go -C example test -race -shuffle=on -count=1 -v -parallel=2 ./...
```

This Go module has no publish task. Consumers use `github.com/totto2727-org/e2e` through normal Go module resolution.
