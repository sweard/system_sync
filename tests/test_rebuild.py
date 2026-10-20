"""Rebuild public mappings in an empty HOME without native package operations."""

import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")


@unittest.skipUnless(MISE and sys.platform in ("darwin", "linux"), "mise or supported OS unavailable")
class PublicRebuildTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="system-sync-rebuild-")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name).resolve()
        self.project, self.home = self.base / "project", self.base / "home"
        self.project.mkdir()
        self.home.mkdir()
        for name in ("mise.toml", "mise.macos.toml", "mise.linux.toml", ".miserc.toml"):
            shutil.copy2(ROOT / name, self.project / name)
        shutil.copytree(ROOT / "dotfiles", self.project / "dotfiles")
        self.env = {
            "HOME": str(self.home), "PATH": f"{Path(MISE).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
            "MISE_CONFIG_DIR": str(self.home / ".config/mise"),
            "MISE_SYSTEM_CONFIG_DIR": str(self.base / "system"),
            "MISE_DATA_DIR": str(self.base / "data"), "MISE_STATE_DIR": str(self.base / "state"),
            "MISE_CACHE_DIR": str(self.base / "cache"), "MISE_CEILING_PATHS": str(self.base),
            "MISE_TRUSTED_CONFIG_PATHS": str(self.base), "MISE_OFFLINE": "1",
            "MISE_AUTO_INSTALL": "false", "MISE_TASK_RUN_AUTO_INSTALL": "false",
        }
        self.code_settings = self.home / ("Library/Application Support/Code/User/settings.json"
                                           if sys.platform == "darwin" else ".config/Code/User/settings.json")

    def mise(self, *args):
        result = subprocess.run([MISE, *args], cwd=self.project, env=self.env,
                                capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout

    def test_empty_home_rebuild_and_second_apply_are_idempotent(self):
        self.mise("bootstrap", "dotfiles", "apply")
        self.assertTrue((self.home / ".zshrc").is_symlink())
        config = self.home / ".config/mise"
        self.assertTrue((config / "config.toml").is_symlink())
        lock = config / "mise.lock"
        self.assertTrue(lock.is_symlink())
        graph = tomllib.loads(lock.read_text())["tools"]["npm:yarn"][0]["aube"]["path"]
        self.assertTrue((lock.resolve().parent / graph / "aube-lock.yaml").is_file())
        if sys.platform == "darwin":
            self.assertTrue((config / "mise.macos.lock").is_symlink())
        self.assertFalse((config / "secrets.env").exists())
        self.assertFalse((self.home / ".zshrc.local").exists())
        self.assertTrue(self.code_settings.is_file())
        self.mise("bootstrap", "dotfiles", "apply")
        self.assertEqual(self.mise("bootstrap", "dotfiles", "diff").strip(), "")
        self.assertFalse((self.base / "data/installs").exists(), "config deployment installed tools")

    def test_rebuild_preserves_local_keys_without_importing_them_to_sources(self):
        codex = self.home / ".codex/config.toml"
        codex.parent.mkdir()
        codex.write_text('private_fixture = "local-canary"\n')
        self.code_settings.parent.mkdir(parents=True)
        self.code_settings.write_text('{"private.fixture": "local-canary"}\n')
        self.mise("bootstrap", "dotfiles", "apply")
        self.assertEqual(tomllib.loads(codex.read_text())["private_fixture"], "local-canary")
        self.assertEqual(json.loads(self.code_settings.read_text())["private.fixture"], "local-canary")
        for path in (self.project / "dotfiles").rglob("*"):
            if path.is_file():
                self.assertNotIn(b"local-canary", path.read_bytes())


class ToolLocksTest(unittest.TestCase):
    def test_locks_cover_declared_versions_and_keep_dependency_graphs_in_repository(self):
        for config_name, lock_name in (("common/mise/config.toml", "common/mise/mise.lock"),
                                       ("macos/mise/config.toml", "macos/mise/mise.macos.lock")):
            config = tomllib.loads((ROOT / "dotfiles" / config_name).read_text())
            lock_path = ROOT / "dotfiles" / lock_name
            lock = tomllib.loads(lock_path.read_text())
            self.assertEqual(set(config["tools"]), set(lock["tools"]))
            for name, spec in config["tools"].items():
                version = spec if isinstance(spec, str) else spec["version"]
                self.assertEqual([entry["version"] for entry in lock["tools"][name]], [version])
                for entry in lock["tools"][name]:
                    for value in entry.values():
                        if isinstance(value, dict) and "url" in value:
                            self.assertTrue(value["url"].startswith("https://"))
                            self.assertTrue(value.get("checksum"))
                    if "aube" in entry:
                        sidecar = (lock_path.parent / entry["aube"]["path"]).resolve()
                        self.assertTrue(sidecar.is_relative_to(ROOT))
                        self.assertTrue((sidecar / "aube-lock.yaml").is_file())


if __name__ == "__main__":
    unittest.main()
