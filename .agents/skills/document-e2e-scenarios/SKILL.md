---
name: document-e2e-scenarios
description: Create and validate English sibling Markdown documents for Go E2E scenario files, covering commands, options, fixtures, ordered execution, expected output and state, and notes. Use when adding, changing, reviewing, or backfilling `*_test.go` scenario documentation; do not use for API reference, unit-test comments, or general QA plans.
metadata:
  author: totto2727
  version: 1.0.0
---

# Document E2E scenarios

Keep human-readable scenario intent beside executable Go E2E tests so reviewers can understand the workflow without reconstructing it from helper calls.

## Naming contract

- For every `*_test.go` file containing one or more functions whose names end in `Scenario`, create a sibling file with the same stem and `.md` suffix.
- Example: `add_test.go` becomes `add_test.md`.
- If one Go file contains multiple scenarios, document them in the same Markdown file with one complete second-level section per `*Scenario` function, in source order.
- Write all scenario prose in English. Preserve exact CLI output, paths, flags, and code identifiers verbatim.

## Authoring workflow

1. Read the complete Go scenario, shared helpers, and fixture builders it calls.
2. Read the complete generated-file example pair: [Go scenarios](references/example/multiple_scenarios_test.go) and [sibling scenario document](references/example/multiple_scenarios_test.md).
3. Copy [the scenario template](assets/scenario-test.template.md) beside the Go source and rename it to `<stem>_test.md`.
4. Duplicate the complete scenario block beginning with the `##` heading once for every `*Scenario` function, preserving source order, then replace every placeholder.
5. Remove unused optional rows instead of leaving empty prose. Keep each scenario's commands, arguments, fixtures, execution flow, results, and notes inside that scenario's section.
6. List each executable and subcommand path without arguments or options under `Commands under test`. List argument and option tokens with their purpose under `Arguments and options`; do not add an `Applies to` column. Put each complete invocation and its argv order under `Execution flow`.
7. Separate expected results into exit status, stdout, persisted JSON, filesystem/link state, and non-mutation guarantees where relevant.
8. Link the document back to its sibling Go source with `[<stem>_test.go](./<stem>_test.go)`.
9. Run the validator from the skill directory:

   ```console
   python3 scripts/validate_scenario_docs.py /absolute/path/to/scenario/package
   ```

10. Read the finished document against the Go source once more. The validator checks structure and coverage, not semantic truth.

## Required sections

Create one second-level heading for each scenario function, using `##` followed by the backtick-wrapped function name and preserving source order. Under every scenario heading, use these third-level headings exactly and in this order:

1. `### Scope`
2. `### Commands under test`
3. `### Arguments and options`
4. `### Preconditions and fixtures`
5. `### Execution flow`
6. `### Expected results`
7. `### Notes`

The document title and source link precede all scenario sections. Never aggregate content from different scenarios under a shared topic heading. Use tables for compact command/argument/result mappings and numbered lists for ordered execution.

## Validation behavior

The bundled validator:

- discovers scenario-bearing Go files from `*Scenario` function declarations;
- requires the exact sibling Markdown name;
- requires exactly one second-level section per discovered `*Scenario` function in source order;
- checks the source link and every scenario section's required third-level heading order;
- rejects unfilled `TODO`, `TBD`, or template placeholders;
- rejects symlinked, non-regular, out-of-root, or oversized source and document files;
- reports all errors in one run and exits nonzero.

Run it after every scenario or documentation change. A clean validator result is required but does not replace the real E2E run.

## Failure handling

- If a command or expectation cannot be derived from the Go source, inspect the called helper or fixture instead of guessing.
- If the scenario intentionally accepts multiple outputs, document the invariant substring or shape the test actually asserts.
- If a sibling document covers multiple scenario functions, keep each scenario self-contained rather than grouping commands or expectations by topic across scenarios.
- If the validator discovers a helper-only file accidentally, rename the helper so only executable scenario entrypoints end in `Scenario`.
