# Example CLI scenarios

Source: [multiple_scenarios_test.go](./multiple_scenarios_test.go)

## `printsGreetingScenario`

### Scope

Verify that the CLI image can print a caller-visible greeting successfully.

### Commands under test

| Command | Purpose |
| --- | --- |
| `echo "hello from e2e"` | Print the expected greeting. |

### Arguments and options

| Argument or option | Applies to | Purpose |
| --- | --- | --- |
| `hello from e2e` | `echo` | Supply the exact greeting text. |

### Preconditions and fixtures

- The caller has built the local `example-cli:local` image.
- `cli.Run` provides a fresh container for this scenario.

### Execution flow

1. Run `echo` with the greeting as a separate argv entry.
2. Capture the exit status and standard output.

### Expected results

| Observation | Expected result |
| --- | --- |
| Exit status | `0` |
| Standard output | Exactly `hello from e2e\n` |
| Persisted state | No state is persisted. |
| Filesystem | No files are created or changed. |

### Notes

- The scenario asserts exact output, including the trailing newline.

## `returnsExpectedFailureScenario`

### Scope

Verify that an expected nonzero CLI result remains observable without failing the test harness itself.

### Commands under test

| Command | Purpose |
| --- | --- |
| `false` | Produce the expected nonzero exit status. |

### Arguments and options

| Argument or option | Applies to | Purpose |
| --- | --- | --- |
| None | `false` | The command takes no arguments in this scenario. |

### Preconditions and fixtures

- The caller has built the local `example-cli:local` image.
- `cli.Run` provides a different fresh container from `printsGreetingScenario`.

### Execution flow

1. Run `false` without arguments.
2. Capture the exit status and standard output.

### Expected results

| Observation | Expected result |
| --- | --- |
| Exit status | `1` |
| Standard output | Empty |
| Persisted state | No state is persisted. |
| Filesystem | No files are created or changed. |

### Notes

- The nonzero status is the expected caller-visible result for this scenario.
