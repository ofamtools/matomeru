#!/usr/bin/env python3
"""Idempotently opt cmd/mtm's top-level tests into parallel execution.

Adapted from the 2026-10-31 mtm-prune experiment. Tests which require
process-wide state must explain why with a // serial: reason doc comment.
The Go AST guard TestParallelTests checks the policy (including tagged files).
"""
from pathlib import Path
import re


def parallelize(source):
    pattern = re.compile(r"((?:(?://[^\n]*\n)|\n)*)(func Test(?!Main\b)\w+\(t \*testing\.T\) \{)([^\n]*)\n")

    def insert(match):
        comment, signature, tail = match.groups()
        if re.search(r"// serial: \S", comment):
            return match.group(0)
        following = tail + "\n" + source[match.end():]
        if re.match(r"\s*t\.Parallel\(\)", following):
            return match.group(0)
        body = "\t" + tail.strip() + "\n" if tail.strip() else ""
        return comment + signature + "\n\tt.Parallel()\n" + body

    return pattern.sub(insert, source)


def main():
    directory = Path(__file__).resolve().parents[1] / "cmd" / "mtm"
    changed = 0
    for path in sorted(directory.glob("*_test.go")):
        source = path.read_text()
        result = parallelize(source)
        if result != source:
            path.write_text(result)
            changed += 1
    print(f"parallelized tests in {changed} files")


if __name__ == "__main__":
    main()
