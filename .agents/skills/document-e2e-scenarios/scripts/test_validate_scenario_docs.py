from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

import validate_scenario_docs as validator


class ScenarioDocumentValidatorTest(unittest.TestCase):
    def test_requires_function_names_in_scope_not_fences_or_notes(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "health_test.go"
            source.write_text("func healthScenario() {}\n", encoding="utf-8")
            source.with_suffix(".md").write_text(
                """# Health

Source: [health_test.go](./health_test.go)

## Scope

```text
`healthScenario`
```

## Commands under test
## Arguments and options
## Preconditions and fixtures
## Execution flow
## Expected results
## Notes

`healthScenario`
""",
                encoding="utf-8",
            )

            errors = validator.validate_document(source, ("healthScenario",))

            self.assertIn("Scope must name", "\n".join(errors))

    def test_ignores_scenario_text_inside_go_raw_strings(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
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
