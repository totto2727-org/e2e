# {{SCENARIO_FILE_TITLE}}

Source: [{{SOURCE_FILE}}](./{{SOURCE_FILE}})

## `{{SCENARIO_TITLE}}`

### Scope

Explain the single user-visible behavior covered by this scenario.

### Commands under test

| Command path        | Purpose                                                    |
| ------------------- | ---------------------------------------------------------- |
| `my-cli subcommand` | Describe the behavior exercised through this command path. |

### Arguments and options

| Argument or option | Purpose                     |
| ------------------ | --------------------------- |
| `--example`        | Explain the tested meaning. |

### Preconditions and fixtures

- Describe the initial environment and working directory required by the scenario.
- Describe any configuration, fixtures, persisted state, and foreign paths required before the first command.

### Execution flow

1. Write the complete first invocation, including argv order, and explain why it runs first.
2. Describe subsequent mutations and commands in exact order.

### Expected results

| Observation     | Expected result                                                              |
| --------------- | ---------------------------------------------------------------------------- |
| Exit status     | State the expected code or success/failure condition.                        |
| Standard output | Quote exact output or the invariant substring asserted by the test.          |
| Persisted state | Describe configuration and state-file expectations.                          |
| Filesystem      | Describe created, preserved, replaced, or removed paths and symlink targets. |

### Notes

- Record normalization, idempotency, ownership, isolation, or intentionally untested behavior when relevant.
