import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("exporter", Path(__file__).with_name("tutorials_export.py"))
exporter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(exporter)


class ExportMetadataTests(unittest.TestCase):
    def test_metadata_and_fixture_paths_survive_portable_export(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            stories, output = root / "stories", root / "export"
            stories.mkdir()
            (output / "tutorials").mkdir(parents=True)
            (stories / "panes.json").write_text(json.dumps({"where": "Panes list", "what": "Choose a pane"}))
            (output / "index.json").write_text(json.dumps({"version": 1, "stories": [
                {"id": "panes", "file": "tutorials/panes.json"}]}))
            exported = output / "tutorials/panes.json"
            exported.write_text(json.dumps({"chapters": [{"html": str(root / "fixture/mtm.db")}]}))
            exporter.enrich(stories, output, root)
            data = json.loads(exported.read_text())
            self.assertEqual(data["where"], "Panes list")
            self.assertEqual(data["what"], "Choose a pane")
            self.assertEqual(data["chapters"][0]["html"], "~/…/fixture/mtm.db")
            index = json.loads((output / "index.json").read_text())
            self.assertEqual(index["stories"][0]["what"], "Choose a pane")
            first = exported.read_bytes()
            exporter.enrich(stories, output, root)
            self.assertEqual(first, exported.read_bytes())


if __name__ == "__main__":
    unittest.main()
