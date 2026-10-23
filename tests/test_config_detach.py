"""Detach real mise deployments in disposable homes and preserve local files."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import tomllib
import unittest

from test_converge import run_terminal
from helper_fixture import operation_lock, prepare_helper, snapshot


ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")


@unittest.skipUnless(MISE and sys.platform in ("darwin", "linux"), "mise or supported OS unavailable")
class DeployedDetachTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="system-sync-detach-")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name).resolve()
        self.home, self.project = self.base / "home", self.base / "project with spaces 中文"
        self.home.mkdir()
        (self.project / "scripts").mkdir(parents=True)
        for name in ("mise.toml", "mise.macos.toml", "mise.linux.toml", ".miserc.toml"):
            shutil.copy2(ROOT / name, self.project / name)
        prepare_helper(self.project)
        shutil.copytree(ROOT / "dotfiles", self.project / "dotfiles",
                        ignore=shutil.ignore_patterns("private.local", "*.local", "__pycache__"))
        self.env = {
            "HOME": str(self.home), "PATH": f"{Path(MISE).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
            "MISE_CONFIG_DIR": str(self.home / ".config/mise"),
            "MISE_SYSTEM_CONFIG_DIR": str(self.base / "system"), "MISE_DATA_DIR": str(self.base / "data"),
            "MISE_STATE_DIR": str(self.home / ".local/state/mise"), "MISE_CACHE_DIR": str(self.base / "cache"),
            "MISE_CEILING_PATHS": str(self.base), "MISE_TRUSTED_CONFIG_PATHS": str(self.base),
            "MISE_OFFLINE": "1", "MISE_AUTO_INSTALL": "false", "MISE_TASK_RUN_AUTO_INSTALL": "false",
        }
        self.config = self.home / ".config/mise/config.toml"
        self.codex = self.home / ".codex/config.toml"
        self.code = self.home / ("Library/Application Support/Code/User/settings.json"
                                  if sys.platform == "darwin" else ".config/Code/User/settings.json")
        for path, value in ((self.codex, 'private_fixture = "private-canary"\n'),
                            (self.code, '{"private.fixture": "private-canary"}\n'),
                            (self.home / ".zshrc.local", '# local-canary\n')):
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(value)
            path.chmod(0o600)
        self.espanso = self.home / ("Library/Application Support/espanso"
                                     if sys.platform == "darwin" else ".config/espanso")
        self.private = self.project / "private-fixture.yml"
        self.private.write_text('private-canary: local-only\n')
        self.private.chmod(0o600)
        (self.project / "mise.local.toml").write_text(
            '[dotfiles]\n' + json.dumps(str(self.espanso / "match/private.yml")) +
            ' = {source="private-fixture.yml", mode="symlink"}\n')
        self.mise("bootstrap", "dotfiles", "apply")
        self.command([str(ROOT / "helper/scripts/run.sh"), "config-history", "init"])
        self.enrollment = self.home / ".config/mise/conf.d/system-sync-history.toml"
        self.backups = self.home / ".local/state/system-sync/config-detach"
        self.original_config = (self.project / "dotfiles/common/mise/config.toml").read_bytes()
        # Native status lazily creates history/watch.lock, which is query metadata.
        self.mise("bootstrap", "dotfiles", "status", "--json")

    def command(self, command, success=True, cwd=None):
        result = subprocess.run(command, cwd=cwd or self.project, env=self.env, input="",
                                capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        self.assertNotIn("private-canary", result.stdout + result.stderr)
        return result

    def mise(self, *args, cwd=None):
        return self.command([MISE, *args], cwd=cwd).stdout

    def detach(self, *args, success=True):
        return self.command([str(self.project / "helper/scripts/run.sh"), "config-detach", *args], success)

    def test_preview_and_noninteractive_refusal_leave_configuration_untouched(self):
        before = snapshot(self.home)
        preview = self.detach("--dry-run")
        self.assertIn("以上仅为预览", preview.stdout)
        self.assertIn("复制锁文件依赖目录", preview.stdout)
        self.assertIn("将 PATH 脚本引用改为本机位置", preview.stdout)
        self.assertEqual(snapshot(self.home), before)
        self.detach(success=False)
        self.assertEqual(snapshot(self.home), before)
        self.assertFalse(self.backups.exists())
        self.assertFalse((self.project / ".system-sync/operation.lock").exists())

    def test_interactive_detach_requires_yes_and_cancellation_leaves_no_backup(self):
        before = snapshot(self.home)
        command = [str(self.project / "helper/scripts/run.sh"), "config-detach"]
        for response in ("", "no", "y", "DETACH"):
            with self.subTest(response=response):
                result = run_terminal(command, self.env, [("输入 yes 确认", response)])
                self.assertEqual(result.returncode, 2, result.stdout)
                self.assertEqual(snapshot(self.home), before)
                self.assertFalse(self.backups.exists())
        result = run_terminal(command, self.env, [("输入 yes 确认", "yes")])
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("配置已保留在本机位置", result.stdout)
        self.assertFalse(self.config.is_symlink())
        self.assertFalse(self.enrollment.exists())
        self.assertTrue(self.backups.exists())
        self.assertNotIn("private-canary", result.stdout)

    def test_detach_preserves_current_values_private_files_and_works_without_repository(self):
        unmanaged = self.espanso / "match/unmanaged.yml"
        unmanaged.write_text("keep: user-owned\n")
        private_before = self.codex.read_bytes(), self.code.read_bytes(), (self.home / ".zshrc.local").read_bytes()
        history = self.home / ".local/state/mise/history-local/keep"
        history.write_text("existing-checkpoint\n")
        # Preserve local edits to a copied target rather than reapply its source.
        agents = self.home / ".codex/AGENTS.md"
        agents.write_text(agents.read_text() + "\nlocal collaboration rule\n")
        result = self.detach("--yes")
        self.assertIn("配置已保留在本机位置", result.stdout)
        self.assertFalse((self.home / ".zshrc").is_symlink())
        self.assertFalse(self.config.is_symlink())
        self.assertFalse(self.enrollment.exists())
        self.assertEqual(private_before, (self.codex.read_bytes(), self.code.read_bytes(),
                                          (self.home / ".zshrc.local").read_bytes()))
        self.assertEqual(unmanaged.read_text(), "keep: user-owned\n")
        self.assertIn("local collaboration rule", agents.read_text())
        private = self.espanso / "match/private.yml"
        self.assertFalse(private.is_symlink())
        self.assertEqual(private.stat().st_mode & 0o777, 0o600)
        self.assertEqual(private.read_bytes(), self.private.read_bytes())
        self.assertEqual((self.project / "dotfiles/common/mise/config.toml").read_bytes(), self.original_config)
        self.assertEqual(history.read_text(), "existing-checkpoint\n")
        backups = list(self.backups.iterdir())
        self.assertEqual(len(backups), 1)
        record = json.loads((backups[0] / "transaction.json").read_text())
        self.assertEqual(record["phase"], "complete")
        self.assertFalse(backups[0].is_relative_to(self.project))
        for path in backups[0].rglob("*"):
            if path.is_file() and not path.is_symlink():
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertIn("无需迁出", self.detach("--yes").stdout)
        self.assertEqual(list(self.backups.iterdir()), backups)
        self.project.rename(self.base / "archived project")
        data = tomllib.loads(self.config.read_text())
        path_script = Path(data["env"]["_"]["source"]["path"])
        self.assertEqual(path_script, self.home / ".config/zsh/path.sh")
        self.assertTrue(path_script.is_file())
        sidecars = json.loads(self.mise("lock", "--global", "--sidecars", "--json", cwd=self.home))
        self.assertNotIn(str(self.project), json.dumps(sidecars))
        self.assertTrue(any(item["sidecars"] for item in sidecars))
        for item in sidecars:
            self.assertTrue(all(graph["exists"] for graph in item["sidecars"]))
        self.command(["bash", "-c", '. "$HOME/.config/zsh/path.sh"'], cwd=self.home)
        self.assertFalse((self.base / "data/installs").exists(), "detaching installed tools")

    def test_conflicting_sidecar_aborts_before_replacing_any_target(self):
        collision = self.home / ".config/mise/.mise/locks/npm-yarn/1.22.22"
        collision.mkdir(parents=True)
        (collision / "unrelated").write_text("keep me")
        before = snapshot(self.home)
        self.detach("--yes", success=False)
        self.assertEqual(snapshot(self.home), before)
        self.assertFalse(self.backups.exists())

    def test_custom_history_enrollment_and_broken_links_are_not_overwritten(self):
        self.enrollment.write_text('[dotfiles]\n"/unexpected" = {mode="track"}\n')
        before = snapshot(self.home)
        self.detach("--yes", success=False)
        self.assertEqual(snapshot(self.home), before)
        self.enrollment.unlink()
        self.private.unlink()
        before = snapshot(self.home)
        self.detach("--yes", success=False)
        self.assertEqual(snapshot(self.home), before)
        self.assertFalse(self.backups.exists())

    def test_shared_operation_lock_prevents_detach_during_package_changes(self):
        before = snapshot(self.home)
        with operation_lock(self.project):
            self.detach("--yes", success=False)
        self.assertEqual(snapshot(self.home), before)
        self.assertFalse(self.backups.exists())

    def test_user_replaced_external_config_links_are_preserved(self):
        external = self.home / "own-config"
        external.mkdir()
        (external / "config.toml").write_bytes(self.original_config)
        self.config.unlink()
        self.config.symlink_to(external / "config.toml")
        self.detach("--yes")
        self.assertTrue(self.config.is_symlink())
        self.assertEqual(self.config.resolve(), external / "config.toml")
        self.assertEqual((external / "config.toml").read_bytes(), self.original_config)


if __name__ == "__main__":
    unittest.main()
