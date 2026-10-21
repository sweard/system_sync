"""Exercise config-dry-run through real mise, using only synthetic HOME files."""

from pathlib import Path
import json
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest

from helper_fixture import prepare_helper
from test_converge import run_terminal


ROOT = Path(__file__).resolve().parents[1]
MISE = shutil.which("mise")


@unittest.skipUnless(MISE and sys.platform in ("darwin", "linux"), "mise or supported OS unavailable")
class ConfigPreviewTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="system-sync-config-preview-")
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name).resolve()
        self.home, self.project = self.base / "home", self.base / "project with spaces"
        self.home.mkdir()
        (self.project / "scripts").mkdir(parents=True)
        shutil.copy2(ROOT / "scripts/config-preview.sh", self.project / "scripts/config-preview.sh")
        prepare_helper(self.project)
        self.environment = {
            "HOME": str(self.home), "PATH": f"{Path(MISE).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
            "MISE_CONFIG_DIR": str(self.base / "config"), "MISE_SYSTEM_CONFIG_DIR": str(self.base / "system"),
            "MISE_DATA_DIR": str(self.base / "data"), "MISE_STATE_DIR": str(self.base / "state"),
            "MISE_CACHE_DIR": str(self.base / "cache"), "MISE_CEILING_PATHS": str(self.base),
            "MISE_TRUSTED_CONFIG_PATHS": f"{self.base}:{ROOT}", "MISE_OFFLINE": "1", "NO_COLOR": "1",
        }
        task = self.run_mise("config", "get", "--file", str(ROOT / "mise.toml"), "tasks.config-dry-run").stdout
        sync = self.run_mise("config", "get", "--file", str(ROOT / "mise.toml"), "tasks.config-sync").stdout
        self.header = ("[settings.task]\nrun_auto_install = false\n[tasks.config-dry-run]\n" + task
                       + "\n[tasks.config-sync]\n" + sync + "\n[dotfiles]\n")
        self.source, self.target = self.project / "source", self.home / ".example"
        self.source.write_text("shared=true\n")
        self.target.write_text("shared=true\n")
        self.configure()

    def configure(self, mode="template", extra=""):
        (self.project / "mise.toml").write_text(
            self.header + '"~/.example" = {source = "source", mode = "' + mode + '"}\n' + extra)

    def run_mise(self, *arguments):
        return subprocess.run([MISE, *arguments], cwd=self.project, env=self.environment,
                              text=True, capture_output=True, timeout=30)

    def preview(self, *arguments, success=True):
        result = self.run_mise("run", "--quiet", "config-dry-run", *arguments)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn("无需同步", result.stdout + result.stderr)
        return result

    def snapshot(self):
        stat = self.target.stat()
        return self.target.read_bytes(), stat.st_mtime_ns, stat.st_mode, self.target.is_symlink()

    def test_unchanged_template_explicitly_needs_no_sync(self):
        before = self.snapshot()
        result = self.preview()
        self.assertIn("[+] 未发现需要应用的配置差异，无需同步。", result.stdout)
        self.assertNotIn("render ", result.stdout + result.stderr)
        self.assertEqual(self.snapshot(), before)

    def test_changed_template_is_rendered_without_deploying_or_running_hooks(self):
        self.source.write_text("shared={{ vars.shared }}\n")
        manifest = self.project / "mise.toml"
        manifest.write_text(manifest.read_text() + '\n[vars]\nshared = "desired"\n'
                            '[bootstrap.hooks]\npre-dotfiles = "touch pre-hook-ran"\npost-dotfiles = "touch post-hook-ran"\n')
        before = self.snapshot()
        result = self.preview()
        self.assertIn("-shared=true", result.stdout)
        self.assertIn("+shared=desired", result.stdout)
        self.assertNotIn("无需同步", result.stdout)
        self.assertNotIn("(if changed)", result.stdout)
        self.assertEqual(self.snapshot(), before)
        self.assertFalse((self.project / "pre-hook-ran").exists())
        self.assertFalse((self.project / "post-hook-ran").exists())

    def test_permissions_and_link_structure_are_not_mistaken_for_no_change(self):
        for mode, expected in (("template", "permissions differ"), ("symlink", "desired symlink")):
            with self.subTest(mode=mode):
                self.configure(mode)
                self.source.chmod(0o644)
                self.target.chmod(0o600 if mode == "template" else 0o644)
                before = self.snapshot()
                result = self.preview()
                self.assertIn(expected, result.stdout)
                self.assertNotIn("无需同步", result.stdout)
                self.assertEqual(self.snapshot(), before)

    def test_missing_target_and_source_are_reported(self):
        self.configure("copy")
        self.target.unlink()
        result = self.preview()
        self.assertIn("+shared=true", result.stdout)
        self.assertFalse(self.target.exists())
        self.source.unlink()
        result = self.preview()
        self.assertIn("source missing", result.stdout)
        self.assertNotIn("无需同步", result.stdout)

    def test_merge_preserves_private_keys_and_ignores_private_only_changes(self):
        self.source, self.target = self.project / "source.json", self.home / ".example.json"
        self.source.write_text('{"shared": 2}\n')
        self.target.write_text('{"shared": 1, "private": "private-canary"}\n')
        (self.project / "mise.toml").write_text(
            self.header + '"~/.example.json/shared" = {source = "source.json", merge = true}\n')
        before = self.snapshot()
        result = self.preview()
        self.assertIn('"shared": 2', result.stdout)
        self.assertNotIn("无需同步", result.stdout)
        self.assertEqual(self.snapshot(), before)
        self.target.write_text('{"shared": 2, "private": "new-private-canary"}\n')
        before = self.snapshot()
        self.assertIn("无需同步", self.preview().stdout)
        self.assertEqual(self.snapshot(), before)

    def test_native_failure_is_not_reported_as_no_change(self):
        self.source.write_text("{{ invalid template\n")
        before = self.snapshot()
        result = self.preview(success=False)
        self.assertIn("配置差异检查失败", result.stderr)
        self.assertEqual(self.snapshot(), before)

    def test_target_filter_and_help_are_forwarded(self):
        self.configure(extra='"~/.other" = {source = "other", mode = "copy"}\n')
        (self.project / "other").write_text("other changed\n")
        self.assertIn("无需同步", self.preview(str(self.target)).stdout)
        self.assertIn("~/.other", self.preview().stdout)
        self.assertFalse((self.home / ".other").exists())
        result = subprocess.run([str(self.project / "scripts/config-preview.sh"), "--help"],
                                cwd=self.project, env=self.environment, text=True, capture_output=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Usage:", result.stdout)
        self.assertNotIn("检测到以下受管配置差异", result.stdout)

    def test_sync_preserves_private_keys_and_backs_up_before_applying(self):
        self.source, self.target = self.project / "source.json", self.home / ".example.json"
        self.source.write_text('{"shared": 2}\n')
        before = '{"shared": 1, "private": "private-canary"}\n'
        self.target.write_text(before)
        (self.project / "mise.toml").write_text(
            self.header + '"~/.example.json/shared" = {source = "source.json", merge = true}\n')
        result = self.run_mise("run", "config-sync", "--yes")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(self.target.read_text()), {"shared": 2, "private": "private-canary"})
        directories = list((self.project / ".system-sync/history").glob("*-config-sync-*"))
        self.assertEqual(len(directories), 1)
        with tarfile.open(directories[0] / "config.before.tar") as archive:
            self.assertEqual(archive.extractfile(".example.json").read().decode(), before)
        self.assertEqual(json.loads((directories[0] / "transaction.json").read_text())["phase"], "complete")
        result = self.run_mise("run", "config-sync", "--yes")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("无需同步", result.stdout)
        self.assertEqual(len(list((self.project / ".system-sync/history").iterdir())), 1)

    def test_sync_preview_cancellation_and_shared_lock_do_not_modify_files(self):
        self.source.write_text("changed=true\n")
        before = self.snapshot()
        result = self.run_mise("run", "config-sync", "--dry-run")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.snapshot(), before)
        self.assertFalse((self.project / ".system-sync/history").exists())
        for answer in ("y", "no"):
            result = run_terminal([str(self.project / "helper/scripts/run.sh"), "config", "sync"],
                                  self.environment, [("输入 yes 确认", answer)])
            self.assertEqual(result.returncode, 2, result.stdout)
            self.assertEqual(self.snapshot(), before)
            self.assertFalse((self.project / ".system-sync/history").exists())
        from reference.sync_runtime import operation_lock
        with operation_lock(self.project):
            result = self.run_mise("run", "config-sync", "--yes")
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(self.snapshot(), before)

    def test_sync_only_applies_selected_owned_targets(self):
        self.configure("copy", extra='"~/.other" = {source = "other", mode = "copy"}\n')
        self.source.write_text("changed=true\n")
        (self.project / "other").write_text("other=true\n")
        result = self.run_mise("run", "config-sync", str(self.target), "--yes")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.target.read_text(), "changed=true\n")
        self.assertFalse((self.home / ".other").exists())

    def test_force_takeover_retains_the_original_file_in_backup(self):
        self.configure("symlink")
        before = self.target.read_bytes()
        result = self.run_mise("run", "config-sync", "--force", "--yes")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(self.target.is_symlink())
        self.assertEqual(self.target.resolve(), self.source)
        directory, = (self.project / ".system-sync/history").glob("*-config-sync-*")
        with tarfile.open(directory / "config.before.tar") as archive:
            self.assertEqual(archive.extractfile(".example").read(), before)

    def test_symlink_each_keeps_unmanaged_files_and_backs_up_the_directory(self):
        self.source.unlink()
        self.source.mkdir()
        (self.source / "shared").write_text("managed\n")
        self.target.unlink()
        self.target.mkdir()
        (self.target / "private").write_text("local-only\n")
        self.configure("symlink-each")
        result = self.run_mise("run", "config-sync", str(self.target), "--yes")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.target / "shared").is_symlink())
        self.assertEqual((self.target / "private").read_text(), "local-only\n")
        directory, = (self.project / ".system-sync/history").glob("*-config-sync-*")
        with tarfile.open(directory / "config.before.tar") as archive:
            self.assertEqual(archive.extractfile(".example/private").read().decode(), "local-only\n")


if __name__ == "__main__":
    unittest.main()
