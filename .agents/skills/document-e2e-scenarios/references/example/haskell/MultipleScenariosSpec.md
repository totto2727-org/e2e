# Haskell example CLI scenarios

Source: [MultipleScenariosSpec.hs](./MultipleScenariosSpec.hs)

## `printsGreetingScenario`

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
- `runCases` creates a fresh container for the uniquely named `prints greeting` case.
- See [local build and explicit Docker commands](../README.md).

### Execution flow

1. Register `printsGreetingScenario` as `Case "prints greeting"` in `main`.
2. Execute argv `["echo", "hello from e2e"]` through `checkCommand`, using the image working directory and no environment overrides.
3. Compare the actual result with `Result 0 "hello from e2e\n" ""`.
4. After `runCases` completes cleanup, map this case's outcome to its own Hspec example, rethrowing any failure with `either throwIO pure`.

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
- Cases may execute concurrently. Registration order does not impose execution order.
- Hspec filters operate after these actions have already run. Select the case list before `runCases` to filter execution.

## `returnsExpectedFailureScenario`

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
- `runCases` creates a different fresh container for `returns expected failure`, not the greeting container.

### Execution flow

1. Register `returnsExpectedFailureScenario` as `Case "returns expected failure"` in `main`.
2. Execute argv `["false"]` through `checkCommand` with the image working directory and no environment overrides.
3. Compare the actual result with `Result 1 "" ""`.
4. Report the collected outcome as its own Hspec example after runner cleanup, rethrowing errors rather than swallowing them.

### Expected results

| Observation                    | Expected result                                  |
| ------------------------------ | ------------------------------------------------ |
| Exit status                    | Exactly `1`                                      |
| Standard output                | Exactly empty                                    |
| Standard error                 | Exactly empty                                    |
| Persisted state and filesystem | No file operations or state assertions are made. |

### Notes

- Application exit code `1` is a normal `Result`, not a Docker or transport error.
- The runner owns cleanup for both case containers and the stopped image lease. The sample does not independently inspect cleanup or container identity.
- These expectations describe executable assertions, not a claim that Docker acceptance has already passed.
