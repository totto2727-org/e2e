---
name: implement-cli-e2e
description: Implement or refactor Go and Testcontainers end-to-end scenarios for CLI projects, including fixtures, argv-based command execution, observable assertions, task wiring, and mandatory sibling scenario documentation. Use when adding or changing consumer CLI E2E tests; do not use for unit tests, reusable container primitives, or image-build automation by itself.
metadata:
  author: totto2727
  version: 1.0.0
---

# Implement CLI E2E

Build each CLI scenario as an isolated Go test that exercises observable behavior through the shared `totto2727-org/e2e/cli` API.

## Required companion skill

Read and apply `$document-e2e-scenarios` before changing a scenario. Scenario documentation is part of the implementation, not a follow-up:

1. Create or update the sibling `<stem>_test.md` from the documentation skill's `assets/scenario-test.template.md`.
2. Use the documentation skill's complete [Go example](../document-e2e-scenarios/references/example/multiple_scenarios_test.go) and [sibling Markdown example](../document-e2e-scenarios/references/example/multiple_scenarios_test.md) as the concrete file-pair reference.
3. Keep commands, options, fixture setup, execution order, output, and state expectations synchronized with the Go source.
4. Run the documentation skill's `scripts/validate_scenario_docs.py` against the scenario package.
5. Treat a missing, stale, or validator-failing document as a blocking implementation failure.
6. Commit the Go scenario and its document together.

Do not report the scenario complete until the documentation validator passes.

## Workflow

### 1. Read the local contract

- Read the repository and package `AGENTS.md` files.
- Inspect the existing test entrypoint, scenario helpers, fixtures, task configuration, and image setup.
- Read [the CLI Testcontainers pattern](references/cli-testcontainers-pattern.md) before choosing file boundaries or lifecycle behavior.

### 2. Keep image preparation outside the test body

- Use a lightweight pre-test task, normally a Just recipe, to build the caller-owned local image.
- Keep the recipe to the required Docker build invocation unless the project contract requires more.
- Do not build, pull, or remove the image from the scenario Go code or the reusable E2E library.
- Make the project test task depend on the pre-test image task when the task runner supports dependencies.

### 3. Isolate every scenario

- Register one `cli.Case` for each independently named scenario.
- Let `cli.Run` create one disposable Testcontainers container per case.
- Keep fixed scenario filesystem roots inside the container; never mount the host workspace or Docker socket into scenario containers.
- Use the shared environment helper for commands and file transfer instead of adding standalone Shell E2E scripts.

### 4. Express fixtures, actions, and assertions in Go

- Build directories and fixture files with Go helpers and `cli.Environment` file APIs.
- Invoke the CLI under test with `cli.Command.Args`, `WorkingDir`, and `Env`.
- Reserve command execution for the CLI behavior under test. When Testcontainers requires an infrastructure command, route it through the shared E2E helper rather than a new shell script.
- Assert caller-visible exit codes, exact or intentionally partial stdout, serialized state, regular files, directories, symlink targets, missing paths, and non-mutation digests as applicable.
- Preserve foreign files and neighboring paths explicitly when ownership behavior is part of the contract.

### 5. Keep helpers narrow

- Pass command arguments as argv entries; do not concatenate shell command strings.
- Return or fail on every error. Avoid panic-based fixture construction when an error can be surfaced through the test helper.

### 6. Document every scenario

Invoke `$document-e2e-scenarios`, copy its template beside every scenario-bearing `*_test.go`, and describe the executable behavior in English. A Go file with multiple scenario functions has one sibling document containing one complete second-level section, headed by the backtick-wrapped function name, per scenario; never aggregate different scenarios under shared topic sections.

### 7. Verify the real surface

Run the repository's configured formatter/linter, build, and race-enabled test tasks. Then run the Docker-backed E2E entrypoint and confirm:

- every registered case starts in a distinct container;
- all CLI behavior and state assertions pass;
- scenario containers are cleaned;
- the caller-owned image remains available;
- the scenario-document validator passes.

## Failure handling

- If the image is absent, fail with the pre-test build instruction; do not add an implicit pull or build fallback.
