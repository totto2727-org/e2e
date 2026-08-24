# CLI Testcontainers implementation pattern

## Responsibility split

| Layer | Responsibility |
| --- | --- |
| Just or package task | Build the caller-owned local Docker image before tests. |
| Test entrypoint | Register named `cli.Case` values and select the prebuilt image. |
| Scenario file | Create fixtures, invoke the CLI, and assert observable outcomes in Go. |
| Shared scenario helpers | Wrap `cli.Environment` command/file operations and reusable assertions. |
| `totto2727-org/e2e/cli` | Resolve the local image, retain its immutable ID, and create one disposable Testcontainers container per case. |
| Sibling Markdown | Explain the executable scenario in English for reviewers. |

## Scenario lifecycle

1. The task runner invokes the lightweight image-build recipe.
2. The test entrypoint selects the caller-owned local image tag.
3. `cli.Run` resolves and retains the local image ID without pulling, building, or removing the image.
4. Each `cli.Case` receives a fresh environment backed by a distinct container.
5. The scenario creates all filesystem and domain fixtures inside that container.
6. The scenario invokes only the CLI behavior under test through argv-based commands.
7. The scenario asserts stdout, exit status, serialized state, links, files, and non-mutation as needed.
8. Testcontainers cleans the scenario container; the parent runner releases its image lease; the image remains caller-owned.

## Tooling contract

- Follow the repository's supported Go version.
- Route formatting through the repository's configured Go formatter and linter.
- Keep image preparation in an explicit pre-test task rather than the Go scenario body.
- Expose project tasks for `check`, `build`, and `test`; make `test` depend on the explicit pre-test image task.
- Run tests with the race detector and shuffled order. Leave `-count` unset unless repeated executions are intentional; `-shuffle=on` already prevents package-test cache reuse.

## Documentation contract

Apply `$document-e2e-scenarios` for every scenario-bearing Go file. Copy its bundled template, preserve the sibling naming contract, and keep every scenario in its own complete second-level section. Read the bundled concrete Go/Markdown example pair and run the validator before completion. The source and document belong in the same commit because either artifact alone is incomplete.
