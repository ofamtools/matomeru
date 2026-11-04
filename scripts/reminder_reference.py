#!/usr/bin/env python3
"""Export the reminder catalog, effective keys and settings using temp app data."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

root = Path(__file__).resolve().parent.parent
with tempfile.TemporaryDirectory(prefix="mtm-reference-") as scratch:
    scratch = Path(scratch)
    binary = scratch / "mtm"
    build_env = dict(os.environ, GOWORK="off", GOFLAGS="-trimpath")
    subprocess.run(["go", "build", "-o", str(binary), "./cmd/mtm"], cwd=root, env=build_env, check=True)
    env = {key: value for key, value in os.environ.items() if not key.startswith(("MTM_", "TUIOS_"))}
    for key, directory in {"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_STATE_HOME": "state", "XDG_DATA_HOME": "data"}.items():
        path = scratch / directory
        path.mkdir()
        env[key] = str(path)
    env.update(MTM_ACTOR="owner", MTM_O_PROJ="none")
    command = [str(binary), "--data", str(scratch / "mtm.db"), "--socket", str(scratch / "no-daemon.sock")]

    def run(*args):
        result = subprocess.run(command + list(args), cwd=root, env=env, capture_output=True, text=True, check=True)
        return json.loads(result.stdout)

    catalog = run("catalog")
    document = {
        "generated-by": "make reference (scripts/reminder_reference.py)",
        "noun": next(noun for noun in catalog["nouns"] if noun["name"] == "reminder"),
        "bindings": [row for row in catalog["bindings"] if row.get("noun") == "reminder" or row.get("instance") == "schedules/reminders"],
        "settings": [value for value in run("settings", "list") if value["name"].startswith("reminders.")],
    }
    output = root / "docs" / "reference" / "reminders.json"
    output.parent.mkdir(exist_ok=True)
    output.write_text(json.dumps(document, ensure_ascii=False, indent=2) + "\n")
    print(output.relative_to(root))
