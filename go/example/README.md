# Go CLI E2E example

This optional Docker example is for users of the reusable [`cli` package](../README.md#api) and contributors who maintain its acceptance scenarios.
The separate Go module uses the local checkout through a `replace` directive.

## Prerequisites

Use the repository's [Go and Docker requirements](../README.md#prerequisites), plus Nix and Just for the development tasks.

## Usage

Run from the repository root:

```bash
pnpm exec vp run @totto2727/e2e-go-example#e2e
```

The task builds `e2e-example:local`, runs the six cases with the race detector and shuffled order, and checks cleanup.
A successful run reports each case and the overall test as `PASS`.

| Case                       | Assertion                                                                                                                                            |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TestCLI/stdout`           | Exit code `0`, stdout `hello from e2e\n`, and empty stderr through `CheckCommand(command, want)`.                                                    |
| `TestCLI/file_output`      | Three commands produce `first\nsecond\n`; `CheckFile(path, want)` checks the exact file bytes. All commands have empty stderr.                       |
| `TestCLI/expected_failure` | Exit code `7`, stdout `expected-failure\n`, and empty stderr are an expected result, not an execution error.                                         |
| `TestCLI/separated_output` | Both `CheckCommand` and native `Run` return exit code `7` and separate exact stdout/stderr, with the requested working directory and environment.    |
| `TestCLI/path_assertions`  | `WriteFile(path, content)` creates a fixture; assertions check physical path types, absent paths, dangling links, and literal symbolic-link targets. |
| `TestCLI/deadline`         | `Run` of `sleep infinity` returns a deadline error within 70 seconds; its exit code is not inspected.                                                |

Empty stderr expectations are intentional exact checks.
No case expects combined stdout and stderr.
The deadline case adds approximately one minute to the run.
The task permits eight parallel Go tests while the helper limits active cases to two.
Each case has a one-minute budget and fresh 30-second container cleanup context.
After the tests, the task checks that the caller-owned image remains and no scenario or lease container remains.

See the [sibling scenario document](./e2e_test.md) for the ordered commands, fixtures, and exact results.
See the [Go API reference](../README.md#api) and [breaking migration table](../README.md#breaking-migration) to write or migrate consumer tests.
See the [shared contract](../../README.md) for behavior common to all language implementations.
The reusable Go module's tasks do not delegate to this independent module.
`pnpm exec vp run @totto2727/e2e-go-example#check` and `pnpm exec vp run @totto2727/e2e-go-example#build` check and build the example without Docker.
`pnpm exec vp run @totto2727/e2e-go-example#test` uses `go test -race -run '^$' ./...` to compile the Docker scenarios without executing them; it is not native behavioral coverage or Docker acceptance.
The executable build output is `go/example/tmp/build/e2e-example`.
For standalone work, `nix develop .#go -c just --justfile go/example/Justfile ci` does not require the JavaScript task runner or Docker.

## API

This example exports no consumer API.
All command execution uses `cli.Command` and `cli.Result` through the reusable package.
