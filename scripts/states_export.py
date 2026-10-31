#!/usr/bin/env python3
"""Split the committed state/key reference into deterministic portable JSON files."""
import argparse
import json
from pathlib import Path
import re


STATE_FIELDS = {"id", "title", "group", "where", "what", "mode", "page", "modal", "modals", "noun", "keys"}


def export(reference, output):
    states = sorted(reference["states"], key=lambda s: s["id"])
    ids = [s["id"] for s in states]
    if len(set(ids)) != len(ids) or any(not re.fullmatch(r"[a-zA-Z0-9_-]+", sid) for sid in ids):
        raise ValueError("state ids must be unique portable filenames")
    index = {"version": reference["version"], "app": reference["app"], "states": []}
    documents = {}
    for state in states:
        if set(state) != STATE_FIELDS:
            raise ValueError(f"{state['id']}: unexpected or missing state fields")
        if not state["where"].strip() or not state["what"].strip():
            raise ValueError(f"{state['id']}: where and what are required")
        for binding in state["keys"]:
            if not binding.get("label") or not (binding.get("keys") or binding.get("pattern")):
                raise ValueError(f"{state['id']}: a key/pattern and action label are required")
        entry = {k: v for k, v in state.items() if k != "keys"}
        entry.update(file=state["id"] + ".json", binding_count=len(state["keys"]),
                     key_count=len({(r.get("keys", ""), r.get("pattern", "")) for r in state["keys"]}))
        index["states"].append(entry)
        documents[entry["file"]] = state
    index["state_count"] = len(states)
    index["binding_count"] = sum(s["binding_count"] for s in index["states"])
    index["key_count"] = sum(s["key_count"] for s in index["states"])
    documents["index.json"] = index
    # Never silently leave a replay export or stale removed states beside the reference.
    if output.exists():
        stale = {p.name for p in output.iterdir()} - set(documents)
        if stale:
            raise ValueError("export directory contains unrelated or stale files: " + ", ".join(sorted(stale)))
    output.mkdir(parents=True, exist_ok=True)
    for name, document in documents.items():
        (output / name).write_text(json.dumps(document, indent=2, ensure_ascii=False) + "\n")
    return index


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("reference", type=Path)
    parser.add_argument("out", type=Path)
    args = parser.parse_args()
    index = export(json.loads(args.reference.read_text()), args.out)
    print(f"Exported {index['state_count']} states, {index['key_count']} state/key pairs, "
          f"{index['binding_count']} binding rows to {args.out}")


if __name__ == "__main__":
    main()
