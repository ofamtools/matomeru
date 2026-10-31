import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("exporter", Path(__file__).with_name("states_export.py"))
exporter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(exporter)


class StateExportTests(unittest.TestCase):
    def reference(self):
        return {"version": 1, "app": "example", "states": [
            {"id": "start", "title": "Panes", "group": "Work", "where": "Panes list",
             "what": "Choose a pane.", "page": "panes", "modal": "", "modals": [],
             "mode": "normal", "noun": "pane", "keys": [
                 {"keys": "x", "label": "Close the pane", "action": "pane.close", "hidden": True},
                 {"keys": "x", "label": "Explain the blocked operation", "when": "blocked"},
                 {"pattern": "printable", "label": "Enter text"}]}]}

    def test_metadata_hidden_guards_patterns_and_determinism(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            reference = self.reference()
            index = exporter.export(reference, root)
            self.assertEqual((index["state_count"], index["key_count"], index["binding_count"]), (1, 2, 3))
            first = {p.name: p.read_bytes() for p in root.iterdir()}
            exporter.export(reference, root)
            self.assertEqual(first, {p.name: p.read_bytes() for p in root.iterdir()})
            self.assertEqual(json.loads((root / "start.json").read_text()), reference["states"][0])
            self.assertEqual(index["states"][0]["what"], "Choose a pane.")

    def test_committed_reference_has_only_portable_states_and_keys(self):
        reference = json.loads((Path(__file__).resolve().parents[1] / "docs/keys/states.json").read_text())
        with tempfile.TemporaryDirectory() as tmp:
            index = exporter.export(reference, Path(tmp))
            self.assertGreater(index["state_count"], 0)
            self.assertEqual(len(list(Path(tmp).glob("*.json"))), index["state_count"] + 1)

    def test_invalid_ids_replay_fields_and_stale_exports_are_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            reference = self.reference()
            reference["states"][0]["id"] = "../outside"
            with self.assertRaisesRegex(ValueError, "filenames"):
                exporter.export(reference, root)
            reference = self.reference()
            reference["states"][0]["chapters"] = []
            with self.assertRaisesRegex(ValueError, "fields"):
                exporter.export(reference, root)
            (root / "old-replay.json").write_text("{}")
            with self.assertRaisesRegex(ValueError, "stale"):
                exporter.export(self.reference(), root)
            self.assertEqual(list(root.iterdir()), [root / "old-replay.json"])


if __name__ == "__main__":
    unittest.main()
