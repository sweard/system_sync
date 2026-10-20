"""Exercise config-dry-run through real mise, using only synthetic HOME files."""

from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


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
        self.environment = {
            "HOME": str(self.home), "PATH": f"{Path(MISE).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
            "MISE_CONFIG_DIR": str(self.base / "config"), "MISE_SYSTEM_CONFIG_DIR": str(self.base / "system"),
            "MISE_DATA_DIR": str(self.base / "data"), "MISE_STATE_DIR": str(self.base / "state"),
            "MISE_CACHE_DIR": str(self.base / "cache"), "MISE_CEILING_PATHS": str(self.base),
            "MISE_TRUSTED_CONFIG_PATHS": f"{self.base}:{ROOT}", "MISE_OFFLINE": "1", "NO_COLOR": "1",
        }
        task = self.run_mise("config", "get", "--file", str(ROOT / "mise.toml"), "tasks.config-dry-run").stdout
        self.header = "[settings.task]\nrun_auto_install = false\n[tasks.config-dry-run]\n" + task + "\n[dotfiles]\n"
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


if __name__ == "__main__":
    unittest.main()
