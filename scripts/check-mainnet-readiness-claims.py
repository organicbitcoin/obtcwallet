#!/usr/bin/env python3

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


RISK_PATTERNS = [
    re.compile(r"\bproduction[- ]ready\b", re.IGNORECASE),
    re.compile(r"\bproduction wallet\b", re.IGNORECASE),
    re.compile(r"\bvaluable mainnet funds\b", re.IGNORECASE),
    re.compile(r"\bexchange[/ -]?(listing|readiness|support)\b", re.IGNORECASE),
    re.compile(r"\bcustody readiness\b", re.IGNORECASE),
    re.compile(r"\bbroad (end-user|consumer) wallet\b", re.IGNORECASE),
    re.compile(r"\bdefault[- ]on auto[- ]renew\b", re.IGNORECASE),
]

NEGATION_RE = re.compile(
    r"\b(not|no|never|without|does not|do not|must not|should not|isn't|is not|"
    r"out of scope|doesn't|cannot|can't|pending|limited|evidence-gated|"
    r"not claimed|not a|until|before)\b",
    re.IGNORECASE,
)


def candidate_files(paths: list[Path]) -> list[Path]:
    files: list[Path] = []
    for path in paths:
        if not path.exists():
            continue
        if path.is_file():
            files.append(path)
            continue
        for child in path.rglob("*"):
            if child.is_file() and child.suffix.lower() in {".md", ".txt", ".conf"}:
                files.append(child)
    return sorted(set(files))


def is_negated(line: str, start: int, context: list[str], negated_block: bool) -> bool:
    prefix = line[:start]
    suffix = line[start:]
    context_text = " ".join(context[-3:])
    in_list_item = line.lstrip().startswith(("*", "-", "•", ";"))
    return bool(
        negated_block
        or (in_list_item and NEGATION_RE.search(context_text))
        or NEGATION_RE.search(prefix)
        or NEGATION_RE.search(suffix[:100])
        or NEGATION_RE.search(context_text)
    )


def check_file(path: Path, root: Path) -> list[str]:
    errors: list[str] = []
    context: list[str] = []
    negated_block = False
    lines = path.read_text(encoding="utf-8", errors="replace").splitlines()
    for lineno, line in enumerate(lines, start=1):
        stripped = line.strip()
        is_list_item = stripped.startswith(("*", "-", "•", ";"))
        if NEGATION_RE.search(line) or "allowed claims" in line.lower():
            negated_block = True
        elif stripped.startswith("#"):
            negated_block = False
        elif not stripped:
            pass
        elif not is_list_item:
            negated_block = False

        for pattern in RISK_PATTERNS:
            match = pattern.search(line)
            if match and not is_negated(line, match.start(), context, negated_block):
                rel = path.relative_to(root) if path.is_relative_to(root) else path
                errors.append(f"{rel}:{lineno}: risky mainnet readiness claim: {match.group(0)}")

        context.append(line)
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description="Check OBTC wallet docs for unsafe mainnet readiness claims.")
    parser.add_argument(
        "paths",
        nargs="*",
        default=["docs/mainnet-readiness.md", "docs/releases", "README.md", "sample-btcwallet.conf"],
        help="files or directories to scan",
    )
    parser.add_argument("--root", default=".", help="root used for relative output")
    args = parser.parse_args()

    root = Path(args.root).resolve()
    files = candidate_files([Path(path).resolve() for path in args.paths])
    errors: list[str] = []
    for path in files:
        errors.extend(check_file(path, root))

    if errors:
        for error in errors:
            print(f"[ERROR] {error}", file=sys.stderr)
        return 1

    print(f"[OK] checked {len(files)} wallet readiness files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
