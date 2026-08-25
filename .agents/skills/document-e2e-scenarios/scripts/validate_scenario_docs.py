from __future__ import annotations

import argparse
import re
import stat
import sys
from pathlib import Path

MAX_FILE_SIZE = 1_048_576
SCENARIO_PATTERN = re.compile(r"^func\s+([A-Za-z0-9_]+Scenario)\s*\(", re.MULTILINE)
REQUIRED_SCENARIO_HEADINGS = (
    "### Scope",
    "### Commands under test",
    "### Arguments and options",
    "### Preconditions and fixtures",
    "### Execution flow",
    "### Expected results",
    "### Notes",
)
PLACEHOLDER_PATTERN = re.compile(r"\{\{[^}]+\}\}|\b(?:TODO|TBD)\b", re.IGNORECASE)
MARKDOWN_FENCE_PATTERN = re.compile(r"^ {0,3}(`{3,}|~{3,})(.*)$")


class _FileValidationError(ValueError):
    pass


def read_regular_text(path: Path, root: Path) -> str:
    metadata = path.lstat()
    if not stat.S_ISREG(metadata.st_mode):
        raise _FileValidationError(f"{path}: expected a regular file without symlinks")
    if metadata.st_size > MAX_FILE_SIZE:
        raise _FileValidationError(f"{path}: exceeds the {MAX_FILE_SIZE}-byte limit")
    resolved = path.resolve(strict=True)
    if not resolved.is_relative_to(root):
        raise _FileValidationError(f"{path}: resolves outside scenario root {root}")
    return path.read_text(encoding="utf-8")


def go_code_only(text: str) -> str:
    result = list(text)
    state = "code"
    index = 0
    while index < len(text):
        character = text[index]
        following = text[index + 1] if index + 1 < len(text) else ""
        if state == "code":
            pair = character + following
            if pair in {"//", "/*"}:
                state = "line-comment" if pair == "//" else "block-comment"
                result[index] = result[index + 1] = " "
                index += 2
                continue
            if character in {'"', "'", "`"}:
                state = character
                result[index] = " "
        elif state == "line-comment":
            if character == "\n":
                state = "code"
            else:
                result[index] = " "
        elif state == "block-comment":
            if character + following == "*/":
                result[index] = result[index + 1] = " "
                state = "code"
                index += 2
                continue
            if character != "\n":
                result[index] = " "
        elif state == "`":
            if character == "`":
                state = "code"
            if character != "\n":
                result[index] = " "
        else:
            if character == "\\" and following:
                result[index] = result[index + 1] = " "
                index += 2
                continue
            if character == state:
                state = "code"
            if character != "\n":
                result[index] = " "
        index += 1
    return "".join(result)


def markdown_prose(text: str) -> str:
    lines: list[str] = []
    fence = ""
    for line in text.splitlines():
        match = MARKDOWN_FENCE_PATTERN.fullmatch(line)
        if fence:
            if match is not None:
                marker, suffix = match.groups()
                if (
                    marker[0] == fence[0]
                    and len(marker) >= len(fence)
                    and not suffix.strip(" \t")
                ):
                    fence = ""
            lines.append("")
            continue
        if match is not None:
            marker, info = match.groups()
            if marker[0] == "~" or "`" not in info:
                fence = marker
                lines.append("")
                continue
        lines.append(line)
    return "\n".join(lines)


def scenario_functions(source: Path, root: Path) -> tuple[str, ...]:
    text = read_regular_text(source, root)
    return tuple(SCENARIO_PATTERN.findall(go_code_only(text)))


def validate_document(
    source: Path, functions: tuple[str, ...], root: Path | None = None
) -> list[str]:
    root = source.parent.resolve() if root is None else root
    document = source.with_suffix(".md")
    if not document.exists() and not document.is_symlink():
        return [f"{source}: missing sibling document {document.name}"]
    try:
        text = read_regular_text(document, root)
    except (OSError, UnicodeError, ValueError) as error:
        return [str(error)]

    prose = markdown_prose(text)
    lines = prose.splitlines()
    errors: list[str] = []
    expected_link = f"[{source.name}](./{source.name})"
    if expected_link not in prose:
        errors.append(f"{document}: missing source link {expected_link}")

    second_level_headings = [
        (index, line.rstrip())
        for index, line in enumerate(lines)
        if line.startswith("## ")
    ]
    expected_scenario_headings = tuple(f"## `{function}`" for function in functions)
    actual_scenario_headings = tuple(heading for _, heading in second_level_headings)
    if actual_scenario_headings != expected_scenario_headings:
        errors.append(
            f"{document}: scenario sections must be exactly "
            f"{', '.join(expected_scenario_headings)} in source order"
        )

    for section_index, (position, expected_heading) in enumerate(second_level_headings):
        if expected_heading not in expected_scenario_headings:
            continue
        section_end = (
            second_level_headings[section_index + 1][0]
            if section_index + 1 < len(second_level_headings)
            else len(lines)
        )
        section_lines = lines[position + 1 : section_end]
        heading_positions: list[int] = []
        for heading in REQUIRED_SCENARIO_HEADINGS:
            matches = [
                index
                for index, line in enumerate(section_lines)
                if line.rstrip() == heading
            ]
            if len(matches) != 1:
                errors.append(
                    f"{document}: {expected_heading} must contain exactly one {heading}"
                )
            else:
                heading_positions.append(matches[0])
        if heading_positions != sorted(heading_positions):
            errors.append(f"{document}: {expected_heading} headings are out of order")

    placeholder = PLACEHOLDER_PATTERN.search(text)
    if placeholder is not None:
        errors.append(f"{document}: unresolved placeholder {placeholder.group(0)!r}")
    return errors


def discover_sources(root: Path) -> list[tuple[Path, tuple[str, ...]]]:
    discovered: list[tuple[Path, tuple[str, ...]]] = []
    for source in sorted(root.rglob("*_test.go")):
        functions = scenario_functions(source, root)
        if functions:
            discovered.append((source, functions))
    return discovered


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Validate sibling Markdown for Go E2E scenario files."
    )
    parser.add_argument("root", type=Path, help="Scenario package directory")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    root = args.root.resolve()
    if not root.is_dir():
        print(f"error: scenario root is not a directory: {root}", file=sys.stderr)
        return 2

    try:
        sources = discover_sources(root)
    except (OSError, UnicodeError, ValueError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1
    if not sources:
        print(
            f"error: no scenario-bearing *_test.go files found under {root}",
            file=sys.stderr,
        )
        return 2

    errors = [
        error
        for source, functions in sources
        for error in validate_document(source, functions, root)
    ]
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        return 1

    print(f"Validated {len(sources)} scenario documents under {root}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
