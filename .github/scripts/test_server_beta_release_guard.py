from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("server_beta_release_guard.py")
SPEC = importlib.util.spec_from_file_location("server_beta_release_guard", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
guard = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(guard)


class VersionTests(unittest.TestCase):
    def test_accepts_plain_and_prefixed_semver(self) -> None:
        self.assertEqual(guard.normalize_version("1.2.3"), ("1.2.3", "server-v1.2.3"))
        self.assertEqual(guard.normalize_version(" v10.20.30 "), ("10.20.30", "server-v10.20.30"))

    def test_rejects_player_tag_prerelease_and_leading_zero(self) -> None:
        for value in ("server-v1.2.3", "1.2", "1.2.3-beta", "01.2.3", "v1.02.3", ""):
            with self.subTest(value=value), self.assertRaises(ValueError):
                guard.normalize_version(value)


class WorkflowTests(unittest.TestCase):
    def test_repository_workflow_obeys_server_only_contract(self) -> None:
        self.assertEqual(guard.verify_workflow(), [])

    def test_detects_player_asset_regression(self) -> None:
        source = guard.DEFAULT_WORKFLOW.read_text(encoding="utf-8")
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "workflow.yml"
            path.write_text(source + "\n# player/dist/forbidden.zip\n", encoding="utf-8")
            self.assertIn("Player asset path", guard.verify_workflow(path))

    def test_detects_unpinned_linter_and_missing_release_title_check(self) -> None:
        source = guard.DEFAULT_WORKFLOW.read_text(encoding="utf-8")
        source = source.replace("version: v2.4.0", "version: latest")
        source = source.replace("--json tagName,name,isPrerelease,isDraft", "--json tagName,isPrerelease,isDraft")
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "workflow.yml"
            path.write_text(source, encoding="utf-8")
            failures = guard.verify_workflow(path)
            self.assertIn("lint version is pinned", failures)
            self.assertIn("release identity and title are checked", failures)

    def test_server_tag_cannot_match_player_tag_trigger(self) -> None:
        source = guard.DEFAULT_PLAYER_WORKFLOW.read_text(encoding="utf-8")
        source = source.replace('- "v*.*.*"', '- "*v*.*.*"')
        with tempfile.TemporaryDirectory() as directory:
            player_path = Path(directory) / "player.yml"
            player_path.write_text(source, encoding="utf-8")
            failures = guard.verify_workflow(guard.DEFAULT_WORKFLOW, player_path)
            self.assertIn("Player tag trigger excludes Server namespace", failures)


if __name__ == "__main__":
    unittest.main()
