# Haskell example CLI scenarios

Source: [MultipleScenariosSpec.hs](./MultipleScenariosSpec.hs)

## `prints greeting`

### Scope

Verify that the local CLI image prints the exact caller-visible greeting successfully.

### Commands under test

| Command path | Purpose             |
| ------------ | ------------------- |
| `echo`       | Print the greeting. |

### Arguments and options

| Argument or option | Purpose                                                 |
| ------------------ | ------------------------------------------------------- |
| `hello from e2e`   | Supply the greeting as one literal positional argument. |

### Preconditions and fixtures

- The caller has built `example-cli:local` with `echo`, `false`, and a default entrypoint that keeps the container running.
- The Hspec `it "prints greeting"` body calls `withEnvironment`, creating a fresh container and a private stopped image lease only when that example executes.

### Execution flow

1. Inside Hspec `it "prints greeting"`, call `withEnvironment "example-cli:local"` with an inline callback containing the command and assertion.
2. Execute argv `["echo", "hello from e2e"]` through `checkCommand`, using the image working directory and no environment overrides.
3. Compare the actual result with `Result 0 "hello from e2e\n" ""`.
4. `withEnvironment` removes this invocation's running container and stopped lease before returning or rethrowing the original example exception.

### Expected results

| Observation                    | Expected result                                                         |
| ------------------------------ | ----------------------------------------------------------------------- |
| Exit status                    | Exactly `0`                                                             |
| Standard output                | Exactly the ASCII bytes `hello from e2e\n`, including the final newline |
| Standard error                 | Exactly empty                                                           |
| Persisted state and filesystem | No file operations or state assertions are made.                        |

### Notes

- `OverloadedStrings` supplies the ASCII `ByteString` expectations, not synthetic command output.
- The library compares every result field exactly and propagates infrastructure failures.
- Hspec owns scheduling. This sample is sequential by default, and a consumer can use Hspec `parallel` and `--jobs N` for independent examples.
- Hspec `--match` selects actual execution and `--dry-run` performs no Docker operations. There is no pre-run outcome aggregation.

## `returns expected failure`

### Scope

Verify that a nonzero application result is observable and can be asserted as the expected outcome.

### Commands under test

| Command path | Purpose                                          |
| ------------ | ------------------------------------------------ |
| `false`      | Produce the expected application failure status. |

### Arguments and options

| Argument or option | Purpose                                      |
| ------------------ | -------------------------------------------- |
| None               | `false` receives no additional argv entries. |

### Preconditions and fixtures

- The caller has built the local `example-cli:local` image with `false` and a long-running default entrypoint.
- The standard Hspec `it "returns expected failure"` calls `withEnvironment` to create its own fresh container and stopped lease, not the greeting container or lease.

### Execution flow

1. Inside Hspec `it "returns expected failure"`, call `withEnvironment "example-cli:local"` with an inline callback containing the command and assertion.
2. Execute argv `["false"]` through `checkCommand` with the image working directory and no environment overrides.
3. Compare the actual result with `Result 1 "" ""`.
4. Finish cleanup before returning to Hspec, preserving the original callback exception if the example failed.

### Expected results

| Observation                    | Expected result                                  |
| ------------------------------ | ------------------------------------------------ |
| Exit status                    | Exactly `1`                                      |
| Standard output                | Exactly empty                                    |
| Standard error                 | Exactly empty                                    |
| Persisted state and filesystem | No file operations or state assertions are made. |

### Notes

- Application exit code `1` is a normal `Result`, not a Docker or transport error.
- The scoped helper owns cleanup for its running container and its stopped image lease. The sample does not independently inspect cleanup or container identity.
- These expectations describe executable assertions, not a claim that Docker acceptance has already passed.
