"""All package-manager commands are fakes; no host packages are changed."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
SPEC = importlib.util.spec_from_file_location("package_command", ROOT / "scripts/package-command.py")
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class FakeRunner:
    def __init__(self, profile="macos", installed=None):
        self.profile = profile
        self.names = set(installed or {"git", "mise", "base", "base-files", "filesystem"})
        self.calls = []
        self.mutations = []
        self.fail = False
        self.no_change = False
        self.on_mutation = lambda: None
        self.extra_removal = None
        self.canonical = {}
        self.taps = {"fixture/tools", "abue-ammar/tinycast"}

    def run(self, command, capture=True):
        self.calls.append(command)
        if not capture:
            self.mutations.append(command)
            if self.fail:
                raise MODULE.Error("fake native failure")
            self.on_mutation()
            if not self.no_change:
                name = command[-1].rsplit("/", 1)[-1] if "--cask" in command else command[-1]
                if any(arg in command for arg in ("uninstall", "remove", "-R")):
                    self.names.discard(name)
                else:
                    self.names.add(name)
            return ""
        if command[:2] == ["brew", "tap"]:
            return "\n".join(sorted(self.taps)) + "\n"
        if command[:2] == ["brew", "info"]:
            name = self.canonical.get(command[-1], command[-1])
            item = {"full_name": name, "full_token": name, "aliases": [], "oldnames": [], "old_tokens": []}
            return json.dumps({"casks" if "--cask" in command else "formulae": [item]})
        if command[:2] == ["brew", "list"] or command == ["pacman", "-Qq"] or command[:2] == ["rpm", "-qa"]:
            return "\n".join(sorted(self.names)) + "\n"
        if command[0] == "dpkg-query":
            return "".join(f"{name}:amd64\tinstalled\n" for name in sorted(self.names))
        if command[:2] == ["apt-cache", "show"]:
            return f"Package: {command[-1]}\nArchitecture: amd64\n"
        if command[:2] == ["apt-get", "--simulate"]:
            return f"Remv {command[-1]} [1.0]\n" + (f"Remv {self.extra_removal} [1.0]\n" if self.extra_removal else "")
        if command[:3] == ["mise", "config", "get"]:
            return ""  # CLI parsing is covered separately by a real, read-only smoke test.
        if command[:2] == ["dnf", "repoquery"]:
            return command[-1] + "\n"
        if command[:2] == ["zypper", "--xmlout"]:
            return f'<stream><search-result><solvable-list><solvable name="{command[-1]}"/></solvable-list></search-result></stream>'
        if "-Si" in command or command[:3] == ["rpm", "-e", "--test"]:
            return ""
        raise AssertionError(f"Unexpected native command: {command}")


class PackageCommandTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="system-sync-command-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name) / "system sync"
        shutil.copytree(ROOT / "packages", self.root / "packages")
        self.brewfile = self.root / "packages/macos/Brewfile"
        # Stable fixtures: normal edits to the user's baseline must not break tests.
        self.brewfile.write_text(
            '# Test baseline\ntap "abue-ammar/tinycast"\n'
            'brew "git"\nbrew "mise"\nbrew "ruby", link: false\n'
            'cask "firefox"\ncask "google-chrome"\n'
            'cask "abue-ammar/tinycast/tinycast", trusted: true\n'
        )
        for name, text in {
            "protected-packages.txt": "formula:git\nformula:mise\n",
            "trusted-formulae.txt": "# Test formula trust\n",
            "trusted-casks.txt": "# Test cask trust\nabue-ammar/tinycast/tinycast\n",
        }.items():
            (self.brewfile.parent / name).write_text(text)
        for profile, manager in MODULE.MANAGERS.items():
            directory = self.root / f"packages/linux/{profile}"
            packages = ["base", "base-files", "filesystem", "git", "ripgrep", "zsh"]
            (directory / "mise.toml").write_text(
                '# Debian / Linux test roots\n[bootstrap.packages]\n' +
                "".join(f'"{manager}:{package}" = "latest"\n' for package in packages)
            )
            (directory / "protected-packages.txt").write_text("git\nzsh\n")
        self.runner = FakeRunner()

    def make(self, *argv, profile="macos"):
        return MODULE.PackageCommand(self.root, profile, MODULE.parse_args(argv), self.runner)

    def execute(self, operation, accept=True):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            operation.execute(confirm=lambda _: f"{operation.args.action.upper()} {operation.name}" if accept else "no")
        return output.getvalue()

    def test_formula_install_records_only_root(self):
        self.runner.names.add("automatically-installed-dependency")
        output = self.execute(self.make("install", "gitui"))
        source = self.brewfile.read_text()
        self.assertIn('brew "gitui"\n', source)
        self.assertNotIn("automatically-installed-dependency", source)
        self.assertEqual(self.runner.mutations, [["brew", "install", "--formula", "gitui"]])
        self.assertIn("完成", output)
        audit = list((self.root / ".system-sync/history").glob("*/transaction.json"))
        self.assertEqual(json.loads(audit[0].read_text())["phase"], "complete")

    def test_cask_install_does_not_add_formula(self):
        self.execute(self.make("install", "--cask", "zed"))
        source = self.brewfile.read_text()
        self.assertIn('cask "zed"\n', source)
        self.assertNotIn('brew "zed"', source)

    def test_dry_run_never_mutates(self):
        before = self.brewfile.read_bytes()
        output = self.execute(self.make("install", "--dry-run", "gitui"))
        self.assertEqual(self.runner.mutations, [])
        self.assertEqual(self.brewfile.read_bytes(), before)
        self.assertFalse((self.root / ".system-sync").exists())
        self.assertIn('brew "gitui"', output)

    def test_cancel_leaves_everything_untouched(self):
        before = self.brewfile.read_bytes()
        self.execute(self.make("uninstall", "firefox", "--cask"), accept=False)
        self.assertEqual(self.runner.mutations, [])
        self.assertEqual(self.brewfile.read_bytes(), before)

    def test_failed_install_keeps_manifest(self):
        self.runner.fail = True
        before = self.brewfile.read_bytes()
        with self.assertRaisesRegex(MODULE.Error, "fake native failure"):
            self.execute(self.make("install", "gitui"))
        self.assertEqual(self.brewfile.read_bytes(), before)

    def test_failed_uninstall_keeps_manifest(self):
        self.runner.names.add("firefox")
        self.runner.fail = True
        before = self.brewfile.read_bytes()
        with self.assertRaisesRegex(MODULE.Error, "fake native failure"):
            self.execute(self.make("uninstall", "--cask", "firefox"))
        self.assertEqual(self.brewfile.read_bytes(), before)

    def test_successful_exit_with_wrong_state_keeps_manifest(self):
        self.runner.no_change = True
        before = self.brewfile.read_bytes()
        with self.assertRaisesRegex(MODULE.Error, "安装状态未达到目标"):
            self.execute(self.make("install", "gitui"))
        self.assertEqual(self.brewfile.read_bytes(), before)

    def test_uninstall_only_removes_requested_cask(self):
        self.runner.names.update(("firefox", "google-chrome"))
        self.execute(self.make("uninstall", "--cask", "firefox"))
        source = self.brewfile.read_text()
        self.assertNotIn('cask "firefox"', source)
        self.assertIn('cask "google-chrome"', source)
        self.assertEqual(self.runner.mutations, [["brew", "uninstall", "--cask", "firefox"]])

    def test_already_absent_can_remove_declaration(self):
        self.execute(self.make("uninstall", "--cask", "firefox"))
        self.assertEqual(self.runner.mutations, [])
        self.assertNotIn('cask "firefox"', self.brewfile.read_text())

    def test_protected_alias_cannot_be_uninstalled(self):
        self.runner.canonical["git-alias"] = "git"
        with self.assertRaisesRegex(MODULE.Error, "受保护包"):
            self.execute(self.make("uninstall", "git-alias"))
        self.assertEqual(self.runner.mutations, [])

    def test_formula_alias_is_recorded_canonically(self):
        self.runner.canonical["rg"] = "ripgrep"
        self.execute(self.make("install", "rg"))
        self.assertIn('brew "ripgrep"', self.brewfile.read_text())
        self.assertNotIn('brew "rg"', self.brewfile.read_text())

    def test_existing_options_and_comments_preserved(self):
        original = self.brewfile.read_text()
        self.execute(self.make("install", "ruby"))
        self.assertEqual(self.brewfile.read_text(), original)
        self.assertIn("--skip-link", self.runner.mutations[0])

    def test_third_party_requires_explicit_trust(self):
        with self.assertRaisesRegex(MODULE.Error, "--trust"):
            self.execute(self.make("install", "--cask", "fixture/apps/demo"))
        self.assertEqual(self.runner.mutations, [])

    def test_third_party_installs_exact_trust_and_tap(self):
        self.execute(self.make("install", "--cask", "--trust", "fixture/apps/demo"))
        source = self.brewfile.read_text()
        self.assertIn('tap "fixture/apps"', source)
        self.assertIn('cask "fixture/apps/demo", trusted: true', source)
        trusted = self.root / "packages/macos/trusted-casks.txt"
        self.assertIn("fixture/apps/demo\n", trusted.read_text())
        self.assertNotIn(["brew", "trust", "fixture/apps"], self.runner.calls)

    def test_untrusted_third_party_preview_never_loads_or_trusts_ruby(self):
        self.execute(self.make("install", "--cask", "--trust", "--dry-run", "fixture/apps/demo"))
        self.assertEqual(self.runner.calls, [["brew", "list", "--cask"]])
        self.assertNotIn("fixture/apps", self.brewfile.read_text())

    def test_conflicting_third_party_sources_fail_closed(self):
        with self.assertRaisesRegex(MODULE.Error, "另一个 tap"):
            self.execute(self.make("install", "--cask", "--trust", "other/apps/tinycast"))

    def test_trusted_cask_uninstall_cleans_sidecar_but_keeps_tap(self):
        self.runner.names.add("tinycast")
        self.runner.canonical["tinycast"] = "abue-ammar/tinycast/tinycast"
        self.execute(self.make("uninstall", "--cask", "tinycast"))
        self.assertNotIn('cask "abue-ammar/tinycast/tinycast"', self.brewfile.read_text())
        self.assertIn('tap "abue-ammar/tinycast"', self.brewfile.read_text())
        trusted = self.root / "packages/macos/trusted-casks.txt"
        self.assertNotIn("abue-ammar/tinycast/tinycast\n", trusted.read_text())

    def test_concurrent_edit_is_not_overwritten(self):
        def edit():
            self.brewfile.write_text(self.brewfile.read_text() + "# user concurrent change\n")
        self.runner.on_mutation = edit
        with self.assertRaisesRegex(MODULE.Error, "拒绝覆盖"):
            self.execute(self.make("install", "gitui"))
        self.assertTrue(self.brewfile.read_text().endswith("# user concurrent change\n"))
        self.assertNotIn('brew "gitui"', self.brewfile.read_text())

    def test_dynamic_brewfile_fails_before_native_operation(self):
        self.brewfile.write_text(self.brewfile.read_text() + 'if OS.mac?\n  brew "gitui"\nend\n')
        with self.assertRaisesRegex(MODULE.Error, "不是支持的静态声明"):
            self.execute(self.make("install", "gitui"))
        self.assertEqual(self.runner.calls, [])

    def test_empty_manifest_protection(self):
        self.brewfile.write_text('cask "firefox"\n')
        with self.assertRaisesRegex(MODULE.Error, "空的软件包清单"):
            self.execute(self.make("uninstall", "--cask", "firefox"))

    def test_linux_install_updates_only_correct_profile(self):
        for profile, manager in MODULE.MANAGERS.items():
            with self.subTest(profile=profile):
                self.runner = FakeRunner(profile)
                self.execute(self.make("install", "gitui", profile=profile))
                path = self.root / f"packages/linux/{profile}/mise.toml"
                entry = f'"{manager}:gitui" = "latest"'
                self.assertIn(entry, path.read_text())
                self.assertEqual(self.runner.mutations[0][-1], "gitui")
                self.assertNotIn('brew "gitui"', self.brewfile.read_text())

    def test_linux_uninstall_preserves_other_declarations(self):
        for profile, manager in MODULE.MANAGERS.items():
            with self.subTest(profile=profile):
                self.runner = FakeRunner(profile, {"ripgrep", "base", "base-files", "filesystem"})
                self.execute(self.make("uninstall", "ripgrep", profile=profile))
                path = self.root / f"packages/linux/{profile}/mise.toml"
                self.assertNotIn("ripgrep", path.read_text())
                self.assertIn("git", path.read_text())
                command = self.runner.mutations[0]
                self.assertNotIn("-Rns", command)
                self.assertNotIn("--clean-deps", command)
                self.assertNotIn("autoremove", command)

    def test_apt_reverse_dependency_removal_is_rejected(self):
        self.runner = FakeRunner("debian", {"ripgrep", "git"})
        self.runner.extra_removal = "git"
        with self.assertRaisesRegex(MODULE.Error, "连带移除其他包"):
            self.execute(self.make("uninstall", "ripgrep", profile="debian"))
        self.assertEqual(self.runner.mutations, [])

    def test_all_linux_profiles_protect_critical_packages(self):
        for profile, package in (("arch", "base"), ("debian", "apt"), ("fedora", "kernel-core"), ("opensuse", "zypper")):
            with self.subTest(profile=profile):
                with self.assertRaisesRegex(MODULE.Error, "受保护包"):
                    self.execute(self.make("uninstall", package, profile=profile))

    def test_aur_install_and_plain_uninstall_share_pacman_database(self):
        self.runner = FakeRunner("arch")
        with patch.object(MODULE.shutil, "which", return_value="/fake/paru"), patch.object(MODULE.os, "geteuid", return_value=501):
            self.execute(self.make("install", "--aur", "sample-aur-bin", profile="arch"))
        path = self.root / "packages/linux/arch/mise.toml"
        self.assertIn('"aur:sample-aur-bin" = "latest"', path.read_text())
        self.execute(self.make("uninstall", "sample-aur-bin", profile="arch"))
        self.assertNotIn('"aur:sample-aur-bin" = "latest"', path.read_text())

    def test_toml_comments_and_other_tables_survive(self):
        path = self.root / "packages/linux/debian/mise.toml"
        path.write_text(path.read_text() + '\n[env]\nEXAMPLE = "retained"\n')
        self.runner = FakeRunner("debian")
        self.execute(self.make("install", "gitui", profile="debian"))
        source = path.read_text()
        self.assertLess(source.index('"apt:gitui"'), source.index("[env]"))
        self.assertIn('EXAMPLE = "retained"', source)
        self.assertIn("# Debian", source)

    def test_multiline_toml_is_rejected_before_native_operation(self):
        path = self.root / "packages/linux/debian/mise.toml"
        path.write_text(path.read_text() + '"apt:example" = {\nversion = "latest"\n}\n')
        with self.assertRaisesRegex(MODULE.Error, "无法安全编辑"):
            self.execute(self.make("install", "gitui", profile="debian"))
        self.assertEqual(self.runner.mutations, [])

    def test_package_selectors_and_flags_are_not_forwarded(self):
        for name in ("https://example.org/app", "../package", "app*", "app;id", "app=1.0"):
            with self.subTest(name=name):
                with self.assertRaisesRegex(MODULE.Error, "准确包名"):
                    self.make("install", name)
        with self.assertRaises(MODULE.Error):
            self.make("install", "--cask", "firefox", profile="debian")

    def test_symlink_manifest_is_not_overwritten(self):
        target = self.root / "saved-Brewfile"
        self.brewfile.rename(target)
        self.brewfile.symlink_to(target)
        with self.assertRaisesRegex(MODULE.Error, "普通的本地清单"):
            self.execute(self.make("install", "gitui"))

    def test_mise_parser_failure_is_fatal(self):
        original = self.runner.run
        def fail_parser(command, capture=True):
            if command[0] == "mise":
                raise MODULE.Error("mise rejects invalid TOML")
            return original(command, capture)
        self.runner.run = fail_parser
        with self.assertRaisesRegex(MODULE.Error, "mise rejects"):
            self.execute(self.make("install", "gitui", profile="debian"))
        self.assertEqual(self.runner.mutations, [])

    def test_version_pin_is_not_silently_overridden(self):
        path = self.root / "packages/linux/debian/mise.toml"
        path.write_text(path.read_text().replace('"apt:ripgrep" = "latest"', '"apt:ripgrep" = "1.0"'))
        with self.assertRaisesRegex(MODULE.Error, "版本固定"):
            self.execute(self.make("install", "ripgrep", profile="debian"))
        self.assertEqual(self.runner.mutations, [])

    def test_partial_multifile_write_is_rolled_back(self):
        operation = self.make("install", "--cask", "--trust", "fixture/apps/demo")
        original_write = operation.atomic_write
        original_brew = self.brewfile.read_text()
        trusted = self.root / "packages/macos/trusted-casks.txt"
        original_trust = trusted.read_text()
        def fail_brewfile(path, content):
            if path == self.brewfile:
                raise OSError("fake disk error")
            original_write(path, content)
        operation.atomic_write = fail_brewfile
        with self.assertRaisesRegex(MODULE.Error, "fake disk error"):
            self.execute(operation)
        self.assertEqual(self.brewfile.read_text(), original_brew)
        self.assertEqual(trusted.read_text(), original_trust)

    def test_unexpected_native_removals_require_review(self):
        self.runner.on_mutation = lambda: self.runner.names.discard("git")
        with self.assertRaisesRegex(MODULE.Error, "未计划的其他包移除"):
            self.execute(self.make("install", "gitui"))
        self.assertNotIn('brew "gitui"', self.brewfile.read_text())

    @unittest.skipUnless(shutil.which("mise"), "real mise CLI is unavailable")
    def test_real_mise_parses_generated_linux_manifests(self):
        native_parser = MODULE.Runner()
        fake = self.runner.run
        def run(command, capture=True):
            if command[:3] == ["mise", "config", "get"]:
                return native_parser.run(command, capture)
            return fake(command, capture)
        self.runner.run = run
        for profile in ("arch", "debian", "fedora", "opensuse"):
            with self.subTest(profile=profile):
                self.execute(self.make("install", "--dry-run", "gitui", profile=profile))
                self.execute(self.make("uninstall", "--dry-run", "ripgrep", profile=profile))
        self.assertEqual(self.runner.mutations, [])


if __name__ == "__main__":
    unittest.main()
