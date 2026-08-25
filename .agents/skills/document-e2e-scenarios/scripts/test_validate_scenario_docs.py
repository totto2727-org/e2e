from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

import validate_scenario_docs as validator


class ScenarioDocumentValidatorTest(unittest.TestCase):
    def test_accepts_sections_grouped_by_scenario(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "health_test.go"
            source.write_text(
                "func healthyScenario() {}\nfunc unhealthyScenario() {}\n",
                encoding="utf-8",
            )
            source.with_suffix(".md").write_text(
                """# Health

Source: [health_test.go](./health_test.go)

## `healthyScenario`

### Scope
### Commands under test
### Arguments and options
### Preconditions and fixtures
### Execution flow
### Expected results
### Notes

## `unhealthyScenario`

### Scope
### Commands under test
### Arguments and options
### Preconditions and fixtures
### Execution flow
### Expected results
### Notes
""",
                encoding="utf-8",
            )

            errors = validator.validate_document(
                source, ("healthyScenario", "unhealthyScenario")
            )

            self.assertEqual(errors, [])

    def test_requires_sections_grouped_by_scenario(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "health_test.go"
            source.write_text(
                "func healthyScenario() {}\nfunc unhealthyScenario() {}\n",
                encoding="utf-8",
            )
            source.with_suffix(".md").write_text(
                """# Health

Source: [health_test.go](./health_test.go)

## Scope

`healthyScenario` and `unhealthyScenario`

## Commands under test
## Arguments and options
## Preconditions and fixtures
## Execution flow
## Expected results
## Notes
""",
                encoding="utf-8",
            )

            errors = validator.validate_document(
                source, ("healthyScenario", "unhealthyScenario")
            )

            self.assertIn("scenario sections must be exactly", "\n".join(errors))

    def test_requires_scenario_heading_outside_fences(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "health_test.go"
            source.write_text("func healthScenario() {}\n", encoding="utf-8")
            source.with_suffix(".md").write_text(
                """# Health

Source: [health_test.go](./health_test.go)

```markdown
## `healthScenario`
### Scope
### Commands under test
### Arguments and options
### Preconditions and fixtures
### Execution flow
### Expected results
### Notes
```
""",
                encoding="utf-8",
            )

            errors = validator.validate_document(source, ("healthScenario",))

            self.assertIn("scenario sections must be exactly", "\n".join(errors))

    def test_requires_scenario_heading_outside_long_fences(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "health_test.go"
            source.write_text("func healthScenario() {}\n", encoding="utf-8")
            source.with_suffix(".md").write_text(
                """# Health

Source: [health_test.go](./health_test.go)

````markdown
```
## `healthScenario`
### Scope
### Commands under test
### Arguments and options
### Preconditions and fixtures
### Execution flow
### Expected results
### Notes
```
````
""",
                encoding="utf-8",
            )

            errors = validator.validate_document(source, ("healthScenario",))

            self.assertIn("scenario sections must be exactly", "\n".join(errors))

    def test_rejects_non_ascii_closing_fence_whitespace(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "health_test.go"
            source.write_text("func healthScenario() {}\n", encoding="utf-8")
            source.with_suffix(".md").write_text(
                """# Health

Source: [health_test.go](./health_test.go)

```markdown
```[NBSP]
## `healthScenario`
### Scope
### Commands under test
### Arguments and options
### Preconditions and fixtures
### Execution flow
### Expected results
### Notes
""".replace("[NBSP]", "\N{NO-BREAK SPACE}"),
                encoding="utf-8",
            )

            errors = validator.validate_document(source, ("healthScenario",))

            self.assertIn("scenario sections must be exactly", "\n".join(errors))

    def test_ignores_scenario_text_inside_go_raw_strings(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            source = root / "fixture_test.go"
            source.write_text(
                "var fixture = `\nfunc fakeScenario() {}\n`\n", encoding="utf-8"
            )

            self.assertEqual(validator.discover_sources(root), [])

    def test_rejects_symlinked_scenario_sources(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            root = base / "cases"
            root.mkdir()
            outside = base / "outside_test.go"
            outside.write_text("func outsideScenario() {}\n", encoding="utf-8")
            (root / "linked_test.go").symlink_to(outside)

            with self.assertRaisesRegex(ValueError, "regular file"):
                validator.discover_sources(root.resolve())


if __name__ == "__main__":
    unittest.main()
