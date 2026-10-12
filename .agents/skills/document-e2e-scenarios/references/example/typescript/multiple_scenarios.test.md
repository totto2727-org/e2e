# TypeScript example CLI scenarios

Source: [multiple_scenarios.test.ts](./multiple_scenarios.test.ts)

## `printsGreetingScenario`

### Scope

Verify successful execution with an exact greeting, including its final newline.

### Commands under test

| Command path | Purpose             |
| ------------ | ------------------- |
| `echo`       | Print the greeting. |

### Arguments and options

| Argument or option | Purpose                                                  |
| ------------------ | -------------------------------------------------------- |
| `hello from e2e`   | Pass the complete greeting as one positional argv entry. |

### Preconditions and fixtures

- The caller has built `example-cli:local` with `echo`, `false`, and a long-running default entrypoint.
- Public `registerCases` from `@totto2727/e2e-cli/vitest` registers `prints greeting` with its own fresh container.
- The private workspace dependency resolves public imports to the checkout's built package exports, without aliases or a registry library.
- See [build and project selection instructions](../README.md).

### Execution flow

1. Register the uniquely named case with `run: printsGreetingScenario`.
2. Await `environment.checkCommand` with literal argv `['echo', 'hello from e2e']`, no working-directory override, and no environment overrides.
3. Compare all actual fields with `{ exitCode: 0, stdout: 'hello from e2e\n', stderr: '' }`.

### Expected results

| Observation                    | Expected result                                            |
| ------------------------------ | ---------------------------------------------------------- |
| Exit status                    | Exactly `0`                                                |
| Standard output                | Exactly `hello from e2e\n`, including the trailing newline |
| Standard error                 | Exactly empty                                              |
| Persisted state and filesystem | No file operations or state assertions are made.           |

### Notes

- Expected strings are assertion values, never replacements for Docker execution.
- Registration can execute cases concurrently, so source order is not a timing dependency.
- The `native` project never includes this Docker-bearing source. Explicitly select `docker` to execute it.

## `returnsExpectedFailureScenario`

### Scope

Verify that an expected nonzero application status does not become a harness failure.

### Commands under test

| Command path | Purpose                                         |
| ------------ | ----------------------------------------------- |
| `false`      | Return the expected application failure status. |

### Arguments and options

| Argument or option | Purpose                                             |
| ------------------ | --------------------------------------------------- |
| None               | The executable receives no additional argv entries. |

### Preconditions and fixtures

- `example-cli:local` already exists locally, provides `false`, and remains running with its default entrypoint.
- `returns expected failure` receives a different fresh container from `prints greeting`.

### Execution flow

1. Register the separate case with `run: returnsExpectedFailureScenario`.
2. Await `environment.checkCommand` with literal argv `['false']`, retaining the image working directory and default environment.
3. Compare all actual fields with `{ exitCode: 1, stdout: '', stderr: '' }`.

### Expected results

| Observation                    | Expected result                                  |
| ------------------------------ | ------------------------------------------------ |
| Exit status                    | Exactly `1`                                      |
| Standard output                | Exactly empty                                    |
| Standard error                 | Exactly empty                                    |
| Persisted state and filesystem | No file operations or state assertions are made. |

### Notes

- Docker, transport, and cancellation errors propagate rather than being treated as the expected exit code.
- The library owns case and lease cleanup and leaves the caller's image intact. This sample makes no separate identity or cleanup assertions.
- These are executable expectations, not a record of completed Docker acceptance.
