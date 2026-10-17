---
name: implement-cli-e2e
description: Integrate or refactor consumer CLI E2E tests with the shared environment library and the project's existing test framework, fixtures, and tasks. Use when adding or changing consumer CLI E2E tests; do not use for unit tests, reusable container primitives, or image-build automation by itself.
metadata:
  author: totto2727
  version: 1.1.0
---

# Implement CLI E2E

Supplement the project's existing tests without replacing its language or framework conventions.
Use [document-e2e-scenarios](../document-e2e-scenarios/SKILL.md) for scenario design, examples, and sibling documentation rather than defining a second set of rules here.
Use the [library README](../../../README.md) and its language-specific API links for supported operations and lifecycle guarantees.

## Integration workflow

1. Read the project's instructions, existing tests, fixtures, image preparation, and task configuration.
2. Choose the appropriate language implementation and use its public API inside the existing test framework. Keep discovery, naming, assertions, selection, scheduling, and reporting with that framework.
3. Reuse the project's fixture and task conventions. Add only the integration needed for the scenario, with image preparation outside test bodies and Docker-backed execution separate from lightweight tests.
4. Update the test and its sibling document together, following the scenario documentation skill.
5. Run the project's affected static checks and tests, then its actual Docker-backed E2E entrypoint. Confirm the intended behavior, resource cleanup, and preservation of the caller-owned image.

If execution is blocked by unavailable dependencies, images, or Docker access, report the prerequisite and what remains unverified instead of adding an implicit fallback.
