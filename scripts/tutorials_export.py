#!/usr/bin/env python3
"""Export checked mtm tutorials and preserve their introductory metadata.

monogatari v0.3.0 exports chapters but omits app-defined Story.Fields.
This glue can go away once monogatari exports those fields itself.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile


def enrich(stories, output, sandbox):
    index_path = output / "index.json"
    index = json.loads(index_path.read_text())
    for entry in index["stories"]:
        source = json.loads((stories / (entry["id"] + ".json")).read_text())
        path = output / entry["file"]
        exported = json.loads(path.read_text())
        for field in ("where", "what"):
            value = source[field]
            if not isinstance(value, str) or not value.strip():
                raise ValueError(f"{entry['id']}: {field} must be nonempty text")
            exported[field] = entry[field] = value
        # Fixture directories are stable before rendering (Env.TempDir).
        # Keep implementation paths out of the portable reader's frames.
        data = json.dumps(exported, indent=2, ensure_ascii=False)
        data = data.replace(str(sandbox), "~/…")
        path.write_text(data + "\n")
    index_path.write_text(json.dumps(index, indent=2, ensure_ascii=False) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bin", type=Path, default=Path("bin/mtm-story"))
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    binary = args.bin.resolve()
    output = args.out.resolve()
    root = Path(os.environ.get("TUTORIAL_TMPDIR", "/tmp/mtm-tutorials"))
    root.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="export-", dir=root) as tmp:
        sandbox = Path(tmp)
        env = dict(os.environ)
        for key, name in (("HOME", "home"), ("XDG_CONFIG_HOME", "config"),
                          ("XDG_STATE_HOME", "state"), ("XDG_DATA_HOME", "data"),
                          ("XDG_CACHE_HOME", "cache"), ("TMPDIR", "tmp")):
            folder = sandbox / name
            folder.mkdir()
            env[key] = str(folder)
        for key in ("TUIOS_ENV", "TUIOS_SESSION", "TUIOS_PANE_ID", "TUIOS_SOCKET", "MTM_SERVER"):
            env.pop(key, None)
        subprocess.run([str(binary), "--data", str(sandbox / "mtm.db"), "--socket", str(sandbox / "no-daemon.sock"), "story", "export", "tutorials", "--out", str(output),
                        "--format", "json"], cwd=repo, env=env, check=True)
        enrich(repo / "cmd/mtm/stories/tutorials", output, sandbox)


if __name__ == "__main__":
    main()
