# Rust example CLI scenarios

Source: [multiple_scenarios.rs](./multiple_scenarios.rs)

## `prints_greeting_scenario`

### Scope

Verify exact greeting bytes and successful application completion.

### Commands under test

| Command path | Purpose             |
| ------------ | ------------------- |
| `echo`       | Print the greeting. |

### Arguments and options

| Argument or option | Purpose                                                 |
| ------------------ | ------------------------------------------------------- |
| `hello from e2e`   | Pass the greeting as one literal positional argv entry. |

### Preconditions and fixtures

- The caller has built `example-cli:local` with `echo`, `false`, and a long-running default entrypoint.
- The native `prints_greeting_scenario` test invokes `with_environment` with a fresh container.
- The consumer crate depends on the published `caseyard` package from crates.io, without a checkout path.
- Place the source in the consumer's `tests/` directory so Cargo discovers it without a `[[test]]` declaration.

### Execution flow

1. The ignored native `prints_greeting_scenario` Tokio test awaits `with_environment("example-cli:local", callback)`.
2. Await `environment.run` with `Command::new(["echo", "hello from e2e"])`, no working-directory override, and no environment overrides.
3. Use native `assert_eq!` to compare the complete result with `CommandResult { exit_code: 0, stdout: b"hello from e2e\n".to_vec(), stderr: Vec::new() }`.
4. Return `Ok(())` from the callback and propagate `Result<(), ScopeError<Error>>` so lifecycle failures fail the native test.

### Expected results

| Observation                    | Expected result                                                   |
| ------------------------------ | ----------------------------------------------------------------- |
| Exit status                    | Exactly `0`                                                       |
| Standard output                | Exactly the bytes `hello from e2e\n`, including the final newline |
| Standard error                 | Exactly empty                                                     |
| Persisted state and filesystem | No file operations or state assertions are made.                  |

### Notes

- Expected byte vectors are assertions against real Docker execution, not fake results.
- The entrypoint is marked `#[ignore]`, so ordinary `cargo test` compiles but does not execute Docker actions.
- Native tests can run concurrently or be selected by name. Source order is not an execution dependency.

## `returns_expected_failure_scenario`

### Scope

Verify that the expected nonzero application result remains available for exact comparison.

### Commands under test

| Command path | Purpose                                          |
| ------------ | ------------------------------------------------ |
| `false`      | Produce the expected application failure status. |

### Arguments and options

| Argument or option | Purpose                                             |
| ------------------ | --------------------------------------------------- |
| None               | The executable receives no additional argv entries. |

### Preconditions and fixtures

- The caller-built `example-cli:local` provides `false` and stays running with its default entrypoint.
- The independent `returns_expected_failure_scenario` native test invokes `with_environment` and gets its own fresh container.

### Execution flow

1. The ignored native `returns_expected_failure_scenario` Tokio test awaits `with_environment("example-cli:local", callback)`.
2. Await `environment.run` with `Command::new(["false"])`, using the image working directory and default environment.
3. Use native `assert_eq!` to compare the complete result with `CommandResult { exit_code: 1, stdout: Vec::new(), stderr: Vec::new() }`.
4. Return errors rather than converting them to successful outcomes.

### Expected results

| Observation                    | Expected result                                  |
| ------------------------------ | ------------------------------------------------ |
| Exit status                    | Exactly `1`                                      |
| Standard output                | Exactly empty                                    |
| Standard error                 | Exactly empty                                    |
| Persisted state and filesystem | No file operations or state assertions are made. |

### Notes

- Exit code `1` is an application result. Docker, transport, framing, and cancellation errors remain errors.
- Testcontainers manages container cleanup. This sample does not independently inspect cleanup or identity.
- The documented expectations are not a claim of completed Docker acceptance.
