"""Exercise local-only history and rollback through real mise, with synthetic data."""

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
class ConfigHistoryTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="system-sync-history-")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name).resolve()
        self.home, self.project = self.base / "home", self.base / "project"
        self.home.mkdir()
        self.project.mkdir()
        self.target = self.home / ".codex/config.toml"
        self.target.parent.mkdir()
        self.target.write_text('shared = "original"\nprivate = "private-canary"\n')
        (self.project / "source.toml").write_text('shared = "desired"\n')
        (self.project / "mise.toml").write_text('[dotfiles]\n"~/.codex/config.toml/shared" = '
                                               '{ source="source.toml", merge=true }\n')
        config = self.home / ".config/mise"
        self.enrollment = config / "conf.d/system-sync-history.toml"
        self.state = self.base / "state"
        self.env = {
            "HOME": str(self.home), "PATH": f"{Path(MISE).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
            "MISE_CONFIG_DIR": str(config), "MISE_SYSTEM_CONFIG_DIR": str(self.base / "system"),
            "MISE_DATA_DIR": str(self.base / "data"), "MISE_STATE_DIR": str(self.state),
            "MISE_CACHE_DIR": str(self.base / "cache"), "MISE_CEILING_PATHS": str(self.base),
            "MISE_TRUSTED_CONFIG_PATHS": str(self.base), "MISE_OFFLINE": "1",
        }

    def run_command(self, args, success=True):
        result = subprocess.run(args, cwd=self.project, env=self.env, text=True,
                                capture_output=True, timeout=30)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result.stdout

    def helper(self, action, success=True):
        return self.run_command([str(ROOT / "helper/scripts/run.sh"), "config-history", action], success)

    def checkpoints(self, local=True):
        return json.loads(self.run_command([MISE, "dot", *(["--local"] if local else []), "history", "ls", "--json"]))

    def test_manual_save_and_rollback_keep_private_data_only_in_local_history(self):
        self.helper("init")
        entries = tomllib.loads(self.enrollment.read_text())["dotfiles"]
        self.assertTrue(all(v == {"mode": "track-local", "autosave": False} for v in entries.values()))
        self.assertEqual(self.enrollment.stat().st_mode & 0o777, 0o600)
        self.assertEqual((self.state / "history-local").stat().st_mode & 0o777, 0o700)
        self.helper("save")
        before = self.checkpoints()[0]["id"]
        self.target.write_text('shared = "changed"\nprivate = "second-canary"\n')
        # An unnamed save skips entries that explicitly require manual named saves.
        self.run_command([MISE, "dot", "save"])
        self.assertEqual(self.checkpoints()[0]["id"], before)
        self.run_command([MISE, "dot", "--local", "rollback", str(self.target), "--to", str(before), "--dry-run"])
        self.assertIn("second-canary", self.target.read_text())
        self.run_command([MISE, "dot", "--local", "rollback", str(self.target), "--to", str(before), "--yes"])
        self.assertIn("private-canary", self.target.read_text())
        self.assertEqual(self.checkpoints(local=False), [])
        # Applying the public merge afterwards still preserves the restored private value.
        self.run_command([MISE, "bootstrap", "dotfiles", "apply"])
        self.assertEqual(tomllib.loads(self.target.read_text()), {"shared": "desired", "private": "private-canary"})
        self.assertNotIn("canary", (self.project / "source.toml").read_text())

    def test_existing_custom_enrollment_and_shared_mode_are_not_overwritten(self):
        self.helper("init")
        self.enrollment.write_text('[dotfiles]\n"/unexpected" = {mode="track"}\n')
        original = self.enrollment.read_bytes()
        self.helper("init", success=False)
        self.helper("save", success=False)
        self.assertEqual(self.enrollment.read_bytes(), original)
        self.assertFalse((self.state / "history").exists())

    def test_registration_and_save_list_paths_and_report_missing_registered_files(self):
        local = self.home / ".zshrc.local"
        local.write_text("# shell-private-canary\n")
        output = self.helper("init")
        self.assertIn("  - " + str(self.target) + "\n", output)
        self.assertIn("  - " + str(local) + "\n", output)
        self.assertNotIn("canary", output)
        local.unlink()
        output = self.helper("save")
        self.assertIn("[!] 跳过 1 个不可用的已登记配置：\n  - " + str(local), output)
        self.assertIn("[+] 已手动保存 1 个配置到本机历史。\n  - " + str(self.target), output)
        self.assertEqual(len(self.checkpoints()), 1)
        self.assertNotIn("canary", output)
        self.target.unlink()
        output = self.helper("save", success=False)
        self.assertIn("[!] 跳过 2 个不可用的已登记配置", output)
        self.assertIn(str(self.target), output)
        self.assertIn(str(local), output)
        self.assertNotIn("已手动保存", output)
        self.assertEqual(len(self.checkpoints()), 1)


if __name__ == "__main__":
    unittest.main()
