# {{SCENARIO_TITLE}}

Source: [{{SOURCE_FILE}}](./{{SOURCE_FILE}})

## Scope

Explain the user-visible behavior and list every `*Scenario` function covered by this file.

## Commands under test

| Command | Purpose |
| --- | --- |
| `my-cli ...` | Describe the behavior exercised by this invocation. |

## Arguments and options

| Argument or option | Applies to | Purpose |
| --- | --- | --- |
| `--example` | `my-cli ...` | Explain the tested meaning. |

## Preconditions and fixtures

- Describe the isolated HOME and working directory.
- Describe configuration, domain fixtures, persisted state, and foreign paths created before the first command.

## Execution flow

1. Describe the first command and why it runs first.
2. Describe subsequent mutations and commands in exact order.

## Expected results

| Observation | Expected result |
| --- | --- |
| Exit status | State the expected code or success/failure condition. |
| Standard output | Quote exact output or the invariant substring asserted by the test. |
| Persisted state | Describe configuration and state-file expectations. |
| Filesystem | Describe created, preserved, replaced, or removed paths and symlink targets. |

## Notes

- Record normalization, idempotency, ownership, isolation, or intentionally untested behavior when relevant.
