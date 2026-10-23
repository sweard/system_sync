"""Verify extension reconciliation with an isolated fake public VS Code CLI."""

import json
import os
from pathlib import Path
import shutil
import select
import subprocess
import sys
import tempfile
import time
import tomllib
import unittest

from helper_fixture import prepare_helper

ROOT = Path(__file__).resolve().parents[1]


class VSCodeExtensionsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="system-sync-vscode-")
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.manifest = self.base / "extensions.toml"
        self.manifest.write_text('[common]\n"example.shared" = "1.2.3"\n')
        self.state, self.log = self.base / "state.json", self.base / "calls.jsonl"
        self.state.write_text('{"example.local":"4.5.6"}')
        self.code = self.base / "code"
        self.code.write_text(f'''#!{sys.executable}
import json, pathlib, sys
state = pathlib.Path({str(self.state)!r})
log = pathlib.Path({str(self.log)!r})
with log.open('a') as f: f.write(json.dumps(sys.argv[1:]) + '\\n')
data = json.loads(state.read_text())
if '--install-extension' in sys.argv:
    if (state.parent / 'fail-install').exists(): sys.exit(7)
    if (state.parent / 'wait-install').exists():
        import time
        print('private-install-token', flush=True)
        deadline = time.monotonic() + 5
        while not (state.parent / 'release-install').exists():
            if time.monotonic() > deadline: sys.exit(98)
            time.sleep(0.01)
    name, version = sys.argv[sys.argv.index('--install-extension')+1].split('@')
    if not (state.parent / 'wrong-version').exists(): data[name] = version
    state.write_text(json.dumps(data))
else:
    if (state.parent / 'bad-list').exists(): print('unknown output')
    else:
        for name, version in data.items(): print(name + '@' + version)
''')
        self.code.chmod(0o755)

    def run_cli(self, *args, success=True):
        result = subprocess.run([str(ROOT / "helper/scripts/run.sh"), "vscode", *args,
                                 "--manifest", str(self.manifest), "--code", str(self.code)],
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def run_capture(self, *args, success=True, input=None):
        project = self.base / "project"
        if not project.exists():
            prepare_helper(project)
        home = self.base / "home"
        home.mkdir(exist_ok=True)
        result = subprocess.run([str(project / "helper/scripts/run.sh"), "vscode", "status", "--capture", *args,
                                 "--manifest", str(self.manifest), "--code", str(self.code)],
                                env=dict(os.environ, HOME=str(home)), input=input,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def test_capture_defaults_to_preview_and_lists_selected_and_excluded_versions(self):
        self.state.write_text('{"zeta.local":"4.5.6","alpha.local":"1.0.0-beta.2","example.shared":"9.0.0"}')
        before = self.manifest.read_bytes(), self.state.read_bytes()
        for args in ((), ("--dry-run",), ("--only", "ALPHA.LOCAL,zeta.local", "--exclude", "zeta.local")):
            with self.subTest(args=args):
                result = self.run_capture(*args)
                self.assertIn("以上仅为预览", result.stdout)
                self.assertIn("alpha.local@1.0.0-beta.2", result.stdout)
                self.assertIn('+"alpha.local" = "1.0.0-beta.2"', result.stdout)
                if "--only" in args:
                    self.assertNotIn('+"example.shared"', result.stdout)
                else:
                    self.assertIn('example.shared: 1.2.3 -> 9.0.0 [common]', result.stdout)
                    self.assertIn('+"example.shared" = "9.0.0"', result.stdout)
                self.assertIn("到 [common]", result.stdout)
                self.assertNotIn("[macos]", result.stdout)
                self.assertNotIn("[linux]", result.stdout)
                if "--exclude" in args:
                    self.assertIn("[~] 跳过 2", result.stdout)
                    self.assertNotIn('+"zeta.local"', result.stdout)
                self.assertEqual((self.manifest.read_bytes(), self.state.read_bytes()), before)
                self.assertFalse((self.base / "project/.system-sync/capture").exists())
        self.assertNotIn("--install-extension", self.log.read_text())
        self.assertNotIn("--uninstall-extension", self.log.read_text())

    def test_capture_apply_appends_common_preserves_pins_and_is_idempotent(self):
        source = '# keep comment\n[common]\n"example.shared" = "1.2.3" # pinned\n"example.missing" = "2.0.0"\n'
        self.manifest.write_text(source)
        self.manifest.chmod(0o640)
        self.state.write_text('{"example.shared":"9.0.0","example.local":"4.5.6","excluded.local":"8.0.0"}')
        before_state = self.state.read_bytes()
        args = ("--apply", "--yes", "--only", "example.local,excluded.local",
                "--exclude", "excluded.local", "--exclude", "absent.local")
        self.assertIn("[+] 已更新扩展清单", self.run_capture(*args).stdout)
        data = tomllib.loads(self.manifest.read_text())["common"]
        self.assertEqual(data, {"example.shared": "1.2.3", "example.missing": "2.0.0", "example.local": "4.5.6"})
        self.assertIn(source, self.manifest.read_text())
        self.assertEqual(self.manifest.stat().st_mode & 0o777, 0o640)
        backups = list((self.base / "project/.system-sync/capture").glob("*-vscode-*"))
        self.assertEqual(len(backups), 1)
        self.assertEqual((backups[0] / "extensions.toml.before").read_text(), source)
        self.assertEqual((backups[0] / "extensions.toml.candidate").read_bytes(), self.manifest.read_bytes())
        self.assertEqual((backups[0] / "extensions.toml.before").stat().st_mode & 0o777, 0o600)
        self.assertEqual(backups[0].stat().st_mode & 0o777, 0o700)
        before = self.manifest.read_bytes(), self.manifest.stat().st_mtime_ns
        self.assertIn("无需修改清单", self.run_capture(*args).stdout)
        self.assertEqual((self.manifest.read_bytes(), self.manifest.stat().st_mtime_ns), before)
        self.assertEqual(list(backups[0].parent.iterdir()), backups)
        self.assertEqual(self.state.read_bytes(), before_state)
        self.assertTrue(all("--list-extensions" in json.loads(line) for line in self.log.read_text().splitlines()))

    def test_capture_explicit_platform_group_keeps_its_scope_on_later_default_capture(self):
        platform = "macos" if sys.platform == "darwin" else "linux"
        other = "linux" if platform == "macos" else "macos"
        before_state = self.state.read_bytes()
        result = self.run_capture("--group", platform, "--apply", "--yes")
        self.assertIn(f"到 [{platform}]", result.stdout)
        data = tomllib.loads(self.manifest.read_text())
        self.assertEqual(data["common"], {"example.shared": "1.2.3"})
        self.assertEqual(data[platform], {"example.local": "4.5.6"})
        self.assertNotIn("example.local", data.get(other, {}))
        before = self.manifest.read_bytes(), self.manifest.stat().st_mtime_ns
        self.assertIn("无需修改清单", self.run_capture("--apply", "--yes").stdout)
        self.assertEqual((self.manifest.read_bytes(), self.manifest.stat().st_mtime_ns), before)
        self.assertEqual(self.state.read_bytes(), before_state)
        self.assertTrue(all("--list-extensions" in json.loads(line) for line in self.log.read_text().splitlines()))

    def test_capture_exclusions_win_and_noop_does_not_create_backups(self):
        before = self.manifest.read_bytes()
        result = self.run_capture("--only", "example.local", "--exclude", "EXAMPLE.LOCAL",
                                  "--apply", "--yes")
        self.assertIn("[~] 跳过 1", result.stdout)
        self.assertIn("无需修改清单", result.stdout)
        self.assertNotIn("[+] 已更新扩展清单", result.stdout)
        self.assertEqual(self.manifest.read_bytes(), before)
        self.assertFalse((self.base / "project/.system-sync/capture").exists())

    def test_capture_rejects_conflicting_flags_invalid_filters_and_wrong_platform(self):
        other = "linux" if sys.platform == "darwin" else "macos"
        before = self.manifest.read_bytes()
        for args in (("--apply", "--dry-run"), ("--yes",), ("--group", other),
                     ("--only", "missing.extension"), ("--exclude", "example.*"),
                     ("--only", "example.local@4.5.6"), ("--group", "future")):
            with self.subTest(args=args):
                self.run_capture(*args, success=False)
                self.assertEqual(self.manifest.read_bytes(), before)
        for action in ("status", "sync"):
            result = self.run_cli(action, "--exclude", "example.local", success=False)
            self.assertEqual(result.returncode, 2)
        self.run_cli("sync", "--capture", success=False)
        self.assertFalse((self.base / "project/.system-sync/capture").exists())
        self.assertNotIn("--install-extension", self.log.read_text())

    def test_capture_refreshes_managed_versions_without_installing(self):
        source = '# keep\n[common]\n"anthropic.claude-code" = "2.1.304" # pinned\n"example.missing" = "1.0.0"\n'
        self.manifest.write_text(source)
        self.state.write_text('{"anthropic.claude-code":"2.1.309"}')
        installed = self.state.read_bytes()
        result = self.run_capture()
        self.assertIn("待更新 1 个已纳管扩展版本", result.stdout)
        self.assertIn("anthropic.claude-code: 2.1.304 -> 2.1.309 [common]", result.stdout)
        self.assertIn('+"anthropic.claude-code" = "2.1.309" # pinned', result.stdout)
        self.assertNotIn("待追加", result.stdout)
        self.assertEqual(self.manifest.read_text(), source)
        self.assertIn("无需修改清单", self.run_capture("--exclude", "anthropic.claude-code", "--apply", "--yes").stdout)
        backup_root = self.base / "project/.system-sync/capture"
        self.assertFalse(backup_root.exists())
        result = self.run_capture("--only", "anthropic.claude-code", "--apply", "--yes")
        self.assertIn("已更新扩展清单", result.stdout)
        self.assertEqual(self.manifest.read_text(), source.replace("2.1.304", "2.1.309"))
        backups = list(backup_root.iterdir())
        self.assertEqual(len(backups), 1)
        self.assertEqual((backups[0] / "extensions.toml.before").read_text(), source)
        self.assertIn("无需修改清单", self.run_capture("--apply", "--yes").stdout)
        self.assertEqual(list(backup_root.iterdir()), backups)
        self.assertEqual(self.state.read_bytes(), installed)
        self.assertTrue(all("--list-extensions" in json.loads(line) for line in self.log.read_text().splitlines()))

    def test_sync_warns_before_rolling_newer_extensions_back(self):
        self.manifest.write_text('[common]\n"anthropic.claude-code" = "2.1.304"\n')
        self.state.write_text('{"anthropic.claude-code":"2.1.309","example.local":"4.5.6"}')
        installed = self.state.read_bytes()
        for arguments in (("status",), ("sync", "--dry-run")):
            result = self.run_cli(*arguments)
            self.assertIn("2.1.309 -> 2.1.304（降级/回滚至清单固定版本）", result.stdout)
            self.assertIn("mise run vscode-status --capture", result.stdout)
            self.assertEqual(self.state.read_bytes(), installed)
        result = self.run_cli("sync")
        self.assertLess(result.stdout.index("降级/回滚"), result.stdout.index("安装 VS Code 扩展"))
        self.assertEqual(json.loads(self.state.read_text()), {"anthropic.claude-code": "2.1.304", "example.local": "4.5.6"})

    def test_capture_noninteractive_apply_requires_explicit_yes_flag(self):
        before = self.manifest.read_bytes()
        result = self.run_capture("--apply", input="yes\n", success=False)
        self.assertIn("交互终端", result.stderr)
        self.assertEqual(self.manifest.read_bytes(), before)
        self.assertFalse((self.base / "project/.system-sync/capture").exists())

    def test_capture_query_failure_and_invalid_manifest_do_not_write(self):
        before = self.manifest.read_bytes()
        (self.base / "bad-list").touch()
        result = self.run_capture("--apply", "--yes", success=False)
        self.assertIn("无法识别", result.stderr)
        self.assertEqual(self.manifest.read_bytes(), before)
        self.log.unlink()
        self.manifest.write_text('[common]\n"example.shared" = "latest"\n')
        self.run_capture("--apply", "--yes", success=False)
        self.assertFalse(self.log.exists())
        self.assertFalse((self.base / "project/.system-sync/capture").exists())

    @unittest.skipUnless(shutil.which("mise"), "mise unavailable")
    def test_capture_task_forwards_flags_with_real_mise_in_isolated_home(self):
        project, home = self.base / "project", self.base / "home"
        prepare_helper(project)
        home.mkdir()
        shutil.copy2(ROOT / "mise.toml", project / "mise.toml")
        manifest = project / "dotfiles/common/vscode/extensions.toml"
        manifest.parent.mkdir(parents=True)
        shutil.copy2(self.manifest, manifest)
        mise = shutil.which("mise")
        environment = {
            "HOME": str(home), "PATH": f"{Path(mise).parent}:/usr/bin:/bin:/usr/sbin:/sbin",
            "MISE_CONFIG_DIR": str(self.base / "config"), "MISE_SYSTEM_CONFIG_DIR": str(self.base / "system"),
            "MISE_DATA_DIR": str(self.base / "data"), "MISE_STATE_DIR": str(self.base / "state"),
            "MISE_CACHE_DIR": str(self.base / "cache"), "MISE_CEILING_PATHS": str(self.base),
            "MISE_TRUSTED_CONFIG_PATHS": str(self.base), "MISE_OFFLINE": "1", "NO_COLOR": "1",
        }
        for flags, writes in ((("--exclude", "example.local"), False),
                              (("--apply", "--yes", "--only", "example.local"), True)):
            result = subprocess.run([mise, "run", "vscode-status", "--capture", "--code", str(self.code), *flags],
                                    cwd=project, env=environment, capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual("example.local" in tomllib.loads(manifest.read_text())["common"], writes)
        self.assertNotIn("--install-extension", self.log.read_text())

        self.state.write_text('{"example.shared":"9.0.0","example.local":"4.5.6"}')
        result = subprocess.run([mise, "run", "vscode-status", "--capture", "--code", str(self.code),
                                 "--only", "example.shared", "--apply", "--yes"], cwd=project,
                                env=environment, capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("example.shared: 1.2.3 -> 9.0.0", result.stdout)
        self.assertEqual(tomllib.loads(manifest.read_text())["common"]["example.shared"], "9.0.0")
        self.assertNotIn("--install-extension", self.log.read_text())

    def test_platform_selection_and_conflict_fail_before_cli(self):
        self.manifest.write_text('[common]\n"example.shared"="1.2.3"\n'
                                 '[macos]\n"example.mac"="2.0.0"\n'
                                 '[linux]\n"example.linux"="3.0.0"\n')
        platform = "mac" if sys.platform == "darwin" else "linux"
        other = "linux" if platform == "mac" else "mac"
        output = self.run_cli("status").stdout
        self.assertIn("example.shared", output)
        self.assertIn("example." + platform, output)
        self.assertNotIn("example." + other, output)
        self.log.unlink()
        with self.manifest.open('a') as f: f.write('"--bad.flag"="1.0.0"\n')
        self.run_cli("sync", success=False)
        self.assertFalse(self.log.exists())

    def test_status_and_preview_do_not_install_or_change_manifest(self):
        before = self.manifest.read_bytes(), self.state.read_bytes()
        for args in (("status",), ("sync", "--dry-run")):
            result = self.run_cli(*args)
            self.assertIn("[!] example.shared", result.stdout)
            self.assertIn("[~] 保留 1", result.stdout)
            self.assertIn("  - example.local@4.5.6\n", result.stdout)
        self.assertEqual((self.manifest.read_bytes(), self.state.read_bytes()), before)
        self.assertNotIn("--install-extension", self.log.read_text())

    def test_sync_verifies_versions_preserves_extra_and_isolates_both_directories(self):
        args = ("sync", "--user-data-dir", str(self.base / "user data"),
                "--extensions-dir", str(self.base / "extensions"))
        self.assertIn("  - example.local@4.5.6\n", self.run_cli(*args).stdout)
        self.assertEqual(json.loads(self.state.read_text()),
                         {"example.local": "4.5.6", "example.shared": "1.2.3"})
        calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertTrue(all("--user-data-dir" in call and "--extensions-dir" in call for call in calls))
        self.assertIn("无需同步", self.run_cli(*args).stdout)
        self.assertEqual(sum("--install-extension" in json.loads(line)
                             for line in self.log.read_text().splitlines()), 1)
        self.assertNotIn("--uninstall-extension", self.log.read_text())

    def test_extra_details_are_sorted_when_managed_extensions_match(self):
        state = {"zeta.local": "4.5.6", "example.shared": "1.2.3", "alpha.local": "1.0.0-beta.2"}
        self.state.write_text(json.dumps(state))
        expected = ("[~] 保留 2 个清单外扩展：\n"
                    "  - alpha.local@1.0.0-beta.2\n"
                    "  - zeta.local@4.5.6\n")
        for args in (("status",), ("sync", "--dry-run"), ("sync",)):
            result = self.run_cli(*args)
            self.assertIn(expected, result.stdout)
            self.assertIn("无需同步", result.stdout)
            self.assertEqual(json.loads(self.state.read_text()), state)
        self.assertEqual(self.run_cli("status").stdout,
                         expected + "[+] 1 个公共扩展均符合清单，无需同步。\n")
        self.assertTrue(all("--list-extensions" in json.loads(line) for line in self.log.read_text().splitlines()))
        self.state.write_text('{"example.shared":"1.2.3"}')
        self.assertNotIn("清单外扩展", self.run_cli("status").stdout)

    def test_unknown_output_and_partial_isolation_fail_closed(self):
        (self.base / "bad-list").touch()
        self.run_cli("sync", success=False)
        self.assertNotIn("--install-extension", self.log.read_text())
        self.log.unlink()
        self.run_cli("sync", "--extensions-dir", str(self.base / "ext"), success=False)
        self.assertFalse(self.log.exists())

    def test_failed_and_ineffective_installations_are_not_reported_as_success(self):
        for marker in ("fail-install", "wrong-version"):
            with self.subTest(marker=marker):
                path = self.base / marker
                path.touch()
                result = self.run_cli("sync", success=False)
                self.assertNotIn("[+] 已同步", result.stdout)
                path.unlink()

    def test_sync_reports_installation_before_completion_without_private_output(self):
        (self.base / "wait-install").touch()
        process = subprocess.Popen([str(ROOT / "helper/scripts/run.sh"), "vscode", "sync",
                                    "--manifest", str(self.manifest), "--code", str(self.code)],
                                   stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        output, released = bytearray(), False
        deadline = time.monotonic() + 10
        try:
            while time.monotonic() < deadline:
                ready, _, _ = select.select([process.stdout], [], [], 0.1)
                if ready:
                    part = os.read(process.stdout.fileno(), 65536)
                    if not part:
                        break
                    output.extend(part)
                    if "安装 VS Code 扩展 1/1：example.shared@1.2.3" in output.decode(errors="replace"):
                        (self.base / "release-install").touch()
                        released = True
                elif process.poll() is not None:
                    break
            tail, _ = process.communicate(timeout=3)
            output.extend(tail)
            text = output.decode(errors="replace")
            self.assertEqual(process.returncode, 0, text)
            self.assertTrue(released, "installation had no status before completing")
            self.assertIn("复查 VS Code 扩展安装版本", text)
            self.assertIn("已同步 1 个公共扩展", text)
            self.assertNotIn("private-install-token", text)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stdout.close()

if __name__ == "__main__":
    unittest.main()
