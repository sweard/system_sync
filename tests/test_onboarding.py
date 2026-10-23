"""Exercise the shipped CLI with isolated HOME and synthetic package managers."""

import fcntl
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import unittest

from helper_fixture import prepare_helper
from check_scope import for_platforms, selected_platforms
from test_converge import run_terminal


FAKE = r'''
import json, os, re, sys
from pathlib import Path
program, args = sys.argv[1], sys.argv[2:]
path = Path(os.environ["ONBOARD_TEST_STATE"])
data = json.loads(path.read_text())
data["calls"].append([program, *args])
path.write_text(json.dumps(data))
def save(): path.write_text(json.dumps(data))
if program == "uname":
    print("Linux" if data["profile"] == "arch" else "Darwin")
elif program == "mise" and args == ["--version"]:
    print("2026.10.15")
elif program == "mise" and args[:1] == ["-C"] and args[-4:] == ["bootstrap", "packages", "status", "--json"]:
    print(json.dumps({"pacman": {"available": True, "packages": [
        {"package": name, "state": "installed" if name in data["installed"] else "missing"}
        for name in ("git", "missing-tool")]}}))
elif program == "brew" and args[:2] == ["list", "--formula"]:
    print("\n".join(data["installed"]))
elif program == "brew" and args == ["list", "--cask"]:
    pass
elif program == "brew" and args[:2] == ["bundle", "dump"]:
    target = next(item.split("=", 1)[1] for item in args if item.startswith("--file="))
    Path(target).write_text("".join(f'brew "{name}"\n' for name in data["roots"]))
elif program == "brew" and args[:2] == ["bundle", "install"]:
    assert os.environ.get("HOMEBREW_NO_INSTALL_CLEANUP") == "1"
    assert os.environ.get("HOMEBREW_NO_AUTOREMOVE") == "1"
    assert "--no-upgrade" in args
    assert "--verbose" in args
    if data.get("live_progress"):
        print("Fixture: onboard download", flush=True)
        assert input() == "download-seen"
        print("Fixture: onboard install", file=sys.stderr, flush=True)
        assert input() == "install-seen"
    target = next(item.split("=", 1)[1] for item in args if item.startswith("--file="))
    added = re.findall(r'^brew "([^"]+)"', Path(target).read_text(), re.M)
    assert added == ["missing-tool"], added
    data["installed"] += added
    data["roots"] += added
    data["mutations"].append([program, *args])
    save()
elif program == "pacman" and args == ["-Qq"]:
    print("\n".join(data["installed"]))
elif program == "pacman" and args == ["-Qqen"]:
    print("\n".join(data["roots"]))
elif program == "pacman" and args in (["-Qqem"], ["-Qdtq"]):
    if data.get("query_failure"):
        print("database-error-fixture", file=sys.stderr)
        sys.exit(1)
    sys.exit(1)  # valid no-matches query
elif program == "sudo":
    assert args[:4] == ["pacman", "-S", "--needed", "--noconfirm"], args
    assert args[4:] == ["--", "missing-tool"], args
    data["installed"].append("missing-tool")
    data["roots"].append("missing-tool")
    data["mutations"].append([program, *args])
    save()
else:
    print("unexpected mock command", program, args, file=sys.stderr)
    sys.exit(91)
'''


