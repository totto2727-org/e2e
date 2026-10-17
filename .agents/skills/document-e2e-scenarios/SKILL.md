---
name: document-e2e-scenarios
description: Create English sibling Markdown documents for Go, Haskell, TypeScript, and Rust E2E scenario files, covering commands, options, fixtures, ordered execution, exact output and state, and notes. Use when adding, changing, reviewing, or backfilling consumer scenario documentation. The bundled structural validator supports Go only. Do not use for API references, unit-test comments, or general QA plans.
metadata:
  author: totto2727
  version: 1.1.0
---

# Document E2E scenarios

Keep human-readable scenario intent beside executable E2E tests so reviewers can understand the workflow without reconstructing it from helper calls.
The documentation structure is language-independent. Automated discovery and coverage validation remain explicitly Go-only.

## Naming contract

- Create one sibling Markdown file per scenario-bearing source file by replacing its final source extension with `.md`.
- Go: `multiple_scenarios_test.go` becomes `multiple_scenarios_test.md`.
- Haskell: `MultipleScenariosSpec.hs` becomes `MultipleScenariosSpec.md`.
- TypeScript: `multiple_scenarios.test.ts` becomes `multiple_scenarios.test.md`.
- Rust: `multiple_scenarios.rs` becomes `multiple_scenarios.md`.
- For Go, scenario entrypoints are functions whose names end in `Scenario` in `*_test.go` files, as required by the bundled validator.
- For Haskell and TypeScript, use the exact display name from each `it` or `test` declaration. For Rust, use the native test function identifier, including snake_case. Do not add a separate function just to give a scenario an identifier.
- If one source file contains multiple scenarios, document them in one Markdown file with one complete second-level section per executable test case, preserving source order.
- Keep case-specific actions and assertions inside the standard test body. Extract helpers only for shared fixtures, lifecycle management, or assertions.
- Write all scenario prose in English. Preserve exact output, paths, flags, argv entries, and code identifiers verbatim.

## Authoring workflow

1. Read the complete scenario source, shared helpers, fixture builders, and current public library API.
2. Read the source/document pair for the language: [Go](references/example/multiple_scenarios_test.md), [Haskell](references/example/haskell/MultipleScenariosSpec.md), [TypeScript](references/example/typescript/multiple_scenarios.test.md), or [Rust](references/example/rust/multiple_scenarios.md). Each document links to its exact source. Keep examples as source and sibling documentation only, without standalone manifests, lockfiles, runner entrypoints, tool configuration, or an extra index.
3. Copy [the scenario template](assets/scenario-test.template.md) beside the source, name it according to the naming contract, and replace its placeholders.
4. Duplicate the complete scenario block beginning with the `##` heading once per executable test case. Define tests with the language's standard framework and call the environment helper inside their bodies. Do not introduce a library-specific case registry or document a suite wrapper as another scenario.
5. Remove unused optional rows instead of leaving empty prose. Keep each scenario's commands, arguments, fixtures, execution flow, results, and notes inside that scenario's section.
6. List each executable and subcommand path without arguments or options under `Commands under test`. List argument and option tokens with their purpose under `Arguments and options`, without an `Applies to` column. Put each complete invocation and its literal argv order under `Execution flow`.
7. Separate expected results into exit status, stdout, stderr, persisted state, filesystem/link state, and non-mutation guarantees where relevant. Distinguish asserted guarantees from behavior not independently tested.
8. Link the document back to its exact sibling source with `[source filename](./source filename)`.
9. For Go, run `python3 scripts/validate_scenario_docs.py /absolute/path/to/scenario/package` from the skill directory. For Haskell, TypeScript, and Rust, manually check source links, registered test cases, section order, placeholders, and assertions against the source. The Go validator does not validate those languages, even if it exits successfully when pointed at a mixed directory.
10. When execution is needed, integrate the exact source into an existing consumer test project. Use its normal package dependencies and framework commands. Do not create permanent sample infrastructure or add documentation examples to CI. Report unavailable package releases as a verification prerequisite, not as a reason to add checkout-relative dependencies.
11. Read the finished document against the source once more. Neither a structural check nor compilation proves semantic truth or successful Docker acceptance. Report which checks actually ran.

## Required sections

Create one second-level heading for each executable test case, using `##` followed by its backtick-wrapped display name (Haskell or TypeScript) or native test function identifier (Go or Rust), preserving source order. Under every scenario heading, use these third-level headings exactly and in this order:

1. `### Scope`
2. `### Commands under test`
3. `### Arguments and options`
4. `### Preconditions and fixtures`
5. `### Execution flow`
6. `### Expected results`
7. `### Notes`

The document title and source link precede all scenario sections. Never aggregate content from different scenarios under a shared topic heading. Use tables for compact command/argument/result mappings and numbered lists for ordered execution.

## Validation behavior

The bundled validator supports Go only. It:

- discovers scenario-bearing Go files from `*Scenario` function declarations;
- requires the exact sibling Markdown name;
- requires exactly one second-level section per discovered `*Scenario` function in source order, so keep Go declaration and registration order aligned;
- checks the source link and every scenario section's required third-level heading order;
- rejects unfilled `TODO`, `TBD`, or template placeholders;
- rejects symlinked, non-regular, out-of-root, or oversized source and document files;
- reports all errors in one run and exits nonzero.

Run it after every Go scenario or documentation change. A clean validator result is required for Go but does not replace source review or a real E2E run.
There is no bundled Haskell, TypeScript, or Rust coverage parser. Do not claim multi-language automated documentation validation or semantic validation.

## Failure handling

- If a command or expectation cannot be derived from the source, inspect the called helper or fixture instead of guessing.
- If the scenario intentionally accepts multiple outputs, document only the invariant substring or shape the test actually asserts.
- If a sibling document covers multiple test cases, keep each scenario self-contained rather than grouping commands or expectations across scenarios.
- If the Go validator discovers a helper-only file accidentally, rename the helper so only executable scenario entrypoints end in `Scenario`.
- Keep native checks free of Docker execution. TypeScript requires explicit Docker project selection, Rust Docker tests are ignored by default, and Haskell consumer Docker execution must remain a separately selected suite/task.
