---
name: document-e2e-scenarios
description: Create English sibling Markdown documents for CLI E2E scenarios, explaining intent, commands, fixtures, ordered execution, observable results, and limitations. Use when adding, changing, reviewing, or backfilling consumer scenario documentation. The bundled structural validator supports Go only. Do not use for API references, unit-test comments, or general QA plans.
metadata:
  author: totto2727
  version: 1.1.0
---

# Document E2E scenarios

Keep scenario intent beside the executable tests so reviewers can understand the workflow without reconstructing it from helper calls.
This skill explains how to describe observable behavior, not language syntax or test-framework configuration.

## Scenario design

- Start with the behavior the consumer needs to verify, not a list of SDK methods to exercise.
- Use the environment helper to execute the real CLI and observe its output or filesystem state. A helper call is an operation, not the scenario's purpose.
- State the initial conditions, the ordered actions, and the observations that establish success.
- Keep actions that depend on shared state in one scenario. Do not make one scenario depend on another scenario's execution order.
- Distinguish application results, including expected nonzero exits, from infrastructure failures.
- Document only guarantees that the assertions establish. Identify important state, cleanup, or non-mutation behavior that the scenario does not independently verify.

## Source and document relationship

Create one sibling Markdown file per scenario-bearing source file by replacing its final source extension with `.md`.
Link the document to its exact source.
Use one complete section per scenario, with names and order matching the source.
Keep each scenario's preconditions, actions, and results together rather than grouping unrelated scenarios under shared topic headings.
Write scenario prose in English and preserve exact output, paths, arguments, options, and identifiers.

## Examples

The following source/document pairs show the same greeting and expected-failure behaviors through the public APIs.
Each document links to its source.
Use them to understand the correspondence between scenario intent, executable operations, assertions, and explanation, not as rules for framework syntax.

- [Go](references/example/multiple_scenarios_test.md)
- [Haskell](references/example/haskell/MultipleScenariosSpec.md)
- [TypeScript](references/example/typescript/multiple_scenarios.test.md)
- [Rust](references/example/rust/multiple_scenarios.md)

## Authoring workflow

1. Read the scenario source, called helpers, fixtures, and public API contracts.
2. Identify the behavior under test and the observations that distinguish success from failure.
3. Read a relevant example pair, then copy [the scenario template](assets/scenario-test.template.md) beside the source.
4. Describe the required initial state, actual invocation order, and assertions for each scenario. Remove unused optional rows instead of leaving empty prose.
5. Separate command names from their arguments and options. Put each complete invocation and its literal argument order under `Execution flow`.
6. Separate expected exit status, stdout, stderr, persisted state, filesystem state, and non-mutation guarantees where relevant.
7. Check names, source links, scenario order, placeholders, and expected results against the source. Inspect helpers or fixtures when an expectation is unclear instead of guessing.
8. Validate the document structure where tooling supports it. Report structural checks separately from compilation and actual E2E execution.

## Required sections

Use a second-level heading with the exact scenario name in backticks for each case, preserving source order.
Under every scenario heading, use these third-level headings in this order:

1. `### Scope`
2. `### Commands under test`
3. `### Arguments and options`
4. `### Preconditions and fixtures`
5. `### Execution flow`
6. `### Expected results`
7. `### Notes`

Place the document title and source link before the scenario sections.
Use tables for compact command, argument, and result mappings, and numbered lists for ordered execution.

## Validation and evidence

The bundled structural validator supports Go sources only.
From the skill directory, run `python3 scripts/validate_scenario_docs.py /absolute/path/to/scenario/package` for a supported source/document pair.
It checks discovered scenarios, sibling filenames, source links, section coverage and order, placeholders, and file safety.
Its discovery rules do not define how other languages or test frameworks must be written.
For other source types, review the same document properties against the source without claiming automated coverage.

A structural check does not establish semantic accuracy or successful Docker execution.
When execution is needed, integrate the example source into an existing consumer test project and use its normal dependencies and verification commands.
Report unavailable package releases as a verification prerequisite rather than adding checkout-relative dependency overrides.
Keep examples limited to source/document pairs, without permanent execution scaffolding or extra CI dependencies.
Report which checks ran, what they observed, and what remains unverified.