@for_platforms("macos")
class OnboardingCLITest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="system-sync-onboard-")
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name).resolve()
        self.root, self.home = self.base / "repo 中文", self.base / "home"
        self.root.mkdir()
        self.home.mkdir()
        prepare_helper(self.root)
        self.bin = self.base / "bin"
        self.bin.mkdir()
        self.state = self.base / "state.json"
        self.state.write_text(json.dumps({"profile": "macos", "installed": ["git", "local-tool", "orphan-lib"],
                                          "roots": ["git", "local-tool"], "calls": [], "mutations": []}))
        backend = self.base / "fake.py"
        backend.write_text(FAKE)
        for name in ("mise", "brew", "uname", "pacman", "sudo"):
            path = self.bin / name
            path.write_text(f'#!/bin/sh\nexec {shlex.quote(sys.executable)} -I -B '
                            f'{shlex.quote(str(backend))} {name} "$@"\n')
            path.chmod(0o755)
        self.marker = self.base / "unexpected-runtime"
        for name in ("go", "python3", "curl", "wget"):
            path = self.bin / name
            path.write_text(f'#!/bin/sh\necho called > {shlex.quote(str(self.marker))}\nexit 99\n')
            path.chmod(0o755)
        self.environment = dict(os.environ, HOME=str(self.home), ONBOARD_TEST_STATE=str(self.state),
                                PATH=f"{self.bin}:/usr/bin:/bin:/usr/sbin:/sbin")
        self.environment.pop("SYSTEM_SYNC_LOCK_FD", None)
        self.environment.pop("MISE_GLOBAL_CONFIG_FILE", None)
        self.write_manifest("macos")

    def write_manifest(self, profile):
        data = self.data()
        data["profile"] = profile
        self.state.write_text(json.dumps(data))
        if profile == "macos":
            path = self.root / "packages/macos/Brewfile"
            text = '# preserve options\nbrew "git", link: false\nbrew "missing-tool", link: false\n'
        else:
            path = self.root / "packages/linux/arch/mise.toml"
            text = '[bootstrap.packages]\n"pacman:git" = "latest"\n"pacman:missing-tool" = "latest"\n'
            detector = self.root / "scripts/detect-linux-profile.sh"
            detector.write_text('#!/bin/sh\nprintf "arch\\n"\n')
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        self.manifest = path

    def data(self):
        return json.loads(self.state.read_text())

    def run_helper(self, *args, success=True):
        result = subprocess.run([str(self.root / "helper/scripts/run.sh"), *args], cwd=self.base,
                                env=self.environment, input="", text=True, capture_output=True, timeout=30)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        self.assertFalse(self.marker.exists(), "runtime invoked compiler, Python CLI or downloader")
        return result

    def test_default_preview_and_json_never_save_or_mutate(self):
        before = self.manifest.read_bytes()
        capture = self.run_helper("package-capture")
        self.assertIn("仅为预览", capture.stdout)
        plan = json.loads(self.run_helper("machine-init", "--only", "packages", "--json").stdout)
        self.assertEqual(plan["packages"]["missing"][0]["name"], "missing-tool")
        self.assertIn("local-tool", plan["packages"]["candidate"]["diff"])
        self.assertEqual(self.manifest.read_bytes(), before)
        self.assertEqual(self.data()["mutations"], [])
        for name in ("capture", "history"):
            self.assertFalse((self.root / ".system-sync" / name).exists())

    def test_capture_apply_only_saves_private_candidates(self):
        before = self.manifest.read_bytes()
        self.run_helper("package-capture", "--apply")
        directories = list((self.root / ".system-sync/capture").iterdir())
        self.assertEqual(len(directories), 1)
        candidate = directories[0] / "Brewfile.candidate"
        self.assertIn('brew "git", link: false', candidate.read_text())
        self.assertIn('brew "local-tool"', candidate.read_text())
        self.assertEqual(candidate.stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.manifest.read_bytes(), before)
        self.assertEqual(self.data()["mutations"], [])

    @for_platforms("macos", "arch")
    def test_install_only_on_macos_and_arch_preserves_extras_and_is_repeatable(self):
        for profile in selected_platforms(("macos", "arch")):
            with self.subTest(profile=profile):
                self.write_manifest(profile)
                data = self.data()
                data.update(installed=["git", "local-tool", "orphan-lib"], roots=["git", "local-tool"], mutations=[])
                self.state.write_text(json.dumps(data))
                before = self.manifest.read_bytes()
                self.run_helper("machine-init", "--only", "packages", "--apply", "--yes")
                self.assertEqual(set(self.data()["installed"]), {"git", "local-tool", "orphan-lib", "missing-tool"})
                self.assertEqual(self.manifest.read_bytes(), before)
                self.assertEqual(len(self.data()["mutations"]), 1)
                self.run_helper("machine-init", "--only", "packages", "--apply", "--yes")
                self.assertEqual(len(self.data()["mutations"]), 1)

    def test_confirmation_and_invalid_flags_never_mutate(self):
        self.run_helper("machine-init", "--only", "packages", "--apply", success=False)
        for args in (("machine-init", "--apply", "--dry-run"),
                     ("machine-init", "--yes"), ("machine-init", "--only", "unknown"),
                     ("package-capture", "--apply", "--json")):
            self.run_helper(*args, success=False)
        self.assertEqual(self.data()["mutations"], [])
        self.assertFalse((self.root / ".system-sync/history").exists())

    def test_machine_init_streams_homebrew_output_and_reports_verification(self):
        data = self.data()
        data["live_progress"] = True
        self.state.write_text(json.dumps(data))
        result = run_terminal([str(self.root / "helper/scripts/run.sh"), "machine-init", "--only", "packages", "--apply", "--yes"],
                              self.environment, [("Fixture: onboard download", "download-seen"),
                                                 ("Fixture: onboard install", "install-seen")])
        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("开始接入阶段 1/1：packages", result.stdout)
        self.assertLess(result.stdout.index("Fixture: onboard install"), result.stdout.index("复核补装结果"))
        self.assertIn("缺失软件已补装", result.stdout)

    @for_platforms("arch")
    def test_query_failure_does_not_publish_partial_capture(self):
        self.write_manifest("arch")
        data = self.data()
        data["query_failure"] = True
        self.state.write_text(json.dumps(data))
        result = self.run_helper("package-capture", "--apply", success=False)
        self.assertNotIn("database-error-fixture", result.stdout + result.stderr)
        self.assertFalse((self.root / ".system-sync/capture").exists())

    def test_shared_lock_prevents_capture_and_initialization(self):
        lock = self.root / ".system-sync/operation.lock"
        with lock.open("w") as file:
            fcntl.flock(file, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.run_helper("package-capture", success=False)
            self.run_helper("machine-init", "--only", "packages", "--apply", "--yes", success=False)
        self.assertEqual(self.data()["mutations"], [])

    @unittest.skipUnless(shutil.which("mise"), "real mise CLI unavailable")
    @for_platforms("common")
    def test_real_mise_config_deployment_preserves_private_keys_and_backs_up(self):
        real_mise = shutil.which("mise")
        mise = self.bin / "mise"
        mise.write_text(f'#!/bin/sh\nexec {shlex.quote(real_mise)} "$@"\n')
        common = self.root / "dotfiles/common/mise/config.toml"
        common.parent.mkdir(parents=True)
        common.write_text("# Global configuration\n")
        shared = self.root / "shared.json"
        shared.write_text(json.dumps({"shared": "new"}))
        private = self.home / ".private-app.json"
        private.write_text(json.dumps({"shared": "old", "secret": "private-test-value"}))
        (self.root / "mise.toml").write_text(
            '[dotfiles]\n'
            '"~/.config/mise/config.toml" = { source = "dotfiles/common/mise/config.toml", mode = "symlink" }\n'
            '"~/.private-app.json/shared" = { source = "shared.json", merge = true }\n')
        self.environment.update(
            MISE_CONFIG_DIR=str(self.home / ".config/mise"),
            MISE_SYSTEM_CONFIG_DIR=str(self.base / "system"),
            MISE_DATA_DIR=str(self.base / "data"), MISE_STATE_DIR=str(self.base / "mise-state"),
            MISE_CACHE_DIR=str(self.base / "cache"), MISE_CEILING_PATHS=str(self.base),
            MISE_TRUSTED_CONFIG_PATHS=str(self.base), MISE_AUTO_INSTALL="false",
        )
        before = private.read_bytes()
        self.run_helper("machine-init", "--only", "config")
        self.assertEqual(private.read_bytes(), before)
        self.assertFalse((self.home / ".config/mise/config.toml").exists())
        self.run_helper("machine-init", "--only", "config", "--apply", "--yes")
        self.assertEqual(json.loads(private.read_text()), {"shared": "new", "secret": "private-test-value"})
        self.assertTrue((self.home / ".config/mise/config.toml").is_symlink())
        import tarfile
        archive = next((self.root / ".system-sync/history").glob("*/config.before.tar"))
        with tarfile.open(archive) as backup:
            self.assertEqual(backup.extractfile(".private-app.json").read(), before)
        after = private.read_bytes()
        self.run_helper("machine-init", "--only", "config", "--apply", "--yes")
        self.assertEqual(private.read_bytes(), after)


if __name__ == "__main__":
    unittest.main()
