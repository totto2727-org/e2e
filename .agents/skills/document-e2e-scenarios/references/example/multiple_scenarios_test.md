# Example CLI scenarios

Source: [multiple_scenarios_test.go](./multiple_scenarios_test.go)

## `TestPrintsGreetingScenario`

### Scope

Verify that the CLI image can print a caller-visible greeting successfully.

### Commands under test

| Command path | Purpose                      |
| ------------ | ---------------------------- |
| `echo`       | Print the expected greeting. |

### Arguments and options

| Argument or option | Purpose                                               |
| ------------------ | ----------------------------------------------------- |
| `hello from e2e`   | Supply the exact greeting as one positional argument. |

### Preconditions and fixtures

- The caller has built the local `example-cli:local` image.
- The ordinary `TestPrintsGreetingScenario` test calls `t.Parallel()` and then `cli.NewEnvironment(t, "example-cli:local")` inside its body to prepare a fresh container and immutable image lease.

### Execution flow

1. Run `echo "hello from e2e"`, passing the greeting as one argv entry after `echo`.
2. Use `CheckCommand(command, want)` to compare exit status, stdout, and stderr with an exact `cli.Result`.

### Expected results

| Observation     | Expected result                  |
| --------------- | -------------------------------- |
| Exit status     | `0`                              |
| Standard output | Exactly `hello from e2e\n`       |
| Standard error  | Exactly empty                    |
| Persisted state | No state is persisted.           |
| Filesystem      | No files are created or changed. |

### Notes

- The scenario asserts exact output, including the trailing newline.

## `TestReturnsExpectedFailureScenario`

### Scope

Verify that an expected nonzero CLI result remains observable without failing the test harness itself.

### Commands under test

| Command path | Purpose                                   |
| ------------ | ----------------------------------------- |
| `false`      | Produce the expected nonzero exit status. |

### Arguments and options

| Argument or option | Purpose                                          |
| ------------------ | ------------------------------------------------ |
| None               | The command takes no arguments in this scenario. |

### Preconditions and fixtures

- The caller has built the local `example-cli:local` image.
- The ordinary `TestReturnsExpectedFailureScenario` test calls `t.Parallel()` and then `cli.NewEnvironment(t, "example-cli:local")` inside its body to prepare a different fresh container and immutable image lease from `TestPrintsGreetingScenario`.

### Execution flow

1. Run `false` without arguments.
2. Use `CheckCommand(command, want)` to compare exit status, stdout, and stderr with an exact `cli.Result`.

### Expected results

| Observation     | Expected result                  |
| --------------- | -------------------------------- |
| Exit status     | `1`                              |
| Standard output | Empty                            |
| Standard error  | Exactly empty                    |
| Persisted state | No state is persisted.           |
| Filesystem      | No files are created or changed. |

### Notes

- The nonzero status is the expected caller-visible result for this scenario.
