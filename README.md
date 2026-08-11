# e2e

Reusable Go helpers for running CLI end-to-end test cases in isolated Testcontainers containers.

## Layout

- `cli/` owns the reusable image, container, command, stdout, and file assertions.
- `example/` is an independent Go module that demonstrates the package against a small Docker image.

The reusable package is the repository root. The example remains explicit so normal package tests do not require Docker.

## Development

```sh
nix develop
just fix
just check
just build
just test
just e2e
```

This Go module has no publish task. Consumers use `github.com/totto2727-org/e2e` through normal Go module resolution.
