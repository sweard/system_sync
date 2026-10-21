"""Verify extension reconciliation with an isolated fake public VS Code CLI."""

import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts/vscode-extensions.py"
spec = importlib.util.spec_from_file_location("vscode_extensions", SCRIPT)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


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
        result = subprocess.run([str(ROOT / "scripts/system-sync-helper.sh"), "vscode", *args,
                                 "--manifest", str(self.manifest), "--code", str(self.code)],
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def test_platform_selection_and_conflict_fail_before_cli(self):
        self.manifest.write_text('[common]\n"example.shared"="1.2.3"\n'
                                 '[macos]\n"example.mac"="2.0.0"\n'
                                 '[linux]\n"example.linux"="3.0.0"\n')
        self.assertEqual(set(module.declarations(self.manifest, "linux")),
                         {"example.shared", "example.linux"})
        self.assertEqual(set(module.declarations(self.manifest, "macos")),
                         {"example.shared", "example.mac"})
        with self.manifest.open('a') as f: f.write('"--bad.flag"="1.0.0"\n')
        self.run_cli("sync", success=False)
        self.assertFalse(self.log.exists())

    def test_status_and_preview_do_not_install_or_change_manifest(self):
        before = self.manifest.read_bytes(), self.state.read_bytes()
        for args in (("status",), ("sync", "--dry-run")):
            result = self.run_cli(*args)
            self.assertIn("[!] example.shared", result.stdout)
            self.assertIn("[~] 保留 1", result.stdout)
        self.assertEqual((self.manifest.read_bytes(), self.state.read_bytes()), before)
        self.assertNotIn("--install-extension", self.log.read_text())

    def test_sync_verifies_versions_preserves_extra_and_isolates_both_directories(self):
        args = ("sync", "--user-data-dir", str(self.base / "user data"),
                "--extensions-dir", str(self.base / "extensions"))
        self.run_cli(*args)
        self.assertEqual(json.loads(self.state.read_text()),
                         {"example.local": "4.5.6", "example.shared": "1.2.3"})
        calls = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertTrue(all("--user-data-dir" in call and "--extensions-dir" in call for call in calls))
        self.assertIn("无需同步", self.run_cli(*args).stdout)
        self.assertEqual(sum("--install-extension" in json.loads(line)
                             for line in self.log.read_text().splitlines()), 1)

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

    @unittest.skipUnless(sys.platform in ("darwin", "linux"), "requires process groups")
    def test_timeout_stops_child_installer_from_writing_after_failure(self):
        marker = self.base / "orphan-wrote"
        child = "import pathlib,time; time.sleep(0.7); pathlib.Path(" + repr(str(marker)) + ").touch()"
        parent = "import subprocess,sys,time; subprocess.Popen([sys.executable,'-c'," + repr(child) + "]); time.sleep(10)"
        with self.assertRaisesRegex(ValueError, "超时"):
            module.invoke([sys.executable, "-c", parent], timeout=0.3)
        time.sleep(0.8)
        self.assertFalse(marker.exists(), "child installer survived the deadline")


if __name__ == "__main__":
    unittest.main()
