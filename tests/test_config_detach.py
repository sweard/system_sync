"""Detach real mise deployments in disposable homes and exercise failed transactions."""

import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import tomllib
import unittest
from unittest.mock import patch

from test_converge import run_terminal
from helper_fixture import prepare_helper


ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")
sys.path.insert(0, str(ROOT / "scripts"))
SPEC = importlib.util.spec_from_file_location("config_detach", ROOT / "scripts/config-detach.py")
DETACH = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = DETACH
SPEC.loader.exec_module(DETACH)


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
        self.command([str(ROOT / "scripts/system-sync-helper.sh"), "config-history", "init"])
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
        return self.command([str(self.project / "scripts/system-sync-helper.sh"), "config-detach", *args], success)

    def test_preview_and_noninteractive_refusal_leave_configuration_untouched(self):
        before = DETACH.snapshot(self.home)
        preview = self.detach("--dry-run")
        self.assertIn("以上仅为预览", preview.stdout)
        self.assertIn("复制锁文件依赖目录", preview.stdout)
        self.assertIn("将 PATH 脚本引用改为本机位置", preview.stdout)
        self.assertEqual(DETACH.snapshot(self.home), before)
        self.detach(success=False)
        self.assertEqual(DETACH.snapshot(self.home), before)
        self.assertFalse(self.backups.exists())
        self.assertFalse((self.project / ".system-sync/operation.lock").exists())

    def test_interactive_detach_requires_yes_and_cancellation_leaves_no_backup(self):
        before = DETACH.snapshot(self.home)
        command = [str(self.project / "scripts/system-sync-helper.sh"), "config-detach"]
        for response in ("", "no", "y", "DETACH"):
            with self.subTest(response=response):
                result = run_terminal(command, self.env, [("输入 yes 确认", response)])
                self.assertEqual(result.returncode, 2, result.stdout)
                self.assertEqual(DETACH.snapshot(self.home), before)
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
        before = DETACH.snapshot(self.home)
        self.detach("--yes", success=False)
        self.assertEqual(DETACH.snapshot(self.home), before)
        self.assertFalse(self.backups.exists())

    def test_custom_history_enrollment_and_broken_links_are_not_overwritten(self):
        self.enrollment.write_text('[dotfiles]\n"/unexpected" = {mode="track"}\n')
        before = DETACH.snapshot(self.home)
        self.detach("--yes", success=False)
        self.assertEqual(DETACH.snapshot(self.home), before)
        self.enrollment.unlink()
        self.private.unlink()
        before = DETACH.snapshot(self.home)
        self.detach("--yes", success=False)
        self.assertEqual(DETACH.snapshot(self.home), before)
        self.assertFalse(self.backups.exists())

    def test_shared_operation_lock_prevents_detach_during_package_changes(self):
        before = DETACH.snapshot(self.home)
        with DETACH.operation_lock(self.project):
            self.detach("--yes", success=False)
        self.assertEqual(DETACH.snapshot(self.home), before)
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


class DetachTransactionTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="system-sync-detach-transaction-")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name).resolve()
        self.root, self.home = self.base / "repo", self.base / "home"
        self.root.mkdir()
        self.home.mkdir()
        self.backups = self.home / "state/backups"

    def plan(self):
        plan = DETACH.DetachPlan(self.root, self.home)
        for name in ("a", "b"):
            (self.root / name).write_text(f"{name}-current-content")
            (self.home / name).symlink_to(self.root / name)
            plan.links(self.home / name)
        return plan

    def test_failed_replace_restores_all_previous_links(self):
        plan = self.plan()
        before = DETACH.snapshot(self.home)
        original_replace = os.replace
        failed = False

        def fail_once(source, target):
            nonlocal failed
            if Path(target) == self.home / "b" and not failed:
                failed = True
                raise OSError("injected replacement failure")
            return original_replace(source, target)

        with patch.object(DETACH.os, "replace", side_effect=fail_once):
            with self.assertRaises(OSError):
                plan.apply(self.backups)
        self.assertEqual({name: DETACH.snapshot(self.home / name) for name in ("a", "b")}, dict(before.value))
        transaction = next(self.backups.glob("*/transaction.json"))
        self.assertEqual(json.loads(transaction.read_text())["phase"], "rolled_back")
        self.assertFalse(list(self.home.glob(".system-sync-*")))

    def test_source_changes_and_linked_parent_abort_without_writes(self):
        plan = self.plan()
        (self.root / "a").write_text("new edit after preview")
        with self.assertRaises(DETACH.RuntimeFailure):
            plan.apply(self.backups)
        self.assertTrue((self.home / "a").is_symlink())
        self.assertFalse(self.backups.exists())
        external = self.base / "external"
        external.mkdir()
        (self.home / "alias").symlink_to(external)
        with self.assertRaises(DETACH.RuntimeFailure):
            plan.check_parents(self.home / "alias/config")
        with self.assertRaises(DETACH.RuntimeFailure):
            plan.target(self.home / "../outside")

    def test_rollback_preserves_a_concurrent_change_instead_of_clobbering_it(self):
        plan = self.plan()
        original_replace = os.replace
        failed = False

        def concurrent_edit(source, target):
            nonlocal failed
            if Path(target) == self.home / "b" and not failed:
                failed = True
                (self.home / "a").unlink()
                raise OSError("injected concurrent deletion")
            return original_replace(source, target)

        with patch.object(DETACH.os, "replace", side_effect=concurrent_edit):
            with self.assertRaises(OSError):
                plan.apply(self.backups)
        self.assertFalse((self.home / "a").exists())
        transaction = json.loads(next(self.backups.glob("*/transaction.json")).read_text())
        self.assertEqual(transaction["phase"], "recovery_required")
        self.assertEqual(transaction["conflicts"], [str(self.home / "a")])
        self.assertTrue((self.home / "b").is_symlink())

    def test_failed_directory_replacement_restores_the_original_link(self):
        source = self.root / "dir"
        source.mkdir()
        (source / "data").write_text("keep directory contents")
        target = self.home / "dir"
        target.symlink_to(source)
        plan = DETACH.DetachPlan(self.root, self.home)
        plan.links(target)
        original_replace = os.replace
        failed = False

        def fail_once(staged, destination):
            nonlocal failed
            if Path(destination) == target and not failed:
                failed = True
                raise OSError("injected directory rename failure")
            return original_replace(staged, destination)

        with patch.object(DETACH.os, "replace", side_effect=fail_once):
            with self.assertRaises(OSError):
                plan.apply(self.backups)
        self.assertTrue(target.is_symlink())
        self.assertEqual((target / "data").read_text(), "keep directory contents")
        transaction = json.loads(next(self.backups.glob("*/transaction.json")).read_text())
        self.assertEqual(transaction["phase"], "rolled_back")

    def test_directory_links_materialize_repo_links_and_keep_external_references(self):
        source = self.root / "config"
        source.mkdir()
        (source / "private").write_text("private-directory-data")
        (source / "private").chmod(0o600)
        (source / "internal").symlink_to(source / "private")
        external = self.home / "user-file"
        external.write_text("unmanaged")
        (source / "external").symlink_to(os.path.relpath(external, source))
        target = self.home / "config"
        target.symlink_to(source)
        plan = DETACH.DetachPlan(self.root, self.home)
        plan.links(target)
        plan.apply(self.backups)
        self.assertFalse(target.is_symlink())
        self.assertFalse((target / "internal").is_symlink())
        self.assertEqual((target / "private").stat().st_mode & 0o777, 0o600)
        self.assertEqual((target / "internal").read_text(), "private-directory-data")
        self.assertEqual((target / "external").resolve(), external)
        self.root.rename(self.base / "moved-repo")
        self.assertEqual((target / "internal").read_text(), "private-directory-data")
        self.assertEqual((target / "external").read_text(), "unmanaged")


if __name__ == "__main__":
    unittest.main()
