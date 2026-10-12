# CLI Testcontainers implementation pattern

## Responsibility split

| Layer                      | Responsibility                                                                                                 |
| -------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Just or package task       | Build the caller-owned local Docker image before tests.                                                        |
| Test entrypoint            | Define ordinary `Test*` or `t.Run` tests and invoke `cli.NewEnvironment(t, image)` within each body.           |
| Scenario file              | Create fixtures, invoke the CLI, and assert observable outcomes in Go.                                         |
| Shared scenario helpers    | Wrap `cli.Environment` command/file operations and reusable assertions.                                        |
| `totto2727-org/e2e/go/cli` | Resolve the local image, retain its immutable ID, and create one disposable Testcontainers container per case. |
| Sibling Markdown           | Explain the executable scenario in English for reviewers.                                                      |

## Scenario lifecycle

1. The task runner invokes the lightweight image-build recipe.
2. The test entrypoint selects the caller-owned local image tag.
3. The ordinary test body calls `cli.NewEnvironment(t, image)`, which resolves and retains the local immutable image without pulling, building, or removing it.
4. Each invocation returns a fresh environment backed by a distinct container. Go's framework controls parallelism and selection, not a library runner.
5. The scenario creates all filesystem and domain fixtures inside that container.
6. The scenario invokes only the CLI behavior under test through argv-based commands.
7. The scenario asserts stdout, exit status, serialized state, links, files, and non-mutation as needed.
8. The test's registered `t.Cleanup` functions remove its container and image lease, report cleanup errors, and preserve the caller's image.

## Tooling contract

- Follow the repository's supported Go version.
- Route formatting through the repository's configured Go formatter and linter.
- Keep image preparation in an explicit pre-test task rather than the Go scenario body.
- Expose `check`, `build`, Docker-free `test`, and explicit `e2e` tasks. Only `e2e` depends on the image task.
- Run tests with the race detector and shuffled order. Leave `-count` unset unless repeated executions are intentional; `-shuffle=on` already prevents package-test cache reuse.

## Documentation contract

Apply `$document-e2e-scenarios` for every scenario-bearing Go file. Copy its bundled template, preserve the sibling naming contract, and keep every scenario in its own complete second-level section. Read the bundled concrete Go/Markdown example pair and run the validator before completion. The source and document belong in the same commit because either artifact alone is incomplete.
